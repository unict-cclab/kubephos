package api

import (
	"context"

	"encoding/json"
	"errors"
	"fmt"

	"net/http"

	"strings"

	"time"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	"kubephos.dev/kubephos/internal/plugins"

	"kubephos.dev/kubephos/internal/storage"

	"kubephos.dev/kubephos/internal/workflows"
)

type experimentExecutionInput struct {
	ConfigurationID string     `json:"configurationId"`
	Name            string     `json:"name"`
	Runs            int        `json:"runs"`
	ScheduledFor    *time.Time `json:"scheduledFor"`
}

type experimentArtifactSource struct {
	ExternalID string
	StageID    string
}

func (s *Server) createExperimentExecution(response http.ResponseWriter, request *http.Request) {
	var input experimentExecutionInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Runs == 0 {
		input.Runs = 3
	}
	input.ConfigurationID = strings.TrimSpace(input.ConfigurationID)
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || len([]rune(input.Name)) > 120 || input.Runs < 1 || input.Runs > 20 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_instance", "Name is required and runs must be between 1 and 20.")
		return
	}
	if input.ScheduledFor != nil {
		value := input.ScheduledFor.UTC()
		if value.Before(time.Now().UTC().Add(-time.Minute)) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_schedule", "Scheduled time cannot be in the past.")
			return
		}
		input.ScheduledFor = &value
	}
	configuration, err := s.store.GetExperimentConfiguration(request.Context(), input.ConfigurationID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the experiment configuration.")
		return
	}
	user := requestUser(request)
	if !ownsResource(user, configuration.OwnerUserID) {
		writeError(response, http.StatusNotFound, "not_found", "Experiment configuration not found.")
		return
	}
	if !s.validateClusterAssignment(response, request, user, configuration.ClusterResourceID) {
		return
	}
	if configuration.Kind == "suite" {
		var definition experimentConfigurationDefinitionInput
		if err := json.Unmarshal(configuration.Definition, &definition); err != nil {
			writeError(response, http.StatusConflict, "invalid_snapshot", "The suite configuration is invalid.")
			return
		}
		s.executeExperimentSuite(response, request, experimentSuiteInput{Name: input.Name, Description: configuration.Description, Variants: definition.Variants, Runs: definition.Runs, ScheduledFor: input.ScheduledFor}, configuration)
		return
	}
	var definition experimentConfigurationDefinitionInput
	if err := json.Unmarshal(configuration.Definition, &definition); err != nil {
		writeError(response, http.StatusConflict, "invalid_snapshot", "The saved experiment configuration is invalid.")
		return
	}
	revalidated, failure := s.validatedExperimentConfiguration(request.Context(), experimentConfigurationInput{
		WorkspaceID: configuration.WorkspaceID, ClusterResourceID: configuration.ClusterResourceID,
		Name: configuration.Name, Description: configuration.Description, ApplicationRef: configuration.ApplicationRef, Definition: definition,
	})
	if failure != nil {
		failure.write(response)
		return
	}
	configuration, failure = s.persistRevalidatedExperimentConfiguration(request.Context(), configuration, revalidated)
	if failure != nil {
		failure.write(response)
		return
	}
	if err := json.Unmarshal(configuration.Definition, &definition); err != nil {
		writeError(response, http.StatusConflict, "invalid_snapshot", "The refreshed experiment configuration is invalid.")
		return
	}
	pipelineDefinition, failure := s.experimentInstancePipeline(request.Context(), configuration, definition)
	if failure != nil {
		failure.write(response)
		return
	}
	validated, failure := s.validatedManagedPipeline(request.Context(), configuration.WorkspaceID, pipelineDefinition)
	if failure != nil {
		failure.write(response)
		return
	}
	instanceID := id.New("exp")
	pipeline, err := s.store.CreatePipeline(request.Context(), domain.Pipeline{
		ID: id.New("pipe"), WorkspaceID: configuration.WorkspaceID, Name: input.Name + " · " + instanceID,
		Description: "Immutable execution pipeline for " + configuration.Name,
		Definition:  validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash,
	})
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the validated execution pipeline.")
		return
	}
	variantID := id.New("variant")
	experiment := domain.Experiment{
		ID: instanceID, WorkspaceID: configuration.WorkspaceID, ConfigurationID: configuration.ID,
		OwnerUserID:           user.ID,
		ConfigurationSnapshot: executionConfigurationSnapshot(configuration, nil, input.Runs),
		Kind:                  "instance",
		Name:                  input.Name, Description: configuration.Description, Status: domain.OperationQueued,
		ResultType: validated.Resolution.Result.Type, ResultVersion: validated.Resolution.Result.Version, ScheduledFor: input.ScheduledFor,
		Variants: []domain.ExperimentVariant{{ID: variantID, ExperimentID: instanceID, Position: 1, Name: configuration.Name, PipelineID: pipeline.ID, PipelineHash: pipeline.Hash, ConfigurationID: configuration.ID, Configuration: configuration.Definition}},
	}
	runs := map[string]domain.PipelineRun{}
	isolationMode := experimentIsolationMode(definition)
	for position := 1; position <= input.Runs; position++ {
		trialID := id.New("trial")
		run := domain.PipelineRun{
			ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: configuration.WorkspaceID, ClusterResourceID: configuration.ClusterResourceID,
			OwnerUserID: user.ID, IsolationMode: isolationMode,
			Name: fmt.Sprintf("%s · Run %d", input.Name, position), Status: domain.OperationQueued, PipelineHash: pipeline.Hash,
			ResultType: experiment.ResultType, ResultVersion: experiment.ResultVersion, ScheduledFor: input.ScheduledFor,
		}
		for stagePosition, stage := range validated.Definition.Stages {
			run.Stages = append(run.Stages, domain.PipelineRunStage{ID: id.New("runstage"), RunID: run.ID, Position: stagePosition + 1, StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending, CleanupStatus: domain.StepPending})
		}
		experiment.Variants[0].Trials = append(experiment.Variants[0].Trials, domain.ExperimentTrial{ID: trialID, VariantID: variantID, Position: position, Status: domain.OperationQueued, PipelineRunID: run.ID})
		runs[trialID] = run
	}
	created, err := s.store.CreatePipelineExperiment(request.Context(), experiment, runs)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not queue the experiment.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

