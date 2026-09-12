package workflows

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type workflowPlugin struct {
	manifest plugins.Manifest
	plan     domain.Plan
}

func (p workflowPlugin) Manifest() plugins.Manifest {
	return p.manifest
}

func (p workflowPlugin) Validate(context.Context, json.RawMessage) domain.ValidationReport {
	return domain.ValidationReport{Valid: true}
}

func (p workflowPlugin) Plan(_ context.Context, spec json.RawMessage) (domain.Plan, error) {
	var values map[string]string
	_ = json.Unmarshal(spec, &values)
	plan := p.plan
	for stepIndex := range plan.Steps {
		plan.Steps[stepIndex].Input = append(json.RawMessage(nil), spec...)
		for inputIndex := range plan.Steps[stepIndex].ArtifactInputs {
			name := plan.Steps[stepIndex].ArtifactInputs[inputIndex].Name
			plan.Steps[stepIndex].ArtifactInputs[inputIndex].ArtifactID = values[name]
		}
	}
	return plan, nil
}

func (p workflowPlugin) Precheck(context.Context, domain.PlanStep, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{Status: domain.HealthHealthy}, nil
}

func (p workflowPlugin) Execute(context.Context, domain.PlanStep, plugins.Logger) (json.RawMessage, error) {
	return nil, nil
}

func (p workflowPlugin) Verify(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{Status: domain.HealthHealthy}, nil
}

