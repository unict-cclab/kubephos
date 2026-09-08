package api

import (
	"context"
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

type preflightPlugin struct {
	calls  int
	health domain.HealthReport
}

func (p *preflightPlugin) Manifest() plugins.Manifest { return plugins.Manifest{ID: "test"} }
func (p *preflightPlugin) Validate(context.Context, json.RawMessage) domain.ValidationReport {
	return domain.ValidationReport{Valid: true}
}
func (p *preflightPlugin) Plan(context.Context, json.RawMessage) (domain.Plan, error) {
	return domain.Plan{}, nil
}
func (p *preflightPlugin) Precheck(context.Context, domain.PlanStep, plugins.Logger) (domain.HealthReport, error) {
	p.calls++
	return p.health, nil
}
func (p *preflightPlugin) Execute(context.Context, domain.PlanStep, plugins.Logger) (json.RawMessage, error) {
	return nil, nil
}
func (p *preflightPlugin) Verify(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{}, nil
}
func (p *preflightPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func TestPreflightPlanRunsReachableChecksAndDefersArtifactConsumers(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{
		{ID: "first"},
		{ID: "second", ArtifactInputs: []domain.ArtifactInput{{Name: "input"}}},
	}}
	issues := preflightPlan(context.Background(), plugin, plan)
	if plugin.calls != 1 {
		t.Fatalf("expected one reachable preflight, got %d", plugin.calls)
	}
	if len(issues) != 2 || issues[0].Level != "info" || issues[1].Level != "info" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanRejectsUnhealthyStep(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "unreachable"}}
	issues := preflightPlan(context.Background(), plugin, domain.Plan{Steps: []domain.PlanStep{{ID: "first"}}})
	if len(issues) != 1 || issues[0].Level != "error" {
		t.Fatalf("unexpected issues %#v", issues)
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
