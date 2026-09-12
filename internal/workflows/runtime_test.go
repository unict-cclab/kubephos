package workflows

import (
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestResolveRuntimeTokensMaterializesPlanInput(t *testing.T) {
	stage := domain.ResolvedPipelineStage{Plan: domain.Plan{Steps: []domain.PlanStep{{Input: json.RawMessage(`{"namespace":"run-${KUBEPHOS_EXECUTION_ID}"}`)}}}}
	resolved, err := ResolveRuntimeTokens(stage, map[string]string{domain.RuntimeExecutionIDToken: "run-abc"})
	if err != nil {
		t.Fatal(err)
	}
	if string(resolved.Plan.Steps[0].Input) != `{"namespace":"run-run-abc"}` {
		t.Fatalf("unexpected input %s", resolved.Plan.Steps[0].Input)
	}
	if string(stage.Plan.Steps[0].Input) == string(resolved.Plan.Steps[0].Input) {
		t.Fatal("source resolution was mutated")
	}
}
