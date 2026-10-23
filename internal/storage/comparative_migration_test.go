package storage

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestComparativeMigration(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `
ALTER TABLE experiments DROP COLUMN configuration_snapshot;
CREATE TABLE workspaces (id text PRIMARY KEY);
CREATE TABLE managed_resources (id text PRIMARY KEY);
INSERT INTO workspaces VALUES ('workspace'); INSERT INTO managed_resources VALUES ('cluster');
CREATE TABLE experiment_configurations (
 id text PRIMARY KEY, workspace_id text NOT NULL REFERENCES workspaces(id), cluster_resource_id text NOT NULL REFERENCES managed_resources(id),
 owner_user_id text NOT NULL REFERENCES users(id), name text NOT NULL, description text NOT NULL DEFAULT '', application_ref text NOT NULL,
 application_digest text NOT NULL, definition jsonb NOT NULL, validation jsonb NOT NULL, created_at timestamptz NOT NULL DEFAULT now(), updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX experiment_configurations_owner_name_idx ON experiment_configurations(owner_user_id, lower(name));
INSERT INTO experiment_configurations (id, workspace_id, cluster_resource_id, owner_user_id, name, application_ref, application_digest, definition, validation)
 VALUES ('single-a','workspace','cluster','alice','A','app:test','digest','{"kind":"single","components":[]}','{"valid":true,"issues":[]}'),
 ('single-b','workspace','cluster','alice','B','app:test','digest','{"kind":"single","components":[]}','{"valid":true,"issues":[]}');
UPDATE experiments SET kind='suite' WHERE id IN ('experiment-0','experiment-1','experiment-2','empty-experiment');
UPDATE experiment_variants SET configuration_id=CASE WHEN position=1 THEN 'single-a' ELSE 'single-b' END WHERE experiment_id IN ('experiment-0','experiment-2');
ALTER TABLE experiments ADD CONSTRAINT experiments_configuration_id_fkey FOREIGN KEY (configuration_id) REFERENCES experiment_configurations(id) ON DELETE SET NULL;
ALTER TABLE experiment_variants ADD CONSTRAINT experiment_variants_configuration_id_fkey FOREIGN KEY (configuration_id) REFERENCES experiment_configurations(id) ON DELETE SET NULL;
`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/changes/040-comparative-configurations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var suites, linked, snapshots int
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM experiment_configurations WHERE definition->>'kind'='suite'`).Scan(&suites); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM experiments WHERE kind='suite' AND configuration_id IS NOT NULL`).Scan(&linked); err != nil {
		t.Fatal(err)
	}
	if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM experiments WHERE configuration_snapshot <> '{}'`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if suites != 4 || linked != 4 || snapshots != 31 {
		t.Fatalf("suites=%d linked=%d snapshots=%d", suites, linked, snapshots)
	}
	configurations, err := store.ListExperimentConfigurations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	valid, invalid := 0, 0
	for _, configuration := range configurations {
		if configuration.Kind != "suite" {
			continue
		}
		if configuration.Validation.Valid {
			valid++
		} else {
			invalid++
		}
	}
	if valid != 2 || invalid != 2 {
		t.Fatalf("valid=%d invalid=%d; deleted-source suites must be preserved but not runnable", valid, invalid)
	}
	if err := store.DeleteExperimentConfiguration(ctx, "single-a"); err != ErrConflict {
		t.Fatalf("expected referenced single to be protected, got %v", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE experiment_configurations SET definition='{"kind":"suite","variants":[]}' WHERE id='single-a'`); err == nil {
		t.Fatal("referenced single converted to nested suite")
	}
	if _, err := store.pool.Exec(ctx, `INSERT INTO experiment_configurations (id,workspace_id,owner_user_id,name,application_ref,application_digest,definition,validation) VALUES ('bad','workspace','alice','Bad','','','{"kind":"single"}','{}')`); err == nil {
		t.Fatal("single without a cluster was accepted")
	}
	// Changes to today's source configuration must not rewrite old results.
	if _, err := store.pool.Exec(ctx, `UPDATE experiment_configurations SET definition='{"kind":"single","components":[{"configuration":{"rps":100}}]}' WHERE id='single-a'`); err != nil {
		t.Fatal(err)
	}
	var original json.RawMessage
	if err := store.pool.QueryRow(ctx, `SELECT configuration_snapshot->'variants'->0->'definition' FROM experiments WHERE id='experiment-0'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	if string(original) != "{}" {
		t.Fatalf("historical snapshot changed: %s", original)
	}
	// Persist and reload a newly queued suite with its complete immutable snapshot.
	if _, err := store.pool.Exec(ctx, `CREATE TABLE pipelines(id text PRIMARY KEY, workspace_id text, pipeline_hash text, resolution jsonb); INSERT INTO pipelines VALUES ('pipeline','workspace','hash','{"result":{"type":"TimeSeriesDataset","version":"v1alpha1"}}');`); err != nil {
		t.Fatal(err)
	}
	suite := domain.Experiment{ID: "new-suite", WorkspaceID: "workspace", OwnerUserID: "alice", Kind: "suite", Name: "New suite", ResultType: "TimeSeriesDataset", ResultVersion: "v1alpha1", ConfigurationSnapshot: json.RawMessage(`{"configuration":{"kind":"suite"},"members":[{"id":"single-a"}],"runs":1}`), Variants: []domain.ExperimentVariant{{ID: "new-variant", Position: 1, Name: "A", PipelineID: "pipeline", PipelineHash: "hash", ConfigurationID: "single-a", Configuration: json.RawMessage(`{"rps":100}`), Trials: []domain.ExperimentTrial{{ID: "new-trial", Position: 1}}}}}
	_, err = store.CreatePipelineExperiment(ctx, suite, map[string]domain.PipelineRun{"new-trial": {ID: "new-run", PipelineID: "pipeline", PipelineHash: "hash", WorkspaceID: "workspace", ClusterResourceID: "cluster", OwnerUserID: "alice", IsolationMode: "namespaced", Name: "New run", ResultType: "TimeSeriesDataset", ResultVersion: "v1alpha1"}})
	if err != nil {
		t.Fatal(err)
	}
	experiments, err := store.ListExperimentsForUser(ctx, "alice", 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, experiment := range experiments {
		if experiment.ID == suite.ID {
			found = true
			var snapshot struct{ Runs int }
			value, err := store.GetExperimentConfigurationSnapshot(ctx, experiment.ID)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(value, &snapshot); err != nil || snapshot.Runs != 1 {
				t.Fatalf("snapshot not persisted: %s, %v", value, err)
			}
		}
	}
	if !found {
		t.Fatal("queued suite missing from experiment history")
	}
}
