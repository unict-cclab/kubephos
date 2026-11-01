package storage

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/testutil"
)

func TestManagedResourceCreationWithConnectivitySchema(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	if _, err := store.pool.Exec(ctx, `INSERT INTO provider_connections(id,name,provider,plugin_id,configuration) VALUES('conn','Proxmox','proxmox','test','{}')`); err != nil {
		t.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	resource := func(id, kind string) domain.ManagedResource {
		return domain.ManagedResource{ID: id, WorkspaceID: "ws_default", Name: id, Kind: kind, Provider: "proxmox", ConnectionID: "conn", PluginID: "test", PluginVersion: "1.0.0", Spec: json.RawMessage(`{}`)}
	}
	t.Run("operation-backed template", func(t *testing.T) {
		created, err := store.CreateManagedResource(ctx, resource("template", "machine-template"), domain.Operation{ID: "op-template", WorkspaceID: "ws_default", PluginID: "test", PluginVersion: "1.0.0", Title: "Create template", Status: domain.OperationQueued, Spec: json.RawMessage(`{}`), PlanHash: hash, Validation: domain.ValidationReport{Valid: true}})
		if err != nil {
			t.Fatal(err)
		}
		if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() || created.OperationID != "op-template" || created.Connectivity != nil {
			t.Fatalf("invalid created resource: %+v", created)
		}
		if _, err := store.GetManagedResource(ctx, created.ID); err != nil {
			t.Fatal(err)
		}
	})
	for _, kind := range []string{"kubernetes-cluster", "harbor", "nfs"} {
		t.Run("pipeline-backed "+kind, func(t *testing.T) {
			pipeline := domain.Pipeline{ID: "pipe-" + kind, WorkspaceID: "ws_default", Name: "Create " + kind, Hash: hash, Validation: domain.ValidationReport{Valid: true}, Resolution: domain.PipelineResolution{Result: domain.ArtifactContract{Type: "ClusterConnection", Version: "v1alpha1"}}}
			run := domain.PipelineRun{ID: "run-" + kind, PipelineID: pipeline.ID, WorkspaceID: pipeline.WorkspaceID, Name: pipeline.Name, PipelineHash: hash, ResultType: "ClusterConnection", ResultVersion: "v1alpha1"}
			dependencies := []domain.ManagedResourceDependency{{ResourceID: kind, DependsOnID: "template", Relation: "machine-template"}}
			created, err := store.CreateManagedPipelineResource(ctx, resource(kind, kind), pipeline, run, dependencies)
			if err != nil {
				t.Fatal(err)
			}
			if created.CreatedAt.IsZero() || created.PipelineRunID != run.ID || created.Status != "provisioning" || created.Connectivity != nil {
				t.Fatalf("invalid created resource: %+v", created)
			}
			saved, err := store.GetManagedResource(ctx, created.ID)
			if err != nil || saved.PipelineRunID != run.ID {
				t.Fatalf("read created resource: %+v %v", saved, err)
			}
			var count int
			if err := store.pool.QueryRow(ctx, `SELECT count(*) FROM managed_resource_dependencies WHERE resource_id=$1`, created.ID).Scan(&count); err != nil || count != 1 {
				t.Fatalf("dependencies not committed: %d %v", count, err)
			}
		})
	}
}
