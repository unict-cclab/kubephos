package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/testutil"
)

type parallelPlugin struct{}

func (parallelPlugin) Manifest() plugins.Manifest {
	return plugins.Manifest{ID: "parallel-test", Version: "v1", Capabilities: []string{"lifecycle.cleanup"}}
}
func (parallelPlugin) Validate(context.Context, json.RawMessage) domain.ValidationReport {
	return domain.ValidationReport{Valid: true}
}
func (parallelPlugin) Plan(context.Context, json.RawMessage) (domain.Plan, error) {
	return domain.Plan{}, nil
}
func (parallelPlugin) Precheck(context.Context, domain.PlanStep, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{Status: domain.HealthHealthy}, nil
}
func (parallelPlugin) Execute(context.Context, domain.PlanStep, plugins.Logger) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (parallelPlugin) Verify(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{Status: domain.HealthHealthy}, nil
}
func (parallelPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func parallelCoordinator(t *testing.T) (*Worker, *pgxpool.Pool, func() domain.PipelineRun) {
	t.Helper()
	ctx := context.Background()
	uri := testutil.Database(t)
	store, err := storage.Open(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	pool, err := pgxpool.New(ctx, uri)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	def := domain.PipelineDefinition{MaxParallelStages: 2, Stages: []domain.PipelineStage{{ID: "a", PluginID: "parallel-test", Title: "A", Spec: json.RawMessage(`{}`)}, {ID: "a-next", PluginID: "parallel-test", Title: "A next", Spec: json.RawMessage(`{}`), DependsOn: []string{"a"}}, {ID: "b", PluginID: "parallel-test", Title: "B", Spec: json.RawMessage(`{}`)}}}
	resolution := domain.PipelineResolution{}
	run := domain.PipelineRun{ID: "parallel-run", PipelineID: "parallel-pipeline", WorkspaceID: "ws_default", Name: "Parallel test", PipelineHash: strings.Repeat("a", 64)}
	for i, s := range def.Stages {
		resolution.Stages = append(resolution.Stages, domain.ResolvedPipelineStage{ID: s.ID, PluginID: s.PluginID, PluginVersion: "v1", Spec: s.Spec, Plan: domain.Plan{PluginID: s.PluginID, Steps: []domain.PlanStep{{ID: "work", Name: "Work", Mutating: true, Input: json.RawMessage(`{}`)}}}})
		run.Stages = append(run.Stages, domain.PipelineRunStage{ID: "parallel-" + s.ID, Position: i + 1, StageID: s.ID, PluginID: s.PluginID, Title: s.Title})
	}
	_, err = store.CreatePipeline(ctx, domain.Pipeline{ID: run.PipelineID, WorkspaceID: run.WorkspaceID, Name: run.Name, Definition: def, Resolution: resolution, Validation: domain.ValidationReport{Valid: true}, Hash: run.PipelineHash})
	if err != nil {
		t.Fatal(err)
	}
	// Seed a task-only pipeline: no result artifact contract.
	_, err = pool.Exec(ctx, `UPDATE pipelines SET resolution=jsonb_set(resolution,'{result}','{"type":"","version":""}') WHERE id=$1`, run.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreatePipelineRun(ctx, run)
	if err != nil {
		t.Fatal(err)
	}
	worker := &Worker{store: store, registry: plugins.NewRegistry(parallelPlugin{})}
	tick := func() domain.PipelineRun {
		t.Helper()
		claimed, found, err := store.ClaimPipelineRun(ctx, "test-coordinator")
		if err != nil || !found {
			t.Fatalf("claim: %v %v", found, err)
		}
		if err = worker.reconcilePipelineRun(ctx, "test-coordinator", claimed); err != nil {
			t.Fatal(err)
		}
		latest, err := store.GetPipelineRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		return latest
	}
	return worker, pool, tick
}

func TestParallelCoordinatorCompletesBranchesWithoutSerializing(t *testing.T) {
	_, pool, tick := parallelCoordinator(t)
	ctx := context.Background()
	run := tick()
	if run.Stages[0].OperationID == "" || run.Stages[2].OperationID == "" || run.Stages[1].OperationID != "" {
		t.Fatal("independent branches did not start together")
	}
	if _, err := pool.Exec(ctx, `UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, run.Stages[0].OperationID); err != nil {
		t.Fatal(err)
	}
	run = tick()
	if run.Stages[1].OperationID == "" {
		t.Fatal("ready branch waited for unrelated running branch")
	}
	if _, err := pool.Exec(ctx, `UPDATE operations SET status='succeeded',completed_at=now()`); err != nil {
		t.Fatal(err)
	}
	run = tick()
	if run.Status != domain.OperationSucceeded {
		t.Fatalf("not completed: %s", run.Status)
	}
}
func TestParallelCoordinatorDrainsEverySiblingBeforeFailureOrCancellation(t *testing.T) {
	for _, mode := range []string{"failure", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			_, pool, tick := parallelCoordinator(t)
			ctx := context.Background()
			run := tick()
			if _, err := pool.Exec(ctx, `UPDATE operations SET status='running',started_at=now()`); err != nil {
				t.Fatal(err)
			}
			if mode == "failure" {
				if _, err := pool.Exec(ctx, `UPDATE operations SET status='failed',error='test failure',completed_at=now() WHERE id=$1`, run.Stages[0].OperationID); err != nil {
					t.Fatal(err)
				}
			} else {
				if _, err := pool.Exec(ctx, `UPDATE pipeline_runs SET cancel_requested=true`); err != nil {
					t.Fatal(err)
				}
			}
			run = tick()
			if run.Status != domain.OperationRunning || run.TerminalStatus == "" {
				t.Fatal("pipeline finalized before active siblings stopped")
			}
			var pending int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM operations WHERE status='running' AND NOT cancel_requested`).Scan(&pending); err != nil || pending != 0 {
				t.Fatalf("uncancelled siblings: %d %v", pending, err)
			}
			if run.Stages[1].OperationID != "" {
				t.Fatal("new stage started during termination")
			}
			if _, err := pool.Exec(ctx, `UPDATE operations SET status='canceled',completed_at=now() WHERE status='running'`); err != nil {
				t.Fatal(err)
			}
			run = tick()
			want := domain.OperationFailed
			if mode == "cancel" {
				want = domain.OperationCanceled
			}
			if run.Status != want {
				t.Fatalf("status=%s want=%s", run.Status, want)
			}
		})
	}
}

