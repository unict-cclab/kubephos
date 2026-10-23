package api

import (
	"context"

	"encoding/json"
	"errors"

	"log/slog"

	"net/http"

	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	"kubephos.dev/kubephos/internal/schema"

	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) listCatalogApplications(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListCatalogApplications(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list catalog applications.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

type applicationPreview struct {
	ApplicationRef     string                           `json:"applicationRef"`
	Renderer           string                           `json:"renderer"`
	Digest             string                           `json:"digest"`
	Content            string                           `json:"content"`
	LoadScenarioDigest string                           `json:"loadScenarioDigest,omitempty"`
	LoadScenarios      []applicationLoadScenarioPreview `json:"loadScenarios"`
	WorkloadTargets    []applicationWorkloadPreview     `json:"workloadTargets"`
}

type applicationWorkloadPreview struct {
	ID           string            `json:"id"`
	APIVersion   string            `json:"apiVersion"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	Selector     map[string]string `json:"selector"`
	Traits       []string          `json:"traits"`
	Index        *int              `json:"index,omitempty"`
	Dependencies []string          `json:"dependencies,omitempty"`
}

type applicationLoadScenarioPreview struct {
	ID             string `json:"id"`
	Engine         string `json:"engine"`
	TargetEndpoint string `json:"targetEndpoint"`
	RuntimeImage   string `json:"runtimeImage"`
	Script         string `json:"script"`
	ScriptDigest   string `json:"scriptDigest"`
}

type applicationPreviewFlight struct {
	done    chan struct{}
	preview applicationPreview
	failure *managedOperationFailure
}

func validateApplicationPreview(preview applicationPreview, applicationRef string) error {
	if preview.ApplicationRef != applicationRef || strings.TrimSpace(preview.Content) == "" || !strings.HasPrefix(preview.Digest, "sha256:") {
		return errors.New("the materializer returned no verified deployment manifest")
	}
	if len(preview.LoadScenarios) != 0 && !strings.HasPrefix(preview.LoadScenarioDigest, "sha256:") {
		return errors.New("the materializer returned unverified load scenarios")
	}
	for _, scenario := range preview.LoadScenarios {
		if strings.TrimSpace(scenario.ID) == "" || strings.TrimSpace(scenario.Engine) == "" || strings.TrimSpace(scenario.TargetEndpoint) == "" || strings.TrimSpace(scenario.RuntimeImage) == "" || strings.TrimSpace(scenario.Script) == "" || !strings.HasPrefix(scenario.ScriptDigest, "sha256:") {
			return errors.New("the materializer returned an invalid load scenario")
		}
	}
	for _, workload := range preview.WorkloadTargets {
		if strings.TrimSpace(workload.ID) == "" || strings.TrimSpace(workload.APIVersion) == "" || strings.TrimSpace(workload.Kind) == "" || strings.TrimSpace(workload.Name) == "" {
			return errors.New("the materializer returned an invalid workload target")
		}
	}
	return nil
}

func decodeApplicationPreview(result []byte, applicationRef string) (applicationPreview, error) {
	var materialized struct {
		ManifestSet struct {
			Metadata struct {
				Digest string `json:"digest"`
			} `json:"metadata"`
			Spec struct {
				Renderer string `json:"renderer"`
				Content  string `json:"content"`
			} `json:"spec"`
		} `json:"manifestSet"`
		LoadScenarioSet struct {
			Metadata struct {
				Digest string `json:"digest"`
			} `json:"metadata"`
			Spec struct {
				Scenarios []applicationLoadScenarioPreview `json:"scenarios"`
			} `json:"spec"`
		} `json:"loadScenarioSet"`
		WorkloadTargets []applicationWorkloadPreview `json:"workloadTargets"`
	}
	if err := json.Unmarshal(result, &materialized); err != nil {
		return applicationPreview{}, errors.New("the materializer returned no verified deployment manifest")
	}
	preview := applicationPreview{
		ApplicationRef: applicationRef, Renderer: materialized.ManifestSet.Spec.Renderer, Digest: materialized.ManifestSet.Metadata.Digest,
		Content: materialized.ManifestSet.Spec.Content, LoadScenarioDigest: materialized.LoadScenarioSet.Metadata.Digest, LoadScenarios: materialized.LoadScenarioSet.Spec.Scenarios, WorkloadTargets: materialized.WorkloadTargets,
	}
	return preview, validateApplicationPreview(preview, applicationRef)
}

func (s *Server) getCatalogApplicationManifest(response http.ResponseWriter, request *http.Request) {
	application, err := s.store.GetCatalogApplication(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Catalog application not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the catalog application.")
		return
	}
	refresh := request.URL.Query().Get("refresh") == "true"
	preview, cached, failure := s.catalogApplicationPreview(request.Context(), application, refresh)
	if failure != nil {
		failure.write(response)
		return
	}
	if cached {
		response.Header().Set("X-KubePhos-Preview-Cache", "hit")
	} else {
		response.Header().Set("X-KubePhos-Preview-Cache", "materialized")
	}
	if refresh {
		response.Header().Set("Cache-Control", "private, no-store")
	} else {
		response.Header().Set("Cache-Control", "private, max-age=300")
	}
	writeJSON(response, http.StatusOK, preview)
}

func (s *Server) resolveCatalogApplication(response http.ResponseWriter, request *http.Request) {
	application, err := s.store.GetCatalogApplication(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Catalog application not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the catalog application.")
		return
	}
	var values map[string]any
	if err := decodeJSON(request, &values); err != nil || values == nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Application values must be a JSON object.")
		return
	}
	preview, failure := s.buildApplicationPreviewWithValues(request.Context(), application, values)
	if failure != nil {
		failure.write(response)
		return
	}
	writeJSON(response, http.StatusOK, preview)
}

func (s *Server) catalogApplicationPreview(ctx context.Context, application domain.CatalogApplication, refresh bool) (applicationPreview, bool, *managedOperationFailure) {
	if !refresh {
		raw, err := s.store.GetCatalogApplicationPreview(ctx, application.ID, application.Digest)
		if err == nil {
			var preview applicationPreview
			if json.Unmarshal(raw, &preview) == nil && validateApplicationPreview(preview, application.Reference) == nil {
				return preview, true, nil
			}
			slog.Warn("discard invalid application preview", "application", application.Reference)
		} else if !errors.Is(err, storage.ErrNotFound) {
			return applicationPreview{}, false, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not read the application preview."}
		}
	}
	preview, failure := s.materializeApplicationPreview(ctx, application)
	return preview, false, failure
}

func (s *Server) materializeApplicationPreview(ctx context.Context, application domain.CatalogApplication) (applicationPreview, *managedOperationFailure) {
	key := application.Reference + ":" + application.Digest
	s.previewMu.Lock()
	if s.previewFlights == nil {
		s.previewFlights = map[string]*applicationPreviewFlight{}
	}
	if flight := s.previewFlights[key]; flight != nil {
		s.previewMu.Unlock()
		select {
		case <-flight.done:
			return flight.preview, flight.failure
		case <-ctx.Done():
			return applicationPreview{}, &managedOperationFailure{status: http.StatusRequestTimeout, code: "materialization_canceled", message: "Application preview preparation was canceled."}
		}
	}
	flight := &applicationPreviewFlight{done: make(chan struct{})}
	s.previewFlights[key] = flight
	s.previewMu.Unlock()

	preview, failure := s.buildApplicationPreview(ctx, application)
	if failure == nil {
		raw, err := json.Marshal(preview)
		if err != nil {
			failure = &managedOperationFailure{status: http.StatusInternalServerError, code: "materialization_failed", message: "Could not encode the application preview."}
		} else if err := s.store.PutCatalogApplicationPreview(ctx, application.ID, application.Digest, raw); err != nil {
			slog.Warn("store application preview", "application", application.Reference, "error", err)
		}
	}

	s.previewMu.Lock()
	flight.preview = preview
	flight.failure = failure
	close(flight.done)
	delete(s.previewFlights, key)
	s.previewMu.Unlock()
	return preview, failure
}

func (s *Server) buildApplicationPreview(ctx context.Context, application domain.CatalogApplication) (applicationPreview, *managedOperationFailure) {
	return s.buildApplicationPreviewWithValues(ctx, application, nil)
}

func (s *Server) buildApplicationPreviewWithValues(ctx context.Context, application domain.CatalogApplication, overrides map[string]any) (applicationPreview, *managedOperationFailure) {
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(application.Descriptor, &descriptor); err != nil {
		return applicationPreview{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_application", message: "The catalog application descriptor is invalid."}
	}
	plugin, err := s.applicationMaterializer(ctx, application.Reference)
	if err != nil {
		return applicationPreview{}, &managedOperationFailure{status: http.StatusConflict, code: "materializer_unavailable", message: err.Error()}
	}
	values := map[string]any{}
	for name, value := range descriptor.Spec.Defaults {
		values[name] = value
	}
	for name, value := range overrides {
		values[name] = value
	}
	configuration, err := json.Marshal(map[string]any{"applicationRef": application.Reference, "values": values})
	if err != nil {
		return applicationPreview{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "materialization_failed", message: "Could not prepare application defaults."}
	}
	issues, err := schema.Validate(plugin.Manifest().Schema, configuration)
	if err != nil || len(issues) != 0 {
		return applicationPreview{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_defaults", message: "Application defaults do not satisfy the materializer schema."}
	}
	validation := plugin.Validate(ctx, configuration)
	if !validation.Valid {
		return applicationPreview{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": validation}}
	}
	plan, err := plugin.Plan(ctx, configuration)
	if err != nil || len(plan.Steps) != 1 {
		message := "The application materializer produced an invalid plan."
		if err != nil {
			message = err.Error()
		}
		return applicationPreview{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "planning_failed", message: message}
	}
	step := plan.Steps[0]
	if step.Mutating || len(step.Effects) != 0 || len(step.ArtifactInputs) != 0 {
		return applicationPreview{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "unsafe_materializer", message: "Application manifest previews must be side-effect free."}
	}
	quiet := func(string, string) error { return nil }
	precheck, err := plugin.Precheck(ctx, step, quiet)
	if err != nil || precheck.Status != domain.HealthHealthy {
		message := precheck.Summary
		if err != nil {
			message = err.Error()
		}
		return applicationPreview{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "precheck_failed", message: message}
	}
	result, err := plugin.Execute(ctx, step, quiet)
	if err != nil {
		return applicationPreview{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "materialization_failed", message: err.Error()}
	}
	health, err := plugin.Verify(ctx, step, result, quiet)
	if err != nil || health.Status != domain.HealthHealthy {
		message := health.Summary
		if err != nil {
			message = err.Error()
		}
		return applicationPreview{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "verification_failed", message: message}
	}
	preview, err := decodeApplicationPreview(result, application.Reference)
	if err != nil {
		return applicationPreview{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "materialization_failed", message: err.Error() + "."}
	}
	return preview, nil
}

func (s *Server) warmApplicationPreviews() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	applications, err := s.store.ListCatalogApplications(ctx)
	if err != nil {
		slog.Warn("list applications for preview warmup", "error", err)
		return
	}
	jobs := make(chan domain.CatalogApplication)
	var workers sync.WaitGroup
	for range 2 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for application := range jobs {
				if _, _, failure := s.catalogApplicationPreview(ctx, application, false); failure != nil {
					slog.Warn("warm application preview", "application", application.Reference, "error", failure.message)
				}
			}
		}()
	}
	for _, application := range applications {
		select {
		case jobs <- application:
		case <-ctx.Done():
			close(jobs)
			workers.Wait()
			return
		}
	}
	close(jobs)
	workers.Wait()
}

func (s *Server) warmApplicationPreview(application domain.CatalogApplication) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if _, _, failure := s.catalogApplicationPreview(ctx, application, false); failure != nil {
		slog.Warn("warm application preview", "application", application.Reference, "error", failure.message)
	}
}

func (s *Server) listCatalogStrategies(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListCatalogStrategies(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list strategy images.")
		return
	}
	for index := range items {
		if items[index].ArtifactID == "" {
			continue
		}
		artifact, err := s.store.GetArtifact(request.Context(), items[index].ArtifactID)
		if err != nil {
			continue
		}
		raw, err := s.readArtifactPayload(request.Context(), artifact)
		if err != nil {
			continue
		}
		var value struct {
			StrategyImage struct {
				Spec struct {
					SourceDigest   string `json:"sourceDigest"`
					MirroredImage  string `json:"mirroredImage"`
					MirroredDigest string `json:"mirroredDigest"`
				} `json:"spec"`
			} `json:"strategyImage"`
		}
		if json.Unmarshal(raw, &value) == nil {
			items[index].SourceDigest = value.StrategyImage.Spec.SourceDigest
			items[index].MirroredImage = value.StrategyImage.Spec.MirroredImage
			items[index].MirroredDigest = value.StrategyImage.Spec.MirroredDigest
		}
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) importCatalogStrategy(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID          string          `json:"workspaceId"`
		Name                 string          `json:"name"`
		Kind                 string          `json:"kind"`
		SourceImage          string          `json:"sourceImage"`
		DefaultConfiguration json.RawMessage `json:"defaultConfiguration"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var existing domain.CatalogStrategy
	if request.Method == http.MethodPut {
		var err error
		existing, err = s.store.GetCatalogStrategy(request.Context(), request.PathValue("id"))
		if errors.Is(err, storage.ErrNotFound) {
			writeError(response, http.StatusNotFound, "not_found", "Catalog image not found.")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not read the strategy image.")
			return
		}
		input.WorkspaceID, input.Kind = existing.WorkspaceID, existing.Kind
		if input.Name == "" {
			input.Name = existing.Name
		}
		if len(input.DefaultConfiguration) == 0 {
			input.DefaultConfiguration = existing.DefaultConfiguration
		}
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Kind = strings.TrimSpace(input.Kind)
	input.SourceImage = strings.TrimSpace(input.SourceImage)
	if len(input.DefaultConfiguration) == 0 {
		input.DefaultConfiguration = json.RawMessage(`{}`)
	}
	if !strategyNamePattern.MatchString(input.Name) || !map[string]bool{"scheduler": true, "descheduler": true, "autoscaler": true}[input.Kind] || input.SourceImage == "" || len(input.SourceImage) > 512 || strings.ContainsAny(input.SourceImage, "\n\r\t ") {
		writeError(response, http.StatusUnprocessableEntity, "invalid_strategy", "Name, type or Docker image is invalid.")
		return
	}
	strategyPlugin, err := s.pluginForCapability("", input.Kind+".kubernetes.install")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "strategy_unsupported", "No installed plugin can run this strategy type.")
		return
	}
	strategyConfiguration := map[string]any{}
	if json.Unmarshal(input.DefaultConfiguration, &strategyConfiguration) != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_strategy_configuration", "Default strategy configuration must be a JSON object.")
		return
	}
	strategyConfiguration["clusterConnectionRef"] = "art_validation_cluster"
	strategyConfiguration["applicationDeploymentRef"] = "art_validation_application"
	strategyConfiguration["targetBindingRef"] = "art_validation_binding"
	strategyConfiguration["image"] = input.SourceImage
	strategyRaw, _ := json.Marshal(strategyConfiguration)
	strategyReport := strategyPlugin.Validate(request.Context(), strategyRaw)
	if !strategyReport.Valid {
		writeJSON(response, http.StatusUnprocessableEntity, map[string]any{"code": "invalid_strategy_configuration", "validation": strategyReport})
		return
	}
	strategy := domain.CatalogStrategy{ID: id.New("strategy"), WorkspaceID: input.WorkspaceID, Name: input.Name, Kind: input.Kind, SourceImage: input.SourceImage, DefaultConfiguration: input.DefaultConfiguration}
	if request.Method == http.MethodPut {
		strategy.ID = existing.ID
		strategy, err = s.store.UpdateCatalogStrategy(request.Context(), strategy)
	} else {
		strategy, err = s.store.CreateCatalogStrategy(request.Context(), strategy)
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "strategy_conflict", "The strategy conflicts with an existing entry or has an import in progress.")
		return
	}
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "strategy_conflict", "A strategy with this name and type already exists in the environment.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the strategy image.")
		return
	}
	if request.Method == http.MethodPut {
		writeJSON(response, http.StatusOK, strategy)
	} else {
		writeJSON(response, http.StatusCreated, strategy)
	}
}

func (s *Server) deleteCatalogStrategy(response http.ResponseWriter, request *http.Request) {
	err := s.store.DeleteCatalogStrategy(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Catalog image not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "strategy_in_use", "Wait for the import to finish and remove experiment configurations that use this image first.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not remove the catalog image.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) importCatalogApplication(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Descriptor string `json:"descriptor"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	application, err := catalog.Parse([]byte(input.Descriptor), "imported")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_application", err.Error())
		return
	}
	application, err = s.store.CreateCatalogApplication(request.Context(), application)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "application_conflict", "This application already exists.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not store application.")
		return
	}
	go s.warmApplicationPreview(application)
	writeJSON(response, http.StatusCreated, application)
}

func (s *Server) deleteCatalogApplication(response http.ResponseWriter, request *http.Request) {
	err := s.store.DeleteCatalogApplication(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Application not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "application_in_use", "Delete the experiment configurations that use this application first.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not remove the application from the catalog.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
