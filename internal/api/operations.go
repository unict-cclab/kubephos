package api

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"

	"net/http"

	"strconv"
	"strings"

	"time"

	"kubephos.dev/kubephos/internal/artifacts"

	"kubephos.dev/kubephos/internal/domain"

	"kubephos.dev/kubephos/internal/plugins"

	"kubephos.dev/kubephos/internal/storage"

	"kubephos.dev/kubephos/internal/workflows"
)

func (s *Server) listOperations(response http.ResponseWriter, request *http.Request) {
	limit := 50
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
	items, err := s.store.ListOperationsVisibleToUser(request.Context(), ownerID, limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list operations.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getOperation(response http.ResponseWriter, request *http.Request) {
	if !s.authorizeOperation(response, request, request.PathValue("id")) {
		return
	}
	operation, err := s.store.GetOperation(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Operation not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read operation.")
		return
	}
	writeJSON(response, http.StatusOK, operation)
}

func (s *Server) createOperation(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID string          `json:"workspaceId"`
		PluginID    string          `json:"pluginId"`
		Title       string          `json:"title"`
		Spec        json.RawMessage `json:"spec"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	if input.Title == "" || len(input.Title) > 120 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_title", "Title must contain between 1 and 120 characters.")
		return
	}
	plugin, err := s.registry.Get(input.PluginID)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	operation, failure := s.validatedManagedOperation(request.Context(), input.WorkspaceID, plugin, input.Title, input.Spec)
	if failure != nil {
		failure.write(response)
		return
	}
	operation, err = s.store.CreateOperation(request.Context(), operation)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated operation.")
		return
	}
	writeJSON(response, http.StatusCreated, operation)
}

func preflightPlan(ctx context.Context, plugin plugins.Plugin, plan domain.Plan, hydrate func(context.Context, domain.PlanStep) (domain.PlanStep, error)) []domain.ValidationIssue {
	issues := make([]domain.ValidationIssue, 0, len(plan.Steps))
	priorMutation := false
	for _, step := range plan.Steps {
		path := "plan.steps." + step.ID
		requiresFutureOutput := false
		for _, input := range step.ArtifactInputs {
			requiresFutureOutput = requiresFutureOutput || input.ArtifactID == ""
		}
		if priorMutation || requiresFutureOutput || len(step.ArtifactInputs) > 0 && hydrate == nil {
			issues = append(issues, domain.ValidationIssue{Level: "info", Path: path, Message: "Dynamic preflight will run after prior outputs and mutations are verified."})
			priorMutation = priorMutation || step.Mutating || len(step.Effects) > 0
			continue
		}
		runtimeStep := step
		if len(step.ArtifactInputs) > 0 {
			var err error
			runtimeStep, err = hydrate(ctx, step)
			if err != nil {
				issues = append(issues, domain.ValidationIssue{Level: "error", Path: path, Message: "Artifact preflight failed: " + err.Error()})
				priorMutation = priorMutation || step.Mutating || len(step.Effects) > 0
				continue
			}
		}
		health, err := plugin.Precheck(ctx, runtimeStep, func(string, string) error { return nil })
		if err != nil {
			issues = append(issues, domain.ValidationIssue{Level: "error", Path: path, Message: "Preflight failed: " + err.Error()})
			continue
		}
		if health.Status != domain.HealthHealthy {
			issues = append(issues, domain.ValidationIssue{Level: "error", Path: path, Message: "Preflight is not healthy: " + health.Summary})
			continue
		}
		issues = append(issues, domain.ValidationIssue{Level: "info", Path: path, Message: "Non-mutating preflight passed: " + health.Summary})
		priorMutation = step.Mutating || len(step.Effects) > 0
	}
	return issues
}

func (s *Server) resolvePreflightArtifactInputs(ctx context.Context, workspaceID string, step domain.PlanStep) (domain.PlanStep, error) {
	step.ResolvedInputs = make(map[string]domain.ResolvedArtifact, len(step.ArtifactInputs))
	for _, input := range step.ArtifactInputs {
		if input.ArtifactID == "" {
			return domain.PlanStep{}, fmt.Errorf("input %q requires a future output", input.Name)
		}
		artifact, err := s.store.GetArtifact(ctx, input.ArtifactID)
		if err != nil {
			return domain.PlanStep{}, fmt.Errorf("resolve input %q: %w", input.Name, err)
		}
		source, err := s.store.GetOperation(ctx, artifact.OperationID)
		if err != nil || source.WorkspaceID != workspaceID || source.Status != domain.OperationSucceeded {
			return domain.PlanStep{}, fmt.Errorf("input %q does not belong to a successful operation in this workspace", input.Name)
		}
		if artifact.Type != input.Type || artifact.Version != input.Version {
			return domain.PlanStep{}, fmt.Errorf("input %q expected %s/%s, received %s/%s", input.Name, input.Type, input.Version, artifact.Type, artifact.Version)
		}
		value, err := s.readArtifactPayload(ctx, artifact)
		if err != nil {
			return domain.PlanStep{}, fmt.Errorf("verify input %q: %w", input.Name, err)
		}
		step.ResolvedInputs[input.Name] = domain.ResolvedArtifact{
			ID: artifact.ID, Type: artifact.Type, Version: artifact.Version, MediaType: artifact.MediaType,
			Digest: artifact.Digest, SizeBytes: artifact.SizeBytes, Sensitive: artifact.Sensitive, Value: value,
		}
	}
	return step, nil
}

func (s *Server) createCleanupOperation(response http.ResponseWriter, request *http.Request) {
	source, err := s.store.GetOperation(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Operation not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the source operation.")
		return
	}
	operation, failure := s.validatedManagedCleanup(request.Context(), source)
	if failure != nil {
		failure.write(response)
		return
	}
	operation.Title = "Cleanup: " + source.Title
	operation, err = s.store.CreateOperation(request.Context(), operation)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated cleanup operation.")
		return
	}
	writeJSON(response, http.StatusCreated, operation)
}

func cleanupPlan(source domain.Operation) (domain.Plan, error) {
	return workflows.CleanupPlan(source)
}

func (s *Server) validateExternalArtifactInputs(ctx context.Context, workspaceID string, plan domain.Plan) error {
	verified := map[string]domain.Artifact{}
	for _, step := range plan.Steps {
		for _, input := range step.ArtifactInputs {
			if input.ArtifactID == "" {
				continue
			}
			artifact, ok := verified[input.ArtifactID]
			if !ok {
				var err error
				artifact, err = s.store.GetArtifact(ctx, input.ArtifactID)
				if err != nil {
					return fmt.Errorf("step %q input %q references an unavailable artifact", step.ID, input.Name)
				}
				source, err := s.store.GetOperation(ctx, artifact.OperationID)
				if err != nil || source.WorkspaceID != workspaceID || source.Status != domain.OperationSucceeded {
					return fmt.Errorf("step %q input %q must reference a successful operation in the same workspace", step.ID, input.Name)
				}
				if err := s.verifyArtifactPayload(ctx, artifact); err != nil {
					return fmt.Errorf("step %q input %q failed integrity verification: %w", step.ID, input.Name, err)
				}
				verified[input.ArtifactID] = artifact
			}
			if artifact.Type != input.Type || artifact.Version != input.Version {
				return fmt.Errorf("step %q input %q requires %s/%s but artifact %s provides %s/%s", step.ID, input.Name, input.Type, input.Version, input.ArtifactID, artifact.Type, artifact.Version)
			}
		}
	}
	return nil
}

func (s *Server) verifyArtifactPayload(ctx context.Context, artifact domain.Artifact) error {
	_, err := s.readArtifactPayload(ctx, artifact)
	return err
}

func (s *Server) readArtifactPayload(ctx context.Context, artifact domain.Artifact) ([]byte, error) {
	stored, err := s.artifacts.ReadVerified(ctx, artifact.StorageKey, artifact.StorageDigest, artifact.StoredSizeBytes)
	if err != nil {
		return nil, err
	}
	value := stored
	if artifact.Sensitive {
		if s.vault == nil {
			return nil, errors.New("artifact vault is unavailable")
		}
		value, err = s.vault.DecryptBound(artifact.EncryptionNonce, stored, []byte(artifact.ID))
		if err != nil {
			return nil, err
		}
	}
	if err := artifacts.Verify(value, artifact.Digest, artifact.SizeBytes); err != nil {
		return nil, err
	}
	if artifact.MediaType == "application/json" && !json.Valid(value) {
		return nil, errors.New("artifact contains invalid JSON")
	}
	return value, nil
}

func (s *Server) queueOperation(response http.ResponseWriter, request *http.Request) {
	var input struct {
		PlanHash       string `json:"planHash"`
		AcceptWarnings bool   `json:"acceptWarnings"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	pending, err := s.store.GetOperation(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Operation not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read operation.")
		return
	}
	plugin, err := s.registry.Get(pending.PluginID)
	if err != nil || !plugin.Manifest().Matches(pending.PluginVersion, pending.PluginDigest) {
		writeError(response, http.StatusConflict, "plugin_changed", "The installed plug-in no longer matches the validated operation. Validate it again.")
		return
	}
	operation, err := s.store.QueueOperation(request.Context(), pending.ID, input.PlanHash, input.AcceptWarnings)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "invalid_transition", err.Error())
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not queue operation.")
		return
	}
	writeJSON(response, http.StatusAccepted, operation)
}