func TestParallelCleanupWaitsForSiblingsAndRunsInReverseOrder(t *testing.T) {
	_, pool, tick := parallelCoordinator(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE pipelines SET definition=jsonb_set(definition,'{cleanupAfterRun}','true')`)
	run := tick()
	a, b := run.Stages[0].OperationID, run.Stages[2].OperationID
	exec(`UPDATE operations SET status='running',started_at=now()`)
	exec(`UPDATE operation_steps SET status='failed' WHERE operation_id=$1`, a)
	exec(`UPDATE operations SET status='failed',error='failure',completed_at=now() WHERE id=$1`, a)
	run = tick()
	run = tick()
	for _, stage := range run.Stages {
		if stage.CleanupOperationID != "" {
			t.Fatal("cleanup raced with a running sibling")
		}
	}
	exec(`UPDATE operations SET status='canceled',completed_at=now() WHERE id=$1`, b)
	exec(`UPDATE operation_steps SET status='canceled' WHERE operation_id=$1`, b)
	run = tick()
	if run.Stages[2].CleanupOperationID == "" || run.Stages[0].CleanupOperationID != "" {
		t.Fatal("cleanup order must begin with later branch")
	}
	exec(`UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, run.Stages[2].CleanupOperationID)
	run = tick()
	if run.Stages[0].CleanupOperationID == "" {
		t.Fatal("earlier branch cleanup not scheduled")
	}
	exec(`UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, run.Stages[0].CleanupOperationID)
	run = tick()
	if run.Status != domain.OperationFailed {
		t.Fatalf("pipeline did not finish after cleanup: %s", run.Status)
	}
}

func TestCoordinatorPollingDefersSameRunAndAllowsAnotherPipeline(t *testing.T) {
	worker, pool, tick := parallelCoordinator(t)
	worker.poll = time.Second
	run := tick()
	var deferred bool
	if err := pool.QueryRow(context.Background(), `SELECT lease_owner IS NULL AND lease_until>now() FROM pipeline_runs WHERE id=$1`, run.ID).Scan(&deferred); err != nil || !deferred {
		t.Fatalf("poll delay not applied: %v %v", deferred, err)
	}
	// A second ready run can be claimed while the first awaits its next poll.
	_, err := pool.Exec(context.Background(), `INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) SELECT 'other-run',pipeline_id,workspace_id,'Other run','queued',pipeline_hash,result_type,result_version FROM pipeline_runs WHERE id=$1`, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, found, err := worker.store.ClaimPipelineRun(context.Background(), "other-coordinator")
	if err != nil || !found || claimed.ID != "other-run" {
		t.Fatalf("pipeline starved by active run: %s %v %v", claimed.ID, found, err)
	}
}

func TestParallelStartFailureCancelsSiblingCreatedInSameTick(t *testing.T) {
	_, pool, tick := parallelCoordinator(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE pipelines SET resolution=jsonb_set(resolution,'{stages,2,pluginVersion}','"different-version"') WHERE id='parallel-pipeline'`); err != nil {
		t.Fatal(err)
	}
	run := tick()
	if run.Stages[0].OperationID == "" || run.TerminalStatus != domain.OperationFailed {
		t.Fatal("did not start first sibling then enter termination")
	}
	var requested bool
	if err := pool.QueryRow(ctx, `SELECT cancel_requested FROM operations WHERE id=$1`, run.Stages[0].OperationID).Scan(&requested); err != nil {
		t.Fatal(err)
	}
	if !requested {
		t.Fatal("new sibling was left executing after another stage failed to start")
	}
}
