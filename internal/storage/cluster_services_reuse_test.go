package storage

import (
	"context"
	"encoding/json"
	"errors"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/testutil"
	"strings"
	"testing"
)

func TestMaintenanceReusesOnlySuccessfulOriginalServices(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := store.pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO pipelines(id,workspace_id,name,definition,resolution,validation,pipeline_hash) VALUES('p','ws_default','cluster','{}','{}','{}',repeat('a',64));
 INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('provision','p','ws_default','provision','succeeded',repeat('a',64),'','');
 INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('cluster','ws_default','cluster','kubernetes-cluster','proxmox','test','1','{}','p','provision');`)
	names := []string{"machines", "kubernetes", "storage", "observability", "platform"}
	for i, name := range names {
		exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default',$2,$2,'succeeded','{}','{}','{}',repeat('a',64))`, "owner-"+name, name)
		exec(`INSERT INTO pipeline_run_stages(id,run_id,position,stage_id,plugin_id,title,status,operation_id) VALUES($1,'provision',$2,$3,$3,$3,'succeeded',$4)`, "old-"+name, i+1, name, "owner-"+name)
	}
	makeRun := func(suffix string) (domain.Pipeline, domain.PipelineRun) {
		p := domain.Pipeline{ID: "p-" + suffix, WorkspaceID: "ws_default", Name: suffix, Hash: strings.Repeat("b", 64), Validation: domain.ValidationReport{Valid: true}}
		r := domain.PipelineRun{ID: "r-" + suffix, PipelineID: p.ID, WorkspaceID: p.WorkspaceID, Name: suffix, PipelineHash: p.Hash}
		for i, name := range names {
			r.Stages = append(r.Stages, domain.PipelineRunStage{ID: suffix + name, Position: i + 1, StageID: name, PluginID: name, Title: name})
		}
		return p, r
	}
	// A service that failed or changed ownership is not silently adopted.
	exec(`UPDATE operations SET status='failed' WHERE id='owner-storage'`)
	p, r := makeRun("invalid")
	if err := store.CreateManagedClusterMaintenance(ctx, "cluster", "provision", json.RawMessage("{}"), p, r); !errors.Is(err, ErrConflict) {
		t.Fatalf("accepted incomplete service: %v", err)
	}
	exec(`UPDATE operations SET status='succeeded' WHERE id='owner-storage'`)
	p, r = makeRun("valid")
	if err := store.CreateManagedClusterMaintenance(ctx, "cluster", "provision", json.RawMessage("{}"), p, r); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetPipelineRun(ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, stage := range got.Stages {
		if stage.StageID == "machines" || stage.StageID == "kubernetes" {
			if stage.OperationID != "" || stage.Status != "pending" {
				t.Fatal("node maintenance skipped", stage)
			}
			continue
		}
		if stage.OperationID != "owner-"+stage.StageID || stage.Status != "succeeded" {
			t.Fatal("service reinstallation queued", stage)
		}
	}
}
