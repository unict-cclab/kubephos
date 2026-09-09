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

func TestNormalizeAndValidateExperimentInput(t *testing.T) {
	input := createExperimentInput{
		WorkspaceID: " ws_dev ",
		Name:        " Scheduler comparison ",
		Variants: []createExperimentVariantInput{
			{Name: " Default ", TrialArtifactIDs: []string{" art_one ", "art_two"}},
			{Name: " Custom", Configuration: json.RawMessage(`{"weight":2}`), TrialArtifactIDs: []string{"art_three"}},
		},
	}
	if err := normalizeAndValidateExperimentInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.WorkspaceID != "ws_dev" || input.Name != "Scheduler comparison" || input.Variants[0].Name != "Default" || input.Variants[0].TrialArtifactIDs[0] != "art_one" {
		t.Fatalf("input was not normalized: %#v", input)
	}
	if string(input.Variants[0].Configuration) != "{}" {
		t.Fatalf("expected default configuration, got %s", input.Variants[0].Configuration)
	}
}

func TestNormalizeAndValidateExperimentInputRejectsInvalidComparisons(t *testing.T) {
	tests := []struct {
		name  string
		input createExperimentInput
	}{
		{name: "one variant", input: createExperimentInput{WorkspaceID: "ws", Name: "test", Variants: []createExperimentVariantInput{{Name: "one", TrialArtifactIDs: []string{"art"}}}}},
		{name: "duplicate names", input: createExperimentInput{WorkspaceID: "ws", Name: "test", Variants: []createExperimentVariantInput{{Name: "same", TrialArtifactIDs: []string{"one"}}, {Name: "Same", TrialArtifactIDs: []string{"two"}}}}},
		{name: "no trials", input: createExperimentInput{WorkspaceID: "ws", Name: "test", Variants: []createExperimentVariantInput{{Name: "one"}, {Name: "two", TrialArtifactIDs: []string{"two"}}}}},
		{name: "non object configuration", input: createExperimentInput{WorkspaceID: "ws", Name: "test", Variants: []createExperimentVariantInput{{Name: "one", Configuration: json.RawMessage(`[]`), TrialArtifactIDs: []string{"one"}}, {Name: "two", TrialArtifactIDs: []string{"two"}}}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := normalizeAndValidateExperimentInput(&test.input); err == nil {
				t.Fatal("expected validation error")
			}
		})
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

func TestPreflightPlanDefersStepsAfterInfrastructureEffects(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{
		{ID: "create", Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "machine/1", Kind: "machine", Name: "one"}}},
		{ID: "verify-dependent-state"},
	}}
	issues := preflightPlan(context.Background(), plugin, plan)
	if plugin.calls != 1 {
		t.Fatalf("expected only the first preflight to run, got %d", plugin.calls)
	}
	if len(issues) != 2 || issues[1].Level != "info" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanDefersStepsAfterGenericMutation(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{
		{ID: "install", Mutating: true},
		{ID: "verify-dependent-state"},
	}}
	issues := preflightPlan(context.Background(), plugin, plan)
	if plugin.calls != 1 {
		t.Fatalf("expected only the mutating step preflight to run, got %d", plugin.calls)
	}
	if len(issues) != 2 || issues[1].Level != "info" {
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

func TestPlanEffectsRejectDuplicateCreates(t *testing.T) {
	plan := domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{
		{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "one"},
		{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "two"},
	}}}}
	manifest := plugins.Manifest{ID: "test", Provider: "proxmox", Capabilities: []string{"infrastructure.provision"}}
	if err := validatePlanEffects(manifest, plan); err == nil {
		t.Fatal("expected duplicate resource create rejection")
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

func TestCleanupPlanReversesSuccessfulMutableStepsAndDeleteEffects(t *testing.T) {
	source := domain.Operation{
		PluginID: "test",
		Plan: domain.Plan{PluginID: "test", Steps: []domain.PlanStep{
			{ID: "machines", Name: "Create machines", Input: json.RawMessage(`{"one":1}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "vm/1", Kind: "machine", Name: "one"}}},
			{ID: "cluster", Name: "Install cluster", Input: json.RawMessage(`{"two":2}`), Mutating: true, ArtifactInputs: []domain.ArtifactInput{{Name: "machines", Type: "MachineSet", Version: "v1", ArtifactID: "art_one"}}},
			{ID: "read", Name: "Read only", Input: json.RawMessage(`{}`)},
		}},
		Steps: []domain.OperationStep{{Status: domain.StepSucceeded}, {Status: domain.StepSucceeded}, {Status: domain.StepSucceeded}},
	}
	plan, err := cleanupPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].ID != "cleanup-cluster" || plan.Steps[1].ID != "cleanup-machines" {
		t.Fatalf("unexpected cleanup order %#v", plan.Steps)
	}
	if !plan.Steps[0].Cleanup || !plan.Steps[1].Cleanup || plan.Steps[1].Effects[0].Action != "delete" {
		t.Fatalf("unexpected cleanup plan %#v", plan)
	}
	if len(plan.Steps[0].Outputs) != 0 || len(plan.Steps[0].ArtifactInputs) != 1 {
		t.Fatalf("cleanup must retain inputs but not outputs %#v", plan.Steps[0])
	}
}

func TestCleanupPlanRejectsIncompleteHistory(t *testing.T) {
	_, err := cleanupPlan(domain.Operation{Plan: domain.Plan{Steps: []domain.PlanStep{{ID: "one"}}}})
	if err == nil {
		t.Fatal("expected incomplete source history rejection")
	}
}

func TestCleanupPlanIncludesFailedMutableStepForRecovery(t *testing.T) {
	source := domain.Operation{
		PluginID: "test",
		Plan:     domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "create", Name: "Create", Input: json.RawMessage(`{}`), Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "vm/110", Kind: "machine", Name: "temporary"}}}}},
		Steps:    []domain.OperationStep{{Status: domain.StepFailed}},
	}
	plan, err := cleanupPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || !plan.Steps[0].Cleanup || plan.Steps[0].Effects[0].Action != "delete" {
		t.Fatalf("unexpected recovery plan %#v", plan)
	}
}
