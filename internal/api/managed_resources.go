package api

import (
	"encoding/json"
	"errors"

	"log/slog"

	"net/http"

	"slices"

	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) listMachineTemplates(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListManagedResources(request.Context(), "machine-template")
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list machine templates.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getMachineTemplate(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "machine-template" {
		writeError(response, http.StatusNotFound, "not_found", "Machine template not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read machine template.")
		return
	}
	writeJSON(response, http.StatusOK, resource)
}

func (s *Server) createMachineTemplate(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID   string          `json:"workspaceId"`
		ConnectionID  string          `json:"connectionId"`
		Name          string          `json:"name"`
		Configuration json.RawMessage `json:"configuration"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Name = strings.TrimSpace(input.Name)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	if input.Name == "" || len(input.Name) > 32 || !shortResourceNamePattern.MatchString(input.Name) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Template name must be a lowercase label with at most 32 characters.")
		return
	}
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), input.ConnectionID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_connection", "Select an existing infrastructure connection.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the infrastructure connection.")
		return
	}
	plugin, err := s.pluginForCapability(connection.Provider, "infrastructure.machine-template.provision")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	configuration := map[string]any{}
	if len(input.Configuration) > 0 {
		if err := json.Unmarshal(input.Configuration, &configuration); err != nil {
			writeError(response, http.StatusBadRequest, "invalid_request", "Configuration must be a JSON object.")
			return
		}
	}
	configuration["connectionRef"] = connection.ID
	configuration["name"] = input.Name
	spec, err := json.Marshal(configuration)
	if err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", "Configuration could not be encoded.")
		return
	}
	release, valid := s.validateProvisioning(response, request, connection.ID, configuration, 1)
	if !valid {
		return
	}
	defer release()
	operation, failure := s.validatedManagedOperation(request.Context(), input.WorkspaceID, plugin, "Create VM template "+input.Name, spec)
	if failure != nil {
		failure.write(response)
		return
	}
	operation.Status = domain.OperationQueued
	manifest := plugin.Manifest()
	resource, err := s.store.CreateManagedResource(request.Context(), domain.ManagedResource{ID: id.New("tmpl"), WorkspaceID: input.WorkspaceID, Name: input.Name, Kind: "machine-template", Provider: connection.Provider, ConnectionID: connection.ID, PluginID: manifest.ID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Spec: spec}, operation)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "template_conflict", "A template with this name already exists for the selected connection.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the machine template workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

func (s *Server) deleteMachineTemplate(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "machine-template" {
		writeError(response, http.StatusNotFound, "not_found", "Machine template not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read machine template.")
		return
	}
	if resource.Status == "deletion-failed" {
		if err := s.store.ResetFailedManagedResourceDeletion(request.Context(), resource.ID); err != nil {
			writeError(response, http.StatusConflict, "cleanup_unavailable", "The failed template deletion cannot be retried in its current state.")
			return
		}
		resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not reload the machine template lifecycle.")
			return
		}
	}
	if resource.Status != "ready" && resource.Status != "failed" && resource.Status != "canceled" {
		writeError(response, http.StatusConflict, "template_not_ready", "Only a completed machine template lifecycle can be deleted.")
		return
	}
	source, err := s.store.GetOperation(request.Context(), resource.OperationID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the template lifecycle.")
		return
	}
	operation, failure := s.validatedManagedCleanup(request.Context(), source)
	if failure != nil {
		failure.write(response)
		return
	}
	operation.Status = domain.OperationQueued
	if err := s.store.CreateManagedResourceDeletion(request.Context(), resource.ID, operation); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "template_in_use", "The template is still in use or already being deleted.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the template deletion workflow.")
		return
	}
	resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the template deletion workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

func (s *Server) listInfrastructureServices(response http.ResponseWriter, request *http.Request) {
	kind := strings.TrimSpace(request.URL.Query().Get("kind"))
	kinds := []string{"harbor", "nfs"}
	if kind != "" {
		if kind != "harbor" && kind != "nfs" {
			writeError(response, http.StatusUnprocessableEntity, "invalid_kind", "Service kind must be harbor or nfs.")
			return
		}
		kinds = []string{kind}
	}
	items := []domain.ManagedResource{}
	for _, current := range kinds {
		resources, err := s.store.ListManagedResources(request.Context(), current)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not list infrastructure services.")
			return
		}
		items = append(items, resources...)
	}
	slices.SortFunc(items, func(left, right domain.ManagedResource) int { return right.CreatedAt.Compare(left.CreatedAt) })
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getInfrastructureService(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "harbor" && resource.Kind != "nfs" {
		writeError(response, http.StatusNotFound, "not_found", "Infrastructure service not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the infrastructure service.")
		return
	}
	writeJSON(response, http.StatusOK, resource)
}

func (s *Server) createInfrastructureService(response http.ResponseWriter, request *http.Request) {
	var input struct {
		WorkspaceID  string `json:"workspaceId"`
		ConnectionID string `json:"connectionId"`
		TemplateID   string `json:"templateId"`
		Kind         string `json:"kind"`
		Name         string `json:"name"`
		VMID         int    `json:"vmid"`
		Address      string `json:"address"`
		PrefixLength int    `json:"prefixLength"`
		Gateway      string `json:"gateway"`
		DNSServer    string `json:"dnsServer"`
		Cores        int    `json:"cores"`
		MemoryMiB    int    `json:"memoryMiB"`
		DiskGiB      int    `json:"diskGiB"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.Kind = strings.TrimSpace(input.Kind)
	input.Name = strings.TrimSpace(input.Name)
	input.Address = strings.TrimSpace(input.Address)
	input.Gateway = strings.TrimSpace(input.Gateway)
	input.DNSServer = strings.TrimSpace(input.DNSServer)
	if input.Kind != "harbor" && input.Kind != "nfs" {
		writeError(response, http.StatusUnprocessableEntity, "invalid_kind", "Service kind must be harbor or nfs.")
		return
	}
	if input.Name == "" || len(input.Name) > 32 || !shortResourceNamePattern.MatchString(input.Name) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Service name must be a lowercase label with at most 32 characters.")
		return
	}
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), input.ConnectionID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_connection", "Select an existing infrastructure connection.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the infrastructure connection.")
		return
	}
	template, err := s.store.GetManagedResource(request.Context(), input.TemplateID)
	if errors.Is(err, storage.ErrNotFound) || err == nil && template.Kind != "machine-template" {
		writeError(response, http.StatusUnprocessableEntity, "invalid_template", "Select an existing machine template.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the machine template.")
		return
	}
	if template.Status != "ready" || template.ArtifactID == "" {
		writeError(response, http.StatusConflict, "template_not_ready", "The selected machine template is not ready.")
		return
	}
	if template.ConnectionID != connection.ID || template.Provider != connection.Provider || template.WorkspaceID != input.WorkspaceID {
		writeError(response, http.StatusUnprocessableEntity, "template_mismatch", "The template must belong to the selected connection and workspace.")
		return
	}
	var templateSpec struct {
		Node    string `json:"node"`
		VMID    int    `json:"vmid"`
		DiskGiB int    `json:"diskGiB"`
		SSHUser string `json:"sshUser"`
	}
	if err := json.Unmarshal(template.Spec, &templateSpec); err != nil || templateSpec.Node == "" || templateSpec.VMID < 100 || templateSpec.SSHUser == "" {
		writeError(response, http.StatusConflict, "template_invalid", "The saved machine template configuration is incomplete.")
		return
	}
	if input.VMID < 100 || input.VMID > 999999999 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_vmid", "VM ID must be between 100 and 999999999.")
		return
	}
	if input.Cores == 0 {
		input.Cores = map[string]int{"harbor": 4, "nfs": 2}[input.Kind]
	}
	if input.MemoryMiB == 0 {
		input.MemoryMiB = map[string]int{"harbor": 8192, "nfs": 4096}[input.Kind]
	}
	if input.DiskGiB == 0 {
		input.DiskGiB = map[string]int{"harbor": 100, "nfs": 200}[input.Kind]
	}
	if input.Cores < 1 || input.Cores > 32 || input.MemoryMiB < 512 || input.MemoryMiB > 131072 || input.DiskGiB < templateSpec.DiskGiB || input.DiskGiB > 2048 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "Capacity is outside the supported range or smaller than the template disk.")
		return
	}
	topologyPlugin, err := s.pluginForCapability(connection.Provider, "infrastructure.machine-topology.provision")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	serviceCapability := map[string]string{"harbor": "registry.oci.provision", "nfs": "storage.shared.provision"}[input.Kind]
	servicePlugin, err := s.pluginForCapability("", serviceCapability)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	topologySpec, _ := json.Marshal(map[string]any{
		"connectionRef": connection.ID, "machineTemplateRef": template.ArtifactID, "node": templateSpec.Node,
		"templateVMID": templateSpec.VMID, "baseVMID": input.VMID, "namePrefix": input.Name, "machineCount": 1,
		"machineNames": []string{input.Name},
		"addressStart": input.Address, "prefixLength": input.PrefixLength, "gateway": input.Gateway, "dnsServer": input.DNSServer,
		"cores": input.Cores, "memoryMiB": input.MemoryMiB, "diskGiB": input.DiskGiB, "sshUser": templateSpec.SSHUser, "cleanupAfterTest": false,
	})
	var allocationSpec map[string]any
	json.Unmarshal(topologySpec, &allocationSpec)
	release, valid := s.validateProvisioning(response, request, connection.ID, allocationSpec, 1)
	if !valid {
		return
	}
	defer release()
	serviceSpec := map[string]any{"machineSetRef": "", "machineAccessRef": ""}
	if input.Kind == "harbor" {
		serviceSpec["registryName"] = input.Name
	} else {
		serviceSpec["storageName"] = input.Name
	}
	serviceRaw, _ := json.Marshal(serviceSpec)
	resultOutput := map[string]string{"harbor": "registry-endpoint", "nfs": "shared-storage-endpoint"}[input.Kind]
	definition := domain.PipelineDefinition{
		Stages: []domain.PipelineStage{
			{ID: "machine", PluginID: topologyPlugin.Manifest().ID, Title: "Provision dedicated machine", Spec: topologySpec},
			{ID: "service", PluginID: servicePlugin.Manifest().ID, Title: "Install and verify " + input.Kind, Spec: serviceRaw, Bindings: []domain.PipelineBinding{
				{Path: "/machineSetRef", FromStage: "machine", FromOutput: "machine-set"},
				{Path: "/machineAccessRef", FromStage: "machine", FromOutput: "machine-access"},
			}},
		},
		Result: domain.PipelineOutput{Stage: "service", Output: resultOutput},
	}
	validated, failure := s.validatedManagedPipeline(request.Context(), input.WorkspaceID, definition)
	if failure != nil {
		failure.write(response)
		return
	}
	resourceID := id.New("svc")
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: input.WorkspaceID, Name: "managed-" + input.Kind + "-" + resourceID, Description: "", Definition: validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash}
	run := domain.PipelineRun{ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: input.WorkspaceID, Name: "Provision " + input.Name, PipelineHash: pipeline.Hash, ResultType: pipeline.Resolution.Result.Type, ResultVersion: pipeline.Resolution.Result.Version}
	for position, stage := range pipeline.Definition.Stages {
		run.Stages = append(run.Stages, domain.PipelineRunStage{ID: run.ID + "_" + stage.ID, RunID: run.ID, Position: position + 1, StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending})
	}
	managedSpec, _ := json.Marshal(input)
	manifest := servicePlugin.Manifest()
	resource, err := s.store.CreateManagedPipelineResource(request.Context(), domain.ManagedResource{ID: resourceID, WorkspaceID: input.WorkspaceID, Name: input.Name, Kind: input.Kind, Provider: connection.Provider, ConnectionID: connection.ID, PluginID: manifest.ID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Spec: managedSpec}, pipeline, run, []domain.ManagedResourceDependency{{ResourceID: resourceID, DependsOnID: template.ID, Relation: "machine-template"}})
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "service_conflict", "A service with this name already exists for the selected connection.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the infrastructure service workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

