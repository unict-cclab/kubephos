package workflows

import (
	"errors"

	"kubephos.dev/kubephos/internal/domain"
)

var ErrNoCleanupSteps = errors.New("the source operation has no mutable steps eligible for cleanup")

func CleanupPlan(source domain.Operation) (domain.Plan, error) {
	if len(source.Plan.Steps) != len(source.Steps) {
		return domain.Plan{}, errors.New("source operation step history is incomplete")
	}
	steps := make([]domain.PlanStep, 0, len(source.Plan.Steps))
	for position := len(source.Plan.Steps) - 1; position >= 0; position-- {
		planned := source.Plan.Steps[position]
		status := source.Steps[position].Status
		if planned.Cleanup || status != domain.StepSucceeded && status != domain.StepFailed && status != domain.StepCanceled {
			continue
		}
		effects := make([]domain.ResourceEffect, 0, len(planned.Effects))
		for _, effect := range planned.Effects {
			if effect.Action == "create" {
				effect.Action = "delete"
				effects = append(effects, effect)
			}
		}
		if !planned.Mutating && len(effects) == 0 {
			continue
		}
		steps = append(steps, domain.PlanStep{ID: "cleanup-" + planned.ID, Name: "Clean up " + planned.Name, Input: planned.Input, Mutating: true, Cleanup: true, ArtifactInputs: planned.ArtifactInputs, Effects: effects})
	}
	if len(steps) == 0 {
		return domain.Plan{}, ErrNoCleanupSteps
	}
	return domain.Plan{PluginID: source.PluginID, Steps: steps}, nil
}
