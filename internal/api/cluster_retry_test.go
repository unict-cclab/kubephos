package api

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
)

func TestClusterRetryCleanupIncludesPartialProvisioning(t *testing.T) {
	run := domain.PipelineRun{Stages: []domain.PipelineRunStage{{StageID: "c1-machines", Title: "First machines", OperationID: "machines", Status: "succeeded"}, {StageID: "c1-kubernetes", Title: "First Kubernetes", OperationID: "kubernetes", Status: "succeeded"}, {StageID: "c1-observability", Title: "Monitoring", OperationID: "monitoring", Status: "failed"}, {StageID: "c2-machines", Status: "pending"}, {StageID: "prune-machines", OperationID: "already-pruned", Status: "succeeded"}}}
	targets, err := clusterRecreationCleanupTargets(run, domain.PipelineDefinition{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, target := range targets {
		ids = append(ids, target.OperationID)
	}
	if !reflect.DeepEqual(ids, []string{"monitoring", "kubernetes", "machines"}) {
		t.Fatalf("unsafe cleanup order: %v", ids)
	}
}

func TestClusterRetryResumesUnfinishedCleanupInOriginalOrder(t *testing.T) {
	run := domain.PipelineRun{Stages: []domain.PipelineRunStage{{StageID: "cleanup-platform", Title: "Remove platform", Status: "succeeded", OperationID: "cleanup-platform-op"}, {StageID: "cleanup-kubernetes", Title: "Remove Kubernetes", Status: "failed", OperationID: "cleanup-kubernetes-op"}, {StageID: "cleanup-machines", Title: "Remove VMs", Status: "pending"}, {StageID: "machines", Status: "pending"}}}
	definition := domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "cleanup-kubernetes", Spec: json.RawMessage(`{"sourceOperationId":"original-kubernetes"}`)}, {ID: "cleanup-machines", Spec: json.RawMessage(`{"sourceOperationId":"original-machines"}`)}}}
	targets, err := clusterRecreationCleanupTargets(run, definition)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].OperationID != "original-kubernetes" || targets[1].OperationID != "original-machines" || targets[0].StageID != "cleanup-kubernetes" {
		t.Fatalf("unfinished cleanup lost its ownership or order: %+v", targets)
	}
	if _, err := clusterRecreationCleanupTargets(run, domain.PipelineDefinition{}); err == nil {
		t.Fatal("retry accepted missing original cleanup targets")
	}
}

func TestClusterRetryRetainsSavedConfigurationAndSerializesAdmission(t *testing.T) {
	for _, status := range []string{"failed", "canceled", "succeeded", "running"} {
		t.Run(status, func(t *testing.T) {
			server, pool := transferServer(t, "")
			ctx := context.Background()
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`INSERT INTO pipelines(id,workspace_id,name,definition,resolution,validation,pipeline_hash) VALUES('original-pipeline','ws_default','original','{}','{}','{}',repeat('a',64))`)
			exec(`INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('original-run','original-pipeline','ws_default','Provision test',$1,repeat('a',64),'ClusterEnvironment','v1alpha1')`, status)
			spec := json.RawMessage(`{"name":"test","mode":"multi","members":[{"name":"a","baseVMID":103,"machineNodes":{"a-master":"minis01"}},{"name":"b","baseVMID":107,"addressStart":"192.168.0.107"}]}`)
			exec(`INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('cluster','ws_default','test','kubernetes-cluster','proxmox','mesh','v1',$1,'original-pipeline','original-run')`, spec)
			makeRun := func(suffix string) (domain.Pipeline, domain.PipelineRun) {
				p := domain.Pipeline{ID: "pipe-" + suffix, WorkspaceID: "ws_default", Name: "Retry " + suffix, Hash: strings.Repeat("b", 64), Resolution: domain.PipelineResolution{Result: domain.ArtifactContract{Type: "ClusterEnvironment", Version: "v1alpha1"}}}
				return p, pipelineRunForManagedPipeline(p, "Recreate test")
			}
			pipeline, run := makeRun("first")
			if status == "running" {
				if err := server.store.CreateManagedResourceRecreation(ctx, "cluster", "original-run", spec, pipeline, run); !errors.Is(err, storage.ErrConflict) {
					t.Fatalf("active provisioning was accepted: %v", err)
				}
				return
			}
			if err := server.store.CreateManagedResourceRecreation(ctx, "cluster", "original-run", spec, pipeline, run); err != nil {
				t.Fatal(err)
			}
			resource, err := server.store.GetManagedResource(ctx, "cluster")
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			_ = json.Unmarshal(spec, &before)
			_ = json.Unmarshal(resource.Spec, &after)
			if resource.ID != "cluster" || resource.Name != "test" || resource.Status != "recreating" || !reflect.DeepEqual(before, after) {
				t.Fatalf("retry changed identity or configuration: %+v", resource)
			}
			duplicate, duplicateRun := makeRun("duplicate")
			if err := server.store.CreateManagedResourceRecreation(ctx, "cluster", "original-run", spec, duplicate, duplicateRun); !errors.Is(err, storage.ErrConflict) {
				t.Fatalf("duplicate retry accepted: %v", err)
			}
			exec(`UPDATE pipeline_runs SET status='failed' WHERE id=$1`, run.ID)
			pipeline2, run2 := makeRun("second")
			if err := server.store.CreateManagedResourceRecreation(ctx, "cluster", run.ID, spec, pipeline2, run2); err != nil {
				t.Fatalf("could not retry failed recreation: %v", err)
			}
			exec(`UPDATE pipeline_runs SET status='failed' WHERE id=$1`, run2.ID)
			exec(`INSERT INTO users(id,username,role,external_id) VALUES('admin','admin','admin','keycloak-admin')`)
			exec(`INSERT INTO experiment_configurations(id,workspace_id,cluster_resource_id,name,application_ref,application_digest,definition,validation,owner_user_id) VALUES('configuration','ws_default','cluster','test','app:test','sha256:test','{}','{}','admin')`)
			exec(`INSERT INTO experiments(id,workspace_id,name,status,result_type,result_version,owner_user_id) VALUES('experiment','ws_default','active','running','TimeSeriesDataset','v1alpha1','admin')`)
			exec(`INSERT INTO experiment_variants(id,experiment_id,position,name,display_alias,configuration_id) VALUES('variant','experiment',1,'active','active','configuration')`)
			blocked, blockedRun := makeRun("blocked")
			if err := server.store.CreateManagedResourceRecreation(ctx, "cluster", run2.ID, spec, blocked, blockedRun); !errors.Is(err, storage.ErrConflict) {
				t.Fatalf("active experiment did not block retry: %v", err)
			}
			var retainedRun string
			if err := pool.QueryRow(ctx, `SELECT recreation_pipeline_run_id FROM managed_resources WHERE id='cluster'`).Scan(&retainedRun); err != nil || retainedRun != run2.ID {
				t.Fatalf("failed validation discarded previous retry: %s %v", retainedRun, err)
			}
			var old string
			if err := pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id='original-run'`).Scan(&old); err != nil || old != status {
				t.Fatalf("original history changed: %s %v", old, err)
			}
		})
	}
}
