package storage

import (
	"context"
	"encoding/json"
	"kubephos.dev/kubephos/internal/domain"
	"os"
	"testing"
	"time"
)

func TestClusterMaintenanceSerializesExperimentAdmission(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `CREATE TABLE managed_resources(id text PRIMARY KEY,deleted_at timestamptz,recreation_pipeline_run_id text,deletion_pipeline_run_id text,deletion_operation_id text); INSERT INTO managed_resources(id) VALUES ('cluster');`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/changes/044-cluster-maintenance-guard.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	tx, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM managed_resources WHERE id='cluster' FOR UPDATE`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := store.pool.Exec(ctx, `INSERT INTO pipeline_runs(id,cluster_resource_id,status) VALUES ('new-run','cluster','queued')`)
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("experiment did not wait for maintenance lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err = tx.Exec(ctx, `UPDATE managed_resources SET recreation_pipeline_run_id='maintenance' WHERE id='cluster'`); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("experiment admitted during maintenance")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("experiment admission remained blocked")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE managed_resources SET recreation_pipeline_run_id=NULL WHERE id='cluster'; INSERT INTO pipeline_runs(id,cluster_resource_id,status) VALUES ('after-maintenance','cluster','queued')`); err != nil {
		t.Fatalf("experiment blocked after maintenance: %v", err)
	}
}

func TestReconfigurationKeepsOriginalResourceOwnership(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `CREATE TABLE infrastructure_resources(provider text,external_id text,workspace_id text,name text,kind text,state text,metadata jsonb,ownership text,protection text,created_by_operation_id text,updated_at timestamptz,last_seen_at timestamptz,PRIMARY KEY(provider,external_id)); INSERT INTO infrastructure_resources VALUES ('proxmox','qemu/100','workspace','node','virtual-machine','running','{}','managed','managed','original-operation',now(),now()),('proxmox','qemu/101','workspace','protected','virtual-machine','running','{}','imported','read-only',NULL,now(),now());`)
	if err != nil {
		t.Fatal(err)
	}
	resource := domain.DiscoveredResource{ExternalID: "qemu/100", Name: "node", Kind: "virtual-machine", State: "running", Metadata: json.RawMessage(`{"node":"pve-2"}`)}
	if err := store.UpdateManagedResource(ctx, "proxmox", "workspace", resource); err != nil {
		t.Fatal(err)
	}
	var creator, node string
	if err := store.pool.QueryRow(ctx, `SELECT created_by_operation_id,metadata->>'node' FROM infrastructure_resources WHERE external_id='qemu/100'`).Scan(&creator, &node); err != nil || creator != "original-operation" || node != "pve-2" {
		t.Fatalf("ownership or placement changed incorrectly: %s %s %v", creator, node, err)
	}
	resource.ExternalID = "qemu/101"
	if err := store.UpdateManagedResource(ctx, "proxmox", "workspace", resource); err == nil {
		t.Fatal("updated imported VM")
	}
	if _, err := store.pool.Exec(ctx, `UPDATE infrastructure_resources SET state='deleted' WHERE external_id='qemu/100'`); err != nil {
		t.Fatal(err)
	}
	if err := store.AuthorizeManagedCleanup(ctx, "proxmox", "qemu/100", "workspace"); err != nil {
		t.Fatal(err)
	}
	if err := store.AuthorizeManagedCleanup(ctx, "proxmox", "qemu/100", "other"); err == nil {
		t.Fatal("authorized other workspace")
	}
	if err := store.AuthorizeManagedResource(ctx, "proxmox", "qemu/100", "workspace"); err == nil {
		t.Fatal("authorized update of deleted VM")
	}
}
