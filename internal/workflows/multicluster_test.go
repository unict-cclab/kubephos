package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	mesh "kubephos.dev/kubephos/plugins/cilium-mesh/runtime"
)

type meshWorkflowPlugin struct {
	workflowPlugin
	runtime mesh.Plugin
}

func (p meshWorkflowPlugin) Manifest() plugins.Manifest { return p.runtime.Manifest() }
func (p meshWorkflowPlugin) Validate(ctx context.Context, raw json.RawMessage) domain.ValidationReport {
	return p.runtime.Validate(ctx, mesh.Invocation{Input: raw})
}
func (p meshWorkflowPlugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	return p.runtime.Plan(ctx, raw)
}

func TestMultiClusterBindingsResolveMemberArtifactsIndependently(t *testing.T) {
	outputs := []domain.ArtifactOutput{}
	contracts := []domain.ArtifactContract{}
	for name, kind := range map[string]string{"connection": "ClusterConnection", "inventory": "ClusterInventory", "observability": "ObservabilityCapability", "platform": "ManagedPlatformCapability"} {
		outputs = append(outputs, domain.ArtifactOutput{Name: name, Type: kind, Version: "v1alpha1", MediaType: "application/json", Source: "/" + name})
		contracts = append(contracts, domain.ArtifactContract{Type: kind, Version: "v1alpha1"})
	}
	producer := workflowPlugin{manifest: plugins.Manifest{ID: "member", Name: "Member", Version: "1", Schema: json.RawMessage(`{"type":"object"}`), ArtifactOutputs: contracts}, plan: domain.Plan{PluginID: "member", Steps: []domain.PlanStep{{ID: "member", Name: "Member", Outputs: outputs}}}}
	registry := plugins.NewRegistry(producer, meshWorkflowPlugin{})
	definition := domain.PipelineDefinition{}
	spec := mesh.Spec{Name: "research"}
	bindings := []domain.PipelineBinding{}
	produced := map[string]map[string]string{}
	for i := 0; i < 3; i++ {
		stage := fmt.Sprintf("c%d-member", i+1)
		definition.Stages = append(definition.Stages, domain.PipelineStage{ID: stage, PluginID: "member", Title: stage, Spec: json.RawMessage(`{}`)})
		spec.Members = append(spec.Members, mesh.Member{Name: fmt.Sprintf("research-%d", i+1), ClusterID: i + 1, PodCIDR: fmt.Sprintf("10.%d.0.0/16", 64+i)})
		produced[stage] = map[string]string{}
		for field, output := range map[string]string{"connectionRef": "connection", "inventoryRef": "inventory", "observabilityRef": "observability", "platformRef": "platform"} {
			bindings = append(bindings, domain.PipelineBinding{Path: fmt.Sprintf("/members/%d/%s", i, field), FromStage: stage, FromOutput: output})
			produced[stage][output] = "art_" + stage + "_" + output
		}
	}
	raw, _ := json.Marshal(spec)
	definition.Stages = append(definition.Stages, domain.PipelineStage{ID: "mesh", PluginID: (mesh.Plugin{}).Manifest().ID, Title: "Mesh", Spec: raw, Bindings: bindings})
	definition.Result = domain.PipelineOutput{Stage: "mesh", Output: "cluster-environment"}
	validated, err := ValidateDeferred(context.Background(), registry, definition)
	if err != nil {
		t.Fatal(err)
	}
	if !validated.Validation.Valid {
		t.Fatalf("invalid graph: %+v", validated.Validation)
	}
	resolved, err := ResolveStage(domain.Pipeline{Definition: validated.Definition, Resolution: validated.Resolution}, 3, produced)
	if err != nil {
		t.Fatal(err)
	}
	inputs := resolved.Plan.Steps[0].ArtifactInputs
	if len(inputs) != 12 || validated.Resolution.Result.Type != "ClusterEnvironment" {
		t.Fatalf("invalid environment contract: %+v", resolved.Plan)
	}
	for i := 0; i < 3; i++ {
		for _, input := range inputs {
			if input.Name == fmt.Sprintf("connection-%d", i) && input.ArtifactID != produced[fmt.Sprintf("c%d-member", i+1)]["connection"] {
				t.Fatalf("member binding crossed: %+v", input)
			}
		}
	}
	var runtimeSpec mesh.Spec
	if err := json.Unmarshal(resolved.Plan.Steps[0].Input, &runtimeSpec); err != nil {
		t.Fatal(err)
	}
	if runtimeSpec.Members[2].PlatformRef != produced["c3-member"]["platform"] {
		t.Fatalf("nested plan input not resolved: %+v", runtimeSpec)
	}
}
