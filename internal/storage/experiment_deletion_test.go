package storage

import (
	"context"
	"errors"
	"testing"
)

func deletionStore(t *testing.T) *Store {
	t.Helper()
	s, _ := batchedListStore(t)
	_, err := s.pool.Exec(context.Background(), `
TRUNCATE experiment_trials,experiment_variants,experiments,pipeline_run_stages,pipeline_runs;
CREATE TABLE pipelines(id text PRIMARY KEY, description text,definition jsonb DEFAULT '{}');
CREATE TABLE operations(id text PRIMARY KEY,status text,spec jsonb DEFAULT '{}',plan jsonb DEFAULT '{}',plugin_id text DEFAULT 'plugin');
CREATE TABLE artifacts(id text PRIMARY KEY,operation_id text REFERENCES operations(id),storage_key text);
CREATE TABLE infrastructure_resources(created_by_operation_id text REFERENCES operations(id));
CREATE TABLE managed_resources(id text,operation_id text REFERENCES operations(id),deletion_operation_id text REFERENCES operations(id));
CREATE TABLE strategy_catalog(operation_id text REFERENCES operations(id));
CREATE TABLE plugin_runtime_profiles(endpoint_artifact_id text,credential_artifact_id text);
CREATE TABLE plugin_runtime_registries(endpoint_artifact_id text,credential_artifact_id text);
CREATE TABLE experiment_figures(experiment_id text REFERENCES experiments(id) ON DELETE CASCADE,storage_key text,source_artifact_ids text[]);
CREATE TABLE artifact_deletions(storage_key text PRIMARY KEY,queued_at timestamptz DEFAULT now(),retry_after timestamptz DEFAULT now());
CREATE TABLE audit_events(target_id text);
CREATE TABLE operation_logs(operation_id text REFERENCES operations(id) ON DELETE CASCADE);
CREATE TABLE operation_steps(operation_id text REFERENCES operations(id) ON DELETE CASCADE);
ALTER TABLE experiment_variants ADD FOREIGN KEY(experiment_id) REFERENCES experiments(id) ON DELETE CASCADE;
ALTER TABLE experiment_trials ADD FOREIGN KEY(variant_id) REFERENCES experiment_variants(id) ON DELETE CASCADE;
ALTER TABLE pipeline_run_stages ADD FOREIGN KEY(run_id) REFERENCES pipeline_runs(id) ON DELETE CASCADE;
ALTER TABLE pipeline_run_stages ADD FOREIGN KEY(operation_id) REFERENCES operations(id);
ALTER TABLE pipeline_run_stages ADD FOREIGN KEY(cleanup_operation_id) REFERENCES operations(id);
ALTER TABLE pipeline_runs ADD FOREIGN KEY(result_artifact_id) REFERENCES artifacts(id);
ALTER TABLE pipeline_runs ADD COLUMN lease_owner text,ADD COLUMN lease_until timestamptz;
INSERT INTO experiments(id,owner_user_id) VALUES ('exp_delete','alice'),('exp_keep','alice');
INSERT INTO experiment_variants(id,experiment_id,position,pipeline_id) VALUES ('v_delete','exp_delete',1,'pipe_delete');
INSERT INTO pipelines VALUES('pipe_delete','Immutable execution pipeline for test','{}'),('infra','Cluster','{}');
INSERT INTO pipeline_runs(id,pipeline_id) VALUES('run_delete','pipe_delete'),('run_infra','infra');
INSERT INTO experiment_trials(id,variant_id,position,pipeline_run_id) VALUES('trial_delete','v_delete',1,'run_delete');
INSERT INTO operations(id,status,spec) VALUES('op_delete','succeeded','{"run":"delete"}'),('op_infra','succeeded','{}'),('op_cleanup','succeeded','{"sourceOperationId":"op_delete"}');
INSERT INTO operations(id,status,spec,plan) VALUES('op_legacy_cleanup','failed','{"run":"delete"}','{"steps":[{"cleanup":true}]}');
INSERT INTO pipeline_run_stages(id,run_id,position,operation_id) VALUES('stage_delete','run_delete',1,'op_delete'),('stage_infra','run_infra',1,'op_infra');
INSERT INTO artifacts VALUES('art_delete','op_delete','/kubephos/operations/op_delete/result.json'),('art_infra','op_infra','/kubephos/operations/op_infra/result.json');
INSERT INTO operation_logs VALUES('op_delete'),('op_cleanup'),('op_infra');
INSERT INTO operation_steps VALUES('op_delete'),('op_cleanup'),('op_infra');
INSERT INTO experiment_figures VALUES('exp_delete','/experiments/exp_delete/figures/chart.png','{art_delete}');
`)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestExperimentDeletionRemovesOwnedHistoryAndQueuesObjects(t *testing.T) {
	s := deletionStore(t)
	ctx := context.Background()
	if err := s.DeleteExperiment(ctx, "exp_delete"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"experiment_variants", "experiment_trials", "experiment_figures"} {
		var count int
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
	for _, table := range []string{"operations", "operation_logs", "operation_steps", "artifacts", "pipeline_runs", "pipeline_run_stages", "pipelines"} {
		var count int
		if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s: %d %v", table, count, err)
		}
	}
	var jobs int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM artifact_deletions`).Scan(&jobs); err != nil || jobs != 4 {
		t.Fatalf("deletion jobs=%d %v", jobs, err)
	}
	if err := s.ProcessArtifactDeletions(ctx, func(context.Context, string) error { return errors.New("filer offline") }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE artifact_deletions SET retry_after=now()`); err != nil {
		t.Fatal(err)
	}
	if err := s.ProcessArtifactDeletions(ctx, func(context.Context, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM artifact_deletions`).Scan(&jobs); err != nil || jobs != 0 {
		t.Fatalf("jobs not drained: %d %v", jobs, err)
	}
}

func TestExperimentDeletionPreservesSharedArtifactAndItsProducer(t *testing.T) {
	s := deletionStore(t)
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `INSERT INTO experiment_variants(id,experiment_id,position) VALUES('v_keep','exp_keep',1); INSERT INTO experiment_trials(id,variant_id,position,result_artifact_id) VALUES('t_keep','v_keep',1,'art_delete')`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExperiment(ctx, "exp_delete"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM artifacts WHERE id='art_delete'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("shared artifact lost: %d %v", count, err)
	}
}

func TestExperimentDeletionPreservesExternalTypedArtifactReferences(t *testing.T) {
	for _, consumer := range []string{"operation", "pipeline", "transitive"} {
		t.Run(consumer, func(t *testing.T) {
			s := deletionStore(t)
			ctx := context.Background()
			query := `UPDATE operations SET plan='{"steps":[{"artifactInputs":[{"artifactId":"art_delete"}]}]}' WHERE id='op_infra'`
			if consumer == "pipeline" {
				query = `UPDATE pipelines SET definition='{"stages":[{"spec":{"nested":{"connectionRef":"art_delete"}}}]}' WHERE id='infra'`
			} else if consumer == "transitive" {
				query = `INSERT INTO operations(id,status,spec,plan) VALUES('op_dependent','succeeded','{"dependent":true}','{"steps":[{"artifactInputs":[{"artifactId":"art_delete"}]}]}');
INSERT INTO artifacts VALUES('art_dependent','op_dependent','/kubephos/operations/op_dependent/result.json');
INSERT INTO pipeline_run_stages(id,run_id,position,operation_id) VALUES('stage_dependent','run_delete',2,'op_dependent');
UPDATE operations SET plan='{"steps":[{"artifactInputs":[{"artifactId":"art_dependent"}]}]}' WHERE id='op_infra'`
			}
			if _, err := s.pool.Exec(ctx, query); err != nil {
				t.Fatal(err)
			}
			if err := s.DeleteExperiment(ctx, "exp_delete"); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM artifacts WHERE id='art_delete'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("referenced artifact lost: %d %v", count, err)
			}
		})
	}
}

func TestExperimentDeletionRollsBackWhileCleanupIsRunning(t *testing.T) {
	s := deletionStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `UPDATE operations SET status='running' WHERE id='op_cleanup'`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteExperiment(ctx, "exp_delete"); !errors.Is(err, ErrConflict) {
		t.Fatalf("want conflict, got %v", err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM experiments WHERE id='exp_delete'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial deletion: %d %v", count, err)
	}
}

func TestOrphanedExperimentHistoryPurgePreservesInfrastructure(t *testing.T) {
	s := deletionStore(t)
	ctx := context.Background()
	if _, err := s.pool.Exec(ctx, `DELETE FROM experiments WHERE id='exp_delete'`); err != nil {
		t.Fatal(err)
	}
	if err := s.PurgeOrphanedExperimentHistory(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM pipeline_runs`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("infrastructure changed: %d %v", count, err)
	}
}

func TestRetryExperimentCleanupDoesNotRerunWorkload(t *testing.T) {
	s := deletionStore(t)
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `INSERT INTO managed_resources(id) VALUES('cluster'); UPDATE pipeline_runs SET cluster_resource_id='cluster',status='failed' WHERE id='run_delete'; UPDATE pipeline_run_stages SET cleanup_status='failed',cleanup_operation_id='op_legacy_cleanup' WHERE id='stage_delete'`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RetryExperimentCleanup(ctx, "run_delete"); err != nil {
		t.Fatal(err)
	}
	var status, terminal, execution, cleanup string
	if err := s.pool.QueryRow(ctx, `SELECT r.status,r.terminal_status,s.status,s.cleanup_status FROM pipeline_runs r JOIN pipeline_run_stages s ON s.run_id=r.id WHERE r.id='run_delete'`).Scan(&status, &terminal, &execution, &cleanup); err != nil {
		t.Fatal(err)
	}
	if status != "running" || terminal != "failed" || execution != "succeeded" || cleanup != "pending" {
		t.Fatalf("unexpected retry state: %s %s %s %s", status, terminal, execution, cleanup)
	}
	if err := s.RetryExperimentCleanup(ctx, "run_delete"); !errors.Is(err, ErrConflict) {
		t.Fatalf("concurrent retry accepted: %v", err)
	}
}

func TestRetryExperimentCleanupRejectsBusyCluster(t *testing.T) {
	s := deletionStore(t)
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `INSERT INTO managed_resources(id) VALUES('cluster'); UPDATE pipeline_runs SET cluster_resource_id='cluster',status=CASE WHEN id='run_delete' THEN 'failed' ELSE 'running' END; UPDATE pipeline_run_stages SET cleanup_status='failed' WHERE id='stage_delete'`)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.RetryExperimentCleanup(ctx, "run_delete"); !errors.Is(err, ErrConflict) {
		t.Fatalf("busy cluster cleanup accepted: %v", err)
	}
}
