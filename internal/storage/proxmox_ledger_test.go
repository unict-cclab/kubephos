package storage

import (
	"context"
	"encoding/json"
	"errors"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/resourceidentity"
	"os"
	"testing"
)

func TestProxmoxLedgerMigrationAndIndependentVMIDLifecycles(t *testing.T) {
	s, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := s.pool.Exec(ctx, `CREATE TABLE operations(id text PRIMARY KEY,spec jsonb); CREATE TABLE infrastructure_resources(id text PRIMARY KEY,provider text,external_id text,workspace_id text,kind text,name text,state text,ownership text,protection text,metadata jsonb DEFAULT '{}',created_by_operation_id text,last_seen_at timestamptz DEFAULT now(),created_at timestamptz DEFAULT now(),updated_at timestamptz DEFAULT now(), UNIQUE(provider,external_id)); INSERT INTO operations VALUES('old','{"connectionRef":"conn_a"}'); INSERT INTO infrastructure_resources(id,provider,external_id,workspace_id,kind,name,state,ownership,protection,created_by_operation_id) VALUES('old-resource','proxmox','qemu/100','workspace','virtual-machine','old','running','managed','managed','old'),('legacy-import','proxmox','qemu/200',NULL,'virtual-machine','unknown','running','imported','read-only',NULL);`)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/changes/050-scope-proxmox-ledger.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err = s.pool.Exec(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := resourceidentity.LedgerID("proxmox", json.RawMessage(`{"connectionRef":"conn_a"}`), "qemu/100")
	b, _ := resourceidentity.LedgerID("proxmox", json.RawMessage(`{"connectionRef":"conn_b"}`), "qemu/100")
	var migrated string
	if err = s.pool.QueryRow(ctx, `SELECT external_id FROM infrastructure_resources WHERE id='old-resource'`).Scan(&migrated); err != nil || migrated != a {
		t.Fatalf("incorrect existing mapping: %s %v", migrated, err)
	}
	if err = s.EnsureResourceAvailable(ctx, "proxmox", a); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-connection conflict lost: %v", err)
	}
	if err = s.EnsureResourceAvailable(ctx, "proxmox", b); err != nil {
		t.Fatal(err)
	}
	resource := domain.InfrastructureResource{ID: "new-resource", Provider: "proxmox", ExternalID: b, WorkspaceID: "workspace", Kind: "virtual-machine", Name: "other-cluster"}
	if _, err = s.ReserveManagedResources(ctx, []domain.InfrastructureResource{resource}, "new"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ReserveManagedResources(ctx, []domain.InfrastructureResource{resource}, "conflicting"); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate reservation accepted: %v", err)
	}
	discovered := domain.DiscoveredResource{ExternalID: b, Kind: "virtual-machine", Name: "other-cluster", State: "running"}
	if err = s.CompleteManagedResource(ctx, "new", "proxmox", discovered); err != nil {
		t.Fatal(err)
	}
	discovered.Name = "updated-other"
	discovered.Metadata = json.RawMessage(`{"node":"pve2"}`)
	if err = s.UpdateManagedResource(ctx, "proxmox", "workspace", discovered); err != nil {
		t.Fatal(err)
	}
	if err = s.AuthorizeManagedCleanup(ctx, "proxmox", a, "workspace"); err != nil {
		t.Fatal(err)
	}
	if err = s.SetManagedResourceState(ctx, "proxmox", a, "workspace", "deleted"); err != nil {
		t.Fatal(err)
	}
	var state, name string
	if err = s.pool.QueryRow(ctx, `SELECT state,name FROM infrastructure_resources WHERE external_id=$1`, b).Scan(&state, &name); err != nil || state != "running" || name != "updated-other" {
		t.Fatalf("cross-connection modification: %s %s %v", state, name, err)
	}
	if err = s.AuthorizeManagedResource(ctx, "proxmox", "conn_b/qemu/200", "workspace"); !errors.Is(err, ErrConflict) {
		t.Fatal("unscoped import authorized scoped change")
	}
}
