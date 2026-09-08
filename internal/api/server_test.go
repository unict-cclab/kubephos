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

func TestPlanEffectsRequireProvisionCapability(t *testing.T) {
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "kubephos-test"}}}}}
	if err := validatePlanEffects(plugins.Manifest{ID: "test"}, plan); err == nil {
		t.Fatal("expected missing capability error")
	}
	manifest := plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.provision"}}
	if err := validatePlanEffects(manifest, plan); err != nil {
		t.Fatal(err)
	}
}

func TestPlanEffectsRejectIncompleteIdentity(t *testing.T) {
	manifest := plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.provision"}}
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "qemu/9000"}}}}}
	if err := validatePlanEffects(manifest, plan); err == nil {
		t.Fatal("expected incomplete identity error")
	}
}

func TestDeleteEffectsRequireDeprovisionCapability(t *testing.T) {
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "delete", Name: "Delete", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "delete", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "kubephos-test"}}}}}
	if err := validatePlanEffects(plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.provision"}}, plan); err == nil {
		t.Fatal("expected missing deprovision capability error")
	}
	if err := validatePlanEffects(plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.deprovision"}}, plan); err != nil {
		t.Fatal(err)
	}
}

func TestPlanEffectsRequireProviderIdentity(t *testing.T) {
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "kubephos-test"}}}}}
	if err := validatePlanEffects(plugins.Manifest{ID: "test", Capabilities: []string{"infrastructure.provision"}}, plan); err == nil {
		t.Fatal("expected missing provider error")
	}
}