func (p workflowPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func TestValidateResolvesTypedPipeline(t *testing.T) {
	registry := plugins.NewRegistry(
		workflowPlugin{
			manifest: plugins.Manifest{ID: "producer", Name: "Producer", Version: "1.2.3", Schema: json.RawMessage(`{"type":"object","additionalProperties":false}`), ArtifactOutputs: []domain.ArtifactContract{{Type: "Cluster", Version: "v1"}}},
			plan:     domain.Plan{PluginID: "producer", Steps: []domain.PlanStep{{ID: "produce", Name: "Produce", Input: json.RawMessage(`{}`), Outputs: []domain.ArtifactOutput{{Name: "cluster", Type: "Cluster", Version: "v1", MediaType: "application/json", Source: "/result"}}}}},
		},
		workflowPlugin{
			manifest: plugins.Manifest{ID: "consumer", Name: "Consumer", Version: "4.5.6", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["cluster"],"properties":{"cluster":{"type":"string","format":"kubephos-artifact-ref"}}}`), ArtifactInputs: []domain.ArtifactContract{{Type: "Cluster", Version: "v1"}}, ArtifactOutputs: []domain.ArtifactContract{{Type: "Dataset", Version: "v1"}}},
			plan:     domain.Plan{PluginID: "consumer", Steps: []domain.PlanStep{{ID: "consume", Name: "Consume", Input: json.RawMessage(`{}`), ArtifactInputs: []domain.ArtifactInput{{Name: "cluster", Type: "Cluster", Version: "v1"}}, Outputs: []domain.ArtifactOutput{{Name: "dataset", Type: "Dataset", Version: "v1", MediaType: "application/json", Source: "/result"}}}}},
		},
	)
	definition := domain.PipelineDefinition{
		Stages: []domain.PipelineStage{
			{ID: "cluster", PluginID: "producer", Title: "Create cluster", Spec: json.RawMessage(`{}`)},
			{ID: "measure", PluginID: "consumer", Title: "Collect data", Spec: json.RawMessage(`{}`), Bindings: []domain.PipelineBinding{{Path: "/cluster", FromStage: "cluster"}}},
		},
		Result: domain.PipelineOutput{Stage: "measure"},
	}
	result, err := Validate(context.Background(), registry, definition)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Validation.Valid || len(result.Resolution.Stages) != 2 || result.Resolution.Result.Type != "Dataset" || len(result.Hash) != 64 {
		t.Fatalf("unexpected result %#v", result)
	}
	var resolved map[string]string
	if err := json.Unmarshal(result.Resolution.Stages[1].Spec, &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved["cluster"] != "art_pipeline_2_1" {
		t.Fatalf("unexpected resolved binding %#v", resolved)
	}
	if result.Definition.Stages[1].Bindings[0].FromOutput != "cluster" || result.Definition.Result.Output != "dataset" {
		t.Fatalf("expected inferred outputs, got %#v", result.Definition)
	}
	second, err := Validate(context.Background(), registry, definition)
	if err != nil || second.Hash != result.Hash {
		t.Fatalf("expected deterministic hash, got %q and %q", result.Hash, second.Hash)
	}
	pipeline := domain.Pipeline{Definition: result.Definition, Resolution: result.Resolution}
	runtimeStage, err := ResolveStage(pipeline, 1, map[string]map[string]string{"cluster": {"cluster": "art_real"}})
	if err != nil {
		t.Fatal(err)
	}
	if runtimeStage.Plan.Steps[0].ArtifactInputs[0].ArtifactID != "art_real" {
		t.Fatalf("binding was not resolved: %#v", runtimeStage.Plan)
	}
	var planInput map[string]string
	if err := json.Unmarshal(runtimeStage.Plan.Steps[0].Input, &planInput); err != nil || planInput["cluster"] != "art_real" {
		t.Fatalf("plan input was not resolved: %s", runtimeStage.Plan.Steps[0].Input)
	}
	if err := json.Unmarshal(runtimeStage.Spec, &resolved); err != nil || resolved["cluster"] != "art_real" {
		t.Fatalf("runtime spec was not resolved: %s", runtimeStage.Spec)
	}
}

func TestValidateRejectsForwardReference(t *testing.T) {
	plugin := workflowPlugin{manifest: plugins.Manifest{ID: "test", Name: "Test", Version: "1", Schema: json.RawMessage(`{"type":"object"}`)}, plan: domain.Plan{PluginID: "test", Steps: []domain.PlanStep{{ID: "run", Name: "Run", Input: json.RawMessage(`{}`)}}}}
	_, err := Validate(context.Background(), plugins.NewRegistry(plugin), domain.PipelineDefinition{
		Stages: []domain.PipelineStage{
			{ID: "first", PluginID: "test", Title: "First", Spec: json.RawMessage(`{}`), Bindings: []domain.PipelineBinding{{Path: "/input", FromStage: "second", FromOutput: "value"}}},
			{ID: "second", PluginID: "test", Title: "Second", Spec: json.RawMessage(`{}`)},
		},
		Result: domain.PipelineOutput{Stage: "second", Output: "value"},
	})
	if err == nil {
		t.Fatal("expected forward reference rejection")
	}
}

func TestValidateRequiresCleanupCapabilityForMutableExperimentStage(t *testing.T) {
	plugin := workflowPlugin{
		manifest: plugins.Manifest{ID: "mutable", Name: "Mutable", Version: "1", Schema: json.RawMessage(`{"type":"object"}`), ArtifactOutputs: []domain.ArtifactContract{{Type: "Dataset", Version: "v1"}}},
		plan:     domain.Plan{PluginID: "mutable", Steps: []domain.PlanStep{{ID: "run", Name: "Run", Input: json.RawMessage(`{}`), Mutating: true, Outputs: []domain.ArtifactOutput{{Name: "dataset", Type: "Dataset", Version: "v1", MediaType: "application/json", Source: "/result"}}}}},
	}
	definition := domain.PipelineDefinition{CleanupAfterRun: true, Stages: []domain.PipelineStage{{ID: "mutate", PluginID: "mutable", Title: "Mutate", Spec: json.RawMessage(`{}`)}}, Result: domain.PipelineOutput{Stage: "mutate", Output: "dataset"}}
	if _, err := Validate(context.Background(), plugins.NewRegistry(plugin), definition); err == nil || !strings.Contains(err.Error(), "lifecycle.cleanup") {
		t.Fatalf("expected cleanup capability rejection, got %v", err)
	}
	plugin.manifest.Capabilities = []string{"lifecycle.cleanup"}
	result, err := Validate(context.Background(), plugins.NewRegistry(plugin), definition)
	if err != nil || !result.Validation.Valid {
		t.Fatalf("expected cleanup-capable stage to validate, got %#v %v", result.Validation, err)
	}
}

func TestValidateRejectsContractMismatch(t *testing.T) {
	producer := workflowPlugin{
		manifest: plugins.Manifest{ID: "producer", Name: "Producer", Version: "1", Schema: json.RawMessage(`{"type":"object"}`), ArtifactOutputs: []domain.ArtifactContract{{Type: "Cluster", Version: "v1"}}},
		plan:     domain.Plan{PluginID: "producer", Steps: []domain.PlanStep{{ID: "run", Name: "Run", Input: json.RawMessage(`{}`), Outputs: []domain.ArtifactOutput{{Name: "value", Type: "Cluster", Version: "v1", MediaType: "application/json", Source: "/result"}}}}},
	}
	consumer := workflowPlugin{
		manifest: plugins.Manifest{ID: "consumer", Name: "Consumer", Version: "1", Schema: json.RawMessage(`{"type":"object","required":["input"],"properties":{"input":{"type":"string"}}}`), ArtifactInputs: []domain.ArtifactContract{{Type: "Application", Version: "v1"}}, ArtifactOutputs: []domain.ArtifactContract{{Type: "Dataset", Version: "v1"}}},
		plan:     domain.Plan{PluginID: "consumer", Steps: []domain.PlanStep{{ID: "run", Name: "Run", Input: json.RawMessage(`{}`), ArtifactInputs: []domain.ArtifactInput{{Name: "input", Type: "Application", Version: "v1"}}, Outputs: []domain.ArtifactOutput{{Name: "dataset", Type: "Dataset", Version: "v1", MediaType: "application/json", Source: "/result"}}}}},
	}
	_, err := Validate(context.Background(), plugins.NewRegistry(producer, consumer), domain.PipelineDefinition{
		Stages: []domain.PipelineStage{
			{ID: "produce", PluginID: "producer", Title: "Produce", Spec: json.RawMessage(`{}`)},
			{ID: "consume", PluginID: "consumer", Title: "Consume", Spec: json.RawMessage(`{}`), Bindings: []domain.PipelineBinding{{Path: "/input", FromStage: "produce", FromOutput: "value"}}},
		},
		Result: domain.PipelineOutput{Stage: "consume", Output: "dataset"},
	})
	if err == nil || !strings.Contains(err.Error(), "requires Application/v1") {
		t.Fatalf("expected contract mismatch, got %v", err)
	}
}
