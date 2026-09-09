package targetbinding

import (
	"context"
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestBindingSelectsCompatibleWorkloadsAndVerifiesResult(t *testing.T) {
	plugin := Plugin{}
	spec := Spec{WorkloadTargetsRef: "art_targets", RequiredTrait: "scalable", IncludeComponentIDs: []string{"frontend", "cart"}}
	step := resolvedStep(t, spec, sampleTargets())
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || health.Checks["selected"] != "2" {
		t.Fatalf("unexpected precheck: %#v %v", health, err)
	}
	result, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, result, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || health.Checks["targets"] != "2" {
		t.Fatalf("unexpected verification: %#v %v", health, err)
	}
	var value Result
	if err := json.Unmarshal(result, &value); err != nil {
		t.Fatal(err)
	}
	if value.TargetBinding.Spec.Targets[0].ID != "frontend" || value.TargetBinding.Spec.Targets[1].ID != "cart" || value.TargetBinding.Spec.SourceDigest != "sha256:targets" {
		t.Fatalf("unexpected binding %#v", value.TargetBinding)
	}
}

func TestBindingSupportsAllTargetsAndExclusions(t *testing.T) {
	plugin := Plugin{}
	spec := Spec{WorkloadTargetsRef: "art_targets", RequiredTrait: "*", ExcludeComponentIDs: []string{"database"}}
	result, err := plugin.Execute(context.Background(), resolvedStep(t, spec, sampleTargets()), discardLog)
	if err != nil {
		t.Fatal(err)
	}
	var value Result
	if err := json.Unmarshal(result, &value); err != nil {
		t.Fatal(err)
	}
	if len(value.TargetBinding.Spec.Targets) != 2 {
		t.Fatalf("expected two targets, got %d", len(value.TargetBinding.Spec.Targets))
	}
}

func TestBindingRejectsInvalidSelectionsBeforeExecution(t *testing.T) {
	tests := []Spec{
		{WorkloadTargetsRef: "bad", RequiredTrait: "scalable"},
		{WorkloadTargetsRef: "art_targets", RequiredTrait: "Bad Trait"},
		{WorkloadTargetsRef: "art_targets", RequiredTrait: "scalable", IncludeComponentIDs: []string{"frontend"}, ExcludeComponentIDs: []string{"frontend"}},
		{WorkloadTargetsRef: "art_targets", RequiredTrait: "missing"},
		{WorkloadTargetsRef: "art_targets", RequiredTrait: "scalable", IncludeComponentIDs: []string{"unknown"}},
		{WorkloadTargetsRef: "art_targets", RequiredTrait: "scalable", IncludeComponentIDs: []string{"database"}},
	}
	plugin := Plugin{}
	for index, spec := range tests {
		raw, _ := json.Marshal(spec)
		report := plugin.Validate(context.Background(), Invocation{Input: raw})
		if index < 3 && report.Valid {
			t.Fatalf("case %d should fail static validation", index)
		}
		if index >= 3 {
			health, err := plugin.Precheck(context.Background(), resolvedStep(t, spec, sampleTargets()), discardLog)
			if err != nil || health.Status != domain.HealthUnhealthy {
				t.Fatalf("case %d should fail dynamic validation: %#v %v", index, health, err)
			}
		}
	}
}

func TestBindingRejectsInvalidWorkloadContract(t *testing.T) {
	targets := sampleTargets()
	targets[1].ID = targets[0].ID
	plugin := Plugin{}
	health, err := plugin.Precheck(context.Background(), resolvedStep(t, Spec{WorkloadTargetsRef: "art_targets", RequiredTrait: "scalable"}, targets), discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected invalid contract health, got %#v %v", health, err)
	}
}

func resolvedStep(t *testing.T, spec Spec, targets []WorkloadTarget) domain.PlanStep {
	t.Helper()
	input, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(targets)
	if err != nil {
		t.Fatal(err)
	}
	return domain.PlanStep{Input: input, ResolvedInputs: map[string]domain.ResolvedArtifact{"workload-targets": {ID: "art_targets", Type: "WorkloadTargets", Version: "v1alpha1", Digest: "sha256:targets", Value: value}}}
}

func sampleTargets() []WorkloadTarget {
	return []WorkloadTarget{
		{ID: "frontend", APIVersion: "apps/v1", Kind: "Deployment", Name: "frontend", Selector: map[string]string{"app": "frontend"}, Traits: []string{"schedulable", "scalable"}},
		{ID: "cart", APIVersion: "apps/v1", Kind: "Deployment", Name: "cart", Selector: map[string]string{"app": "cart"}, Traits: []string{"schedulable", "scalable"}},
		{ID: "database", APIVersion: "apps/v1", Kind: "StatefulSet", Name: "database", Selector: map[string]string{"app": "database"}, Traits: []string{"schedulable", "stateful"}},
	}
}

func discardLog(string, string) error {
	return nil
}
