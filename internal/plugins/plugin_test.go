package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

type manifestPlugin struct {
	manifest Manifest
}

func (p manifestPlugin) Manifest() Manifest { return p.manifest }
func (p manifestPlugin) Validate(context.Context, json.RawMessage) domain.ValidationReport {
	return domain.ValidationReport{Valid: true}
}
func (p manifestPlugin) Plan(context.Context, json.RawMessage) (domain.Plan, error) {
	return domain.Plan{}, nil
}
func (p manifestPlugin) Precheck(context.Context, domain.PlanStep, Logger) (domain.HealthReport, error) {
	return domain.HealthReport{Status: domain.HealthHealthy}, nil
}
func (p manifestPlugin) Execute(context.Context, domain.PlanStep, Logger) (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}
func (p manifestPlugin) Verify(context.Context, domain.PlanStep, json.RawMessage, Logger) (domain.HealthReport, error) {
	return domain.HealthReport{Status: domain.HealthHealthy}, nil
}
func (p manifestPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, Logger) error {
	return nil
}

func TestRegistryInstallsAndReplacesExternalPlugin(t *testing.T) {
	bundled := manifestPlugin{manifest: Manifest{ID: "bundled", Version: "1.0.0"}}
	registry := NewRegistry(bundled)
	if err := registry.Install(manifestPlugin{manifest: Manifest{ID: "external", Version: "1.0.0", Runtime: Runtime{Kind: "oci", Digest: "sha256:" + strings.Repeat("a", 64)}}}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Install(manifestPlugin{manifest: Manifest{ID: "external", Version: "2.0.0", Runtime: Runtime{Kind: "oci", Digest: "sha256:" + strings.Repeat("b", 64)}}}); err != nil {
		t.Fatal(err)
	}
	plugin, err := registry.Get("external")
	if err != nil || plugin.Manifest().Version != "2.0.0" {
		t.Fatalf("external plugin was not replaced: %#v %v", plugin, err)
	}
	if err := registry.Install(manifestPlugin{manifest: Manifest{ID: "bundled", Version: "2.0.0"}}); err == nil {
		t.Fatal("expected bundled plugin replacement rejection")
	}
	if ids := registry.ExternalIDs(); len(ids) != 1 || ids[0] != "external" {
		t.Fatalf("unexpected external ids: %#v", ids)
	}
	if err := registry.Remove("external"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Get("external"); err == nil {
		t.Fatal("external plugin was not removed")
	}
	if err := registry.Remove("bundled"); err == nil {
		t.Fatal("expected bundled plugin removal rejection")
	}
}
