package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"net/http"

	"net/url"

	"regexp"

	"strings"

	"time"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	"kubephos.dev/kubephos/internal/plugins"

	"kubephos.dev/kubephos/internal/schema"

	"kubephos.dev/kubephos/internal/storage"
)

type experimentConfigurationInput struct {
	Kind              string                                 `json:"kind,omitempty"`
	WorkspaceID       string                                 `json:"workspaceId"`
	ClusterResourceID string                                 `json:"clusterResourceId"`
	Name              string                                 `json:"name"`
	Description       string                                 `json:"description"`
	ApplicationRef    string                                 `json:"applicationRef"`
	Definition        experimentConfigurationDefinitionInput `json:"definition"`
	UpdatedAt         *time.Time                             `json:"updatedAt,omitempty"`
}

type experimentConfigurationDefinitionInput struct {
	Kind                            string                                  `json:"kind,omitempty"`
	Variants                        []experimentSuiteVariantInput           `json:"variants,omitempty"`
	Runs                            int                                     `json:"runs,omitempty"`
	DestinationMode                 string                                  `json:"destinationMode,omitempty"`
	ExternalDestination             externalLoadDestinationInput            `json:"externalDestination,omitempty"`
	ApplicationValues               json.RawMessage                         `json:"applicationValues"`
	PodDisruptionBudgetMinAvailable int                                     `json:"podDisruptionBudgetMinAvailable,omitempty"`
	Components                      []experimentConfigurationComponentInput `json:"components"`
}

type externalLoadDestinationInput struct {
	TargetURL    string            `json:"targetUrl"`
	RuntimeImage string            `json:"runtimeImage"`
	Script       string            `json:"script"`
	Parameters   map[string]string `json:"parameters,omitempty"`
}

type experimentConfigurationComponentInput struct {
	ID            string          `json:"id"`
	StrategyID    string          `json:"strategyId,omitempty"`
	PluginID      string          `json:"pluginId"`
	PluginVersion string          `json:"pluginVersion,omitempty"`
	PluginDigest  string          `json:"pluginDigest,omitempty"`
	Capability    string          `json:"capability"`
	Configuration json.RawMessage `json:"configuration"`
	Targets       struct {
		Include []string `json:"include"`
		Exclude []string `json:"exclude"`
	} `json:"targets"`
}

func (s *Server) listExperimentConfigurations(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListExperimentConfigurationsVisibleToUser(request.Context(), viewerID(requestUser(request)))
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list experiment configurations.")
		return
	}
	for index := range items {
		items[index] = s.refreshChangedExperimentConfiguration(request.Context(), items[index])
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	configuration, err := s.store.GetExperimentConfiguration(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the experiment configuration.")
		return
	}
	if !s.canReadResource(request, "experiment-configurations", configuration.ID) {
		writeError(response, 404, "not_found", "Experiment configuration not found.")
		return
	}
	configuration = s.refreshChangedExperimentConfiguration(request.Context(), configuration)
	writeJSON(response, http.StatusOK, configuration)
}

func (s *Server) createExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	var input experimentConfigurationInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	user := requestUser(request)
	configuration, failure := s.validatedExperimentConfiguration(request.Context(), input)
	if failure != nil {
		failure.write(response)
		return
	}
	if !s.validateClusterAssignment(response, request, user, configuration.ClusterResourceID) {
		return
	}
	configuration.OwnerUserID = user.ID
	created, err := s.store.CreateExperimentConfiguration(request.Context(), configuration)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "configuration_conflict", "An experiment configuration with this name already exists in the environment.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the experiment configuration.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) updateExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	var input experimentConfigurationInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.UpdatedAt == nil {
		writeError(response, http.StatusBadRequest, "missing_revision", "Reload the experiment configuration before editing it.")
		return
	}
	user := requestUser(request)
	source, err := s.store.GetExperimentConfiguration(request.Context(), request.PathValue("id"))
	if err != nil || !ownsResource(user, source.OwnerUserID) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	configuration, failure := s.validatedExperimentConfiguration(request.Context(), input)
	if failure != nil {
		failure.write(response)
		return
	}
	if !s.validateClusterAssignment(response, request, user, configuration.ClusterResourceID) {
		return
	}
	configuration.ID = request.PathValue("id")
	configuration.OwnerUserID = source.OwnerUserID
	updated, err := s.store.UpdateExperimentConfiguration(request.Context(), configuration, *input.UpdatedAt)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "configuration_conflict", "The configuration changed in another session or its name is already in use. Reload it and try again.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not update the experiment configuration.")
		return
	}
	writeJSON(response, http.StatusOK, updated)
}

