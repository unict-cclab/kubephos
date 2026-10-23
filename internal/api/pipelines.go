package api

import (
	"context"

	"errors"

	"net/http"

	"strconv"
	"strings"

	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	"kubephos.dev/kubephos/internal/storage"

	"kubephos.dev/kubephos/internal/workflows"
)

func (s *Server) listPipelines(response http.ResponseWriter, request *http.Request) {
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListPipelines(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list pipelines.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPipeline(response http.ResponseWriter, request *http.Request) {
	pipeline, err := s.store.GetPipeline(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline.")
		return
	}
	writeJSON(response, http.StatusOK, pipeline)
}

func (s *Server) createPipeline(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID string                    `json:"workspaceId"`
		Name        string                    `json:"name"`
		Description string                    `json:"description"`
		Definition  domain.PipelineDefinition `json:"definition"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 120 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 120 characters.")
		return
	}
	if len(input.Description) > 500 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_description", "Description cannot exceed 500 characters.")
		return
	}
	if _, err := s.store.GetWorkspace(request.Context(), input.WorkspaceID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_workspace", "Select an existing workspace.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate workspace.")
		return
	}
	result, err := workflows.Validate(request.Context(), s.registry, input.Definition)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_pipeline", err.Error())
		return
	}
	if !result.Validation.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "validation": result.Validation})
		return
	}
	for index := range result.Resolution.Stages {
		resolved := &result.Resolution.Stages[index]
		plugin, err := s.registry.Get(resolved.PluginID)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
			return
		}
		manifest := plugin.Manifest()
		if err := validatePlanEffects(manifest, resolved.Plan); err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid_plan_effects", "Stage "+resolved.ID+": "+err.Error())
			return
		}
		externalPlan := withoutPipelineBindings(resolved.Plan)
		if err := s.validateExternalArtifactInputs(request.Context(), input.WorkspaceID, externalPlan); err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid_artifact_reference", "Stage "+resolved.ID+": "+err.Error())
			return
		}
		if err := s.validateResourceEffects(request.Context(), input.WorkspaceID, manifest, resolved.Plan); err != nil {
			writeError(response, http.StatusConflict, "resource_conflict", "Stage "+resolved.ID+": "+err.Error())
			return
		}
		if hasPipelineBindings(resolved.Plan) {
			result.Validation.Issues = append(result.Validation.Issues, domain.ValidationIssue{Level: "info", Path: "stages." + resolved.ID, Message: "Runtime preflight will use verified outputs from earlier stages."})
			continue
		}
		preflight := preflightPlan(request.Context(), plugin, resolved.Plan, func(ctx context.Context, step domain.PlanStep) (domain.PlanStep, error) {
			return s.resolvePreflightArtifactInputs(ctx, input.WorkspaceID, step)
		})
		result.Validation.Issues = append(result.Validation.Issues, preflight...)
		for _, issue := range preflight {
			if issue.Level == "error" {
				result.Validation.Valid = false
			}
		}
	}
	result.Validation.CheckedAt = time.Now().UTC()
	if !result.Validation.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "preflight_failed", "validation": result.Validation})
		return
	}
	pipeline, err := s.store.CreatePipeline(request.Context(), domain.Pipeline{
		ID: id.New("pipe"), WorkspaceID: input.WorkspaceID, Name: input.Name, Description: input.Description,
		Definition: result.Definition, Resolution: result.Resolution, Validation: result.Validation, Hash: result.Hash,
	})
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "pipeline_conflict", "A pipeline with this name already exists in the workspace.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated pipeline.")
		return
	}
	writeJSON(response, http.StatusCreated, pipeline)
}

func (s *Server) listPipelineRuns(response http.ResponseWriter, request *http.Request) {
	limit := 100
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	user := requestUser(request)
	ownerID := user.ID
	if user.Role == "admin" {
		ownerID = ""
	}
	items, err := s.store.ListPipelineRunsVisibleToUser(request.Context(), ownerID, limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list pipeline runs.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getPipelineRun(response http.ResponseWriter, request *http.Request) {
	run, err := s.store.GetPipelineRun(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline run not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline run.")
		return
	}
	if !s.canReadResource(request, "pipeline-runs", run.ID) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline run not found.")
		return
	}
	writeJSON(response, http.StatusOK, run)
}

func (s *Server) listPipelineRunLogs(response http.ResponseWriter, request *http.Request) {
	run, err := s.store.GetPipelineRun(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && !s.canReadResource(request, "pipeline-runs", run.ID) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline run not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline run.")
		return
	}
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	items, err := s.store.ListPipelineRunLogs(request.Context(), run.ID, after, 5000)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline run logs.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createPipelineRun(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name         string     `json:"name"`
		ScheduledFor *time.Time `json:"scheduledFor"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 120 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Run name must contain between 1 and 120 characters.")
		return
	}
	scheduledFor, err := normalizeScheduledFor(input.ScheduledFor, time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_schedule", err.Error())
		return
	}
	pipeline, err := s.store.GetPipeline(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read pipeline.")
		return
	}
	if !pipeline.Validation.Valid || len(pipeline.Resolution.Stages) != len(pipeline.Definition.Stages) {
		writeError(response, http.StatusConflict, "pipeline_invalid", "The saved pipeline is not executable.")
		return
	}
	for _, stage := range pipeline.Resolution.Stages {
		plugin, err := s.registry.Get(stage.PluginID)
		if err != nil || !plugin.Manifest().Matches(stage.PluginVersion, stage.PluginDigest) {
			writeError(response, http.StatusConflict, "plugin_changed", "An installed plug-in no longer matches the validated pipeline. Create a new pipeline version.")
			return
		}
	}
	run := domain.PipelineRun{
		ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: pipeline.WorkspaceID, Name: input.Name,
		PipelineHash: pipeline.Hash, ResultType: pipeline.Resolution.Result.Type, ResultVersion: pipeline.Resolution.Result.Version,
		ScheduledFor: &scheduledFor,
	}
	for position, stage := range pipeline.Definition.Stages {
		run.Stages = append(run.Stages, domain.PipelineRunStage{
			ID: run.ID + "_" + stage.ID, RunID: run.ID, Position: position + 1, StageID: stage.ID,
			PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending,
		})
	}
	created, err := s.store.CreatePipelineRun(request.Context(), run)
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusConflict, "pipeline_changed", "The pipeline changed while the run was being created.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not start pipeline run.")
		return
	}
	writeJSON(response, http.StatusAccepted, created)
}