type experimentSuiteInput struct {
	Name             string                        `json:"name"`
	Description      string                        `json:"description"`
	ConfigurationIDs []string                      `json:"configurationIds"`
	Variants         []experimentSuiteVariantInput `json:"variants"`
	Runs             int                           `json:"runs"`
	ScheduledFor     *time.Time                    `json:"scheduledFor"`
}

type experimentSuiteVariantInput struct {
	ConfigurationID string `json:"configurationId"`
	Alias           string `json:"alias"`
	Color           string `json:"color"`
}

type validatedSuiteVariant struct {
	Configuration domain.ExperimentConfiguration
	Definition    experimentConfigurationDefinitionInput
	Pipeline      workflows.Result
	Alias         string
	Color         string
}

var suitePlotColors = []string{"#1f77b4", "#d55e00", "#009e73", "#cc79a7", "#e69f00", "#56b4e9", "#000000", "#7f7f7f"}

func (s *Server) createExperimentSuite(response http.ResponseWriter, request *http.Request) {
	var input experimentSuiteInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	configuration, failure := s.validatedComparativeConfiguration(request.Context(), experimentConfigurationInput{Name: input.Name, Description: input.Description, Kind: "suite", Definition: experimentConfigurationDefinitionInput{Kind: "suite", Variants: input.Variants, Runs: input.Runs}})
	if len(input.Variants) == 0 && len(input.ConfigurationIDs) > 0 {
		for _, member := range input.ConfigurationIDs {
			input.Variants = append(input.Variants, experimentSuiteVariantInput{ConfigurationID: member})
		}
		configuration, failure = s.validatedComparativeConfiguration(request.Context(), experimentConfigurationInput{Name: input.Name, Description: input.Description, Kind: "suite", Definition: experimentConfigurationDefinitionInput{Kind: "suite", Variants: input.Variants, Runs: input.Runs}})
	}
	if failure != nil {
		failure.write(response)
		return
	}
	configuration.OwnerUserID = requestUser(request).ID
	configuration, err := s.store.CreateExperimentConfiguration(request.Context(), configuration)
	if err != nil {
		writeError(response, http.StatusConflict, "configuration_conflict", "Could not save the suite configuration. Use a unique configuration name.")
		return
	}
	var savedDefinition experimentConfigurationDefinitionInput
	if err := json.Unmarshal(configuration.Definition, &savedDefinition); err != nil {
		writeError(response, http.StatusConflict, "invalid_snapshot", "The suite configuration is invalid.")
		return
	}
	input.Variants, input.Runs = savedDefinition.Variants, savedDefinition.Runs
	s.executeExperimentSuite(response, request, input, configuration)
}

