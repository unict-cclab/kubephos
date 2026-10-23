package api

import (
	"encoding/json"
	"errors"
	"fmt"

	"net/http"

	"strconv"
	"strings"

	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) listInfrastructureResources(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListInfrastructureResources(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list infrastructure resources.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listAuditEvents(response http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListAuditEvents(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list audit events.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listWorkspaces(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListWorkspaces(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list workspaces.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getWorkspace(response http.ResponseWriter, request *http.Request) {
	workspace, err := s.store.GetWorkspace(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Workspace not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read workspace.")
		return
	}
	writeJSON(response, http.StatusOK, workspace)
}

func (s *Server) createWorkspace(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len(input.Name) > 80 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 80 characters.")
		return
	}
	if len(input.Description) > 280 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_description", "Description cannot exceed 280 characters.")
		return
	}
	workspace, err := s.store.CreateWorkspace(request.Context(), domain.Workspace{
		ID:          id.New("ws"),
		Name:        input.Name,
		Description: input.Description,
		Status:      "ready",
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create workspace.")
		return
	}
	writeJSON(response, http.StatusCreated, workspace)
}

type createExperimentInput struct {
	WorkspaceID string                         `json:"workspaceId"`
	Name        string                         `json:"name"`
	Description string                         `json:"description"`
	Variants    []createExperimentVariantInput `json:"variants"`
}

type createExperimentVariantInput struct {
	Name             string          `json:"name"`
	Configuration    json.RawMessage `json:"configuration"`
	TrialArtifactIDs []string        `json:"trialArtifactIds"`
}

type createPipelineExperimentInput struct {
	WorkspaceID  string                                 `json:"workspaceId"`
	Name         string                                 `json:"name"`
	Description  string                                 `json:"description"`
	Repetitions  int                                    `json:"repetitions"`
	ScheduledFor *time.Time                             `json:"scheduledFor"`
	Variants     []createPipelineExperimentVariantInput `json:"variants"`
}

type createPipelineExperimentVariantInput struct {
	Name       string `json:"name"`
	PipelineID string `json:"pipelineId"`
}

func (s *Server) listExperiments(response http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListExperimentsVisibleToUser(request.Context(), viewerID(requestUser(request)), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list experiments.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) deleteExperiment(response http.ResponseWriter, request *http.Request) {
	if !s.authorizeExperiment(response, request, request.PathValue("id")) {
		return
	}
	err := s.store.DeleteExperiment(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "experiment_active", "Wait for the experiment and its cleanup operations to finish before deleting it.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not delete the experiment.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) cancelExperiment(response http.ResponseWriter, request *http.Request) {
	if !s.authorizeExperiment(response, request, request.PathValue("id")) {
		return
	}
	err := s.store.RequestExperimentCancel(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "invalid_transition", "Only a queued or running experiment can be stopped.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not stop the experiment.")
		return
	}
	response.WriteHeader(http.StatusAccepted)
}

func (s *Server) createCompletedExperiment(response http.ResponseWriter, request *http.Request) {
	var input createExperimentInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := normalizeAndValidateExperimentInput(&input); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_experiment", err.Error())
		return
	}
	if _, err := s.store.GetWorkspace(request.Context(), input.WorkspaceID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_workspace", "Select an existing workspace.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate workspace.")
		return
	}
	experiment := domain.Experiment{
		ID: id.New("exp"), WorkspaceID: input.WorkspaceID, Name: input.Name,
		OwnerUserID: requestUser(request).ID,
		Description: input.Description, Status: domain.OperationSucceeded,
	}
	seenArtifacts := map[string]bool{}
	for variantPosition, requestedVariant := range input.Variants {
		variant := domain.ExperimentVariant{
			ID: id.New("var"), ExperimentID: experiment.ID, Position: variantPosition + 1,
			Name: requestedVariant.Name, Configuration: requestedVariant.Configuration,
		}
		for trialPosition, artifactID := range requestedVariant.TrialArtifactIDs {
			if seenArtifacts[artifactID] {
				writeError(response, http.StatusUnprocessableEntity, "duplicate_trial", "A result artifact can belong to only one trial in an experiment.")
				return
			}
			seenArtifacts[artifactID] = true
			artifact, err := s.store.GetArtifact(request.Context(), artifactID)
			if errors.Is(err, storage.ErrNotFound) {
				writeError(response, http.StatusUnprocessableEntity, "invalid_trial", "A selected result artifact is unavailable.")
				return
			}
			if err != nil {
				writeError(response, http.StatusInternalServerError, "database_error", "Could not validate a result artifact.")
				return
			}
			if !s.authorizeOperation(response, request, artifact.OperationID) {
				return
			}
			source, err := s.store.GetOperation(request.Context(), artifact.OperationID)
			if err != nil || source.WorkspaceID != input.WorkspaceID || source.Status != domain.OperationSucceeded || artifact.Sensitive {
				writeError(response, http.StatusUnprocessableEntity, "invalid_trial", "Every trial must use a public result from a successful operation in the same workspace.")
				return
			}
			if err := s.verifyArtifactPayload(request.Context(), artifact); err != nil {
				writeError(response, http.StatusUnprocessableEntity, "invalid_trial", "A selected result failed integrity verification.")
				return
			}
			if experiment.ResultType == "" {
				experiment.ResultType = artifact.Type
				experiment.ResultVersion = artifact.Version
			} else if experiment.ResultType != artifact.Type || experiment.ResultVersion != artifact.Version {
				writeError(response, http.StatusUnprocessableEntity, "incompatible_trials", "All trials must implement the same artifact contract.")
				return
			}
			variant.Trials = append(variant.Trials, domain.ExperimentTrial{
				ID: id.New("trial"), VariantID: variant.ID, Position: trialPosition + 1,
				Status: domain.OperationSucceeded, OperationID: artifact.OperationID, ResultArtifactID: artifact.ID,
			})
		}
		experiment.Variants = append(experiment.Variants, variant)
	}
	created, err := s.store.CreateCompletedExperiment(request.Context(), experiment)
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusConflict, "experiment_conflict", "Experiment inputs changed during validation. Refresh and try again.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the experiment.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) createPipelineExperiment(response http.ResponseWriter, request *http.Request) {
	var input createPipelineExperimentInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if err := normalizePipelineExperimentInput(&input); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_experiment", err.Error())
		return
	}
	scheduledFor, err := normalizeScheduledFor(input.ScheduledFor, time.Now().UTC())
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_schedule", err.Error())
		return
	}
	if _, err := s.store.GetWorkspace(request.Context(), input.WorkspaceID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_workspace", "Select an existing workspace.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate workspace.")
		return
	}
	pipelines := map[string]domain.Pipeline{}
	resultType, resultVersion := "", ""
	for _, requested := range input.Variants {
		pipeline, err := s.store.GetPipeline(request.Context(), requested.PipelineID)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_pipeline", "Every variant must select an existing pipeline.")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not validate a pipeline.")
			return
		}
		if pipeline.WorkspaceID != input.WorkspaceID || !pipeline.Validation.Valid {
			writeError(response, http.StatusUnprocessableEntity, "invalid_pipeline", "Every pipeline must be validated and belong to the selected workspace.")
			return
		}
		if pipelineResultSensitive(pipeline) {
			writeError(response, http.StatusUnprocessableEntity, "sensitive_result", "Experiment results must be public artifacts that can be compared and exported.")
			return
		}
		for _, stage := range pipeline.Resolution.Stages {
			plugin, err := s.registry.Get(stage.PluginID)
			if err != nil || !plugin.Manifest().Matches(stage.PluginVersion, stage.PluginDigest) {
				writeError(response, http.StatusConflict, "plugin_changed", "An installed plug-in no longer matches a validated pipeline. Create a new pipeline version.")
				return
			}
		}
		if resultType == "" {
			resultType, resultVersion = pipeline.Resolution.Result.Type, pipeline.Resolution.Result.Version
		} else if resultType != pipeline.Resolution.Result.Type || resultVersion != pipeline.Resolution.Result.Version {
			writeError(response, http.StatusUnprocessableEntity, "incompatible_variants", "Every variant pipeline must produce the same result contract.")
			return
		}
		pipelines[pipeline.ID] = pipeline
	}
	experiment := domain.Experiment{
		ID: id.New("exp"), WorkspaceID: input.WorkspaceID, Name: input.Name, Description: input.Description,
		OwnerUserID: requestUser(request).ID,
		Status:      domain.OperationQueued, ResultType: resultType, ResultVersion: resultVersion,
		ScheduledFor: &scheduledFor,
	}
	runs := map[string]domain.PipelineRun{}
	for variantPosition, requested := range input.Variants {
		pipeline := pipelines[requested.PipelineID]
		variant := domain.ExperimentVariant{
			ID: id.New("var"), ExperimentID: experiment.ID, Position: variantPosition + 1, Name: requested.Name,
			PipelineID: pipeline.ID, PipelineHash: pipeline.Hash, Configuration: json.RawMessage(`{}`),
		}
		for trialPosition := 1; trialPosition <= input.Repetitions; trialPosition++ {
			trial := domain.ExperimentTrial{ID: id.New("trial"), VariantID: variant.ID, Position: trialPosition, Status: domain.OperationQueued}
			run := domain.PipelineRun{
				ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: input.WorkspaceID,
				OwnerUserID: experiment.OwnerUserID, IsolationMode: "exclusive",
				Name:   fmt.Sprintf("%s · %s · Trial %d", input.Name, requested.Name, trialPosition),
				Status: domain.OperationQueued, PipelineHash: pipeline.Hash,
				ResultType: resultType, ResultVersion: resultVersion,
				ScheduledFor: &scheduledFor,
			}
			for stagePosition, stage := range pipeline.Definition.Stages {
				run.Stages = append(run.Stages, domain.PipelineRunStage{
					ID: run.ID + "_" + stage.ID, RunID: run.ID, Position: stagePosition + 1,
					StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending,
				})
			}
			trial.PipelineRunID = run.ID
			variant.Trials = append(variant.Trials, trial)
			runs[trial.ID] = run
		}
		experiment.Variants = append(experiment.Variants, variant)
	}
	created, err := s.store.CreatePipelineExperiment(request.Context(), experiment, runs)
	if errors.Is(err, storage.ErrConflict) || errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusConflict, "experiment_conflict", "A selected pipeline changed during validation. Refresh and try again.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not start the experiment.")
		return
	}
	writeJSON(response, http.StatusAccepted, created)
}

