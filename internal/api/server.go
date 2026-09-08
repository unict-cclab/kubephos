package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/schema"
	"kubephos.dev/kubephos/internal/secrets"
	"kubephos.dev/kubephos/internal/storage"
	"kubephos.dev/kubephos/internal/webui"
)

type Server struct {
	store     *storage.Store
	registry  *plugins.Registry
	artifacts *artifacts.Client
	vault     *secrets.Vault
	version   string
}

func NewServer(store *storage.Store, registry *plugins.Registry, artifactStore *artifacts.Client, vault *secrets.Vault, version string) http.Handler {
	server := &Server{store: store, registry: registry, artifacts: artifactStore, vault: vault, version: version}
	router := http.NewServeMux()
	router.HandleFunc("GET /health/live", server.live)
	router.HandleFunc("GET /health/ready", server.ready)
	router.HandleFunc("GET /api/v1/auth/status", server.authStatus)
	router.HandleFunc("POST /api/v1/auth/setup", server.authSetup)
	router.HandleFunc("POST /api/v1/auth/login", server.authLogin)
	router.HandleFunc("POST /api/v1/auth/logout", server.authLogout)
	router.HandleFunc("GET /api/v1/system", server.system)
	router.HandleFunc("GET /api/v1/plugins", server.listPlugins)
	router.HandleFunc("GET /api/v1/credentials", server.listCredentials)
	router.HandleFunc("POST /api/v1/credentials", server.createCredential)
	router.HandleFunc("GET /api/v1/connections", server.listConnections)
	router.HandleFunc("POST /api/v1/connections", server.createConnection)
	router.HandleFunc("GET /api/v1/infrastructure/resources", server.listInfrastructureResources)
	router.HandleFunc("GET /api/v1/audit", server.listAuditEvents)
	router.HandleFunc("GET /api/v1/workspaces", server.listWorkspaces)
	router.HandleFunc("POST /api/v1/workspaces", server.createWorkspace)
	router.HandleFunc("GET /api/v1/workspaces/{id}", server.getWorkspace)
	router.HandleFunc("GET /api/v1/operations", server.listOperations)
	router.HandleFunc("POST /api/v1/operations", server.createOperation)
	router.HandleFunc("GET /api/v1/operations/{id}", server.getOperation)
	router.HandleFunc("POST /api/v1/operations/{id}/queue", server.queueOperation)
	router.HandleFunc("POST /api/v1/operations/{id}/cancel", server.cancelOperation)
	router.HandleFunc("GET /api/v1/operations/{id}/logs", server.listLogs)
	router.HandleFunc("GET /api/v1/operations/{id}/events", server.operationEvents)
	router.HandleFunc("GET /api/v1/artifacts/{id}/download", server.downloadArtifact)
	router.Handle("/", webui.Handler())
	return securityHeaders(requestLog(server.authenticate(auditMutations(store, recoverer(router)))))
}

func (s *Server) live(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) ready(response http.ResponseWriter, request *http.Request) {
	if err := s.store.Ready(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	if err := s.artifacts.Health(request.Context()); err != nil {
		writeError(response, http.StatusServiceUnavailable, "not_ready", err.Error())
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "healthy"})
}

func (s *Server) system(response http.ResponseWriter, request *http.Request) {
	stats, err := s.store.Stats(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read platform status.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"name":      "KubePhos",
		"version":   s.version,
		"status":    "healthy",
		"stats":     stats,
		"checkedAt": time.Now().UTC(),
	})
}

func (s *Server) listPlugins(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]any{"items": s.registry.Manifests()})
}

func (s *Server) listCredentials(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListCredentials(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list credentials.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createCredential(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name  string          `json:"name"`
		Kind  string          `json:"kind"`
		Value json.RawMessage `json:"value"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	if input.Name == "" || len(input.Name) > 80 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 80 characters.")
		return
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,79}$`).MatchString(input.Kind) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_kind", "Credential kind is invalid.")
		return
	}
	definition, found := s.credentialSchema(input.Kind)
	if !found {
		writeError(response, http.StatusUnprocessableEntity, "unsupported_kind", "No installed plugin declares this credential kind.")
		return
	}
	issues, err := schema.Validate(definition, input.Value)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_schema", err.Error())
		return
	}
	if len(issues) > 0 {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "issues": issues})
		return
	}
	nonce, ciphertext, fingerprint, err := s.vault.Encrypt(input.Value)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "encryption_failed", "Could not encrypt credential.")
		return
	}
	credential, err := s.store.CreateCredential(request.Context(), domain.EncryptedCredential{
		Credential: domain.Credential{ID: id.New("cred"), Name: input.Name, Kind: input.Kind, Fingerprint: fingerprint},
		Nonce:      nonce, Ciphertext: ciphertext,
	})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "credential_conflict", "A credential with this name and kind already exists.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not store credential.")
		return
	}
	writeJSON(response, http.StatusCreated, credential)
}