func (s *Server) executeExperimentSuite(response http.ResponseWriter, request *http.Request, input experimentSuiteInput, suiteConfiguration domain.ExperimentConfiguration) {
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if len(input.Variants) == 0 {
		for index, configurationID := range input.ConfigurationIDs {
			input.Variants = append(input.Variants, experimentSuiteVariantInput{ConfigurationID: configurationID, Color: suitePlotColors[index%len(suitePlotColors)]})
		}
	}
	if input.Name == "" || len([]rune(input.Name)) > 120 || len([]rune(input.Description)) > 1000 || input.Runs < 1 || input.Runs > 20 || len(input.Variants) < 2 || len(input.Variants) > 8 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_suite", "A suite requires a name, two to eight configurations and one to twenty runs per configuration.")
		return
	}
	if input.ScheduledFor != nil {
		value := input.ScheduledFor.UTC()
		if value.Before(time.Now().UTC().Add(-time.Minute)) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_schedule", "Scheduled time cannot be in the past.")
			return
		}
		input.ScheduledFor = &value
	}
	seen := map[string]bool{}
	seenAliases := map[string]bool{}
	seenColors := map[string]bool{}
	variants := make([]validatedSuiteVariant, 0, len(input.Variants))
	user := requestUser(request)
	for position, requested := range input.Variants {
		configurationID := strings.TrimSpace(requested.ConfigurationID)
		if configurationID == "" || seen[configurationID] {
			writeError(response, http.StatusUnprocessableEntity, "invalid_suite", "Suite configurations must be unique.")
			return
		}
		seen[configurationID] = true
		configuration, err := s.store.GetExperimentConfiguration(request.Context(), configurationID)
		if errors.Is(err, storage.ErrNotFound) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_configuration", "A selected experiment configuration is unavailable.")
			return
		}
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not read suite configurations.")
			return
		}
		if !ownsResource(user, configuration.OwnerUserID) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_configuration", "A selected experiment configuration is unavailable.")
			return
		}
		if !s.validateClusterAssignment(response, request, user, configuration.ClusterResourceID) {
			return
		}
		if configuration.Kind == "suite" {
			writeError(response, http.StatusUnprocessableEntity, "nested_suite", "Suites may contain only single configurations.")
			return
		}
		alias := strings.TrimSpace(requested.Alias)
		if alias == "" {
			alias = configuration.Name
		}
		color := strings.ToLower(strings.TrimSpace(requested.Color))
		if color == "" {
			color = suitePlotColors[position%len(suitePlotColors)]
		}
		aliasKey := strings.ToLower(alias)
		if len([]rune(alias)) > 80 || seenAliases[aliasKey] || !plotColorPattern.MatchString(color) || seenColors[color] {
			writeError(response, http.StatusUnprocessableEntity, "invalid_suite_presentation", "Every strategy requires a unique alias and a unique six-digit plot color.")
			return
		}
		seenAliases[aliasKey] = true
		seenColors[color] = true
		var definition experimentConfigurationDefinitionInput
		if err := json.Unmarshal(configuration.Definition, &definition); err != nil {
			writeError(response, http.StatusConflict, "invalid_snapshot", "A saved experiment configuration is invalid.")
			return
		}
		revalidated, failure := s.validatedExperimentConfiguration(request.Context(), experimentConfigurationInput{
			WorkspaceID: configuration.WorkspaceID, ClusterResourceID: configuration.ClusterResourceID,
			Name: configuration.Name, Description: configuration.Description, ApplicationRef: configuration.ApplicationRef, Definition: definition,
		})
		if failure != nil {
			failure.write(response)
			return
		}
		configuration, failure = s.persistRevalidatedExperimentConfiguration(request.Context(), configuration, revalidated)
		if failure != nil {
			failure.write(response)
			return
		}
		if err := json.Unmarshal(configuration.Definition, &definition); err != nil {
			writeError(response, http.StatusConflict, "invalid_snapshot", "The refreshed experiment configuration is invalid.")
			return
		}
		if len(variants) > 0 {
			first := variants[0].Configuration
			if configuration.WorkspaceID != first.WorkspaceID || configuration.ClusterResourceID != first.ClusterResourceID || configuration.ApplicationRef != first.ApplicationRef {
				writeError(response, http.StatusUnprocessableEntity, "incompatible_suite", "Suite configurations must use the same environment, cluster and application.")
				return
			}
		}
		pipelineDefinition, failure := s.experimentInstancePipeline(request.Context(), configuration, definition)
		if failure != nil {
			failure.write(response)
			return
		}
		validated, failure := s.validatedManagedPipeline(request.Context(), configuration.WorkspaceID, pipelineDefinition)
		if failure != nil {
			failure.write(response)
			return
		}
		if len(variants) > 0 && (validated.Resolution.Result.Type != variants[0].Pipeline.Resolution.Result.Type || validated.Resolution.Result.Version != variants[0].Pipeline.Resolution.Result.Version) {
			writeError(response, http.StatusUnprocessableEntity, "incompatible_results", "Every suite configuration must produce the same result contract.")
			return
		}
		variants = append(variants, validatedSuiteVariant{Configuration: configuration, Definition: definition, Pipeline: validated, Alias: alias, Color: color})
	}
	suiteID := id.New("exp")
	experiment := domain.Experiment{
		ID: suiteID, ConfigurationID: suiteConfiguration.ID, ConfigurationSnapshot: executionConfigurationSnapshot(suiteConfiguration, variants, input.Runs), WorkspaceID: variants[0].Configuration.WorkspaceID, Kind: "suite", Name: input.Name, Description: input.Description,
		OwnerUserID: user.ID,
		Status:      domain.OperationQueued, ResultType: variants[0].Pipeline.Resolution.Result.Type, ResultVersion: variants[0].Pipeline.Resolution.Result.Version, ScheduledFor: input.ScheduledFor,
	}
	runs := map[string]domain.PipelineRun{}
	for variantPosition, item := range variants {
		pipeline, err := s.store.CreatePipeline(request.Context(), domain.Pipeline{
			ID: id.New("pipe"), WorkspaceID: experiment.WorkspaceID, Name: fmt.Sprintf("%s · Variant %d · %s", input.Name, variantPosition+1, suiteID),
			Description: "Immutable suite pipeline for " + item.Configuration.Name,
			Definition:  item.Pipeline.Definition, Resolution: item.Pipeline.Resolution, Validation: item.Pipeline.Validation, Hash: item.Pipeline.Hash,
		})
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not save a validated suite pipeline.")
			return
		}
		variantID := id.New("variant")
		variant := domain.ExperimentVariant{ID: variantID, ExperimentID: suiteID, Position: variantPosition + 1, Name: item.Configuration.Name, Alias: item.Alias, Color: item.Color, PipelineID: pipeline.ID, PipelineHash: pipeline.Hash, ConfigurationID: item.Configuration.ID, Configuration: item.Configuration.Definition}
		for trialPosition := 1; trialPosition <= input.Runs; trialPosition++ {
			trialID := id.New("trial")
			run := domain.PipelineRun{
				ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: experiment.WorkspaceID, ClusterResourceID: item.Configuration.ClusterResourceID,
				OwnerUserID: user.ID, IsolationMode: experimentIsolationMode(item.Definition),
				Name: fmt.Sprintf("%s · %s · Run %d", input.Name, item.Configuration.Name, trialPosition), Status: domain.OperationQueued,
				PipelineHash: pipeline.Hash, ResultType: experiment.ResultType, ResultVersion: experiment.ResultVersion, ScheduledFor: input.ScheduledFor,
			}
			for stagePosition, stage := range item.Pipeline.Definition.Stages {
				run.Stages = append(run.Stages, domain.PipelineRunStage{ID: id.New("runstage"), RunID: run.ID, Position: stagePosition + 1, StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending, CleanupStatus: domain.StepPending})
			}
			variant.Trials = append(variant.Trials, domain.ExperimentTrial{ID: trialID, VariantID: variantID, Position: trialPosition, Status: domain.OperationQueued, PipelineRunID: run.ID})
			runs[trialID] = run
		}
		experiment.Variants = append(experiment.Variants, variant)
	}
	created, err := s.store.CreatePipelineExperiment(request.Context(), experiment, runs)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not queue the validated experiment suite.")
		return
	}
	writeJSON(response, http.StatusCreated, created)
}

