package api

import (
	"errors"
	"net/http"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/workflows"
)

func canRetryClusterMesh(run domain.PipelineRun) bool {
	if run.Status != domain.OperationFailed && run.Status != domain.OperationCanceled || len(run.Stages) < 2 {
		return false
	}
	last := run.Stages[len(run.Stages)-1]
	if last.StageID != "mesh" || last.PluginID != "io.kubephos.cluster.cilium.mesh" || last.Status != domain.StepFailed && last.Status != domain.StepCanceled {
		return false
	}
	for _, stage := range run.Stages[:len(run.Stages)-1] {
		if stage.Status != domain.StepSucceeded || stage.OperationID == "" {
			return false
		}
	}
	return true
}

// Keep completed operations and their verified outputs. Only the final mesh
// stage receives a new plan and operation, so healthy clusters stay intact.
func (s *Server) retryClusterMesh(w http.ResponseWriter, r *http.Request, resource domain.ManagedResource, source domain.PipelineRun, previous domain.Pipeline) {
	ctx := r.Context()
	last := len(source.Stages) - 1
	if previous.Definition.CleanupAfterRun || len(previous.Definition.Stages) != len(source.Stages) || len(previous.Resolution.Stages) != len(source.Stages) {
		writeError(w, 409, "retry_unavailable", "The saved cluster workflow cannot resume its mesh stage.")
		return
	}
	provisioning := clusterProvisioningStages(previous.Definition)
	validated, err := workflows.ValidateDeferred(ctx, s.registry, provisioning)
	if err != nil || !validated.Validation.Valid || len(validated.Resolution.Stages) == 0 {
		writeError(w, 422, "retry_invalid", "The saved mesh configuration is no longer valid.")
		return
	}
	meshIndex := len(validated.Resolution.Stages) - 1
	mesh := validated.Resolution.Stages[meshIndex]
	if mesh.ID != "mesh" || mesh.PluginID != "io.kubephos.cluster.cilium.mesh" {
		writeError(w, 409, "retry_unavailable", "The mesh must be the final cluster stage.")
		return
	}
	shiftPipelineTokens(&mesh, meshIndex+1, last+1)
	definition := previous.Definition
	resolution := previous.Resolution
	resolution.Stages = append([]domain.ResolvedPipelineStage(nil), resolution.Stages...)
	resolution.Stages[last] = mesh
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: resource.WorkspaceID, Name: "retry-mesh-" + id.New("attempt"), Definition: definition, Resolution: resolution}
	produced := map[string]map[string]string{}
	for _, stage := range source.Stages[:last] {
		operation, err := s.store.GetOperation(ctx, stage.OperationID)
		if err != nil || operation.Status != domain.OperationSucceeded || operation.WorkspaceID != resource.WorkspaceID {
			writeError(w, 409, "retry_unavailable", "A completed cluster stage is unavailable.")
			return
		}
		outputs := map[string]string{}
		for _, artifact := range operation.Artifacts {
			if artifact.OutputName != "" {
				outputs[artifact.OutputName] = artifact.ID
			}
		}
		produced[stage.StageID] = outputs
	}
	resolved, err := workflows.ResolveStage(pipeline, last, produced)
	if err != nil {
		writeError(w, 409, "retry_unavailable", "The completed clusters no longer provide the required mesh inputs.")
		return
	}
	// This validates artifact integrity and performs a live precheck on every
	// member using the existing connections, without changing any cluster.
	checked, failure := s.validatedManagedPipeline(ctx, resource.WorkspaceID, domain.PipelineDefinition{Stages: []domain.PipelineStage{{ID: "mesh", PluginID: mesh.PluginID, Title: source.Stages[last].Title, Spec: resolved.Spec}}, Result: domain.PipelineOutput{Stage: "mesh", Output: definition.Result.Output}})
	if failure != nil {
		failure.write(w)
		return
	}
	pipeline.Validation = checked.Validation
	pipeline.Hash, err = workflows.Fingerprint(definition, resolution)
	if err != nil {
		writeError(w, 500, "planning_failed", "Could not fingerprint the mesh retry.")
		return
	}
	run := pipelineRunForManagedPipeline(pipeline, "Retry mesh "+resource.Name)
	if err = s.store.RetryManagedClusterMesh(ctx, resource.ID, source.ID, resource.Spec, pipeline, run); errors.Is(err, storage.ErrConflict) {
		writeError(w, 409, "cluster_in_use", "The cluster changed or another operation is running.")
		return
	} else if err != nil {
		writeError(w, 500, "database_error", "Could not queue the mesh retry.")
		return
	}
	updated, err := s.store.GetManagedResource(ctx, resource.ID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not read the mesh retry.")
		return
	}
	writeJSON(w, http.StatusAccepted, updated)
}
