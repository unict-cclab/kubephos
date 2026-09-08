package reference

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

func TestReferenceValidationAndPlan(t *testing.T) {
	plugin := Plugin{}
	spec := json.RawMessage(`{"message":"verify","steps":3,"delayMillis":100,"failureStep":0}`)
	report := plugin.Validate(context.Background(), spec)
	if !report.Valid {
		t.Fatalf("expected valid report: %#v", report)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 3 {
		t.Fatalf("expected 3 steps, got %d", len(plan.Steps))
	}
	if len(plan.Steps[0].Outputs) != 1 || len(plan.Steps[1].ArtifactInputs) != 1 {
		t.Fatal("expected typed output chaining between steps")
	}
}

func TestReferenceRejectsInvalidConfiguration(t *testing.T) {
	plugin := Plugin{}
	report := plugin.Validate(context.Background(), json.RawMessage(`{"message":"","steps":9,"delayMillis":1}`))
	if report.Valid {
		t.Fatal("expected invalid report")
	}
	if len(report.Issues) != 3 {
		t.Fatalf("expected 3 issues, got %d", len(report.Issues))
	}
}

func TestReferenceExecutionAndHealthGate(t *testing.T) {
	plugin := Plugin{}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"message":"verify","steps":1,"delayMillis":100,"failureStep":0}`))
	if err != nil {
		t.Fatal(err)
	}
	log := func(string, string) error { return nil }
	precheck, err := plugin.Precheck(context.Background(), plan.Steps[0], log)
	if err != nil {
		t.Fatal(err)
	}
	if precheck.Status != domain.HealthHealthy {
		t.Fatalf("expected healthy precheck, got %s", precheck.Status)
	}
	result, err := plugin.Execute(context.Background(), plan.Steps[0], log)
	if err != nil {
		t.Fatal(err)
	}
	health, err := plugin.Verify(context.Background(), plan.Steps[0], result, log)
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != domain.HealthHealthy {
		t.Fatalf("expected healthy verification, got %s", health.Status)
	}
}

func TestReferenceExecutionCanBeCanceled(t *testing.T) {
	plugin := Plugin{}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"message":"verify","steps":1,"delayMillis":10000,"failureStep":0}`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err = plugin.Execute(ctx, plan.Steps[0], func(string, string) error { return nil })
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
}