func (s *Server) experimentInstancePipeline(ctx context.Context, configuration domain.ExperimentConfiguration, definition experimentConfigurationDefinitionInput) (domain.PipelineDefinition, *managedOperationFailure) {
	cluster, err := s.store.GetManagedResource(ctx, configuration.ClusterResourceID)
	if err != nil || cluster.Status != "ready" || cluster.PipelineRunID == "" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "cluster_not_ready", message: "The configured cluster is not ready."}
	}
	if _, multi := multiClusterSpec(cluster.Spec); multi {
		return s.multiClusterExperimentPipeline(ctx, configuration, definition, cluster)
	}
	clusterConnection, err := s.store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "cluster-connection")
	if err != nil || clusterConnection.Type != "ClusterConnection" || clusterConnection.Version != "v1alpha1" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "cluster_invalid", message: "The cluster connection is unavailable."}
	}
	observability, err := s.store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "observability-capability")
	if err != nil || observability.Type != "ObservabilityCapability" || observability.Version != "v1alpha1" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "observability_invalid", message: "Managed observability is unavailable on the cluster."}
	}
	return s.experimentPipelineForCluster(ctx, configuration, definition, cluster, clusterConnection, observability, domain.Artifact{})
}
func (s *Server) experimentPipelineForCluster(ctx context.Context, configuration domain.ExperimentConfiguration, definition experimentConfigurationDefinitionInput, cluster domain.ManagedResource, clusterConnection, observability, platform domain.Artifact) (domain.PipelineDefinition, *managedOperationFailure) {
	if definition.DestinationMode == "external" {
		return s.externalDestinationPipeline(configuration, definition, clusterConnection, observability)
	}
	inspectPlugin, err := s.applicationMaterializer(ctx, configuration.ApplicationRef)
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "capability_unavailable", message: err.Error()}
	}
	deployPlugin, err := s.pluginForCapability("", "applications.kubernetes.deploy")
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "capability_unavailable", message: err.Error()}
	}
	bindingPlugin, err := s.pluginForCapability("", "targets.bind")
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "capability_unavailable", message: err.Error()}
	}
	applicationValues := definition.ApplicationValues
	if len(applicationValues) == 0 {
		applicationValues = json.RawMessage(`{}`)
	}
	inspectSpec := map[string]any{"applicationRef": configuration.ApplicationRef}
	var values any
	if err := json.Unmarshal(applicationValues, &values); err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_snapshot", message: "Application values are invalid."}
	}
	inspectSpec["values"] = values
	definitionResult := domain.PipelineDefinition{CleanupAfterRun: true}
	definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: "application", PluginID: inspectPlugin.Manifest().ID, Title: "Prepare application package", Spec: marshalRaw(inspectSpec)})
	sources := map[string]experimentArtifactSource{
		artifactContractKey("ClusterConnection", "v1alpha1"):       {ExternalID: clusterConnection.ID},
		artifactContractKey("ObservabilityCapability", "v1alpha1"): {ExternalID: observability.ID},
	}
	controlNamespace := false
	managedPlatformLoaded := false
	for _, component := range definition.Components {
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil {
			return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "plugin_changed", message: err.Error()}
		}
		manifest := plugin.Manifest()
		if manifest.HasCapability("scheduler.kubernetes.install") || manifest.HasCapability("descheduler.kubernetes.install") {
			controlNamespace = true
		}
		if !managedPlatformLoaded && manifestNeedsArtifact(manifest, "ManagedPlatformCapability", "v1alpha1") {
			managedPlatform := platform
			var err error
			if managedPlatform.ID == "" {
				managedPlatform, err = s.store.GetPipelineRunArtifact(ctx, cluster.PipelineRunID, "managed-platform-capability")
			}
			if err != nil || managedPlatform.Type != "ManagedPlatformCapability" || managedPlatform.Version != "v1alpha1" {
				return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "platform_invalid", message: "Managed Istio and Chaos Mesh are unavailable on the cluster."}
			}
			sources[artifactContractKey("ManagedPlatformCapability", "v1alpha1")] = experimentArtifactSource{ExternalID: managedPlatform.ID}
			managedPlatformLoaded = true
		}
	}
	registerExperimentOutputs(sources, "application", inspectPlugin.Manifest())
	prepareSpec := map[string]any{"mode": "prepare"}
	if controlNamespace {
		prepareSpec["controlNamespace"] = true
	}
	if definition.PodDisruptionBudgetMinAvailable > 0 {
		prepareSpec["podDisruptionBudgetMinAvailable"] = definition.PodDisruptionBudgetMinAvailable
	}
	prepareBindings, failure := experimentArtifactBindings(prepareSpec, deployPlugin.Manifest(), sources, configuration.ApplicationRef)
	if failure != nil {
		return domain.PipelineDefinition{}, failure
	}
	definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: "prepare", PluginID: deployPlugin.Manifest().ID, Title: "Prepare isolated application namespace", Spec: marshalRaw(prepareSpec), Bindings: prepareBindings})
	registerExperimentOutputs(sources, "prepare", deployPlugin.Manifest())
	appendComponent := func(index int) *managedOperationFailure {
		component := definition.Components[index]
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil {
			return &managedOperationFailure{status: http.StatusConflict, code: "plugin_changed", message: err.Error()}
		}
		manifest := plugin.Manifest()
		if manifestNeedsArtifact(manifest, "TargetBinding", "v1alpha1") {
			if manifest.Targeting == nil || manifest.Targeting.RequiredTrait == "" {
				return &managedOperationFailure{status: http.StatusConflict, code: "invalid_plugin", message: "Plugin " + manifest.ID + " does not declare the required workload trait."}
			}
			bindingID := fmt.Sprintf("targets-%d", index+1)
			bindingSpec := map[string]any{"requiredTrait": manifest.Targeting.RequiredTrait, "includeComponentIds": component.Targets.Include, "excludeComponentIds": component.Targets.Exclude}
			bindings, failure := experimentArtifactBindings(bindingSpec, bindingPlugin.Manifest(), sources, configuration.ApplicationRef)
			if failure != nil {
				return failure
			}
			definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: bindingID, PluginID: bindingPlugin.Manifest().ID, Title: "Select targets for " + component.ID, Spec: marshalRaw(bindingSpec), Bindings: bindings})
			registerExperimentOutputs(sources, bindingID, bindingPlugin.Manifest())
		}
		var componentSpec map[string]any
		if err := json.Unmarshal(component.Configuration, &componentSpec); err != nil || componentSpec == nil {
			return &managedOperationFailure{status: http.StatusConflict, code: "invalid_snapshot", message: "Component configuration " + component.ID + " is invalid."}
		}
		bindings, failure := experimentArtifactBindings(componentSpec, manifest, sources, configuration.ApplicationRef)
		if failure != nil {
			return failure
		}
		stageID := fmt.Sprintf("capability-%d", index+1)
		definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: stageID, PluginID: component.PluginID, Title: component.Capability, Spec: marshalRaw(componentSpec), Bindings: bindings})
		registerExperimentOutputs(sources, stageID, manifest)
		if len(manifest.ArtifactOutputs) == 1 {
			definitionResult.Result = domain.PipelineOutput{Stage: stageID, Type: manifest.ArtifactOutputs[0].Type, Version: manifest.ArtifactOutputs[0].Version}
		}
		return nil
	}
	setup := []int{}
	deschedulers := []int{}
	postDeploy := []int{}
	schedulerName := ""
	var schedulerInclude, schedulerExclude []string
	for index, component := range definition.Components {
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil {
			return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "plugin_changed", message: err.Error()}
		}
		manifest := plugin.Manifest()
		if manifest.HasCapability("descheduler.kubernetes.install") {
			deschedulers = append(deschedulers, index)
		} else if manifest.HasCapability("load.session.control") || manifest.HasCapability("metrics.timeseries.collect") {
			postDeploy = append(postDeploy, index)
		} else {
			setup = append(setup, index)
		}
		if manifest.HasCapability("scheduler.kubernetes.install") {
			schedulerName = "kubephos-" + domain.RuntimeExecutionIDToken
			schedulerInclude = component.Targets.Include
			schedulerExclude = component.Targets.Exclude
		}
	}
	for _, index := range setup {
		if failure := appendComponent(index); failure != nil {
			return domain.PipelineDefinition{}, failure
		}
	}
	deploySpec := map[string]any{"mode": "deploy"}
	if controlNamespace {
		deploySpec["controlNamespace"] = true
	}
	if definition.PodDisruptionBudgetMinAvailable > 0 {
		deploySpec["podDisruptionBudgetMinAvailable"] = definition.PodDisruptionBudgetMinAvailable
	}
	if schedulerName != "" {
		deploySpec["schedulerName"] = schedulerName
		deploySpec["schedulerInclude"] = schedulerInclude
		deploySpec["schedulerExclude"] = schedulerExclude
	}
	deployBindings, failure := experimentArtifactBindings(deploySpec, deployPlugin.Manifest(), sources, configuration.ApplicationRef)
	if failure != nil {
		return domain.PipelineDefinition{}, failure
	}
	definitionResult.Stages = append(definitionResult.Stages, domain.PipelineStage{ID: "deploy", PluginID: deployPlugin.Manifest().ID, Title: "Deploy isolated application", Spec: marshalRaw(deploySpec), Bindings: deployBindings})
	registerExperimentOutputs(sources, "deploy", deployPlugin.Manifest())
	// Start evictions only after application readiness, before traffic and metrics.
	for _, index := range deschedulers {
		if failure := appendComponent(index); failure != nil {
			return domain.PipelineDefinition{}, failure
		}
	}
	for _, index := range postDeploy {
		if failure := appendComponent(index); failure != nil {
			return domain.PipelineDefinition{}, failure
		}
	}
	if definitionResult.Result.Stage == "" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "missing_result", message: "Add a result-producing capability such as the metrics collector."}
	}
	if len(definitionResult.Stages) > 32 {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "too_many_stages", message: "The configuration expands to more than 32 validated stages."}
	}
	return definitionResult, nil
}

