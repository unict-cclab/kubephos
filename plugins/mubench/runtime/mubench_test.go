package mubench

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/catalog"
)

func TestMaterializerResolvesConfigurableBuiltInApplication(t *testing.T) {
	applicationRef, descriptor := builtInApplication(t)
	invocation := materializerInvocation(t, applicationRef, descriptor, map[string]any{"serviceCount": 4, "topology": "tree"})
	materializer := MaterializerPlugin{}
	if report := materializer.Validate(context.Background(), invocation); !report.Valid {
		t.Fatalf("materializer validation failed: %#v", report)
	}
	plan, err := materializer.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := materializer.Precheck(context.Background(), plan.Steps[0], discardLog); err != nil || health.Status != "healthy" {
		t.Fatalf("precheck failed: %#v %v", health, err)
	}
	materialized, err := materializer.Execute(context.Background(), plan.Steps[0], discardLog)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := materializer.Verify(context.Background(), plan.Steps[0], materialized, discardLog); err != nil || health.Status != "healthy" {
		t.Fatalf("verification failed: %#v %v", health, err)
	}
	var result materializerResult
	if err := json.Unmarshal(materialized, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.WorkloadTargets) != 5 {
		t.Fatalf("expected four services and one node proxy, got %d workloads", len(result.WorkloadTargets))
	}
	targets := map[string]workloadTarget{}
	for _, target := range result.WorkloadTargets {
		targets[target.ID] = target
	}
	if !slices.Equal(targets["s0"].Dependencies, []string{"s1", "s2"}) || targets["s3"].Index == nil || *targets["s3"].Index != 2 || !slices.Equal(targets["node-proxy"].Dependencies, []string{"s0"}) {
		t.Fatalf("unexpected resolved topology: %#v", result.WorkloadTargets)
	}
	for _, expected := range []string{"kind: DaemonSet", "name: node-proxy", "externalTrafficPolicy: Local", "X-Kubephos-Proxy-Zone", "proxy_pass http://s0:80/api/v1;", "name: s0", "name: s3", `index: "0"`, `index: "2"`} {
		if !strings.Contains(result.ManifestSet.Spec.Content, expected) {
			t.Fatalf("manifest does not contain %q", expected)
		}
	}
	if strings.Contains(result.ManifestSet.Spec.Content, "CustomFunctions") || len(result.LoadScenarioSet.Spec.Scenarios) != 1 {
		t.Fatal("unexpected custom function or load scenario output")
	}
}

func TestMaterializerRejectsInvalidQuantities(t *testing.T) {
	applicationRef, descriptor := builtInApplication(t)
	invocation := materializerInvocation(t, applicationRef, descriptor, map[string]any{"cpuRequest": "a lot"})
	if report := (MaterializerPlugin{}).Validate(context.Background(), invocation); report.Valid {
		t.Fatal("expected invalid CPU request rejection")
	}
}

func TestMaterializerKeepsLegacyReplicaFieldForHistoricalCatalogVersions(t *testing.T) {
	applicationRef, descriptor := builtInApplication(t)
	properties := descriptor.Spec.ValuesSchema["properties"].(map[string]any)
	delete(properties, "initialReplicas")
	delete(properties, "replicaOverrides")
	properties["replicas"] = map[string]any{"type": "integer", "minimum": 1, "maximum": 20}
	delete(descriptor.Spec.Defaults, "initialReplicas")
	delete(descriptor.Spec.Defaults, "replicaOverrides")
	descriptor.Spec.Defaults["replicas"] = 1
	invocation := materializerInvocation(t, applicationRef, descriptor, map[string]any{"replicas": 2})
	if report := (MaterializerPlugin{}).Validate(context.Background(), invocation); !report.Valid {
		t.Fatalf("legacy replica settings must remain valid: %#v", report)
	}
}

func TestMaterializerSupportsLargeTopologiesWithinSafetyLimit(t *testing.T) {
	applicationRef, descriptor := builtInApplication(t)
	materializer := MaterializerPlugin{}
	invocation := materializerInvocation(t, applicationRef, descriptor, map[string]any{"serviceCount": 100, "topology": "tree"})
	plan, err := materializer.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	materialized, err := materializer.Execute(context.Background(), plan.Steps[0], discardLog)
	if err != nil {
		t.Fatal(err)
	}
	var result materializerResult
	if err := json.Unmarshal(materialized, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.WorkloadTargets) != 101 {
		t.Fatalf("expected 100 services and one node proxy, got %d workloads", len(result.WorkloadTargets))
	}
	invalid := materializerInvocation(t, applicationRef, descriptor, map[string]any{"serviceCount": 101})
	if report := materializer.Validate(context.Background(), invalid); report.Valid {
		t.Fatal("expected the safety limit to reject more than 100 services")
	}
}

func builtInApplication(t *testing.T) (string, catalog.Descriptor) {
	t.Helper()
	applications, err := catalog.LoadDirectory("../../../catalog/applications/mubench")
	if err != nil {
		t.Fatal(err)
	}
	if len(applications) != 1 {
		t.Fatalf("expected one built-in muBench application, got %d", len(applications))
	}
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(applications[0].Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	return applications[0].Reference, descriptor
}

func materializerInvocation(t *testing.T, applicationRef string, descriptor catalog.Descriptor, values map[string]any) Invocation {
	t.Helper()
	descriptorRaw, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	input, err := json.Marshal(map[string]any{"applicationRef": applicationRef, "values": values})
	if err != nil {
		t.Fatal(err)
	}
	return Invocation{Input: input, Catalog: map[string]json.RawMessage{applicationRef: descriptorRaw}}
}

func discardLog(string, string) error {
	return nil
}
