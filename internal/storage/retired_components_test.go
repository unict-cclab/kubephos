package storage

import (
	"context"
	"os"
	"testing"
)

func TestRetiredComponentsPreserveHistoricalDefinitions(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `
 CREATE TABLE strategy_catalog(id text PRIMARY KEY, kind text CONSTRAINT strategy_catalog_kind_check CHECK(kind IN ('scheduler','descheduler','autoscaler','orchestrator')));
 INSERT INTO strategy_catalog VALUES ('old','orchestrator'),('scheduler','scheduler'),('descheduler','descheduler'),('autoscaler','autoscaler');
 CREATE TABLE experiment_configurations(id text PRIMARY KEY,definition jsonb,validation jsonb);
 INSERT INTO experiment_configurations VALUES
 ('retired','{"components":[{"capability":"orchestrator.kubernetes.install","strategyId":"old","configuration":{"parameters":"saved"}}]}','{"valid":true}'),
 ('current','{"components":[{"capability":"scheduler.kubernetes.install"}]}','{"valid":true}'),
 ('suite','{"kind":"suite","variants":[{"configurationId":"retired"},{"configurationId":"current"}]}','{"valid":true}'),
 ('other-suite','{"kind":"suite","variants":[{"configurationId":"current"}]}','{"valid":true}');
 CREATE TEMP TABLE original_definitions AS SELECT id,definition FROM experiment_configurations;
 CREATE TEMP TABLE original_history AS SELECT id,configuration_snapshot FROM experiments;
 `)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/changes/048-retire-placement-controllers.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	var unchanged bool
	if err = store.pool.QueryRow(ctx, `SELECT NOT EXISTS(SELECT 1 FROM experiment_configurations c JOIN original_definitions o USING(id) WHERE c.definition<>o.definition) AND NOT EXISTS(SELECT 1 FROM experiments e JOIN original_history o USING(id) WHERE e.configuration_snapshot IS DISTINCT FROM o.configuration_snapshot)`).Scan(&unchanged); err != nil || !unchanged {
		t.Fatalf("historical definitions changed: %v", err)
	}
	rows, err := store.pool.Query(ctx, `SELECT id,(validation->>'valid')::boolean FROM experiment_configurations`)
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"retired": false, "suite": false, "current": true, "other-suite": true}
	for rows.Next() {
		var id string
		var valid bool
		if err = rows.Scan(&id, &valid); err != nil {
			t.Fatal(err)
		}
		if valid != expected[id] {
			t.Errorf("%s valid=%v", id, valid)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM strategy_catalog`).Scan(&count); err != nil || count != 3 {
		t.Fatalf("remaining strategies=%d: %v", count, err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO strategy_catalog VALUES ('unsupported','orchestrator')`); err == nil {
		t.Fatal("retired category accepted")
	}
}