func (s *Server) cloneExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	source, err := s.store.GetExperimentConfiguration(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the experiment configuration.")
		return
	}
	user := requestUser(request)
	if !ownsResource(user, source.OwnerUserID) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if !s.validateClusterAssignment(response, request, user, source.ClusterResourceID) {
		return
	}
	var definition experimentConfigurationDefinitionInput
	if err := json.Unmarshal(source.Definition, &definition); err != nil {
		writeError(response, http.StatusConflict, "invalid_snapshot", "The stored experiment configuration is invalid.")
		return
	}
	configuration, failure := s.validatedExperimentConfiguration(request.Context(), experimentConfigurationInput{WorkspaceID: source.WorkspaceID, ClusterResourceID: source.ClusterResourceID, Name: input.Name, Description: source.Description, ApplicationRef: source.ApplicationRef, Definition: definition})
	if failure != nil {
		failure.write(response)
		return
	}
	configuration.OwnerUserID = user.ID
	created, err := s.store.CreateExperimentConfiguration(request.Context(), configuration)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "configuration_conflict", "An experiment configuration with this name already exists in the environment.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not clone the experiment configuration.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) deleteExperimentConfiguration(response http.ResponseWriter, request *http.Request) {
	configuration, readErr := s.store.GetExperimentConfiguration(request.Context(), request.PathValue("id"))
	if readErr != nil || !ownsResource(requestUser(request), configuration.OwnerUserID) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	err := s.store.DeleteExperimentConfiguration(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "configuration_in_use", "This single configuration is used by a suite. Edit or delete the suite first.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not delete the experiment configuration.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) validatedExperimentConfiguration(ctx context.Context, input experimentConfigurationInput) (domain.ExperimentConfiguration, *managedOperationFailure) {
	if input.Kind == "" {
		input.Kind = input.Definition.Kind
	}
	if input.Kind == "suite" {
		return s.validatedComparativeConfiguration(ctx, input)
	}
	if input.Kind != "" && input.Kind != "single" {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_configuration_kind", message: "Configuration type must be single or suite."}
	}
	input.Definition.Kind = "single"
	if len(input.Definition.Variants) != 0 {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_configuration", message: "Single configurations cannot contain suite members."}
	}

	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ClusterResourceID = strings.TrimSpace(input.ClusterResourceID)
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	input.ApplicationRef = strings.TrimSpace(input.ApplicationRef)
	if input.Name == "" || len([]rune(input.Name)) > 120 || len([]rune(input.Description)) > 1000 {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_name", message: "Name is required, with at most 120 characters; description can contain at most 1000 characters."}
	}
	cluster, err := s.store.GetManagedResource(ctx, input.ClusterResourceID)
	if errors.Is(err, storage.ErrNotFound) || err == nil && cluster.Kind != "kubernetes-cluster" {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_cluster", message: "Select a managed Kubernetes cluster."}
	}
	if err != nil {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate the Kubernetes cluster."}
	}
	if cluster.Status != "ready" || cluster.WorkspaceID != input.WorkspaceID {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusConflict, code: "cluster_not_ready", message: "The selected cluster must be ready and belong to the same environment."}
	}
	input.Definition.DestinationMode = strings.ToLower(strings.TrimSpace(input.Definition.DestinationMode))
	if input.Definition.DestinationMode == "" {
		input.Definition.DestinationMode = "application"
	}
	external := input.Definition.DestinationMode == "external"
	if !external && input.Definition.DestinationMode != "application" {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_destination_mode", message: "Destination mode must be application or external."}
	}
	var application domain.CatalogApplication
	var descriptor catalog.Descriptor
	applicationRef := ""
	applicationDigest := ""
	if external {
		destination, reference, digest, err := normalizeExternalLoadDestination(input.Definition.ExternalDestination)
		if err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_external_destination", message: err.Error()}
		}
		input.Definition.ExternalDestination = destination
		input.Definition.ApplicationValues = json.RawMessage(`{}`)
		input.Definition.PodDisruptionBudgetMinAvailable = 0
		applicationRef, applicationDigest = reference, digest
	} else {
		applicationID, err := catalog.ParseReference(input.ApplicationRef)
		if err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_application", message: err.Error()}
		}
		application, err = s.store.GetCatalogApplication(ctx, applicationID)
		if errors.Is(err, storage.ErrNotFound) {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_application", message: "Select an enabled application from the catalog."}
		}
		if err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate the application."}
		}
		if err := json.Unmarshal(application.Descriptor, &descriptor); err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_application", message: "The selected application contract is invalid."}
		}
		input.Definition.ExternalDestination = externalLoadDestinationInput{}
		applicationRef, applicationDigest = application.Reference, application.Digest
	}
	if len(input.Definition.ApplicationValues) == 0 {
		input.Definition.ApplicationValues = json.RawMessage(`{}`)
	}
	if input.Definition.PodDisruptionBudgetMinAvailable < 0 || input.Definition.PodDisruptionBudgetMinAvailable > 20 {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_pod_disruption_budget", message: "Minimum available replicas must be between 1 and 20."}
	}
	if !external {
		var savedValues map[string]any
		if err := json.Unmarshal(input.Definition.ApplicationValues, &savedValues); err == nil && savedValues != nil {
			input.Definition.ApplicationValues, _ = json.Marshal(catalog.NormalizeApplicationValues(descriptor, savedValues))
		}

		valuesSchema, _ := json.Marshal(descriptor.Spec.ValuesSchema)
		if issues, err := schema.Validate(valuesSchema, input.Definition.ApplicationValues); err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_schema", message: "Could not validate the configuration. Check the application or plugin in the catalog and try again."}
		} else if len(issues) > 0 {
			return domain.ExperimentConfiguration{}, experimentConfigurationIssues("applicationValues", valuesSchema, issues)
		}
	}
	validation := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC()}
	if len(input.Definition.Components) > 15 {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_components", message: "An experiment configuration can contain at most 15 component instances."}
	}
	applicationComponents := map[string]bool{}
	for _, component := range descriptor.Spec.Interface.Components {
		applicationComponents[component.ID] = true
	}
	if dynamic, _ := descriptor.Spec.ValuesSchema["x-kubephos-dynamic-topology"].(bool); dynamic {
		var applicationValues map[string]any
		if err := json.Unmarshal(input.Definition.ApplicationValues, &applicationValues); err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_application_values", message: "Application settings must be a JSON object."}
		}
		preview, failure := s.buildApplicationPreviewWithValues(ctx, application, applicationValues)
		if failure != nil {
			return domain.ExperimentConfiguration{}, failure
		}
		if len(preview.WorkloadTargets) == 0 {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_application_topology", message: "Application settings produced no targetable workload."}
		}
		applicationComponents = make(map[string]bool, len(preview.WorkloadTargets))
		for _, workload := range preview.WorkloadTargets {
			applicationComponents[workload.ID] = true
		}
		validation.Issues = append(validation.Issues, domain.ValidationIssue{Level: "info", Path: "applicationValues", Message: fmt.Sprintf("Application topology resolved to %d workloads.", len(preview.WorkloadTargets))})
	}
	seenIDs := map[string]bool{}
	externalLoadComponents := 0
	externalMetricsComponents := 0
	for index := range input.Definition.Components {
		component := &input.Definition.Components[index]
		component.ID = strings.TrimSpace(component.ID)
		component.PluginID = strings.TrimSpace(component.PluginID)
		component.Capability = strings.TrimSpace(component.Capability)
		if !componentIDPattern.MatchString(component.ID) || seenIDs[component.ID] {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_component", message: "Component instance IDs must be unique lowercase labels."}
		}
		seenIDs[component.ID] = true
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plugin", message: err.Error()}
		}
		manifest := plugin.Manifest()
		component.PluginVersion = manifest.Version
		component.PluginDigest = manifest.Runtime.Digest
		if component.Capability == "" || !manifest.HasCapability(component.Capability) || strings.HasSuffix(component.Capability, ".preflight") || strings.HasSuffix(component.Capability, ".cleanup") || component.Capability == "lifecycle.cleanup" {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_capability", message: "Each component must select a primary capability declared by its plugin."}
		}
		if external {
			switch {
			case manifest.HasCapability("load.session.control") && component.Capability == "load.session.control":
				externalLoadComponents++
			case manifest.HasCapability("metrics.timeseries.collect") && component.Capability == "metrics.timeseries.collect":
				externalMetricsComponents++
			default:
				return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_external_component", message: "External destination configurations can contain only the load profile and metrics collector."}
			}
		}
		if len(component.Configuration) == 0 {
			component.Configuration = json.RawMessage(`{}`)
		}
		if external && component.Capability == "load.session.control" {
			var loadConfiguration map[string]any
			if json.Unmarshal(component.Configuration, &loadConfiguration) != nil || loadConfiguration == nil {
				return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_load_configuration", message: "External load configuration must be a JSON object."}
			}
			loadConfiguration["action"] = "start"
			loadConfiguration["interactive"] = false
			loadConfiguration["scenarioId"] = "external-load"
			delete(loadConfiguration, "geographicSteps")
			component.Configuration, _ = json.Marshal(loadConfiguration)
		}
		component.StrategyID = strings.TrimSpace(component.StrategyID)
		if component.StrategyID != "" {
			strategy, err := s.store.GetCatalogStrategy(ctx, component.StrategyID)
			if err != nil || strategy.WorkspaceID != input.WorkspaceID || strategy.Status != domain.OperationSucceeded || !strings.HasPrefix(component.Capability, strategy.Kind+".") {
				return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_strategy", message: "Select a ready strategy image matching the component type."}
			}
			image := strategy.SourceImage
			defaults := strategy.DefaultConfiguration
			if strategy.ArtifactID != "" {
				artifact, err := s.store.GetArtifact(ctx, strategy.ArtifactID)
				if err != nil {
					return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_strategy", message: "The mirrored strategy image is unavailable."}
				}
				raw, err := s.readArtifactPayload(ctx, artifact)
				if err != nil {
					return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_strategy", message: "The mirrored strategy image failed integrity verification."}
				}
				var mirrored struct {
					StrategyImage struct {
						Spec struct {
							MirroredImage string          `json:"mirroredImage"`
							Configuration json.RawMessage `json:"defaultConfiguration"`
						} `json:"spec"`
					} `json:"strategyImage"`
				}
				if json.Unmarshal(raw, &mirrored) != nil || mirrored.StrategyImage.Spec.MirroredImage == "" {
					return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_strategy", message: "The mirrored strategy artifact is invalid."}
				}
				image = mirrored.StrategyImage.Spec.MirroredImage
				// Catalog settings may have changed since the image was mirrored.
				// The artifact supplies the image; the catalog supplies current defaults.
			}
			merged := map[string]any{}
			_ = json.Unmarshal(defaults, &merged)
			var overrides map[string]any
			if json.Unmarshal(component.Configuration, &overrides) != nil || overrides == nil {
				return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_strategy", message: "Strategy configuration must be a JSON object."}
			}
			for key, value := range overrides {
				merged[key] = value
			}
			merged["image"] = image
			component.Configuration, _ = json.Marshal(merged)
		}
		configSchema, err := experimentComponentSchema(manifest.Schema)
		if err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_schema", message: "Could not validate the configuration. Check the application or plugin in the catalog and try again."}
		}
		if issues, err := schema.Validate(configSchema, component.Configuration); err != nil {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_schema", message: "Could not validate the configuration. Check the application or plugin in the catalog and try again."}
		} else if len(issues) > 0 {
			return domain.ExperimentConfiguration{}, experimentConfigurationIssues("components."+component.ID+".configuration", configSchema, issues)
		}
		if manifest.HasCapability("load.session.control") {
			report, err := validateExperimentComponentConfiguration(ctx, plugin, component.Configuration, applicationRef)
			if err != nil {
				return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_component", message: err.Error()}
			}
			if !report.Valid {
				for index := range report.Issues {
					report.Issues[index].Path = "components." + component.ID + ".configuration." + strings.TrimPrefix(strings.TrimPrefix(report.Issues[index].Path, "$"), ".")
				}
				return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": report}}
			}
		}
		if !validExperimentTargets(component.Targets.Include, component.Targets.Exclude, componentIDPattern, applicationComponents) {
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_targets", message: "Component targets must contain unique catalog component IDs without overlap."}
		}
		validation.Issues = append(validation.Issues, domain.ValidationIssue{Level: "info", Path: "components." + component.ID, Message: manifest.Name + " " + manifest.Version + " is installed and its configurable inputs are valid."})
	}
	if external && (externalLoadComponents != 1 || externalMetricsComponents != 1 || len(input.Definition.Components) != 2) {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_external_components", message: "External destination configurations require exactly one load profile and one metrics collector."}
	}
	validation.Issues = append(validation.Issues, domain.ValidationIssue{Level: "info", Path: "clusterResourceId", Message: "Cluster health and every runtime dependency will be checked again before each run step."})
	definition, _ := json.Marshal(input.Definition)
	return domain.ExperimentConfiguration{ID: id.New("expcfg"), Kind: "single", WorkspaceID: input.WorkspaceID, ClusterResourceID: input.ClusterResourceID, Name: input.Name, Description: input.Description, ApplicationRef: applicationRef, ApplicationDigest: applicationDigest, Definition: definition, Validation: validation}, nil
}

