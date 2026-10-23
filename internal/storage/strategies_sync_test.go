package storage

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// Run against an isolated PostgreSQL database with KUBEPHOS_TEST_DATABASE_URL.
func TestSynchronizeStrategyConfigurations(t *testing.T) {
	url := os.Getenv("KUBEPHOS_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("KUBEPHOS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(ctx)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	_, err = tx.Exec(ctx, `CREATE TEMP TABLE experiment_configurations (id text, workspace_id text, definition jsonb, updated_at timestamptz)`)
	if err != nil {
		t.Fatal(err)
	}
	definition := `{"applicationValues":{"keep":true},"components":[{"id":"scheduler","strategyId":"strategy","configuration":{"image":"mirror:old","configFile":"old","intervalSeconds":10,"obsolete":true,"targetOverrides":{"api":2}},"targets":{"include":["api"]}},{"id":"load","configuration":{"image":"load:old"}},{"id":"scheduler-2","strategyId":"strategy","configuration":{"image":"mirror:old","configFile":"old"}}]}`
	for _, workspace := range []string{"workspace", "other"} {
		if _, err := tx.Exec(ctx, `INSERT INTO experiment_configurations VALUES ($1,$1,$2,now())`, workspace, definition); err != nil {
			t.Fatal(err)
		}
	}
	for _, imageChanged := range []bool{false, true} {
		if _, err := tx.Exec(ctx, synchronizeStrategyConfigurationsSQL, "strategy", json.RawMessage(`{"configFile":"new","intervalSeconds":120}`), imageChanged, "source:new", "workspace", json.RawMessage(`{"configFile":"old","intervalSeconds":60,"obsolete":true}`)); err != nil {
			t.Fatal(err)
		}
		var raw []byte
		if err := tx.QueryRow(ctx, `SELECT definition FROM experiment_configurations WHERE id='workspace'`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var got struct {
			ApplicationValues map[string]bool `json:"applicationValues"`
			Components        []struct {
				ID            string              `json:"id"`
				Configuration map[string]any      `json:"configuration"`
				Targets       map[string][]string `json:"targets"`
			} `json:"components"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatal(err)
		}
		image := "mirror:old"
		if imageChanged {
			image = "source:new"
		}
		if len(got.Components) != 3 || got.Components[0].ID != "scheduler" || got.Components[1].ID != "load" || !got.ApplicationValues["keep"] {
			t.Fatalf("unrelated definition changed: %s", raw)
		}
		for _, index := range []int{0, 2} {
			if got.Components[index].Configuration["configFile"] != "old" || got.Components[index].Configuration["image"] != image {
				t.Fatalf("strategy not synchronized: %s", raw)
			}
		}
		if got.Components[0].Configuration["intervalSeconds"] != float64(10) || got.Components[2].Configuration["intervalSeconds"] != float64(120) {
			t.Fatalf("existing interval changed or missing parameter not added: %s", raw)
		}
		if _, exists := got.Components[0].Configuration["obsolete"]; exists {
			t.Fatalf("removed catalog parameter remains: %s", raw)
		}
		if got.Components[1].Configuration["image"] != "load:old" || len(got.Components[0].Targets["include"]) != 1 || got.Components[0].Configuration["targetOverrides"] == nil {
			t.Fatalf("experiment settings changed: %s", raw)
		}
		var untouched bool
		if err := tx.QueryRow(ctx, `SELECT definition=$1::jsonb FROM experiment_configurations WHERE id='other'`, definition).Scan(&untouched); err != nil || !untouched {
			t.Fatalf("other workspace changed: %v", err)
		}
	}
}
