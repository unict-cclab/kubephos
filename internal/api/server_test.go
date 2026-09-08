package api

import (
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

func TestResolvedPlanHashIsDeterministic(t *testing.T) {
	manifest := plugins.Manifest{ID: "test", Version: "1.0.0"}
	spec := json.RawMessage(`{"value":1}`)
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "one", Name: "One", Input: json.RawMessage(`{}`)}}}
	first, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil {
		t.Fatal(err)
	}
	second, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("expected stable hash, got %s and %s", first, second)
	}
	if len(first) != 64 {
		t.Fatalf("expected SHA-256 hex, got %d characters", len(first))
	}
}
