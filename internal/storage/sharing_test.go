package storage

import (
	"context"
	"errors"
	"os"
	"testing"
)

func TestPrivateSharingAndInheritedConfigurationAccess(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `
 ALTER TABLE users ADD COLUMN enabled boolean NOT NULL DEFAULT true;
 INSERT INTO users(id,username) VALUES ('carol','Carol'),('future','Future');
 CREATE TABLE experiment_configurations (
 id text PRIMARY KEY,workspace_id text NOT NULL DEFAULT 'workspace',cluster_resource_id text DEFAULT 'cluster',owner_user_id text NOT NULL,
 name text NOT NULL DEFAULT 'config',description text NOT NULL DEFAULT '',application_ref text NOT NULL DEFAULT '',application_digest text NOT NULL DEFAULT '',
 definition jsonb NOT NULL DEFAULT '{}',validation jsonb NOT NULL DEFAULT '{"valid":true,"issues":[]}',created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now());
 INSERT INTO experiment_configurations(id,owner_user_id,definition) VALUES ('source','alice','{}'),('other','alice','{}'),('suite','alice','{"kind":"suite","variants":[{"configurationId":"source"},{"configurationId":"other"}]}');
 UPDATE experiments SET configuration_id='suite' WHERE id='experiment-0';
 UPDATE experiment_variants SET configuration_id=CASE WHEN position=1 THEN 'source' ELSE 'other' END WHERE experiment_id='experiment-0';
 UPDATE pipeline_run_stages SET operation_id='op-0' WHERE run_id='run-0';
 CREATE TABLE operations(id text PRIMARY KEY, workspace_id text NOT NULL DEFAULT 'workspace',plugin_id text NOT NULL DEFAULT 'plugin',plugin_version text NOT NULL DEFAULT '1',plugin_digest text NOT NULL DEFAULT '',title text NOT NULL DEFAULT 'op',status text NOT NULL DEFAULT 'succeeded',spec jsonb NOT NULL DEFAULT '{}',plan jsonb NOT NULL DEFAULT '{}',validation jsonb NOT NULL DEFAULT '{"valid":true}',plan_hash text NOT NULL DEFAULT '',cancel_requested boolean NOT NULL DEFAULT false,error text NOT NULL DEFAULT '',created_at timestamptz NOT NULL DEFAULT now(),queued_at timestamptz,started_at timestamptz,completed_at timestamptz);
 INSERT INTO operations(id) VALUES ('op-0');
 CREATE TABLE artifacts(id text PRIMARY KEY, operation_id text NOT NULL,step_id text,output_name text,name text NOT NULL DEFAULT 'data',artifact_type text NOT NULL DEFAULT 'TimeSeriesDataset',artifact_version text NOT NULL DEFAULT 'v1alpha1',media_type text NOT NULL DEFAULT 'application/json',storage_key text NOT NULL DEFAULT 'key',digest text NOT NULL DEFAULT 'digest',size_bytes bigint NOT NULL DEFAULT 1,storage_digest text NOT NULL DEFAULT 'digest',stored_size_bytes bigint NOT NULL DEFAULT 1,encryption_nonce bytea NOT NULL DEFAULT '',sensitive boolean NOT NULL DEFAULT false,verified_at timestamptz DEFAULT now(),created_at timestamptz NOT NULL DEFAULT now());
 INSERT INTO artifacts(id,operation_id) VALUES ('data-0','op-0');
 INSERT INTO artifacts(id,operation_id,sensitive) VALUES ('secret-0','op-0',true);

 `)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/changes/043-resource-sharing.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	access := func(kind, id, user string, want bool) {
		t.Helper()
		got, err := store.CanViewResource(ctx, kind, id, user)
		if err != nil || got != want {
			t.Fatalf("%s %s by %s: %v %v; want %v", kind, id, user, got, err, want)
		}
	}
	set := func(kind, id, owner, mode string, users ...string) {
		t.Helper()
		if err := store.SetSharing(ctx, kind, id, owner, false, Sharing{Visibility: mode, UserIDs: users}); err != nil {
			t.Fatal(err)
		}
	}
	access("experiments", "experiment-0", "bob", false)
	access("experiment-configurations", "source", "bob", false)
	access("experiments", "experiment-0", "alice", true)
	settings, _, err := store.GetSharing(ctx, "experiments", "experiment-0")
	if err != nil || settings.Visibility != "private" {
		t.Fatalf("default is not private: %+v %v", settings, err)
	}
	set("experiments", "experiment-0", "alice", "selected", "bob")
	for _, config := range []string{"suite", "source", "other"} {
		access("experiment-configurations", config, "bob", true)
		access("experiment-configurations", config, "carol", false)
	}
	access("pipeline-runs", "run-0", "bob", true)
	access("operations", "op-0", "bob", true)
	if err := store.SetSharing(ctx, "experiments", "experiment-0", "bob", false, Sharing{Visibility: "everyone"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reader changed sharing: %v", err)
	}
	// Validation is atomic: invalid users cannot remove the previous grant.
	if err := store.SetSharing(ctx, "experiments", "experiment-0", "alice", false, Sharing{Visibility: "selected", UserIDs: []string{"missing"}}); err == nil {
		t.Fatal("accepted unknown recipient")
	}
	access("experiments", "experiment-0", "bob", true)
	set("experiment-configurations", "source", "alice", "selected", "bob")
	set("experiments", "experiment-0", "alice", "private")
	access("experiments", "experiment-0", "bob", false)
	access("experiment-configurations", "source", "bob", true)
	access("experiment-configurations", "other", "bob", false)
	access("pipeline-runs", "run-0", "bob", false)
	access("operations", "op-0", "bob", false)
	set("experiments", "experiment-0", "alice", "everyone")
	access("experiments", "experiment-0", "future", true)
	access("experiment-configurations", "other", "future", true)
	// Sharing a configuration does not expose its experiments.
	set("experiments", "experiment-0", "alice", "private")
	set("experiment-configurations", "suite", "alice", "selected", "carol")
	access("experiment-configurations", "source", "carol", true)
	access("experiments", "experiment-0", "carol", false)
	configs, err := store.ListExperimentConfigurationsVisibleToUser(ctx, "carol")
	if err != nil || len(configs) != 3 {
		t.Fatalf("shared config list: %d %v", len(configs), err)
	}
	experiments, err := store.ListExperimentsVisibleToUser(ctx, "carol", 100)
	if err != nil || len(experiments) != 0 {
		t.Fatalf("private experiment list: %d %v", len(experiments), err)
	}
	set("experiments", "experiment-0", "alice", "selected", "carol")
	experiments, err = store.ListExperimentsVisibleToUser(ctx, "carol", 100)
	if err != nil || len(experiments) != 1 || experiments[0].ID != "experiment-0" {
		t.Fatalf("shared experiment list: %+v %v", experiments, err)
	}
	runs, err := store.ListPipelineRunsVisibleToUser(ctx, "carol", 100)
	if err != nil || len(runs) != 1 || runs[0].ID != "run-0" {
		t.Fatalf("shared run list: %+v %v", runs, err)
	}

	operations, err := store.ListOperationsVisibleToUser(ctx, "carol", 100)
	if err != nil || len(operations) != 1 {
		t.Fatalf("shared operations: %d %v", len(operations), err)
	}
	artifacts, err := store.ListAvailableArtifactsVisibleToUser(ctx, "carol", 100)
	if err != nil || len(artifacts) != 1 || artifacts[0].ID != "data-0" {
		t.Fatalf("shared non-sensitive artifacts: %+v %v", artifacts, err)
	}
	set("experiments", "experiment-0", "alice", "private")
	set("experiment-configurations", "suite", "alice", "private")
	access("experiment-configurations", "source", "carol", false)
	artifacts, err = store.ListAvailableArtifactsVisibleToUser(ctx, "carol", 100)
	if err != nil || len(artifacts) != 0 {
		t.Fatalf("revoked artifacts: %d %v", len(artifacts), err)
	}

	// Historical comparisons can link an operation directly without a pipeline run.
	if _, err := store.pool.Exec(ctx, `INSERT INTO operations(id) VALUES ('legacy-op'); INSERT INTO artifacts(id,operation_id) VALUES ('legacy-data','legacy-op'); UPDATE experiment_trials SET pipeline_run_id=NULL,operation_id='legacy-op',result_artifact_id='legacy-data' WHERE variant_id='variant-2-1';`); err != nil {
		t.Fatal(err)
	}
	set("experiments", "experiment-2", "alice", "selected", "carol")
	access("operations", "legacy-op", "carol", true)
	artifacts, err = store.ListAvailableArtifactsVisibleToUser(ctx, "carol", 100)
	if err != nil || len(artifacts) != 1 || artifacts[0].ID != "legacy-data" {
		t.Fatalf("legacy shared results: %+v %v", artifacts, err)
	}
	if err := store.SetSharing(ctx, "experiments", "experiment-0", "bob", true, Sharing{Visibility: "everyone"}); err != nil {
		t.Fatalf("administrator could not manage sharing: %v", err)
	}
}