func normalizeExternalLoadDestination(value externalLoadDestinationInput) (externalLoadDestinationInput, string, string, error) {
	value.TargetURL = strings.TrimSpace(value.TargetURL)
	value.RuntimeImage = strings.TrimSpace(value.RuntimeImage)
	value.Script = strings.TrimSpace(value.Script)
	target, err := url.Parse(value.TargetURL)
	if err != nil || (target.Scheme != "http" && target.Scheme != "https") || target.Host == "" || target.User != nil || target.Fragment != "" || len(value.TargetURL) > 2048 {
		return externalLoadDestinationInput{}, "", "", errors.New("External endpoint must be an HTTP or HTTPS URL without credentials or fragment.")
	}
	if value.RuntimeImage == "" || len(value.RuntimeImage) > 512 || strings.ContainsAny(value.RuntimeImage, "\n\r \t") {
		return externalLoadDestinationInput{}, "", "", errors.New("Locust runtime image is invalid.")
	}
	if value.Script == "" || len(value.Script) > 262144 || (!strings.Contains(value.Script, "from locust") && !strings.Contains(value.Script, "import locust")) {
		return externalLoadDestinationInput{}, "", "", errors.New("Locust file template must contain a Locust import and be at most 256 KiB.")
	}
	if len(value.Parameters) > 50 {
		return externalLoadDestinationInput{}, "", "", errors.New("At most 50 Locust parameters are allowed.")
	}
	seen := map[string]bool{}
	for name, parameter := range value.Parameters {
		key := strings.ToUpper(name)
		if !locustParameterPattern.MatchString(name) || len(parameter) > 4096 || seen[key] {
			return externalLoadDestinationInput{}, "", "", fmt.Errorf("Locust parameter %q is invalid or duplicated case-insensitively.", name)
		}
		seen[key] = true
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return externalLoadDestinationInput{}, "", "", err
	}
	hash := sha256.Sum256(encoded)
	digest := "sha256:" + hex.EncodeToString(hash[:])
	return value, "external:" + hex.EncodeToString(hash[:]), digest, nil
}

