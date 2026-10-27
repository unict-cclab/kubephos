package api

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
)

func TestMeshRetryReusesOnlyCompletedClusterStages(t *testing.T) {
	for _, broken := range []bool{false, true} {
		t.Run(map[bool]string{false: "healthy_clusters", true: "failed_cluster_operation"}[broken], func(t *testing.T) {
			server, pool := transferServer(t, "")
			ctx := context.Background()
			exec := func(sql string, args ...any) {
				t.Helper()
				if _, err := pool.Exec(ctx, sql, args...); err != nil {
					t.Fatal(err)
				}
			}
			exec(`INSERT INTO pipelines(id,workspace_id,name,definition,resolution,validation,pipeline_hash) VALUES('original','ws_default','original','{}','{}','{}',repeat('a',64))`)
			exec(`INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('source','original','ws_default','Provision test','failed',repeat('a',64),'ClusterEnvironment','v1alpha1')`)
			exec(`INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('cluster','ws_default','test','kubernetes-cluster','proxmox','mesh','v1','{"mode":"multi"}','original','source')`)
			ids := []string{"c1-kubernetes", "c2-kubernetes", "mesh"}
			plugins := []string{"k3s", "k3s", "io.kubephos.cluster.cilium.mesh"}
			source := domain.PipelineRun{ID: "source", Status: domain.OperationFailed}
			definition := domain.PipelineDefinition{}
			for i, stageID := range ids {
				status := "succeeded"
				if i == 2 {
					status = "failed"
				}
				exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default',$2,$1,$3,'{}','{}','{}',repeat('a',64))`, "op-"+stageID, plugins[i], status)
				exec(`INSERT INTO pipeline_run_stages(id,run_id,position,stage_id,plugin_id,title,status,operation_id) VALUES($1,'source',$2,$3,$4,$3,$5,$6)`, "old-"+stageID, i+1, stageID, plugins[i], status, "op-"+stageID)
				definition.Stages = append(definition.Stages, domain.PipelineStage{ID: stageID, PluginID: plugins[i], Title: stageID, Spec: json.RawMessage(`{}`)})
				source.Stages = append(source.Stages, domain.PipelineRunStage{StageID: stageID, PluginID: plugins[i], Status: status, OperationID: "op-" + stageID})
			}
			if !canRetryClusterMesh(source) {
				t.Fatal("eligible final-stage failure was rejected")
			}
			if broken {
				exec(`UPDATE operations SET status='failed' WHERE id='op-c2-kubernetes'`)
			}
			pipeline := domain.Pipeline{ID: "retry-pipeline", WorkspaceID: "ws_default", Name: "mesh retry", Hash: strings.Repeat("b", 64), Definition: definition, Resolution: domain.PipelineResolution{Result: domain.ArtifactContract{Type: "ClusterEnvironment", Version: "v1alpha1"}}}
			run := pipelineRunForManagedPipeline(pipeline, "Retry mesh test")
			err := server.store.RetryManagedClusterMesh(ctx, "cluster", "source", json.RawMessage(`{"mode":"multi"}`), pipeline, run)
			if broken {
				if !errors.Is(err, storage.ErrConflict) {
					t.Fatalf("reused failed operation: %v", err)
				}
				var count int
				if err := pool.QueryRow(ctx, `SELECT count(*) FROM pipelines WHERE id='retry-pipeline'`).Scan(&count); err != nil || count != 0 {
					t.Fatal("rejected retry left partial records")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			saved, err := server.store.GetPipelineRun(ctx, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			for i, stage := range saved.Stages {
				if i < 2 && (stage.Status != "succeeded" || stage.OperationID != "op-"+ids[i]) {
					t.Fatalf("cluster stage would rerun: %+v", stage)
				}
				if i == 2 && (stage.Status != "pending" || stage.OperationID != "") {
					t.Fatalf("mesh must receive a new operation: %+v", stage)
				}
			}
			if err := server.store.RetryManagedClusterMesh(ctx, "cluster", "source", json.RawMessage(`{}`), pipeline, run); !errors.Is(err, storage.ErrConflict) {
				t.Fatalf("duplicate resume accepted: %v", err)
			}
		})
	}
}

func TestMeshRetryRejectsIncompleteProvisioning(t *testing.T) {
	run := domain.PipelineRun{Status: domain.OperationFailed, Stages: []domain.PipelineRunStage{{StageID: "kubernetes", Status: "succeeded", OperationID: "op"}, {StageID: "mesh", PluginID: "io.kubephos.cluster.cilium.mesh", Status: "failed"}}}
	if !canRetryClusterMesh(run) {
		t.Fatal("valid mesh retry rejected")
	}
	run.Stages[0].Status = "failed"
	if canRetryClusterMesh(run) {
		t.Fatal("failed Kubernetes stage was reused")
	}
	run.Stages[0].Status = "succeeded"
	run.Stages[1].StageID = "observability"
	if canRetryClusterMesh(run) {
		t.Fatal("non-mesh failure was reused")
	}
}