func (s *Server) credentialSchema(kind string) (json.RawMessage, bool) {
	for _, manifest := range s.registry.Manifests() {
		for _, definition := range manifest.CredentialSchemas {
			if definition.Kind == kind {
				return definition.Schema, true
			}
		}
	}
	return nil, false
}

func (s *Server) listConnections(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListProviderConnections(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list provider connections.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createConnection(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name          string          `json:"name"`
		PluginID      string          `json:"pluginId"`
		Configuration json.RawMessage `json:"configuration"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len(input.Name) > 80 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Name must contain between 1 and 80 characters.")
		return
	}
	plugin, err := s.registry.Get(input.PluginID)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	manifest := plugin.Manifest()
	if manifest.Provider == "" || !manifest.HasCapability("infrastructure.discovery") {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", "Select an infrastructure discovery plugin.")
		return
	}
	issues, err := schema.Validate(manifest.Schema, input.Configuration)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_schema", err.Error())
		return
	}
	if len(issues) > 0 {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "issues": issues})
		return
	}
	report := plugin.Validate(request.Context(), input.Configuration)
	if !report.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "validation": report})
		return
	}
	connection, err := s.store.CreateProviderConnection(request.Context(), domain.ProviderConnection{ID: id.New("conn"), Name: input.Name, Provider: manifest.Provider, PluginID: manifest.ID, Configuration: input.Configuration})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "connection_conflict", "A connection with this name already exists for the provider.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not store provider connection.")
		return
	}
	writeJSON(response, http.StatusCreated, connection)
}

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

func (s *Server) listOperations(response http.ResponseWriter, request *http.Request) {
	limit := 50
	if raw := request.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err == nil && parsed >= 1 && parsed <= 200 {
			limit = parsed
		}
	}
	items, err := s.store.ListOperations(request.Context(), limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list operations.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getOperation(response http.ResponseWriter, request *http.Request) {
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
	if _, err := s.store.GetWorkspace(request.Context(), input.WorkspaceID); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_workspace", "Select an existing workspace.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate workspace.")
		return
	}
	plugin, err := s.registry.Get(input.PluginID)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plugin", err.Error())
		return
	}
	manifest := plugin.Manifest()
	issues, err := schema.Validate(manifest.Schema, input.Spec)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "invalid_schema", err.Error())
		return
	}
	if len(issues) > 0 {
		converted := make([]domain.ValidationIssue, 0, len(issues))
		for _, issue := range issues {
			converted = append(converted, domain.ValidationIssue{Level: "error", Path: issue.Path, Message: issue.Message})
		}
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "validation": domain.ValidationReport{Valid: false, Issues: converted, CheckedAt: time.Now().UTC()}})
		return
	}
	validation := plugin.Validate(request.Context(), input.Spec)
	if !validation.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "validation_failed", "validation": validation})
		return
	}
	plan, err := plugin.Plan(request.Context(), input.Spec)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "planning_failed", err.Error())
		return
	}
	if len(plan.Steps) == 0 {
		writeError(response, http.StatusUnprocessableEntity, "empty_plan", "The plugin produced an empty plan.")
		return
	}
	if err := validatePlanEffects(manifest, plan); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_plan_effects", err.Error())
		return
	}
	if err := s.validateResourceEffects(request.Context(), input.WorkspaceID, manifest, plan); err != nil {
		writeError(response, http.StatusConflict, "resource_conflict", err.Error())
		return
	}
	planHash, err := resolvedPlanHash(manifest, input.Spec, plan)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "planning_failed", "Could not fingerprint the validated plan.")
		return
	}
	operation, err := s.store.CreateOperation(request.Context(), domain.Operation{
		ID:          id.New("op"),
		WorkspaceID: input.WorkspaceID,
		PluginID:    input.PluginID,
		Title:       input.Title,
		Status:      domain.OperationReady,
		Spec:        input.Spec,
		Plan:        plan,
		Validation:  validation,
		PlanHash:    planHash,
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated operation.")
		return
	}
	writeJSON(response, http.StatusCreated, operation)
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
	operation, err := s.store.QueueOperation(request.Context(), request.PathValue("id"), input.PlanHash, input.AcceptWarnings)
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
	after, _ := strconv.ParseInt(request.URL.Query().Get("after"), 10, 64)
	items, err := s.store.ListLogs(request.Context(), request.PathValue("id"), after, 1000)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read operation logs.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) operationEvents(response http.ResponseWriter, request *http.Request) {
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

func (s *Server) downloadArtifact(response http.ResponseWriter, request *http.Request) {
	artifact, err := s.store.GetArtifact(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Artifact not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read artifact metadata.")
		return
	}
	reader, contentType, err := s.artifacts.Get(request.Context(), artifact.StorageKey)
	if err != nil {
		writeError(response, http.StatusBadGateway, "storage_error", "Could not read artifact data.")
		return
	}
	defer reader.Close()
	if contentType == "" {
		contentType = artifact.MediaType
	}
	response.Header().Set("Content-Type", contentType)
	response.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", artifact.Name+".json"))
	response.Header().Set("X-Content-Type-Options", "nosniff")
	if _, err := io.Copy(response, reader); err != nil {
		slog.Error("stream artifact", "artifact", artifact.ID, "error", err)
	}
}

func resolvedPlanHash(manifest plugins.Manifest, spec json.RawMessage, plan domain.Plan) (string, error) {
	value, err := json.Marshal(struct {
		PluginID      string          `json:"pluginId"`
		PluginVersion string          `json:"pluginVersion"`
		Spec          json.RawMessage `json:"spec"`
		Plan          domain.Plan     `json:"plan"`
	}{manifest.ID, manifest.Version, spec, plan})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:]), nil
}

func validatePlanEffects(manifest plugins.Manifest, plan domain.Plan) error {
	if plan.PluginID != manifest.ID {
		return fmt.Errorf("plan plugin identity does not match the selected plugin")
	}
	if len(plan.Steps) > 100 {
		return fmt.Errorf("plan exceeds the maximum number of steps")
	}
	stepIDs := map[string]bool{}
	for _, step := range plan.Steps {
		if step.ID == "" || step.Name == "" || stepIDs[step.ID] || !json.Valid(step.Input) {
			return fmt.Errorf("plan contains an invalid or duplicate step identity")
		}
		stepIDs[step.ID] = true
		for _, effect := range step.Effects {
			if manifest.Provider == "" {
				return fmt.Errorf("resource effects require a provider identity")
			}
			if effect.Action != "create" && effect.Action != "delete" {
				return fmt.Errorf("step %q declares unsupported effect %q", step.ID, effect.Action)
			}
			if effect.Action == "create" && !manifest.HasCapability("infrastructure.provision") {
				return fmt.Errorf("plugin is not allowed to provision infrastructure")
			}
			if effect.Action == "delete" && !manifest.HasCapability("infrastructure.deprovision") {
				return fmt.Errorf("plugin is not allowed to deprovision infrastructure")
			}
			if effect.ExternalID == "" || effect.Kind == "" || effect.Name == "" {
				return fmt.Errorf("step %q declares an incomplete resource effect", step.ID)
			}
		}
	}
	return nil
}

func (s *Server) validateResourceEffects(ctx context.Context, workspaceID string, manifest plugins.Manifest, plan domain.Plan) error {
	for position, step := range plan.Steps {
		for _, effect := range step.Effects {
			switch effect.Action {
			case "create":
				if err := s.store.EnsureResourceAvailable(ctx, manifest.Provider, effect.ExternalID); err != nil {
					return err
				}
			case "delete":
				createdInPlan := false
				for _, previous := range plan.Steps[:position] {
					for _, candidate := range previous.Effects {
						if candidate.Action == "create" && candidate.ExternalID == effect.ExternalID {
							createdInPlan = true
						}
					}
				}
				if !createdInPlan {
					if err := s.store.AuthorizeManagedResource(ctx, manifest.Provider, effect.ExternalID, workspaceID); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func terminal(status string) bool {
	return status == domain.OperationSucceeded || status == domain.OperationFailed || status == domain.OperationCanceled
}

type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newBufferedResponse() *bufferedResponse {
	return &bufferedResponse{header: make(http.Header)}
}

func (r *bufferedResponse) Header() http.Header {
	return r.header
}

func (r *bufferedResponse) WriteHeader(status int) {
	if r.status == 0 {
		r.status = status
	}
}

func (r *bufferedResponse) Write(value []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return r.body.Write(value)
}

func auditMutations(store *storage.Store, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		action, targetType, targetID, audited := auditTarget(request)
		if !audited {
			next.ServeHTTP(response, request)
			return
		}
		buffered := newBufferedResponse()
		next.ServeHTTP(buffered, request)
		if buffered.status == 0 {
			buffered.status = http.StatusOK
		}
		if targetID == "" && buffered.status < 300 {
			var created struct {
				ID string `json:"id"`
			}
			if json.Unmarshal(buffered.body.Bytes(), &created) == nil {
				targetID = created.ID
			}
		}
		outcome := "succeeded"
		if buffered.status >= 500 {
			outcome = "failed"
		} else if buffered.status >= 400 {
			outcome = "rejected"
		}
		details, _ := json.Marshal(map[string]any{"method": request.Method, "path": request.URL.Path, "status": buffered.status})
		if err := store.AppendAuditEvent(request.Context(), domain.AuditEvent{Actor: actorName(request), Action: action, TargetType: targetType, TargetID: targetID, Outcome: outcome, Details: details}); err != nil {
			slog.Error("append audit event", "action", action, "error", err)
		}
		for key, values := range buffered.header {
			response.Header()[key] = values
		}
		response.WriteHeader(buffered.status)
		_, _ = response.Write(buffered.body.Bytes())
	})
}

func auditTarget(request *http.Request) (string, string, string, bool) {
	if request.Method != http.MethodPost && request.Method != http.MethodPut && request.Method != http.MethodPatch && request.Method != http.MethodDelete {
		return "", "", "", false
	}
	path := strings.Trim(strings.TrimPrefix(request.URL.Path, "/api/v1/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 1 {
		switch parts[0] {
		case "workspaces":
			return "workspace.create", "workspace", "", true
		case "credentials":
			return "credential.create", "credential", "", true
		case "connections":
			return "connection.create", "connection", "", true
		case "operations":
			return "operation.create", "operation", "", true
		}
	}
	if len(parts) == 3 && parts[0] == "operations" {
		switch parts[2] {
		case "queue":
			return "operation.queue", "operation", parts[1], true
		case "cancel":
			return "operation.cancel", "operation", parts[1], true
		}
	}
	return request.Method + " " + path, "api", "", true
}

func decodeJSON(request *http.Request, target any) error {
	reader := io.LimitReader(request.Body, 1<<20)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return errors.New("request body must contain one JSON object")
	}
	return nil
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	if err := json.NewEncoder(response).Encode(value); err != nil {
		slog.Error("write response", "error", err)
	}
}

func writeError(response http.ResponseWriter, status int, code, message string) {
	writeJSON(response, status, map[string]string{"code": code, "message": message})
}

func writeSSE(response io.Writer, event string, value any) {
	payload, err := json.Marshal(value)
	if err != nil {
		return
	}
	fmt.Fprintf(response, "event: %s\ndata: %s\n\n", event, payload)
}

func requestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(response, request)
		if strings.HasPrefix(request.URL.Path, "/api/") {
			slog.Info("request", "method", request.Method, "path", request.URL.Path, "duration", time.Since(started))
		}
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		response.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		response.Header().Set("Referrer-Policy", "no-referrer")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				slog.Error("request panic", "error", recovered)
				writeError(response, http.StatusInternalServerError, "internal_error", "Unexpected server error.")
			}
		}()
		next.ServeHTTP(response, request)
	})
}

func Serve(ctx context.Context, address string, handler http.Handler) error {
	server := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	result := make(chan error, 1)
	go func() {
		result <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
