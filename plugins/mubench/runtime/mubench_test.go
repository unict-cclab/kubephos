package mubench

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/catalog"
)

func TestFactoryAndMaterializerProduceStandardArtifacts(t *testing.T) {
	spec := FactorySpec{Name: "Paper topology", ServiceCount: 4, Topology: "tree", Replicas: 1, Workers: 2, Threads: 4, WorkloadProfile: "balanced", ResponseSizeKB: 10, CPURequest: "250m", MemoryRequest: "256Mi"}
	raw, _ := json.Marshal(spec)
	factory := FactoryPlugin{}
	if report := factory.Validate(context.Background(), Invocation{Input: raw}); !report.Valid {
		t.Fatalf("factory validation failed: %#v", report)
	}
	plan, err := factory.Plan(context.Background(), Invocation{Input: raw})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := factory.Execute(context.Background(), plan.Steps[0], discardLog)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := factory.Verify(context.Background(), plan.Steps[0], generated, discardLog); err != nil || health.Status != "healthy" {
		t.Fatalf("factory verification failed: %#v %v", health, err)
	}
	var result factoryResult
	if err := json.Unmarshal(generated, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Descriptor.Spec.Interface.Components) != 5 || result.Descriptor.Spec.Materializer != materializerID {
		t.Fatalf("unexpected generated contract: %#v", result.Descriptor.Spec)
	}
	components := map[string]catalog.Component{}
	for _, component := range result.Descriptor.Spec.Interface.Components {
		components[component.ID] = component
	}
	if components["s0"].Index == nil || *components["s0"].Index != 0 || !slices.Equal(components["s0"].Dependencies, []string{"s1", "s2"}) || components["s3"].Index == nil || *components["s3"].Index != 2 || !slices.Equal(components["node-proxy"].Dependencies, []string{"s0"}) {
		t.Fatalf("unexpected generated topology: %#v", result.Descriptor.Spec.Interface.Components)
	}
	descriptorRaw, _ := json.Marshal(result.Descriptor)
	applicationRef := "app:" + result.Descriptor.Metadata.ID + "@" + result.Descriptor.Metadata.Version
	input, _ := json.Marshal(map[string]any{"applicationRef": applicationRef, "values": map[string]any{}})
	invocation := Invocation{Input: input, Catalog: map[string]json.RawMessage{applicationRef: descriptorRaw}}
	materializer := MaterializerPlugin{}
	if report := materializer.Validate(context.Background(), invocation); !report.Valid {
		t.Fatalf("materializer validation failed: %#v", report)
	}
	materializationPlan, err := materializer.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := materializer.Precheck(context.Background(), materializationPlan.Steps[0], discardLog); err != nil || health.Status != "healthy" {
		t.Fatalf("precheck failed: %#v %v", health, err)
	}
	materialized, err := materializer.Execute(context.Background(), materializationPlan.Steps[0], discardLog)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := materializer.Verify(context.Background(), materializationPlan.Steps[0], materialized, discardLog); err != nil || health.Status != "healthy" {
		t.Fatalf("verification failed: %#v %v", health, err)
	}
	var application materializerResult
	if err := json.Unmarshal(materialized, &application); err != nil {
		t.Fatal(err)
	}
	manifest := application.ManifestSet.Spec.Content
	for _, expected := range []string{"kind: DaemonSet", "name: node-proxy", "externalTrafficPolicy: Local", "X-Kubephos-Proxy-Zone", "proxy_pass http://s0:80/api/v1;", "name: s0", "name: s3", `index: "0"`, `index: "2"`} {
		if !strings.Contains(manifest, expected) {
			t.Fatalf("manifest does not contain %q", expected)
		}
	}
	if strings.Contains(manifest, "CustomFunctions") || len(application.LoadScenarioSet.Spec.Scenarios) != 1 {
		t.Fatalf("unexpected custom function or load scenario output")
	}
}

func TestFactoryRejectsInvalidQuantities(t *testing.T) {
	spec := FactorySpec{Name: "Invalid", ServiceCount: 4, Topology: "chain", Replicas: 1, Workers: 2, Threads: 4, WorkloadProfile: "balanced", ResponseSizeKB: 10, CPURequest: "a lot", MemoryRequest: "256Mi"}
	if err := validateFactorySpec(spec); err == nil {
		t.Fatal("expected invalid CPU request rejection")
	}
}

func TestFactorySupportsLargeTopologiesWithinSafetyLimit(t *testing.T) {
	spec := FactorySpec{Name: "Large topology", ServiceCount: 100, Topology: "tree", Replicas: 1, Workers: 2, Threads: 4, WorkloadProfile: "balanced", ResponseSizeKB: 10, CPURequest: "250m", MemoryRequest: "256Mi"}
	raw, _ := json.Marshal(spec)
	factory := FactoryPlugin{}
	plan, err := factory.Plan(context.Background(), Invocation{Input: raw})
	if err != nil {
		t.Fatal(err)
	}
	generated, err := factory.Execute(context.Background(), plan.Steps[0], discardLog)
	if err != nil {
		t.Fatal(err)
	}
	var result factoryResult
	if err := json.Unmarshal(generated, &result); err != nil {
		t.Fatal(err)
	}
	if got := len(result.Descriptor.Spec.Interface.Components); got != 101 {
		t.Fatalf("expected 100 services and one node proxy, got %d components", got)
	}
	spec.ServiceCount = 101
	if err := validateFactorySpec(spec); err == nil {
		t.Fatal("expected the safety limit to reject more than 100 services")
	}
}

func discardLog(string, string) error {
	return nil
}
