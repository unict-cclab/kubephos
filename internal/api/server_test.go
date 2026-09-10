package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type unavailableRunner struct{}

func (unavailableRunner) Ready(context.Context) error {
	return errors.New("executor offline")
}
func (unavailableRunner) Run(context.Context, string, string, []byte, bool, plugins.Logger) ([]byte, []string, error) {
	return nil, nil, errors.New("executor offline")
}

type policyRunner struct {
	err error
}

func (p policyRunner) Ready(context.Context) error {
	return nil
}

func (p policyRunner) Run(context.Context, string, string, []byte, bool, plugins.Logger) ([]byte, []string, error) {
	return nil, nil, nil
}

func (p policyRunner) ValidateImage(string) error {
	return p.err
}

func TestRuntimeHealthDistinguishesDisabledAndUnavailable(t *testing.T) {
	disabled := &Server{}
	if state, _ := disabled.runtimeHealth(context.Background()); state != "disabled" {
		t.Fatalf("expected disabled runtime, got %s", state)
	}
	unavailable := &Server{runtime: unavailableRunner{}}
	if state, message := unavailable.runtimeHealth(context.Background()); state != "unavailable" || message != "executor offline" {
		t.Fatalf("unexpected runtime health: %s %s", state, message)
	}
}

func TestInspectPluginReturnsStaticPreviewWithoutExecutor(t *testing.T) {
	digest := strings.Repeat("e", 64)
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.preview
  name: Preview
  version: 1.0.0
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  permissions: [network.egress]
  runtime:
    image: registry.example.test/plugin@sha256:` + digest + `
`
	payload, _ := json.Marshal(map[string]string{"descriptor": descriptor})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/inspect", strings.NewReader(string(payload)))
	response := httptest.NewRecorder()
	server := &Server{registry: plugins.NewRegistry()}
	server.inspectPlugin(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected preview, got %d: %s", response.Code, response.Body.String())
	}
	var body struct {
		Manifest          plugins.Manifest `json:"manifest"`
		ExecutorAvailable bool             `json:"executorAvailable"`
		PolicyAccepted    bool             `json:"policyAccepted"`
		PolicyMessage     string           `json:"policyMessage"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Manifest.ID != "dev.example.preview" || body.ExecutorAvailable || body.PolicyAccepted || body.PolicyMessage == "" {
		t.Fatalf("unexpected preview: %#v", body)
	}
}