func (s *Server) cancelPipelineRun(response http.ResponseWriter, request *http.Request) {
	run, readErr := s.store.GetPipelineRun(request.Context(), request.PathValue("id"))
	if readErr != nil || !ownsResource(requestUser(request), run.OwnerUserID) {
		writeError(response, http.StatusNotFound, "not_found", "Pipeline run not found.")
		return
	}
	if err := s.store.RequestPipelineRunCancel(request.Context(), request.PathValue("id")); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "invalid_transition", "Pipeline run cannot be canceled in its current state.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not request pipeline cancellation.")
		return
	}
	response.WriteHeader(http.StatusAccepted)
}

func hasPipelineBindings(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		for _, input := range step.ArtifactInputs {
			if strings.HasPrefix(input.ArtifactID, "art_pipeline_") {
				return true
			}
		}
	}
	return false
}

func withoutPipelineBindings(plan domain.Plan) domain.Plan {
	copyPlan := plan
	copyPlan.Steps = append([]domain.PlanStep(nil), plan.Steps...)
	for stepIndex := range copyPlan.Steps {
		copyPlan.Steps[stepIndex].ArtifactInputs = append([]domain.ArtifactInput(nil), plan.Steps[stepIndex].ArtifactInputs...)
		for inputIndex := range copyPlan.Steps[stepIndex].ArtifactInputs {
			input := &copyPlan.Steps[stepIndex].ArtifactInputs[inputIndex]
			if strings.HasPrefix(input.ArtifactID, "art_pipeline_") {
				input.ArtifactID = ""
			}
		}
	}
	return copyPlan
}