func (s *Server) externalDestinationPipeline(configuration domain.ExperimentConfiguration, definition experimentConfigurationDefinitionInput, clusterConnection, observability domain.Artifact) (domain.PipelineDefinition, *managedOperationFailure) {
	preparePlugin, err := s.pluginForCapability("", "loads.external.prepare")
	if err != nil {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "capability_unavailable", message: err.Error()}
	}
	sources := map[string]experimentArtifactSource{
		artifactContractKey("ClusterConnection", "v1alpha1"):       {ExternalID: clusterConnection.ID},
		artifactContractKey("ObservabilityCapability", "v1alpha1"): {ExternalID: observability.ID},
	}
	parameters := definition.ExternalDestination.Parameters
	if parameters == nil {
		parameters = map[string]string{}
	}
	targetSpec := map[string]any{
		"targetUrl": definition.ExternalDestination.TargetURL, "runtimeImage": definition.ExternalDestination.RuntimeImage,
		"script": definition.ExternalDestination.Script, "parameters": parameters,
	}
	bindings, failure := experimentArtifactBindings(targetSpec, preparePlugin.Manifest(), sources, configuration.ApplicationRef)
	if failure != nil {
		return domain.PipelineDefinition{}, failure
	}
	result := domain.PipelineDefinition{CleanupAfterRun: true, Stages: []domain.PipelineStage{{ID: "external-destination", PluginID: preparePlugin.Manifest().ID, Title: "Prepare external destination", Spec: marshalRaw(targetSpec), Bindings: bindings}}}
	registerExperimentOutputs(sources, "external-destination", preparePlugin.Manifest())
	ordered := make([]int, 0, len(definition.Components))
	for _, capability := range []string{"load.session.control", "metrics.timeseries.collect"} {
		for index, component := range definition.Components {
			if component.Capability == capability {
				ordered = append(ordered, index)
			}
		}
	}
	for _, index := range ordered {
		component := definition.Components[index]
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil {
			return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "plugin_changed", message: err.Error()}
		}
		var spec map[string]any
		if json.Unmarshal(component.Configuration, &spec) != nil || spec == nil {
			return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusConflict, code: "invalid_snapshot", message: "External load component configuration is invalid."}
		}
		bindings, failure := experimentArtifactBindings(spec, plugin.Manifest(), sources, configuration.ApplicationRef)
		if failure != nil {
			return domain.PipelineDefinition{}, failure
		}
		stageID := fmt.Sprintf("capability-%d", index+1)
		result.Stages = append(result.Stages, domain.PipelineStage{ID: stageID, PluginID: component.PluginID, Title: component.Capability, Spec: marshalRaw(spec), Bindings: bindings})
		registerExperimentOutputs(sources, stageID, plugin.Manifest())
		if component.Capability == "metrics.timeseries.collect" {
			result.Result = domain.PipelineOutput{Stage: stageID, Type: "TimeSeriesDataset", Version: "v1alpha1"}
		}
	}
	if len(ordered) != 2 || result.Result.Stage == "" {
		return domain.PipelineDefinition{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_external_components", message: "External destination pipeline requires load and metrics components."}
	}
	return result, nil
}