func experimentConfigurationIssues(prefix string, definition json.RawMessage, issues []schema.Issue) *managedOperationFailure {
	converted := make([]domain.ValidationIssue, 0, len(issues))
	for _, issue := range issues {
		converted = append(converted, domain.ValidationIssue{Level: "error", Path: prefix + "." + strings.TrimPrefix(issue.Path, "$."), Message: schema.IssueMessage(definition, issue)})
	}
	return &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": domain.ValidationReport{Valid: false, Issues: converted, CheckedAt: time.Now().UTC()}}}
}

func experimentComponentSchema(raw json.RawMessage) (json.RawMessage, error) {
	var definition map[string]any
	if err := json.Unmarshal(raw, &definition); err != nil {
		return nil, err
	}
	properties, _ := definition["properties"].(map[string]any)
	filtered := map[string]any{}
	for name, rawProperty := range properties {
		property, _ := rawProperty.(map[string]any)
		if property["format"] == "kubephos-artifact-ref" || property["format"] == "kubephos-application-ref" {
			continue
		}
		filtered[name] = property
	}
	required := []string{}
	rawRequired, _ := definition["required"].([]any)
	for _, rawName := range rawRequired {
		name, _ := rawName.(string)
		if _, exists := filtered[name]; exists {
			required = append(required, name)
		}
	}
	result := map[string]any{"type": "object", "additionalProperties": false, "properties": filtered, "required": required}
	return json.Marshal(result)
}

func validateExperimentComponentConfiguration(ctx context.Context, plugin plugins.Plugin, configuration json.RawMessage, applicationRef string) (domain.ValidationReport, error) {
	var spec map[string]any
	if err := json.Unmarshal(configuration, &spec); err != nil {
		return domain.ValidationReport{}, err
	}
	var definition struct {
		Properties map[string]struct {
			Format string `json:"format"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(plugin.Manifest().Schema, &definition); err != nil {
		return domain.ValidationReport{}, err
	}
	artifactIndex := 0
	for name, property := range definition.Properties {
		switch property.Format {
		case "kubephos-application-ref":
			spec[name] = applicationRef
		case "kubephos-artifact-ref":
			artifactIndex++
			spec[name] = fmt.Sprintf("art_configuration_validation_%d", artifactIndex)
		}
	}
	return plugin.Validate(ctx, marshalRaw(spec)), nil
}

func validExperimentTargets(include, exclude []string, pattern *regexp.Regexp, allowed map[string]bool) bool {
	seen := map[string]bool{}
	for _, values := range [][]string{include, exclude} {
		for _, value := range values {
			if !pattern.MatchString(value) || seen[value] || !allowed[value] {
				return false
			}
			seen[value] = true
		}
	}
	return true
}