func TestInspectPluginReportsImagePolicy(t *testing.T) {
	digest := strings.Repeat("e", 64)
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.policy-preview
  name: Policy preview
  version: 1.0.0
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  runtime:
    image: registry.example.test/plugin@sha256:` + digest + `
`
	payload, _ := json.Marshal(map[string]string{"descriptor": descriptor})
	for name, runtime := range map[string]policyRunner{"accepted": {}, "rejected": {err: errors.New("registry denied")}} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/inspect", strings.NewReader(string(payload)))
			response := httptest.NewRecorder()
			server := &Server{registry: plugins.NewRegistry(), runtime: runtime}
			server.inspectPlugin(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("expected preview, got %d: %s", response.Code, response.Body.String())
			}
			var body struct {
				ExecutorAvailable bool   `json:"executorAvailable"`
				PolicyAccepted    bool   `json:"policyAccepted"`
				PolicyMessage     string `json:"policyMessage"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if !body.ExecutorAvailable || body.PolicyAccepted != (runtime.err == nil) || body.PolicyMessage == "" {
				t.Fatalf("unexpected policy preview: %#v", body)
			}
		})
	}
}

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
	manifest.Runtime.Digest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	changed, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil || changed == first {
		t.Fatalf("plugin digest did not change hash: %q", changed)
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

func TestNormalizePipelineExperimentInput(t *testing.T) {
	input := createPipelineExperimentInput{
		WorkspaceID: " ws_dev ", Name: " Scheduler comparison ", Repetitions: 3,
		Variants: []createPipelineExperimentVariantInput{
			{Name: " Default ", PipelineID: " pipe_default "},
			{Name: " Secondary ", PipelineID: " pipe_secondary "},
		},
	}
	if err := normalizePipelineExperimentInput(&input); err != nil {
		t.Fatal(err)
	}
	if input.WorkspaceID != "ws_dev" || input.Name != "Scheduler comparison" || input.Variants[1].PipelineID != "pipe_secondary" {
		t.Fatalf("input was not normalized: %#v", input)
	}
}

func TestNormalizePipelineExperimentInputRejectsUnsafeFanout(t *testing.T) {
	tests := []createPipelineExperimentInput{
		{WorkspaceID: "ws", Name: "one", Repetitions: 1, Variants: []createPipelineExperimentVariantInput{{Name: "only", PipelineID: "pipe_one"}}},
		{WorkspaceID: "ws", Name: "duplicate", Repetitions: 1, Variants: []createPipelineExperimentVariantInput{{Name: "same", PipelineID: "pipe_one"}, {Name: "Same", PipelineID: "pipe_two"}}},
		{WorkspaceID: "ws", Name: "pipeline", Repetitions: 1, Variants: []createPipelineExperimentVariantInput{{Name: "one", PipelineID: "pipe_one"}, {Name: "two", PipelineID: "pipe_one"}}},
		{WorkspaceID: "ws", Name: "fanout", Repetitions: 10, Variants: []createPipelineExperimentVariantInput{{Name: "one", PipelineID: "pipe_one"}, {Name: "two", PipelineID: "pipe_two"}, {Name: "three", PipelineID: "pipe_three"}, {Name: "four", PipelineID: "pipe_four"}, {Name: "five", PipelineID: "pipe_five"}}},
	}
	for _, input := range tests {
		if err := normalizePipelineExperimentInput(&input); err == nil {
			t.Fatalf("expected rejection for %#v", input)
		}
	}
}

func TestPipelineResultSensitiveUsesResolvedOutput(t *testing.T) {
	pipeline := domain.Pipeline{
		Definition: domain.PipelineDefinition{Result: domain.PipelineOutput{Stage: "result", Output: "dataset"}},
		Resolution: domain.PipelineResolution{Stages: []domain.ResolvedPipelineStage{{ID: "result", Plan: domain.Plan{Steps: []domain.PlanStep{{Outputs: []domain.ArtifactOutput{{Name: "dataset"}}}}}}}},
	}
	if pipelineResultSensitive(pipeline) {
		t.Fatal("public result was classified as sensitive")
	}
	pipeline.Resolution.Stages[0].Plan.Steps[0].Outputs[0].Sensitive = true
	if !pipelineResultSensitive(pipeline) {
		t.Fatal("sensitive result was not detected")
	}
}

func TestNormalizeScheduledFor(t *testing.T) {
	now := time.Date(2026, time.September, 10, 12, 0, 0, 0, time.UTC)
	immediate, err := normalizeScheduledFor(nil, now)
	if err != nil || !immediate.Equal(now) {
		t.Fatalf("unexpected immediate schedule: %v %v", immediate, err)
	}
	future := now.Add(15 * time.Minute)
	scheduled, err := normalizeScheduledFor(&future, now)
	if err != nil || !scheduled.Equal(future) {
		t.Fatalf("unexpected future schedule: %v %v", scheduled, err)
	}
	past := now.Add(-time.Minute)
	tooFar := now.AddDate(1, 0, 1)
	if _, err := normalizeScheduledFor(&past, now); err == nil {
		t.Fatal("past schedule was accepted")
	}
	if _, err := normalizeScheduledFor(&tooFar, now); err == nil {
		t.Fatal("schedule beyond one year was accepted")
	}
}

type preflightPlugin struct {
	calls            int
	receivedResolved bool
	health           domain.HealthReport
}

func (p *preflightPlugin) Manifest() plugins.Manifest { return plugins.Manifest{ID: "test"} }
func (p *preflightPlugin) Validate(context.Context, json.RawMessage) domain.ValidationReport {
	return domain.ValidationReport{Valid: true}
}
func (p *preflightPlugin) Plan(context.Context, json.RawMessage) (domain.Plan, error) {
	return domain.Plan{}, nil
}
func (p *preflightPlugin) Precheck(_ context.Context, step domain.PlanStep, _ plugins.Logger) (domain.HealthReport, error) {
	p.calls++
	p.receivedResolved = len(step.ResolvedInputs) > 0
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
	issues := preflightPlan(context.Background(), plugin, plan, nil)
	if plugin.calls != 1 {
		t.Fatalf("expected one reachable preflight, got %d", plugin.calls)
	}
	if len(issues) != 2 || issues[0].Level != "info" || issues[1].Level != "info" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanHydratesExistingArtifactInputs(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{{ID: "consumer", ArtifactInputs: []domain.ArtifactInput{{Name: "input", ArtifactID: "art_one"}}}}}
	hydrated := false
	issues := preflightPlan(context.Background(), plugin, plan, func(_ context.Context, step domain.PlanStep) (domain.PlanStep, error) {
		hydrated = true
		step.ResolvedInputs = map[string]domain.ResolvedArtifact{"input": {ID: "art_one"}}
		return step, nil
	})
	if !hydrated || plugin.calls != 1 || !plugin.receivedResolved {
		t.Fatalf("expected hydrated preflight, got hydrated=%t calls=%d resolved=%t", hydrated, plugin.calls, plugin.receivedResolved)
	}
	if len(issues) != 1 || issues[0].Level != "info" || issues[0].Message != "Non-mutating preflight passed: ready" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanDefersStepsAfterInfrastructureEffects(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthHealthy, Summary: "ready"}}
	plan := domain.Plan{Steps: []domain.PlanStep{
		{ID: "create", Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "machine/1", Kind: "machine", Name: "one"}}},
		{ID: "verify-dependent-state"},
	}}
	issues := preflightPlan(context.Background(), plugin, plan, nil)
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
	issues := preflightPlan(context.Background(), plugin, plan, nil)
	if plugin.calls != 1 {
		t.Fatalf("expected only the mutating step preflight to run, got %d", plugin.calls)
	}
	if len(issues) != 2 || issues[1].Level != "info" {
		t.Fatalf("unexpected issues %#v", issues)
	}
}

func TestPreflightPlanRejectsUnhealthyStep(t *testing.T) {
	plugin := &preflightPlugin{health: domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "unreachable"}}
	issues := preflightPlan(context.Background(), plugin, domain.Plan{Steps: []domain.PlanStep{{ID: "first"}}}, nil)
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
