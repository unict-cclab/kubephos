package engine

import (
	"context"
	"errors"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
	"testing"
)

func TestManualInitializationWaitsWithoutStartingTrafficAndResumesPersistedRun(t *testing.T) {
	worker, pool, tick := parallelCoordinator(t)
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO users(id,username,role) VALUES ('manual-owner','manual-owner','user'); INSERT INTO experiments(id,workspace_id,owner_user_id,kind,name,status,result_type,result_version) VALUES ('manual-experiment','ws_default','manual-owner','instance','Manual','queued','',''); INSERT INTO experiment_variants(id,experiment_id,position,name,display_alias,pipeline_id,pipeline_hash) VALUES ('manual-variant','manual-experiment',1,'Manual','Manual','parallel-pipeline',repeat('a',64)); INSERT INTO experiment_trials(id,variant_id,position,status,pipeline_run_id) VALUES ('manual-trial','manual-variant',1,'queued','parallel-run'); UPDATE pipeline_runs SET pause_before_stage='a-next' WHERE id='parallel-run'`)
	if err := worker.store.StartExperimentTraffic(ctx, "manual-experiment", "parallel-run"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("traffic allowed before initialization", err)
	}
	run := tick()
	if run.Phase != "initializing" || run.Stages[0].OperationID == "" || run.Stages[1].OperationID != "" || run.Stages[2].OperationID != "" {
		t.Fatal("runtime stage started before initialization", run)
	}
	exec(`UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, run.Stages[0].OperationID)
	run = tick()
	if run.Phase != "initialized" || run.InitializedAt == nil || run.Stages[1].OperationID != "" {
		t.Fatal("run did not pause after setup", run)
	}
	var status string
	if err := pool.QueryRow(ctx, `SELECT status FROM experiments WHERE id='manual-experiment'`).Scan(&status); err != nil || status != "initialized" {
		t.Fatal("experiment status", status, err)
	}
	run = tick()
	if run.Stages[1].OperationID != "" {
		t.Fatal("waiting run started traffic on the next coordinator tick")
	}
	if err := worker.store.StartExperimentTraffic(ctx, "manual-experiment", "parallel-run"); err != nil {
		t.Fatal(err)
	}
	if err := worker.store.StartExperimentTraffic(ctx, "manual-experiment", "parallel-run"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("duplicate traffic start accepted", err)
	}
	run = tick()
	if !run.ExecutionRequested || run.Stages[1].OperationID == "" || run.Phase != "" {
		t.Fatal("persisted manual start did not resume the existing run", run)
	}

	// Finish run one and verify that a fresh coordinator waits again for run two.
	exec(`UPDATE operations SET status='succeeded',completed_at=now()`)
	run = tick()
	if run.Status != "succeeded" {
		t.Fatal("first run did not finish", run.Status)
	}
	second := domain.PipelineRun{ID: "second-run", PipelineID: run.PipelineID, WorkspaceID: run.WorkspaceID, Name: "Second run", PipelineHash: run.PipelineHash, PauseBeforeStage: run.PauseBeforeStage}
	for _, stage := range run.Stages {
		second.Stages = append(second.Stages, domain.PipelineRunStage{ID: "second-" + stage.StageID, Position: stage.Position, StageID: stage.StageID, PluginID: stage.PluginID, Title: stage.Title})
	}
	if _, err := worker.store.CreatePipelineRun(ctx, second); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO experiment_trials(id,variant_id,position,status,pipeline_run_id) VALUES ('second-trial','manual-variant',2,'queued','second-run'); UPDATE experiments SET status='queued' WHERE id='manual-experiment'`)
	nextTick := func() domain.PipelineRun {
		t.Helper()
		claimed, found, err := worker.store.ClaimPipelineRun(ctx, "replacement-coordinator")
		if err != nil || !found || claimed.ID != "second-run" {
			t.Fatalf("claim second run: %v %v %#v", err, found, claimed)
		}
		replacement := &Worker{store: worker.store, registry: worker.registry}
		if err := replacement.reconcilePipelineRun(ctx, "replacement-coordinator", claimed); err != nil {
			t.Fatal(err)
		}
		latest, err := worker.store.GetPipelineRun(ctx, "second-run")
		if err != nil {
			t.Fatal(err)
		}
		return latest
	}
	second = nextTick()
	if second.Phase != "initializing" {
		t.Fatal("second run skipped initialization", second)
	}
	exec(`UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, second.Stages[0].OperationID)
	second = nextTick()
	if second.Phase != "initialized" || second.ExecutionRequested || second.Stages[1].OperationID != "" {
		t.Fatal("second run did not wait for its own manual start", second)
	}
	if err := worker.store.StartExperimentTraffic(ctx, "manual-experiment", "parallel-run"); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("stale click started the next run", err)
	}
	if err := worker.store.StartExperimentTraffic(ctx, "manual-experiment", "second-run"); err != nil {
		t.Fatal(err)
	}
}