func normalizePipelineExperimentInput(input *createPipelineExperimentInput) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.WorkspaceID == "" {
		return errors.New("workspace is required")
	}
	if input.Name == "" || len(input.Name) > 120 {
		return errors.New("name must contain between 1 and 120 characters")
	}
	if len(input.Description) > 500 {
		return errors.New("description cannot exceed 500 characters")
	}
	if input.Repetitions < 1 || input.Repetitions > 10 {
		return errors.New("repetitions must be between 1 and 10")
	}
	if len(input.Variants) < 2 || len(input.Variants) > 8 || len(input.Variants)*input.Repetitions > 40 {
		return errors.New("use between 2 and 8 variants and at most 40 total trials")
	}
	names, pipelineIDs := map[string]bool{}, map[string]bool{}
	for index := range input.Variants {
		variant := &input.Variants[index]
		variant.Name = strings.TrimSpace(variant.Name)
		variant.PipelineID = strings.TrimSpace(variant.PipelineID)
		if variant.Name == "" || len(variant.Name) > 80 {
			return fmt.Errorf("variant %d name must contain between 1 and 80 characters", index+1)
		}
		if variant.PipelineID == "" {
			return fmt.Errorf("variant %q requires a pipeline", variant.Name)
		}
		nameKey := strings.ToLower(variant.Name)
		if names[nameKey] {
			return errors.New("variant names must be unique")
		}
		if pipelineIDs[variant.PipelineID] {
			return errors.New("each variant must use a different validated pipeline")
		}
		names[nameKey], pipelineIDs[variant.PipelineID] = true, true
	}
	return nil
}