func (s *Server) applicationMaterializer(ctx context.Context, applicationRef string) (plugins.Plugin, error) {
	applicationID, err := catalog.ParseReference(applicationRef)
	if err != nil {
		return nil, err
	}
	raw, err := s.store.GetCatalogApplicationDescriptor(ctx, applicationID)
	if err != nil {
		return nil, err
	}
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		return nil, errors.New("catalog application descriptor is invalid")
	}
	if descriptor.Spec.Materializer == "" {
		return s.pluginForCapability("", "applications.inspect")
	}
	plugin, err := s.registry.Get(descriptor.Spec.Materializer)
	if err != nil {
		return nil, err
	}
	if !plugin.Manifest().HasCapability("applications.materialize") {
		return nil, fmt.Errorf("plugin %s cannot materialize applications", descriptor.Spec.Materializer)
	}
	return plugin, nil
}

func experimentArtifactBindings(spec map[string]any, manifest plugins.Manifest, sources map[string]experimentArtifactSource, applicationRef string) ([]domain.PipelineBinding, *managedOperationFailure) {
	var schemaDefinition struct {
		Properties map[string]struct {
			Format  string `json:"format"`
			Type    string `json:"x-kubephos-artifact-type"`
			Version string `json:"x-kubephos-artifact-version"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(manifest.Schema, &schemaDefinition); err != nil {
		return nil, &managedOperationFailure{status: http.StatusConflict, code: "invalid_plugin", message: "Plugin " + manifest.ID + " has an invalid schema."}
	}
	bindings := []domain.PipelineBinding{}
	for name, property := range schemaDefinition.Properties {
		switch property.Format {
		case "kubephos-application-ref":
			spec[name] = applicationRef
		case "kubephos-artifact-ref":
			source, ok := sources[artifactContractKey(property.Type, property.Version)]
			if !ok {
				return nil, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "missing_input", message: "Plugin " + manifest.ID + " requires unavailable " + property.Type + "/" + property.Version + "."}
			}
			if source.ExternalID != "" {
				spec[name] = source.ExternalID
			} else {
				spec[name] = ""
				bindings = append(bindings, domain.PipelineBinding{Path: "/" + name, FromStage: source.StageID})
			}
		}
	}
	return bindings, nil
}

func registerExperimentOutputs(sources map[string]experimentArtifactSource, stageID string, manifest plugins.Manifest) {
	for _, output := range manifest.ArtifactOutputs {
		sources[artifactContractKey(output.Type, output.Version)] = experimentArtifactSource{StageID: stageID}
	}
}

func manifestNeedsArtifact(manifest plugins.Manifest, artifactType, version string) bool {
	for _, input := range manifest.ArtifactInputs {
		if input.Type == artifactType && input.Version == version {
			return true
		}
	}
	return false
}

func artifactContractKey(artifactType, version string) string {
	return artifactType + "\x00" + version
}

func marshalRaw(value any) json.RawMessage {
	raw, _ := json.Marshal(value)
	return raw
}