func TestInitializedPipelineCanBeCanceledWithoutStartingTraffic(t *testing.T) {
	worker, pool, tick := parallelCoordinator(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE pipeline_runs SET pause_before_stage='a' WHERE id='parallel-run'`); err != nil {
		t.Fatal(err)
	}
	run := tick()
	if run.Phase != "initialized" {
		t.Fatal(run)
	}
	if _, err := pool.Exec(ctx, `UPDATE pipeline_runs SET cancel_requested=true WHERE id='parallel-run'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4 && run.Status != "canceled"; i++ {
		run = tick()
	}
	if run.Status != "canceled" {
		t.Fatal("initialized pipeline did not cancel", run.Status)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM operations`).Scan(&count); err != nil || count != 0 {
		t.Fatal("cancel started an operation", count, err)
	}
	_ = worker
}

func TestInitializedRunReservesOnlyItsNamedNamespace(t *testing.T) {
	worker, pool, tick := parallelCoordinator(t)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES ('namespace-cluster','ws_default','Cluster','kubernetes-cluster','proxmox','test','v1','{}','parallel-pipeline','parallel-run'); UPDATE pipeline_runs SET cluster_resource_id='namespace-cluster',isolation_mode='namespaced',application_namespace='benchmark',pause_before_stage='a' WHERE id='parallel-run'`)
	if err != nil {
		t.Fatal(err)
	}
	run := tick()
	if run.Phase != "initialized" {
		t.Fatal("not initialized", run)
	}
	for _, item := range []struct{ id, namespace string }{{"same-namespace", "benchmark"}, {"other-namespace", "other"}} {
		_, err := worker.store.CreatePipelineRun(ctx, domain.PipelineRun{ID: item.id, PipelineID: run.PipelineID, WorkspaceID: run.WorkspaceID, ClusterResourceID: "namespace-cluster", ApplicationNamespace: item.namespace, IsolationMode: "namespaced", Name: item.id, PipelineHash: run.PipelineHash})
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE pipeline_runs SET lease_owner='waiting',lease_until=now()+interval '1 hour' WHERE id='parallel-run'`); err != nil {
		t.Fatal(err)
	}
	next, found, err := worker.store.ClaimPipelineRun(ctx, "other-coordinator")
	if err != nil || !found || next.ID != "other-namespace" {
		t.Fatalf("namespace admission: %#v %v %v", next, found, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE pipeline_runs SET status='running' WHERE id='same-namespace'`); err == nil {
		t.Fatal("database allowed two running experiments in the same namespace")
	}
}

func TestCancelAfterInitializationCleansPreparedStagesWithoutStartingLoad(t *testing.T) {
	_, pool, tick := parallelCoordinator(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `UPDATE pipeline_runs SET pause_before_stage='a-next' WHERE id='parallel-run'; UPDATE pipelines SET definition=jsonb_set(definition,'{cleanupAfterRun}','true') WHERE id='parallel-pipeline'`); err != nil {
		t.Fatal(err)
	}
	run := tick()
	if _, err := pool.Exec(ctx, `UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, run.Stages[0].OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE operation_steps SET status='succeeded' WHERE operation_id=$1`, run.Stages[0].OperationID); err != nil {
		t.Fatal(err)
	}

	run = tick()
	if run.Phase != "initialized" {
		t.Fatal(run)
	}
	if _, err := pool.Exec(ctx, `UPDATE pipeline_runs SET cancel_requested=true WHERE id='parallel-run'`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 6 && run.Status != "canceled"; i++ {
		run = tick()
		if run.Stages[1].OperationID != "" || run.Stages[2].OperationID != "" {
			t.Fatal("cancellation started a runtime stage")
		}
		if run.Stages[0].CleanupOperationID != "" {
			if _, err := pool.Exec(ctx, `UPDATE operations SET status='succeeded',completed_at=now() WHERE id=$1`, run.Stages[0].CleanupOperationID); err != nil {
				t.Fatal(err)
			}
		}
	}
	if run.Status != "canceled" || run.Stages[0].CleanupOperationID == "" || run.Stages[0].CleanupStatus != "succeeded" {
		t.Fatal("prepared resources not cleaned before cancellation", run)
	}
}
