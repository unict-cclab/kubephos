package storage

import (
	"context"
	"errors"
	"testing"
)

func TestProvisioningLockSerializesSameConnection(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	release, err := store.LockProvisioning(ctx, "pve")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if unlock, err := store.LockProvisioning(ctx, "pve"); !errors.Is(err, ErrConflict) {
		if unlock != nil {
			unlock()
		}
		t.Fatalf("concurrent request admitted: %v", err)
	}
	other, err := store.LockProvisioning(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	other()
	release()
	again, err := store.LockProvisioning(ctx, "pve")
	if err != nil {
		t.Fatal(err)
	}
	again()
}

func TestProvisioningClaimsIncludePendingAndExcludeDeletedResources(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `CREATE TABLE managed_resources(id text,connection_id text,kind text,spec jsonb,deleted_at timestamptz,created_at timestamptz DEFAULT now());
 INSERT INTO managed_resources(id,connection_id,kind,spec,deleted_at) VALUES
 ('pending','pve','nfs','{"vmid":250}',NULL),
 ('deleted','pve','nfs','{"vmid":251}',now()),
 ('other','other','nfs','{"vmid":252}',NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := store.ProvisioningClaims(ctx, "pve")
	if err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].Kind != "nfs" {
		t.Fatalf("unexpected reservations %+v", claims)
	}
}
