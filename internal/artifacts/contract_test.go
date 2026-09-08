package artifacts

import (
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestValidatePlanAcceptsCompatibleEarlierOutput(t *testing.T) {
	plan := domain.Plan{Steps: []domain.PlanStep{
		{ID: "produce", Outputs: []domain.ArtifactOutput{{Name: "targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json", Source: "/targets"}}},
		{ID: "consume", ArtifactInputs: []domain.ArtifactInput{{Name: "targets", Type: "WorkloadTargets", Version: "v1alpha1", FromStep: "produce", FromOutput: "targets"}}},
	}}
	contracts := []domain.ArtifactContract{{Type: "WorkloadTargets", Version: "v1alpha1"}}
	if err := ValidatePlan(plan, contracts, contracts); err != nil {
		t.Fatal(err)
	}
}

func TestValidatePlanRejectsForwardAndMismatchedInputs(t *testing.T) {
	forward := domain.Plan{Steps: []domain.PlanStep{
		{ID: "consume", ArtifactInputs: []domain.ArtifactInput{{Name: "targets", Type: "WorkloadTargets", Version: "v1alpha1", FromStep: "produce", FromOutput: "targets"}}},
		{ID: "produce", Outputs: []domain.ArtifactOutput{{Name: "targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json"}}},
	}}
	contracts := []domain.ArtifactContract{{Type: "WorkloadTargets", Version: "v1alpha1"}, {Type: "ServiceEndpoints", Version: "v1alpha1"}}
	if err := ValidatePlan(forward, contracts, contracts); err == nil {
		t.Fatal("expected forward reference error")
	}
	mismatch := domain.Plan{Steps: []domain.PlanStep{
		{ID: "produce", Outputs: []domain.ArtifactOutput{{Name: "targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json"}}},
		{ID: "consume", ArtifactInputs: []domain.ArtifactInput{{Name: "targets", Type: "ServiceEndpoints", Version: "v1alpha1", FromStep: "produce", FromOutput: "targets"}}},
	}}
	if err := ValidatePlan(mismatch, contracts, contracts); err == nil {
		t.Fatal("expected type mismatch error")
	}
}

func TestValidatePlanRejectsUndeclaredContract(t *testing.T) {
	plan := domain.Plan{Steps: []domain.PlanStep{{ID: "produce", Outputs: []domain.ArtifactOutput{{Name: "targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json"}}}}}
	if err := ValidatePlan(plan, nil, nil); err == nil {
		t.Fatal("expected undeclared output error")
	}
}

func TestExtractJSONUsesRFC6901Pointers(t *testing.T) {
	value, err := ExtractJSON(json.RawMessage(`{"data":{"a/b":[{"value":3}]}}`), "/data/a~1b/0")
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != `{"value":3}` {
		t.Fatalf("unexpected value %s", value)
	}
}

func TestExtractJSONRejectsMissingSource(t *testing.T) {
	if _, err := ExtractJSON(json.RawMessage(`{"data":{}}`), "/missing"); err == nil {
		t.Fatal("expected missing source error")
	}
}
