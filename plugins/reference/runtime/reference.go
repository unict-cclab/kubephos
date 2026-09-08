package reference

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type Plugin struct{}

type referenceSpec struct {
	Message     string `json:"message"`
	Steps       int    `json:"steps"`
	DelayMillis int    `json:"delayMillis"`
	FailureStep int    `json:"failureStep,omitempty"`
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID:              "io.kubephos.reference.workflow",
		Name:            "System verification",
		Version:         "0.1.0",
		Description:     "Verifies validation, workers, health gates and live logs.",
		Schema:          json.RawMessage(`{"type":"object","required":["message","steps","delayMillis"],"properties":{"message":{"type":"string","minLength":1,"maxLength":120,"title":"Message"},"steps":{"type":"integer","minimum":1,"maximum":8,"default":3,"title":"Steps"},"delayMillis":{"type":"integer","minimum":100,"maximum":10000,"default":700,"title":"Delay per step"},"failureStep":{"type":"integer","minimum":0,"maximum":8,"default":0,"title":"Failure step"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "CheckResult", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "CheckResult", Version: "v1alpha1"}},
	}
}

func (Plugin) Validate(ctx context.Context, raw json.RawMessage) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "$", Message: err.Error()})
		return report
	}
	var spec referenceSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "$", Message: "Configuration must be valid JSON."})
		return report
	}
	if spec.Message == "" {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "message", Message: "A message is required."})
	}
	if spec.Steps < 1 || spec.Steps > 8 {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "steps", Message: "Steps must be between 1 and 8."})
	}
	if spec.DelayMillis < 100 || spec.DelayMillis > 10000 {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "delayMillis", Message: "Delay must be between 100 and 10000 milliseconds."})
	}
	if spec.FailureStep < 0 || spec.FailureStep > spec.Steps {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "failureStep", Message: "Failure step must be zero or reference an existing step."})
	}
	if report.Valid {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("All %d steps can be executed.", spec.Steps)})
	}
	return report
}

func (Plugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec referenceSpec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return domain.Plan{}, err
	}
	steps := make([]domain.PlanStep, 0, spec.Steps)
	for position := 1; position <= spec.Steps; position++ {
		input, err := json.Marshal(map[string]any{
			"position":    position,
			"message":     spec.Message,
			"delayMillis": spec.DelayMillis,
			"shouldFail":  position == spec.FailureStep,
		})
		if err != nil {
			return domain.Plan{}, err
		}
		step := domain.PlanStep{
			ID: fmt.Sprintf("check-%d", position), Name: fmt.Sprintf("Verification %d", position), Input: input,
			Outputs: []domain.ArtifactOutput{{Name: "result", Type: "CheckResult", Version: "v1alpha1", MediaType: "application/json"}},
		}
		if position > 1 {
			step.ArtifactInputs = []domain.ArtifactInput{{Name: "previous-result", Type: "CheckResult", Version: "v1alpha1", FromStep: fmt.Sprintf("check-%d", position-1), FromOutput: "result"}}
		}
		steps = append(steps, step)
	}
	return domain.Plan{PluginID: Plugin{}.Manifest().ID, Steps: steps}, nil
}

func (Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	if err := log("info", "Checking step inputs and dependencies"); err != nil {
		return domain.HealthReport{}, err
	}
	select {
	case <-ctx.Done():
		return domain.HealthReport{}, ctx.Err()
	default:
	}
	for _, input := range step.ArtifactInputs {
		resolved, exists := step.ResolvedInputs[input.Name]
		if !exists || len(resolved.Value) == 0 || !json.Valid(resolved.Value) {
			return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "A required typed input is unavailable", Checks: map[string]string{"artifacts": "invalid"}}, nil
		}
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Inputs and dependencies are healthy", Checks: map[string]string{"configuration": "valid", "dependencies": "healthy"}}, nil
}

func (Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	var input struct {
		Position    int    `json:"position"`
		Message     string `json:"message"`
		DelayMillis int    `json:"delayMillis"`
		ShouldFail  bool   `json:"shouldFail"`
	}
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return nil, err
	}
	if err := log("info", fmt.Sprintf("Running %s: %s", step.Name, input.Message)); err != nil {
		return nil, err
	}
	timer := time.NewTimer(time.Duration(input.DelayMillis) * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
	}
	if input.ShouldFail {
		return nil, fmt.Errorf("requested failure at step %d", input.Position)
	}
	result, err := json.Marshal(map[string]any{"position": input.Position, "message": input.Message, "finishedAt": time.Now().UTC()})
	if err != nil {
		return nil, err
	}
	if err := log("info", "Execution completed; starting post-condition checks"); err != nil {
		return nil, err
	}
	return result, nil
}

func (Plugin) Verify(ctx context.Context, step domain.PlanStep, result json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	if len(result) == 0 || !json.Valid(result) {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "The expected result is missing", Checks: map[string]string{"result": "missing"}}, nil
	}
	if err := log("info", "Post-conditions are healthy"); err != nil {
		return domain.HealthReport{}, err
	}
	select {
	case <-ctx.Done():
		return domain.HealthReport{}, ctx.Err()
	default:
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "All post-conditions passed", Checks: map[string]string{"result": "valid", "stability": "healthy"}}, nil
}
