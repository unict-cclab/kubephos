package storage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/catalog"
)

func TestIndependentModelCatalog(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	if _, err := store.pool.Exec(ctx, `CREATE TABLE catalog_applications(id text PRIMARY KEY,name text NOT NULL,description text NOT NULL,origin text NOT NULL,descriptor jsonb NOT NULL,digest text NOT NULL,enabled boolean NOT NULL DEFAULT true,created_at timestamptz NOT NULL DEFAULT now(),updated_at timestamptz NOT NULL DEFAULT now())`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/changes/041-model-catalog.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	apps, err := catalog.LoadDirectory("../../catalog/applications/localai")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCatalogApplications(ctx, apps); err != nil {
		t.Fatal(err)
	}
	model := catalog.Model{ID: "Custom-1B", URL: "https://models.example.org/custom.gguf", SHA256: strings.Repeat("a", 64), Size: 100, Backend: "llama-cpp", Quantization: "Q4_K_M", Enabled: true}
	if _, err := store.SaveCatalogModel(ctx, model, true); err != nil {
		t.Fatal(err)
	}
	descriptor, err := store.GetCatalogApplicationDescriptor(ctx, apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	var d catalog.Descriptor
	_ = json.Unmarshal(descriptor, &d)
	if len(d.Spec.ValuesSchema["properties"].(map[string]any)["models"].(map[string]any)["items"].(map[string]any)["enum"].([]any)) != 10 {
		t.Fatal("custom model not available to application materialization")
	}
	if _, err := store.SaveCatalogModel(ctx, model, true); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate result: %v", err)
	}
	model.URL = "https://models.example.org/updated.gguf"
	if _, err := store.SaveCatalogModel(ctx, model, false); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCatalogApplications(ctx, apps); err != nil {
		t.Fatal(err)
	}
	descriptor, err = store.GetCatalogApplicationDescriptor(ctx, apps[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(descriptor), model.URL) {
		t.Fatal("custom definition lost across restart sync")
	}
	if err := store.ArchiveCatalogModel(ctx, model.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SyncCatalogApplications(ctx, apps); err != nil {
		t.Fatal(err)
	}
	models, err := store.ListCatalogModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 10 {
		t.Fatal("archive must retain model record")
	}
	for _, m := range models {
		if m.ID == model.ID && m.Enabled {
			t.Fatal("archived model became enabled again")
		}
	}
	active := []catalog.Model{}
	for _, m := range models {
		if m.Enabled {
			active = append(active, m)
		}
	}
	for _, m := range active[:len(active)-1] {
		if err := store.ArchiveCatalogModel(ctx, m.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.ArchiveCatalogModel(ctx, active[len(active)-1].ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("last model archive: %v", err)
	}
	models, err = store.ListCatalogModels(ctx)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range models {
		if m.Enabled {
			count++
		}
	}
	if count != 1 {
		t.Fatal("failed archive was not rolled back")
	}
}
