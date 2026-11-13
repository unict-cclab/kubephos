package api

import (
	"context"
	"encoding/json"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
)

// Older maintenance runs attempted to install services again. Reuse only the
// original successful owners when the replacement never reached execution.
func (s *Server) maintenanceCleanupRun(ctx context.Context, resource domain.ManagedResource, run domain.PipelineRun) (domain.PipelineRun, error) {
	if resource.Kind != "kubernetes-cluster" || run.ID == resource.PipelineRunID {
		return run, nil
	}
	pipeline, err := s.store.GetPipeline(ctx, run.PipelineID)
	if err != nil {
		return run, err
	}
	maintenance := false
	for _, stage := range pipeline.Definition.Stages {
		if stage.ID != "machines" {
			continue
		}
		var spec struct {
			Reconcile bool `json:"reconcile"`
		}
		if json.Unmarshal(stage.Spec, &spec) == nil {
			maintenance = spec.Reconcile
		}
	}
	if !maintenance {
		return run, nil
	}
	original, err := s.store.GetPipelineRun(ctx, resource.PipelineRunID)
	if err != nil {
		return run, err
	}
	unstarted := map[string]bool{}
	for _, stage := range run.Stages {
		if !maintenanceService(stage.StageID) {
			continue
		}
		if stage.OperationID == "" {
			unstarted[stage.StageID] = true
			continue
		}
		op, err := s.store.GetOperation(ctx, stage.OperationID)
		if err != nil {
			return run, err
		}
		// Precheck failures occur before the Execute call and cannot own resources.
		unstarted[stage.StageID] = op.Status == domain.OperationFailed && strings.HasPrefix(op.Error, "precheck failed:")
	}
	return mergeMaintenanceServices(original, run, unstarted), nil
}

func maintenanceService(id string) bool {
	return id == "storage" || id == "observability" || id == "platform"
}

func mergeMaintenanceServices(original, current domain.PipelineRun, unstarted map[string]bool) domain.PipelineRun {
	current.Stages = append([]domain.PipelineRunStage(nil), current.Stages...)
	for _, old := range original.Stages {
		if !maintenanceService(old.StageID) || old.Status != domain.OperationSucceeded || old.OperationID == "" {
			continue
		}
		found := false
		for i, stage := range current.Stages {
			if stage.StageID != old.StageID {
				continue
			}
			found = true
			if unstarted[stage.StageID] && stage.PluginID == old.PluginID {
				current.Stages[i] = old
			}
		}
		if !found {
			current.Stages = append(current.Stages, old)
		}
	}
	return current
}