type managedNodePoolInput struct {
	Labels   map[string]string      `json:"labels,omitempty"`
	Name     string                 `json:"name"`
	Count    int                    `json:"count"`
	Zones    []string               `json:"zones"`
	Capacity managedMachineCapacity `json:"capacity"`
}

type managedMachineCapacity struct {
	Cores     int `json:"cores"`
	MemoryMiB int `json:"memoryMiB"`
	DiskGiB   int `json:"diskGiB"`
}

type managedKubernetesClusterInput struct {
	Mode                 string                          `json:"mode,omitempty"`
	Members              []managedKubernetesClusterInput `json:"members,omitempty"`
	Networking           string                          `json:"networking,omitempty"`
	ClusterID            int                             `json:"clusterId,omitempty"`
	PodCIDR              string                          `json:"podCIDR,omitempty"`
	ServiceCIDR          string                          `json:"serviceCIDR,omitempty"`
	MachineNodes         map[string]string               `json:"machineNodes"`
	WorkspaceID          string                          `json:"workspaceId"`
	ConnectionID         string                          `json:"connectionId"`
	TemplateID           string                          `json:"templateId"`
	HarborID             string                          `json:"harborId"`
	NFSID                string                          `json:"nfsId"`
	Name                 string                          `json:"name"`
	BaseVMID             int                             `json:"baseVMID"`
	AddressStart         string                          `json:"addressStart"`
	PrefixLength         int                             `json:"prefixLength"`
	Gateway              string                          `json:"gateway"`
	DNSServer            string                          `json:"dnsServer"`
	ControlPlanes        int                             `json:"controlPlanes"`
	ControlPlaneZones    []string                        `json:"controlPlaneZones"`
	ControlPlaneCapacity managedMachineCapacity          `json:"controlPlaneCapacity"`
	ManagementPool       managedNodePoolInput            `json:"managementPool"`
	ApplicationPools     []managedNodePoolInput          `json:"applicationPools"`
	ManagedProfile       string                          `json:"managedProfile"`
	ScrapeInterval       string                          `json:"scrapeInterval"`
	Retention            string                          `json:"retention"`
}

func (s *Server) listKubernetesClusters(response http.ResponseWriter, request *http.Request) {
	items, err := s.store.ListManagedResources(request.Context(), "kubernetes-cluster")
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list Kubernetes clusters.")
		return
	}
	user := requestUser(request)
	if user.Role != "admin" {
		assignments, assignmentErr := s.store.ListClusterAssignments(request.Context(), user.ID)
		if assignmentErr != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not list assigned Kubernetes clusters.")
			return
		}
		allowed := map[string]bool{}
		for _, assignment := range assignments {
			allowed[assignment.ClusterResourceID] = true
		}
		filtered := items[:0]
		for _, item := range items {
			if allowed[item.ID] {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getKubernetesCluster(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "kubernetes-cluster" {
		writeError(response, http.StatusNotFound, "not_found", "Kubernetes cluster not found.")
		return
	}
	if err != nil {
		slog.Error("read managed cluster", "error", err)
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the Kubernetes cluster.")
		return
	}
	if !s.validateClusterAssignment(response, request, requestUser(request), resource.ID) {
		return
	}
	writeJSON(response, http.StatusOK, resource)
}
