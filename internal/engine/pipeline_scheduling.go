package engine

import (
	"context"
	"errors"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
)

// Omitted parallelism keeps the historical ordered execution of every pipeline.
// Explicit parallel pipelines wait for both control dependencies and artifact inputs.
func readyPipelineStages(def domain.PipelineDefinition, stages []domain.PipelineRunStage) []int {
	limit := def.MaxParallelStages
	if limit < 1 {
		limit = 1
	}
	complete := map[string]bool{}
	active := 0
	for _, s := range stages {
		if s.Status == domain.OperationSucceeded {
			complete[s.StageID] = true
		}
		if s.OperationID != "" && !terminalOperation(s.Status) {
			active++
		}
	}
	positions := []int{}
	for i, s := range stages {
		if active >= limit {
			break
		}
		if s.OperationID != "" || s.Status != domain.StepPending {
			continue
		}
		ready := true
		if def.MaxParallelStages <= 1 && i > 0 && !complete[stages[i-1].StageID] {
			ready = false
		}
		for _, dependency := range def.Stages[i].DependsOn {
			if !complete[dependency] {
				ready = false
			}
		}
		for _, binding := range def.Stages[i].Bindings {
			if !complete[binding.FromStage] {
				ready = false
			}
		}
		if ready {
			positions = append(positions, i)
			active++
		}
	}
	return positions
}

func (w *Worker) stopPipelineOperations(ctx context.Context, run domain.PipelineRun) (bool, error) {
	stopped := true
	for _, stage := range run.Stages {
		if stage.OperationID == "" {
			continue
		}
		operation, err := w.store.GetOperation(ctx, stage.OperationID)
		if err != nil {
			return false, err
		}
		if err = w.store.SyncPipelineRunStage(ctx, stage.ID, operation); err != nil {
			return false, err
		}
		if !terminalOperation(operation.Status) {
			stopped = false
			if err = w.store.RequestCancel(ctx, operation.ID); err != nil && !errors.Is(err, storage.ErrConflict) {
				return false, err
			}
		}
	}
	return stopped, nil
}
