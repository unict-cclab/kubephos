package workflows

import (
	"errors"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestCleanupPlanReversesEligibleSteps(t *testing.T) {
	source := domain.Operation{
		PluginID: "dev.example.plugin",
		Plan: domain.Plan{Steps: []domain.PlanStep{
			{ID: "prepare", Name: "Prepare", Mutating: false},
			{ID: "deploy", Name: "Deploy", Mutating: true, Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "one"}}},
			{ID: "configure", Name: "Configure", Mutating: true},
		}},
		Steps: []domain.OperationStep{{Status: domain.StepSucceeded}, {Status: domain.StepSucceeded}, {Status: domain.StepFailed}},
	}
	plan, err := CleanupPlan(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].ID != "cleanup-configure" || plan.Steps[1].ID != "cleanup-deploy" {
		t.Fatalf("unexpected cleanup order: %#v", plan.Steps)
	}
	if plan.Steps[1].Effects[0].Action != "delete" {
		t.Fatalf("create effect was not reversed: %#v", plan.Steps[1].Effects)
	}
}

func TestCleanupPlanRejectsIncompleteHistoryAndEmptyPlan(t *testing.T) {
	_, err := CleanupPlan(domain.Operation{Plan: domain.Plan{Steps: []domain.PlanStep{{ID: "one"}}}})
	if err == nil {
		t.Fatal("expected incomplete history error")
	}
	_, err = CleanupPlan(domain.Operation{Plan: domain.Plan{Steps: []domain.PlanStep{{ID: "one"}}}, Steps: []domain.OperationStep{{Status: domain.StepSucceeded}}})
	if !errors.Is(err, ErrNoCleanupSteps) {
		t.Fatalf("expected ErrNoCleanupSteps, got %v", err)
	}
}
