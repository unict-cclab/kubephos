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

// measuredExperiment seeds one experiment with two sequential trials of a
// one-stage pipeline whose stage produces the verified result.
func measuredExperiment(t *testing.T, rest int) (*Worker, *pgxpool.Pool, func(runID string) domain.PipelineRun) {
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
	hash := strings.Repeat("b", 64)
	def := domain.PipelineDefinition{CleanupAfterRun: true, Stages: []domain.PipelineStage{{ID: "measure", PluginID: "parallel-test", Title: "Measure", Spec: json.RawMessage(`{}`)}}, Result: domain.PipelineOutput{Stage: "measure", Output: "dataset"}}
	resolution := domain.PipelineResolution{Result: domain.ArtifactContract{Type: "TimeSeriesDataset", Version: "v1alpha1"}, Stages: []domain.ResolvedPipelineStage{{ID: "measure", PluginID: "parallel-test", PluginVersion: "v1", Spec: json.RawMessage(`{}`), Plan: domain.Plan{PluginID: "parallel-test", Steps: []domain.PlanStep{{ID: "work", Name: "Work", Mutating: true, Input: json.RawMessage(`{}`)}}}}}}
	if _, err := store.CreatePipeline(ctx, domain.Pipeline{ID: "measure-pipeline", WorkspaceID: "ws_default", Name: "Measure", Definition: def, Resolution: resolution, Validation: domain.ValidationReport{Valid: true}, Hash: hash}); err != nil {
		t.Fatal(err)
	}
	experiment := domain.Experiment{ID: "measure-experiment", WorkspaceID: "ws_default", OwnerUserID: "measure-owner", Kind: "instance", Name: "Measure", ResultType: "TimeSeriesDataset", ResultVersion: "v1alpha1", RestSecondsBetweenTrials: rest, FirstTrialPilot: true,
		Variants: []domain.ExperimentVariant{{ID: "measure-variant", Position: 1, Name: "Measure", PipelineID: "measure-pipeline", PipelineHash: hash}}}
	runs := map[string]domain.PipelineRun{}
	for position, trialID := range []string{"trial-pilot", "trial-one"} {
		runID := "run-" + strings.TrimPrefix(trialID, "trial-")
		experiment.Variants[0].Trials = append(experiment.Variants[0].Trials, domain.ExperimentTrial{ID: trialID, Position: position, Pilot: position == 0})
		runs[trialID] = domain.PipelineRun{ID: runID, PipelineID: "measure-pipeline", WorkspaceID: "ws_default", OwnerUserID: "measure-owner", Name: runID, PipelineHash: hash, ResultType: "TimeSeriesDataset", ResultVersion: "v1alpha1",
			Stages: []domain.PipelineRunStage{{ID: runID + "-measure", Position: 1, StageID: "measure", PluginID: "parallel-test", Title: "Measure"}}}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,role) VALUES ('measure-owner','measure-owner','user')`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreatePipelineExperiment(ctx, experiment, runs); err != nil {
		t.Fatal(err)
	}
	worker := &Worker{store: store, registry: plugins.NewRegistry(parallelPlugin{})}
	tick := func(runID string) domain.PipelineRun {
		t.Helper()
		claimed, found, err := store.ClaimPipelineRun(ctx, "test-coordinator")
		if err != nil || !found || claimed.ID != runID {
			t.Fatalf("claim %s: %v %v %s", runID, found, err, claimed.ID)
		}
		if err = worker.reconcilePipelineRun(ctx, "test-coordinator", claimed); err != nil {
			t.Fatal(err)
		}
		latest, err := store.GetPipelineRun(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		return latest
	}
	return worker, pool, tick
}

func execSQL(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatal(err)
	}
}

// measure runs the stage to a verified result and starts its cleanup.
func measure(t *testing.T, worker *Worker, pool *pgxpool.Pool, tick func(string) domain.PipelineRun, runID string) (domain.PipelineRun, string) {
	t.Helper()
	run := tick(runID)
	operationID := run.Stages[0].OperationID
	if operationID == "" {
		t.Fatal("measurement stage did not start")
	}
	execSQL(t, pool, `UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, operationID)
	execSQL(t, pool, `UPDATE operation_steps SET status='succeeded' WHERE operation_id=$1`, operationID)
	artifact, err := worker.store.CreateArtifact(context.Background(), domain.Artifact{ID: "art-" + runID, OperationID: operationID, OutputName: "dataset", Name: "dataset", Type: "TimeSeriesDataset", Version: "v1alpha1", MediaType: "application/json", StorageKey: "/results/" + runID, Digest: "sha256:" + strings.Repeat("c", 64), SizeBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	run = tick(runID)
	if run.Status != domain.OperationRunning || run.Stages[0].CleanupOperationID == "" {
		t.Fatalf("cleanup did not start after the verified result: %s %+v", run.Status, run.Stages[0])
	}
	return run, artifact.ID
}

func TestCleanupFailureKeepsVerifiedResultAndStopsLaterRuns(t *testing.T) {
	worker, pool, tick := measuredExperiment(t, 0)
	ctx := context.Background()
	run, artifactID := measure(t, worker, pool, tick, "run-pilot")
	execSQL(t, pool, `UPDATE operations SET status='failed',error='namespace stuck terminating',completed_at=now() WHERE id=$1`, run.Stages[0].CleanupOperationID)
	run = tick("run-pilot")
	if run.Status != domain.OperationSucceeded || run.ResultArtifactID != artifactID || !strings.Contains(run.CleanupError, "namespace stuck terminating") || run.Error != "" {
		t.Fatalf("cleanup failure discarded the result: status=%s artifact=%q cleanup=%q error=%q", run.Status, run.ResultArtifactID, run.CleanupError, run.Error)
	}
	var status, result, cleanup string
	var pilot bool
	if err := pool.QueryRow(ctx, `SELECT status,COALESCE(result_artifact_id,''),cleanup_error,pilot FROM experiment_trials WHERE id='trial-pilot'`).Scan(&status, &result, &cleanup, &pilot); err != nil {
		t.Fatal(err)
	}
	if status != "succeeded" || result != artifactID || cleanup == "" || !pilot {
		t.Fatalf("trial: %s %q %q pilot=%v", status, result, cleanup, pilot)
	}
	// The environment may be dirty: the next sequential run must not start.
	var nextStatus, nextError, experimentStatus string
	if err := pool.QueryRow(ctx, `SELECT t.status,t.error,e.status FROM experiment_trials t JOIN experiment_variants v ON v.id=t.variant_id JOIN experiments e ON e.id=v.experiment_id WHERE t.id='trial-one'`).Scan(&nextStatus, &nextError, &experimentStatus); err != nil {
		t.Fatal(err)
	}
	if nextStatus != "canceled" || !strings.Contains(nextError, "cleanup") || experimentStatus != "failed" {
		t.Fatalf("later run not stopped: %s %q experiment=%s", nextStatus, nextError, experimentStatus)
	}
	if _, found, err := worker.store.ClaimPipelineRun(ctx, "other"); err != nil || found {
		t.Fatalf("a run was claimable after a failed cleanup: %v %v", found, err)
	}
	experiments, err := worker.store.ListExperimentsForUser(ctx, "measure-owner", 10)
	if err != nil || len(experiments) != 1 {
		t.Fatal(err)
	}
	trial := experiments[0].Variants[0].Trials[0]
	if !experiments[0].FirstTrialPilot || !trial.Pilot || trial.CleanupError == "" || trial.ResultArtifactID != artifactID {
		t.Fatalf("API model lost cleanup warning or pilot flag: %+v", trial)
	}

	// Retrying only the cleanup keeps the result and clears the warning.
	if err := worker.store.RetryExperimentCleanup(ctx, "run-pilot"); err != nil {
		t.Fatal(err)
	}
	run = tick("run-pilot")
	if run.Stages[0].CleanupOperationID == "" {
		t.Fatal("cleanup retry not scheduled")
	}
	execSQL(t, pool, `UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, run.Stages[0].CleanupOperationID)
	run = tick("run-pilot")
	if run.Status != domain.OperationSucceeded || run.ResultArtifactID != artifactID || run.CleanupError != "" {
		t.Fatalf("cleanup retry changed the result: %s %q %q", run.Status, run.ResultArtifactID, run.CleanupError)
	}
}

func TestRestBetweenTrialsDefersNextRunWithoutBlockingWorkers(t *testing.T) {
	worker, pool, tick := measuredExperiment(t, 120)
	ctx := context.Background()
	run, _ := measure(t, worker, pool, tick, "run-pilot")
	execSQL(t, pool, `UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, run.Stages[0].CleanupOperationID)
	run = tick("run-pilot")
	if run.Status != domain.OperationSucceeded || run.CleanupError != "" || run.CompletedAt == nil {
		t.Fatalf("run did not complete: %s %q", run.Status, run.CleanupError)
	}
	next, err := worker.store.GetPipelineRun(ctx, "run-one")
	if err != nil {
		t.Fatal(err)
	}
	if next.ScheduledFor == nil || next.ScheduledFor.Sub(*run.CompletedAt) < 119*time.Second {
		t.Fatalf("next run not deferred by the rest: %v after %v", next.ScheduledFor, run.CompletedAt)
	}
	if _, found, err := worker.store.ClaimPipelineRun(ctx, "other"); err != nil || found {
		t.Fatalf("next run claimed during the rest: %v %v", found, err)
	}
	experiments, err := worker.store.ListExperimentsForUser(ctx, "measure-owner", 10)
	if err != nil || experiments[0].RestSecondsBetweenTrials != 120 || experiments[0].Variants[0].Trials[1].ScheduledFor == nil {
		t.Fatalf("rest not exposed: %+v %v", experiments, err)
	}
	// Synchronizing again never moves the schedule further.
	if err := worker.store.SyncExperimentPipelineRun(ctx, "run-pilot"); err != nil {
		t.Fatal(err)
	}
	again, _ := worker.store.GetPipelineRun(ctx, "run-one")
	if !again.ScheduledFor.Equal(*next.ScheduledFor) {
		t.Fatal("rest is not idempotent")
	}
	execSQL(t, pool, `UPDATE pipeline_runs SET scheduled_for=now() WHERE id='run-one'`)
	if run := tick("run-one"); run.Stages[0].OperationID == "" {
		t.Fatal("next run did not start after the rest")
	}
}
