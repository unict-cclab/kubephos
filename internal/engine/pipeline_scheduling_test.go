package engine

import (
	"reflect"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestPipelineSchedulingHonorsLimitsDependenciesAndLegacyOrder(t *testing.T) {
	def := domain.PipelineDefinition{MaxParallelStages: 2, Stages: []domain.PipelineStage{{ID: "a"}, {ID: "a-next", DependsOn: []string{"a"}}, {ID: "b"}, {ID: "mesh", Bindings: []domain.PipelineBinding{{FromStage: "a-next"}, {FromStage: "b"}}}}}
	stages := []domain.PipelineRunStage{{StageID: "a", Status: domain.StepPending}, {StageID: "a-next", Status: domain.StepPending}, {StageID: "b", Status: domain.StepPending}, {StageID: "mesh", Status: domain.StepPending}}
	check := func(want []int) {
		t.Helper()
		if got := readyPipelineStages(def, stages); !reflect.DeepEqual(got, want) {
			t.Fatalf("ready=%v want=%v", got, want)
		}
	}
	check([]int{0, 2})
	stages[0].OperationID = "op-a"
	stages[0].Status = domain.OperationRunning
	stages[2].OperationID = "op-b"
	stages[2].Status = domain.OperationRunning
	check([]int{})
	stages[0].Status = domain.OperationSucceeded
	check([]int{1})
	stages[1].OperationID = "op-a-next"
	stages[1].Status = domain.OperationSucceeded
	check([]int{})
	stages[2].Status = domain.OperationSucceeded
	check([]int{3})
	def.MaxParallelStages = 0
	for i := range stages {
		stages[i].OperationID = ""
		stages[i].Status = domain.StepPending
	}
	check([]int{0})
}
