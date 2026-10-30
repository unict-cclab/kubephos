package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

func TestConnectivityClaimsRetryRecoveryAndAttribution(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `CREATE TABLE provider_connections(id text PRIMARY KEY,updated_at timestamptz NOT NULL DEFAULT now()); CREATE TABLE managed_resources(id text PRIMARY KEY,updated_at timestamptz NOT NULL DEFAULT now()); INSERT INTO provider_connections(id)VALUES('pve');`)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../migrations/changes/051-resource-connectivity.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, string(raw)); err != nil {
		t.Fatal(err)
	}
	var original time.Time
	if err = store.pool.QueryRow(ctx, `SELECT updated_at FROM provider_connections WHERE id='pve'`).Scan(&original); err != nil {
		t.Fatal(err)
	}
	check := func(lease string, status string, want string) {
		t.Helper()
		claimed, err := store.ClaimConnectivityCheck(ctx, true, "pve", lease)
		if err != nil || !claimed {
			t.Fatalf("claim: %t %v", claimed, err)
		}
		if other, err := store.ClaimConnectivityCheck(ctx, true, "pve", "other-worker"); err != nil || other {
			t.Fatalf("concurrent monitor claimed same connection: %t %v", other, err)
		}
		if err = store.SaveConnectivity(ctx, true, "pve", lease, domain.Connectivity{Status: status}); err != nil {
			t.Fatal(err)
		}
		var raw []byte
		var updated time.Time
		if err = store.pool.QueryRow(ctx, `SELECT connectivity,updated_at FROM provider_connections WHERE id='pve'`).Scan(&raw, &updated); err != nil {
			t.Fatal(err)
		}
		var report domain.Connectivity
		if err = json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		if report.Status != want || !updated.Equal(original) {
			t.Fatalf("unexpected status or attribution: %+v %v", report, updated)
		}
		if _, err = store.pool.Exec(ctx, `UPDATE provider_connections SET connectivity_next_check_at=now()-interval '1 second' WHERE id='pve'`); err != nil {
			t.Fatal(err)
		}
	}
	check("first", "unreachable", "checking")
	check("second", "unreachable", "unreachable")
	check("recovered", "reachable", "reachable")
	if err = store.SaveConnectivity(ctx, true, "pve", "second", domain.Connectivity{Status: "unreachable"}); err != nil {
		t.Fatal(err)
	}
	var status string
	if err = store.pool.QueryRow(ctx, `SELECT connectivity->>'status' FROM provider_connections WHERE id='pve'`).Scan(&status); err != nil || status != "reachable" {
		t.Fatalf("stale check overwrote recovery: %s %v", status, err)
	}
}

func TestConnectivityCheckLeaseIsExclusiveAcrossWorkers(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `CREATE TABLE managed_resources(id text PRIMARY KEY, connectivity_next_check_at timestamptz NOT NULL DEFAULT now(),connectivity_lease_id text);INSERT INTO managed_resources(id)VALUES('cluster');`)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan bool, 8)
	for index := 0; index < 8; index++ {
		go func(index int) {
			<-start
			claimed, err := store.ClaimConnectivityCheck(ctx, false, "cluster", fmt.Sprint(index))
			if err != nil {
				t.Error(err)
			}
			results <- claimed
		}(index)
	}
	close(start)
	claimed := 0
	for range 8 {
		if <-results {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("expected one monitor, got %d", claimed)
	}
}
