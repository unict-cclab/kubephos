package storage

import (
	"context"
	"encoding/json"
	"errors"
	"kubephos.dev/kubephos/internal/testutil"
	"testing"
	"time"
)

func TestPoolSettingsCanChangeDuringAnExperimentWithoutReplacingProvisioning(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	_, err = store.pool.Exec(ctx, `INSERT INTO pipelines(id,workspace_id,name,definition,resolution,validation,pipeline_hash) VALUES('p','ws_default','cluster','{}','{}','{}',repeat('a',64));
 INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('provision','p','ws_default','provision','succeeded',repeat('a',64),'','');
 INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('cluster','ws_default','cluster','kubernetes-cluster','proxmox','test','1','{"name":"cluster"}','p','provision');
 INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version,cluster_resource_id) VALUES('experiment','p','ws_default','experiment','running',repeat('a',64),'','','cluster');`)
	if err != nil {
		t.Fatal(err)
	}
	var updated time.Time
	_ = store.pool.QueryRow(ctx, `SELECT updated_at FROM managed_resources WHERE id='cluster'`).Scan(&updated)
	applied := false
	err = store.UpdateClusterSettings(ctx, "cluster", updated, json.RawMessage(`{"name":"cluster","labels":"changed"}`), func() error { applied = true; return nil })
	if err != nil || !applied {
		t.Fatal("metadata edit blocked by experiment", err)
	}
	var runID, status string
	_ = store.pool.QueryRow(ctx, `SELECT pipeline_run_id FROM managed_resources WHERE id='cluster'`).Scan(&runID)
	_ = store.pool.QueryRow(ctx, `SELECT status FROM pipeline_runs WHERE id='experiment'`).Scan(&status)
	if runID != "provision" || status != "running" {
		t.Fatal("modified an existing lifecycle", runID, status)
	}
	applied = false
	if err = store.UpdateClusterSettings(ctx, "cluster", updated, json.RawMessage(`{}`), func() error { applied = true; return nil }); !errors.Is(err, ErrConflict) || applied {
		t.Fatal("stale editor overwrote settings", err)
	}
	_ = store.pool.QueryRow(ctx, `SELECT updated_at FROM managed_resources WHERE id='cluster'`).Scan(&updated)
	err = store.UpdateClusterSettings(ctx, "cluster", updated, json.RawMessage(`{"lost":true}`), func() error { return errors.New("SSH unavailable") })
	if err == nil {
		t.Fatal("accepted failed live edit")
	}
	var raw string
	_ = store.pool.QueryRow(ctx, `SELECT spec::text FROM managed_resources WHERE id='cluster'`).Scan(&raw)
	var data map[string]any
	_ = json.Unmarshal([]byte(raw), &data)
	if data["lost"] != nil {
		t.Fatal("persisted failed update", raw)
	}
}