func normalizeScheduledFor(value *time.Time, now time.Time) (time.Time, error) {
	if value == nil {
		return now.UTC(), nil
	}
	scheduled := value.UTC()
	if scheduled.Before(now.Add(-30 * time.Second)) {
		return time.Time{}, errors.New("scheduled time cannot be in the past")
	}
	if scheduled.After(now.AddDate(1, 0, 0)) {
		return time.Time{}, errors.New("scheduled time cannot be more than one year ahead")
	}
	return scheduled, nil
}

func pipelineResultSensitive(pipeline domain.Pipeline) bool {
	for _, stage := range pipeline.Resolution.Stages {
		if stage.ID != pipeline.Definition.Result.Stage {
			continue
		}
		for _, step := range stage.Plan.Steps {
			for _, output := range step.Outputs {
				if output.Name == pipeline.Definition.Result.Output {
					return output.Sensitive
				}
			}
		}
	}
	return true
}

func normalizeAndValidateExperimentInput(input *createExperimentInput) error {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.WorkspaceID == "" {
		return errors.New("workspace is required")
	}
	if input.Name == "" || len(input.Name) > 120 {
		return errors.New("name must contain between 1 and 120 characters")
	}
	if len(input.Description) > 500 {
		return errors.New("description cannot exceed 500 characters")
	}
	if len(input.Variants) < 2 || len(input.Variants) > 8 {
		return errors.New("an experiment requires between 2 and 8 variants")
	}
	seenNames := map[string]bool{}
	for index := range input.Variants {
		variant := &input.Variants[index]
		variant.Name = strings.TrimSpace(variant.Name)
		if variant.Name == "" || len(variant.Name) > 80 {
			return fmt.Errorf("variant %d name must contain between 1 and 80 characters", index+1)
		}
		key := strings.ToLower(variant.Name)
		if seenNames[key] {
			return errors.New("variant names must be unique")
		}
		seenNames[key] = true
		if len(variant.TrialArtifactIDs) < 1 || len(variant.TrialArtifactIDs) > 20 {
			return fmt.Errorf("variant %q requires between 1 and 20 trials", variant.Name)
		}
		if len(variant.Configuration) == 0 {
			variant.Configuration = json.RawMessage(`{}`)
		}
		var configuration map[string]any
		if err := json.Unmarshal(variant.Configuration, &configuration); err != nil || configuration == nil {
			return fmt.Errorf("variant %q configuration must be a JSON object", variant.Name)
		}
		for trialIndex, artifactID := range variant.TrialArtifactIDs {
			variant.TrialArtifactIDs[trialIndex] = strings.TrimSpace(artifactID)
			if variant.TrialArtifactIDs[trialIndex] == "" {
				return fmt.Errorf("variant %q contains an empty trial artifact", variant.Name)
			}
		}
	}
	return nil
}
