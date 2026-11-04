package storage

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/testutil"
)

func TestApplicationDeploymentNamespaceReservation(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	seed := `INSERT INTO pipelines(id,workspace_id,name,pipeline_hash,definition,resolution,validation) VALUES('base','ws_default','Base',repeat('a',64),'{}','{}','{}');
 INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('cluster-run','base','ws_default','Cluster','succeeded',repeat('a',64),'','');
 INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('cluster','ws_default','Cluster','kubernetes-cluster','kubernetes','test','1','{}','base','cluster-run');`
	if _, err := store.pool.Exec(ctx, seed); err != nil {
		t.Fatal(err)
	}
	create := func(name, namespace string) (domain.ManagedResource, error) {
		hash := strings.Repeat("a", 64)
		pipeline := domain.Pipeline{ID: "pipe-" + name, WorkspaceID: "ws_default", Name: name, Hash: hash, Validation: domain.ValidationReport{Valid: true}}
		run := domain.PipelineRun{ID: "run-" + name, PipelineID: pipeline.ID, WorkspaceID: "ws_default", Name: name, PipelineHash: hash, ClusterResourceID: "cluster", ApplicationNamespace: namespace, IsolationMode: "namespaced"}
		spec, _ := json.Marshal(map[string]any{"clusterResourceId": "cluster", "definition": map[string]string{"namespace": namespace}})
		return store.CreateManagedPipelineResource(ctx, domain.ManagedResource{ID: name, WorkspaceID: "ws_default", Name: name, Kind: "application-deployment", Provider: "kubernetes", PluginID: "test", PluginVersion: "1", Spec: spec}, pipeline, run, []domain.ManagedResourceDependency{{ResourceID: name, DependsOnID: "cluster", Relation: "cluster"}})
	}
	experiment := func(name, namespace, status string) error {
		_, err := store.pool.Exec(ctx, `INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version,cluster_resource_id,isolation_mode,application_namespace) VALUES($1,'base','ws_default',$1,$3,repeat('a',64),'','','cluster','namespaced',$2)`, name, namespace, status)
		return err
	}
	if err := experiment("experiment", "busy", "running"); err != nil {
		t.Fatal(err)
	}
	if _, err := create("conflict", "busy"); err == nil {
		t.Fatal("deployment reused an experiment namespace")
	}
	if _, err := store.GetPipeline(ctx, "pipe-conflict"); err == nil {
		t.Fatal("failed reservation left an orphan pipeline")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE pipeline_runs SET initialized_at=now(),pause_before_stage='load' WHERE id='experiment'`); err != nil {
		t.Fatal(err)
	}
	if _, err := create("initialized-conflict", "busy"); err == nil {
		t.Fatal("initialized experiment lost its reservation")
	}
	if _, err := create("deployment", "app"); err != nil {
		t.Fatal(err)
	}
	saved, err := store.GetPipelineRun(ctx, "run-deployment")
	if err != nil || saved.ApplicationNamespace != "app" || saved.ClusterResourceID != "cluster" {
		t.Fatal("run admission fields lost", saved, err)
	}
	if err := experiment("blocked", "app", "running"); err == nil {
		t.Fatal("experiment reused a deployment namespace")
	}
	if err := store.CheckApplicationNamespaceAvailable(ctx, "cluster", "app"); err == nil {
		t.Fatal("reservation not reported")
	}
	if err := experiment("queued", "app", "queued"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE pipeline_runs SET status='running' WHERE id='queued'`); err == nil {
		t.Fatal("queued experiment bypassed reservation on start")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE pipeline_runs SET status='running' WHERE id='run-deployment'`); err != nil {
		t.Fatal("deployment blocked by its own reservation", err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE pipeline_runs SET status='succeeded' WHERE id='run-deployment'`); err != nil {
		t.Fatal(err)
	}
	if _, err := create("duplicate", "app"); err == nil {
		t.Fatal("duplicate namespace accepted after provisioning completed")
	}
	if err := store.CheckApplicationNamespaceAvailable(ctx, "cluster", "other"); err != nil {
		t.Fatal("unrelated namespace blocked", err)
	}
	if err := experiment("cleanup", "", "failed"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `UPDATE managed_resources SET deletion_pipeline_id='base',deletion_pipeline_run_id='cleanup' WHERE id='deployment'`); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckApplicationNamespaceAvailable(ctx, "cluster", "app"); err == nil {
		t.Fatal("failed cleanup released namespace")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE pipeline_runs SET status='succeeded' WHERE id='cleanup'`); err != nil {
		t.Fatal(err)
	}
	if err := store.CheckApplicationNamespaceAvailable(ctx, "cluster", "app"); err != nil {
		t.Fatal("completed cleanup did not release namespace", err)
	}
	// Failed immutable preparation can be dismissed; possible namespace mutations cannot.
	for _, tc := range []struct {
		name     string
		mutating bool
	}{{"safe-dismiss", false}, {"unsafe-dismiss", true}} {
		if _, err := create(tc.name, tc.name); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `UPDATE pipeline_runs SET status='failed' WHERE id=$1`, "run-"+tc.name); err != nil {
			t.Fatal(err)
		}
		plan, _ := json.Marshal(map[string]any{"steps": []map[string]any{{"mutating": tc.mutating}}})
		if _, err := store.pool.Exec(ctx, `INSERT INTO operations(id,workspace_id,plugin_id,plugin_version,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default','test','1','Prepare','failed','{}',$2,'{}',repeat('a',64))`, "op-"+tc.name, plan); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO pipeline_run_stages(id,run_id,position,stage_id,plugin_id,title,status,operation_id) VALUES($1,$2,1,'prepare','test','Prepare','failed',$3)`, "stage-"+tc.name, "run-"+tc.name, "op-"+tc.name); err != nil {
			t.Fatal(err)
		}
		if _, err := store.pool.Exec(ctx, `INSERT INTO operation_steps(id,operation_id,position,name,status,input) VALUES($1,$2,1,'Prepare','failed','{}')`, "step-"+tc.name, "op-"+tc.name); err != nil {
			t.Fatal(err)
		}
		err := store.DismissFailedApplicationDeployment(ctx, tc.name)
		if (err != nil) != tc.mutating {
			t.Fatal("unsafe dismissal result", tc.name, err)
		}
	}
	// Two requests racing for a namespace commit exactly one complete lifecycle.
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"race-one", "race-two"} {
		wg.Add(1)
		go func(name string) { defer wg.Done(); _, err := create(name, "race"); results <- err }(name)
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatalf("concurrent reservations committed %d lifecycles", success)
	}
}