func (s *Server) cancelOperation(response http.ResponseWriter, request *http.Request) {
	if err := s.store.RequestCancel(request.Context(), request.PathValue("id")); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "invalid_transition", "Operation cannot be canceled in its current state.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not request cancellation.")
		return
	}
	response.WriteHeader(http.StatusAccepted)
}

func (s *Server) listLogs(response http.ResponseWriter, request *http.Request) {
	if !s.authorizeOperation(response, request, request.PathValue("id")) {
		return
	}
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	items, err := s.store.ListLogs(request.Context(), request.PathValue("id"), after, 1000)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read operation logs.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) operationEvents(response http.ResponseWriter, request *http.Request) {
	if !s.authorizeOperation(response, request, request.PathValue("id")) {
		return
	}
	flusher, ok := response.(http.Flusher)
	if !ok {
		writeError(response, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	response.Header().Set("Content-Type", "text/event-stream")
	response.Header().Set("Cache-Control", "no-cache")
	response.Header().Set("Connection", "keep-alive")
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	ticker := time.NewTicker(700 * time.Millisecond)
	defer ticker.Stop()
	for {
		logs, err := s.store.ListLogs(request.Context(), request.PathValue("id"), after, 200)
		if err != nil {
			writeSSE(response, "error", map[string]string{"message": "Could not read logs."})
			flusher.Flush()
			return
		}
		for _, entry := range logs {
			writeSSE(response, "log", entry)
			after = entry.Sequence
		}
		// Drain full batches before reporting completion so long logs are not truncated.
		if len(logs) == 200 {
			flusher.Flush()
			continue
		}
		operation, err := s.store.GetOperation(request.Context(), request.PathValue("id"))
		if err != nil {
			writeSSE(response, "error", map[string]string{"message": "Operation no longer exists."})
			flusher.Flush()
			return
		}
		writeSSE(response, "status", operation)
		flusher.Flush()
		if terminal(operation.Status) {
			return
		}
		select {
		case <-request.Context().Done():
			return
		case <-ticker.C:
		}
	}
}
