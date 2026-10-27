package api

import (
	"encoding/json"
	"fmt"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
)

type clusterCleanupTarget struct{ StageID, Title, OperationID string }

func clusterRecreationCleanupTargets(run domain.PipelineRun, definition domain.PipelineDefinition) ([]clusterCleanupTarget, error) {
	targets := []clusterCleanupTarget{}
	// New provisioning may have left resources behind. Remove these in reverse
	// creation order; never attempt to clean up a cleanup operation itself.
	for i := len(run.Stages) - 1; i >= 0; i-- {
		stage := run.Stages[i]
		if strings.HasPrefix(stage.StageID, "cleanup-") || stage.StageID == "prune-machines" || stage.OperationID == "" {
			continue
		}
		targets = append(targets, clusterCleanupTarget{StageID: "cleanup-" + stage.StageID, Title: "Remove " + strings.ToLower(stage.Title), OperationID: stage.OperationID})
	}
	// A previous retry may have failed during cleanup, before provisioning began.
	// Preserve the original cleanup order and the original source ownership.
	byID := map[string]domain.PipelineStage{}
	for _, stage := range definition.Stages {
		byID[stage.ID] = stage
	}
	for _, stage := range run.Stages {
		if !strings.HasPrefix(stage.StageID, "cleanup-") || stage.Status == domain.StepSucceeded {
			continue
		}
		var input struct {
			SourceOperationID string `json:"sourceOperationId"`
		}
		saved, ok := byID[stage.StageID]
		if !ok || json.Unmarshal(saved.Spec, &input) != nil || input.SourceOperationID == "" {
			return nil, fmt.Errorf("The original cleanup target for %s is unavailable.", stage.Title)
		}
		targets = append(targets, clusterCleanupTarget{StageID: stage.StageID, Title: stage.Title, OperationID: input.SourceOperationID})
	}
	return targets, nil
}
