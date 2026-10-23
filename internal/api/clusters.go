package api

import (
	"bytes"
	"context"

	"encoding/json"
	"errors"
	"fmt"
	"io"

	"net/http"

	"regexp"
	"slices"

	"strings"

	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"kubephos.dev/kubephos/internal/artifacts"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"

	kubeconfigutil "kubephos.dev/kubephos/internal/kubeconfig"
	"kubephos.dev/kubephos/internal/plugins"

	"kubephos.dev/kubephos/internal/schema"

	"kubephos.dev/kubephos/internal/storage"

	"kubephos.dev/kubephos/internal/workflows"
)

func (s *Server) createKubernetesCluster(response http.ResponseWriter, request *http.Request) {
	var input managedKubernetesClusterInput
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Mode == "multi" {
		s.createMultiCluster(response, request, input)
		return
	}
	if input.Mode != "" && input.Mode != "single" {
		writeError(response, http.StatusUnprocessableEntity, "invalid_mode", "Select single cluster or multi cluster.")
		return
	}
	input.Networking = ""
	prepared := s.prepareManagedCluster(response, request, input)
	if prepared == nil {
		return
	}
	var allocationSpec map[string]any
	json.Unmarshal(prepared.TopologySpec, &allocationSpec)
	release, valid := s.validateProvisioning(response, request, prepared.Connection.ID, allocationSpec, prepared.MachineCount)
	if !valid {
		return
	}
	defer release()
	s.persistManagedCluster(response, request, prepared)
}
func (s *Server) persistManagedCluster(response http.ResponseWriter, request *http.Request, prepared *preparedManagedCluster) {
	input, connection, definition := prepared.Input, prepared.Connection, prepared.Definition
	validated, failure := s.validatedManagedPipeline(request.Context(), input.WorkspaceID, definition)
	if failure != nil {
		failure.write(response)
		return
	}
	resourceID := id.New("cluster")
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: input.WorkspaceID, Name: "managed-cluster-" + resourceID, Definition: validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash}
	run := pipelineRunForManagedPipeline(pipeline, "Provision "+input.Name)
	managedSpec, _ := json.Marshal(input)
	manifest := prepared.Manifest
	dependencies := []domain.ManagedResourceDependency{{ResourceID: resourceID, DependsOnID: prepared.DependencyIDs[0], Relation: "machine-template"}, {ResourceID: resourceID, DependsOnID: prepared.DependencyIDs[1], Relation: "registry"}, {ResourceID: resourceID, DependsOnID: prepared.DependencyIDs[2], Relation: "shared-storage"}}
	resource, err := s.store.CreateManagedPipelineResource(request.Context(), domain.ManagedResource{ID: resourceID, WorkspaceID: input.WorkspaceID, Name: input.Name, Kind: "kubernetes-cluster", Provider: connection.Provider, ConnectionID: connection.ID, PluginID: manifest.ID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Spec: managedSpec}, pipeline, run, dependencies)
	if err != nil {
		var databaseError *pgconn.PgError
		if errors.As(err, &databaseError) && databaseError.Code == "23505" {
			writeError(response, http.StatusConflict, "cluster_conflict", "A cluster with this name already exists for the selected connection.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the Kubernetes cluster workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

type preparedManagedCluster struct {
	Input         managedKubernetesClusterInput
	Connection    domain.ProviderConnection
	Definition    domain.PipelineDefinition
	Manifest      plugins.Manifest
	DependencyIDs []string
	MachineCount  int
	TopologySpec  json.RawMessage
}

func (s *Server) prepareManagedCluster(response http.ResponseWriter, request *http.Request, input managedKubernetesClusterInput) *preparedManagedCluster {
	input.WorkspaceID = strings.TrimSpace(input.WorkspaceID)
	input.ConnectionID = strings.TrimSpace(input.ConnectionID)
	input.TemplateID = strings.TrimSpace(input.TemplateID)
	input.HarborID = strings.TrimSpace(input.HarborID)
	input.NFSID = strings.TrimSpace(input.NFSID)
	input.Name = strings.TrimSpace(input.Name)
	input.AddressStart = strings.TrimSpace(input.AddressStart)
	input.Gateway = strings.TrimSpace(input.Gateway)
	input.DNSServer = strings.TrimSpace(input.DNSServer)
	input.ManagedProfile = "2026.09.2"
	if input.ScrapeInterval == "" {
		input.ScrapeInterval = "15s"
	}
	if input.Retention == "" {
		input.Retention = "7d"
	}
	if !slices.Contains([]string{"5s", "10s", "15s", "30s"}, input.ScrapeInterval) || !slices.Contains([]string{"1d", "7d", "15d"}, input.Retention) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_observability", "Select a supported Prometheus scrape interval and retention.")
		return nil
	}

	if !shortResourceNamePattern.MatchString(input.Name) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_name", "Cluster name must be a lowercase label with at most 32 characters.")
		return nil
	}
	if input.ControlPlanes != 1 && input.ControlPlanes != 3 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_control_plane", "Use one or three control plane nodes.")
		return nil
	}
	if len(input.ControlPlaneZones) == 0 {
		input.ControlPlaneZones = []string{"zone-a"}
	}
	input.ControlPlaneCapacity = managedCapacityDefaults(input.ControlPlaneCapacity, managedMachineCapacity{Cores: 2, MemoryMiB: 4096, DiskGiB: 80})
	if input.ManagementPool.Name == "" {
		input.ManagementPool.Name = "management"
	}
	if input.ManagementPool.Count == 0 {
		input.ManagementPool.Count = 1
	}
	if len(input.ManagementPool.Zones) == 0 {
		input.ManagementPool.Zones = []string{"zone-a"}
	}
	input.ManagementPool.Capacity = managedCapacityDefaults(input.ManagementPool.Capacity, managedMachineCapacity{Cores: 4, MemoryMiB: 8192, DiskGiB: 80})
	if len(input.ApplicationPools) == 0 {
		input.ApplicationPools = []managedNodePoolInput{{Name: "applications", Count: 2, Zones: []string{"zone-a"}, Capacity: managedMachineCapacity{Cores: 4, MemoryMiB: 8192, DiskGiB: 80}}}
	}
	seenPools := map[string]bool{input.ManagementPool.Name: true}
	if !validManagedPool(input.ManagementPool, shortResourceNamePattern) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_pool", "The management pool requires a valid name, node count and at least one zone.")
		return nil
	}
	totalMachines := input.ControlPlanes + input.ManagementPool.Count
	for index := range input.ApplicationPools {
		pool := &input.ApplicationPools[index]
		pool.Capacity = managedCapacityDefaults(pool.Capacity, managedMachineCapacity{Cores: 4, MemoryMiB: 8192, DiskGiB: 80})
		if !validManagedPool(*pool, shortResourceNamePattern) || seenPools[pool.Name] {
			writeError(response, http.StatusUnprocessableEntity, "invalid_pool", "Application pools require unique names, a node count and at least one valid zone.")
			return nil
		}
		seenPools[pool.Name] = true
		totalMachines += pool.Count
	}
	if totalMachines > 12 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "A cluster can contain at most 12 machines in the current provider profile.")
		return nil
	}
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), input.ConnectionID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_connection", "Select an existing infrastructure connection.")
		return nil
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate the infrastructure connection.")
		return nil
	}
	template, failure := s.managedDependency(request.Context(), input.TemplateID, "machine-template", input.WorkspaceID, connection.ID)
	if failure != nil {
		failure.write(response)
		return nil
	}
	harbor, failure := s.managedDependency(request.Context(), input.HarborID, "harbor", input.WorkspaceID, connection.ID)
	if failure != nil {
		failure.write(response)
		return nil
	}
	nfs, failure := s.managedDependency(request.Context(), input.NFSID, "nfs", input.WorkspaceID, connection.ID)
	if failure != nil {
		failure.write(response)
		return nil
	}
	registryCredential, err := s.store.GetPipelineRunArtifact(request.Context(), harbor.PipelineRunID, "registry-push-credential")
	if err != nil || registryCredential.Type != "RegistryCredential" || !registryCredential.Sensitive {
		writeError(response, http.StatusConflict, "registry_invalid", "The selected Harbor pull identity is unavailable.")
		return nil
	}
	var templateSpec struct {
		Node    string `json:"node"`
		VMID    int    `json:"vmid"`
		DiskGiB int    `json:"diskGiB"`
		SSHUser string `json:"sshUser"`
	}
	if err := json.Unmarshal(template.Spec, &templateSpec); err != nil || templateSpec.Node == "" || templateSpec.VMID < 100 || templateSpec.SSHUser == "" {
		writeError(response, http.StatusConflict, "template_invalid", "The saved machine template configuration is incomplete.")
		return nil
	}
	if input.BaseVMID < 100 || input.BaseVMID > 999999988 || input.BaseVMID+totalMachines-1 > 999999999 {
		writeError(response, http.StatusUnprocessableEntity, "invalid_vmid", "The consecutive cluster VM ID range is invalid.")
		return nil
	}
	if !validManagedCapacity(input.ControlPlaneCapacity, templateSpec.DiskGiB) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "Control plane capacity is outside the supported range or smaller than the template disk.")
		return nil
	}
	if !validManagedCapacity(input.ManagementPool.Capacity, templateSpec.DiskGiB) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "Management pool capacity is outside the supported range or smaller than the template disk.")
		return nil
	}
	for _, pool := range input.ApplicationPools {
		if validManagedCapacity(pool.Capacity, templateSpec.DiskGiB) {
			continue
		}
		writeError(response, http.StatusUnprocessableEntity, "invalid_capacity", "Application pool "+pool.Name+" has capacity outside the supported range or smaller than the template disk.")
		return nil
	}
	topologyPlugin, err := s.pluginForCapability(connection.Provider, "infrastructure.machine-topology.provision")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return nil
	}
	clusterPlugin, err := s.pluginForCapability("", "cluster.bootstrap")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return nil
	}
	storagePlugin, err := s.pluginForCapability("", "storage.kubernetes.attach")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return nil
	}
	platformPlugin, err := s.pluginForCapability("", "platform.managed.install")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return nil
	}
	observabilityPlugin, err := s.pluginForCapability("", "observability.metrics.install")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return nil
	}
	machineNodes, placementErr := managedClusterMachineNodes(&input, templateSpec.Node)
	if placementErr != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_machine_nodes", placementErr.Error())
		return nil
	}
	machineProfiles := make([]managedMachineCapacity, 0, totalMachines)
	machineProfiles = appendManagedMachineProfiles(machineProfiles, input.ControlPlanes, input.ControlPlaneCapacity)
	machineProfiles = appendManagedMachineProfiles(machineProfiles, input.ManagementPool.Count, input.ManagementPool.Capacity)
	for _, pool := range input.ApplicationPools {
		machineProfiles = appendManagedMachineProfiles(machineProfiles, pool.Count, pool.Capacity)
	}
	topologySpec, _ := json.Marshal(map[string]any{"connectionRef": connection.ID, "machineTemplateRef": template.ArtifactID, "node": templateSpec.Node, "templateVMID": templateSpec.VMID, "baseVMID": input.BaseVMID, "addressStart": input.AddressStart, "prefixLength": input.PrefixLength, "gateway": input.Gateway, "dnsServer": input.DNSServer, "namePrefix": input.Name, "machineNames": managedClusterMachineNames(input), "machineNodes": machineNodes, "machineCount": totalMachines, "cores": input.ControlPlaneCapacity.Cores, "memoryMiB": input.ControlPlaneCapacity.MemoryMiB, "diskGiB": input.ControlPlaneCapacity.DiskGiB, "machineProfiles": machineProfiles, "sshUser": templateSpec.SSHUser, "cleanupAfterTest": false})
	nodePools := []map[string]any{{"name": input.ManagementPool.Name, "role": "management", "count": input.ManagementPool.Count, "zones": input.ManagementPool.Zones, "labels": input.ManagementPool.Labels}}
	for _, pool := range input.ApplicationPools {
		nodePools = append(nodePools, map[string]any{"name": pool.Name, "role": "application", "count": pool.Count, "zones": pool.Zones, "labels": pool.Labels})
	}
	clusterSpec, _ := json.Marshal(map[string]any{"machineSetRef": "", "machineAccessRef": "", "clusterName": input.Name, "controlPlanes": input.ControlPlanes, "controlPlaneZones": input.ControlPlaneZones, "nodePools": nodePools, "registryEndpointRef": harbor.ArtifactID, "registryCredentialRef": registryCredential.ID})
	if input.Networking == "cilium" {
		var spec map[string]any
		json.Unmarshal(clusterSpec, &spec)
		spec["networking"] = "cilium"
		spec["clusterId"] = input.ClusterID
		spec["podCIDR"] = input.PodCIDR
		spec["serviceCIDR"] = input.ServiceCIDR
		clusterSpec, _ = json.Marshal(spec)
	}
	storageSpec, _ := json.Marshal(map[string]any{"clusterConnectionRef": "", "sharedStorageEndpointRef": nfs.ArtifactID, "storageClassName": "shared-storage"})
	platformSpec, _ := json.Marshal(map[string]any{"clusterConnectionRef": ""})
	observabilitySpec, _ := json.Marshal(map[string]any{"clusterConnectionRef": "", "storageClassCapabilityRef": "", "scrapeInterval": input.ScrapeInterval, "retention": input.Retention})
	definition := domain.PipelineDefinition{Stages: []domain.PipelineStage{
		{ID: "machines", PluginID: topologyPlugin.Manifest().ID, Title: "Provision cluster machines", Spec: topologySpec},
		{ID: "kubernetes", PluginID: clusterPlugin.Manifest().ID, Title: "Bootstrap Kubernetes", Spec: clusterSpec, Bindings: []domain.PipelineBinding{{Path: "/machineSetRef", FromStage: "machines", FromOutput: "machine-set"}, {Path: "/machineAccessRef", FromStage: "machines", FromOutput: "machine-access"}}},
		{ID: "storage", PluginID: storagePlugin.Manifest().ID, Title: "Attach managed storage", Spec: storageSpec, Bindings: []domain.PipelineBinding{{Path: "/clusterConnectionRef", FromStage: "kubernetes", FromOutput: "cluster-connection"}}},
		{ID: "observability", PluginID: observabilityPlugin.Manifest().ID, Title: "Install managed observability", Spec: observabilitySpec, Bindings: []domain.PipelineBinding{{Path: "/clusterConnectionRef", FromStage: "kubernetes", FromOutput: "cluster-connection"}, {Path: "/storageClassCapabilityRef", FromStage: "storage", FromOutput: "storage-class-capability"}}},
		{ID: "platform", PluginID: platformPlugin.Manifest().ID, Title: "Install managed platform", Spec: platformSpec, Bindings: []domain.PipelineBinding{{Path: "/clusterConnectionRef", FromStage: "kubernetes", FromOutput: "cluster-connection"}}},
	}, Result: domain.PipelineOutput{Stage: "observability", Output: "observability-capability"}}
	return &preparedManagedCluster{Input: input, Connection: connection, Definition: definition, Manifest: clusterPlugin.Manifest(), DependencyIDs: []string{template.ID, harbor.ID, nfs.ID}, MachineCount: totalMachines, TopologySpec: topologySpec}
}

func validManagedPool(pool managedNodePoolInput, pattern *regexp.Regexp) bool {
	if !pattern.MatchString(pool.Name) || pool.Count < 1 || pool.Count > 12 || len(pool.Zones) == 0 || len(pool.Zones) > 12 {
		return false
	}
	seen := map[string]bool{}
	for _, zone := range pool.Zones {
		if !pattern.MatchString(zone) || seen[zone] {
			return false
		}
		seen[zone] = true
	}
	return true
}

func managedCapacityDefaults(capacity, defaults managedMachineCapacity) managedMachineCapacity {
	if capacity.Cores == 0 {
		capacity.Cores = defaults.Cores
	}
	if capacity.MemoryMiB == 0 {
		capacity.MemoryMiB = defaults.MemoryMiB
	}
	if capacity.DiskGiB == 0 {
		capacity.DiskGiB = defaults.DiskGiB
	}
	return capacity
}

func validManagedCapacity(capacity managedMachineCapacity, templateDiskGiB int) bool {
	return capacity.Cores >= 1 && capacity.Cores <= 32 && capacity.MemoryMiB >= 1024 && capacity.MemoryMiB <= 131072 && capacity.DiskGiB >= templateDiskGiB && capacity.DiskGiB <= 2048
}

// Keep VM names in the same order and format as the k3s node names.
func managedClusterMachineNames(input managedKubernetesClusterInput) []string {
	var names []string
	appendPool := func(pool string, count int) {
		for index := 1; index <= count; index++ {
			names = append(names, fmt.Sprintf("%s-%s-%d", input.Name, pool, index))
		}
	}
	appendPool("control-plane", input.ControlPlanes)
	appendPool(input.ManagementPool.Name, input.ManagementPool.Count)
	for _, pool := range input.ApplicationPools {
		appendPool(pool.Name, pool.Count)
	}
	return names
}

func appendManagedMachineProfiles(profiles []managedMachineCapacity, count int, capacity managedMachineCapacity) []managedMachineCapacity {
	for range count {
		profiles = append(profiles, capacity)
	}
	return profiles
}

func (s *Server) managedDependency(ctx context.Context, resourceID, kind, workspaceID, connectionID string) (domain.ManagedResource, *managedOperationFailure) {
	resource, err := s.store.GetManagedResource(ctx, resourceID)
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != kind {
		return domain.ManagedResource{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_dependency", message: "Select a ready " + kind + " resource."}
	}
	if err != nil {
		return domain.ManagedResource{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate the selected dependency."}
	}
	if resource.Status != "ready" || resource.ArtifactID == "" || resource.WorkspaceID != workspaceID || resource.ConnectionID != connectionID {
		return domain.ManagedResource{}, &managedOperationFailure{status: http.StatusConflict, code: "dependency_not_ready", message: "The selected " + kind + " must be ready and belong to the same environment and connection."}
	}
	return resource, nil
}

func pipelineRunForManagedPipeline(pipeline domain.Pipeline, name string) domain.PipelineRun {
	run := domain.PipelineRun{ID: id.New("run"), PipelineID: pipeline.ID, WorkspaceID: pipeline.WorkspaceID, Name: name, PipelineHash: pipeline.Hash, ResultType: pipeline.Resolution.Result.Type, ResultVersion: pipeline.Resolution.Result.Version}
	for position, stage := range pipeline.Definition.Stages {
		run.Stages = append(run.Stages, domain.PipelineRunStage{ID: run.ID + "_" + stage.ID, RunID: run.ID, Position: position + 1, StageID: stage.ID, PluginID: stage.PluginID, Title: stage.Title, Status: domain.StepPending})
	}
	return run
}

func (s *Server) deleteInfrastructureService(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "harbor" && resource.Kind != "nfs" {
		writeError(response, http.StatusNotFound, "not_found", "Infrastructure service not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the infrastructure service.")
		return
	}
	s.deleteManagedPipelineResource(response, request, resource, "service")
}

func (s *Server) deleteKubernetesCluster(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "kubernetes-cluster" {
		writeError(response, http.StatusNotFound, "not_found", "Kubernetes cluster not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the Kubernetes cluster.")
		return
	}
	used, err := s.store.ClusterHasExperimentConfigurations(request.Context(), resource.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not validate cluster dependencies.")
		return
	}
	if used {
		writeError(response, http.StatusConflict, "cluster_in_use", "Delete the experiment configurations associated with this cluster first.")
		return
	}
	s.deleteManagedPipelineResource(response, request, resource, "cluster")
}

func (s *Server) recreateKubernetesCluster(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "kubernetes-cluster" {
		writeError(response, http.StatusNotFound, "not_found", "Kubernetes cluster not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the Kubernetes cluster.")
		return
	}
	cleanupRunID := resource.PipelineRunID
	provisionPipelineID := resource.PipelineID
	recovering := (resource.Status == "recreation-failed" || resource.Status == "maintenance-failed")
	if recovering {
		cleanupRunID = resource.RecreationPipelineRunID
		provisionPipelineID = resource.RecreationPipelineID
		failedRun, runErr := s.store.GetPipelineRun(request.Context(), cleanupRunID)
		if runErr != nil || failedRun.Status != domain.OperationFailed && failedRun.Status != domain.OperationCanceled || len(failedRun.Stages) == 0 {
			writeError(response, http.StatusConflict, "recreation_unavailable", "The failed recreation lifecycle is unavailable.")
			return
		}
		if _, pipelineErr := s.store.GetPipeline(request.Context(), provisionPipelineID); pipelineErr != nil {
			writeError(response, http.StatusConflict, "recreation_unavailable", "The failed recreation workflow is unavailable.")
			return
		}
		if err := s.store.ResetFailedManagedResourceRecreation(request.Context(), resource.ID); err != nil {
			writeError(response, http.StatusConflict, "recreation_unavailable", "The failed recreation cannot be retried in its current state.")
			return
		}
		resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not reload the Kubernetes cluster.")
			return
		}
	}
	if resource.Status != "ready" || resource.PipelineRunID == "" || resource.PipelineID == "" {
		writeError(response, http.StatusConflict, "cluster_not_ready", "Only a ready cluster can be recreated.")
		return
	}
	var input managedKubernetesClusterInput
	if err := json.Unmarshal(resource.Spec, &input); err != nil {
		writeError(response, http.StatusConflict, "configuration_invalid", "The saved cluster configuration is invalid.")
		return
	}
	input.ManagedProfile = "2026.09.2"
	connection, err := s.store.GetProviderConnectionConfiguration(request.Context(), input.ConnectionID)
	if err != nil || connection.ID != resource.ConnectionID {
		writeError(response, http.StatusConflict, "connection_unavailable", "The saved Proxmox connection is unavailable.")
		return
	}
	for _, dependency := range []struct{ id, kind string }{{input.TemplateID, "machine-template"}, {input.HarborID, "harbor"}, {input.NFSID, "nfs"}} {
		if _, failure := s.managedDependency(request.Context(), dependency.id, dependency.kind, resource.WorkspaceID, resource.ConnectionID); failure != nil {
			failure.write(response)
			return
		}
	}
	sourceRun, err := s.store.GetPipelineRun(request.Context(), cleanupRunID)
	if err != nil || !terminal(sourceRun.Status) || !recovering && sourceRun.Status != domain.OperationSucceeded || len(sourceRun.Stages) == 0 {
		writeError(response, http.StatusConflict, "recreation_unavailable", "The cluster lifecycle is unavailable.")
		return
	}
	sourcePipeline, err := s.store.GetPipeline(request.Context(), provisionPipelineID)
	if err != nil {
		writeError(response, http.StatusConflict, "recreation_unavailable", "The saved cluster workflow is unavailable.")
		return
	}
	definition := domain.PipelineDefinition{}
	resolution := domain.PipelineResolution{}
	validation := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC()}
	for sourcePosition := len(sourceRun.Stages) - 1; sourcePosition >= 0; sourcePosition-- {
		sourceStage := sourceRun.Stages[sourcePosition]
		if sourceStage.OperationID == "" {
			continue
		}
		source, err := s.store.GetOperation(request.Context(), sourceStage.OperationID)
		if err != nil {
			writeError(response, http.StatusConflict, "recreation_unavailable", "A cluster lifecycle stage is unavailable.")
			return
		}
		if _, err := cleanupPlan(source); errors.Is(err, workflows.ErrNoCleanupSteps) {
			continue
		}
		cleanup, failure := s.validatedManagedCleanup(request.Context(), source)
		if failure != nil {
			failure.write(response)
			return
		}
		stageID := "cleanup-" + sourceStage.StageID
		definition.Stages = append(definition.Stages, domain.PipelineStage{ID: stageID, PluginID: cleanup.PluginID, Title: "Remove " + strings.ToLower(sourceStage.Title), Spec: cleanup.Spec})
		resolution.Stages = append(resolution.Stages, domain.ResolvedPipelineStage{ID: stageID, PluginID: cleanup.PluginID, PluginVersion: cleanup.PluginVersion, PluginDigest: cleanup.PluginDigest, Spec: cleanup.Spec, Plan: cleanup.Plan})
		validation.Issues = append(validation.Issues, cleanup.Validation.Issues...)
	}
	provisionDefinition := clusterProvisioningStages(sourcePipeline.Definition)
	for index := range provisionDefinition.Stages {
		stage := &provisionDefinition.Stages[index]
		if stage.PluginID != "io.kubephos.infrastructure.proxmox.topology" {
			continue
		}
		machineInput := input
		if input.Mode == "multi" {
			found := false
			for index, member := range input.Members {
				if stage.ID == fmt.Sprintf("c%d-machines", index+1) {
					machineInput = member
					found = true
					break
				}
			}
			if !found {
				writeError(response, 409, "configuration_invalid", "The saved member topology is unavailable.")
				return
			}
		}
		var spec map[string]any
		if err := json.Unmarshal(stage.Spec, &spec); err != nil {
			writeError(response, http.StatusConflict, "configuration_invalid", "The saved machine configuration is invalid.")
			return
		}
		defaultNode, _ := spec["node"].(string)
		nodes, err := managedClusterMachineNodes(&machineInput, defaultNode)
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid_machine_nodes", err.Error())
			return
		}
		for _, key := range []string{"reconcile", "prune", "previousMachineSetRef", "machineAccessRef", "readyClusterRef", "ownershipMarker", "plannedMachines", "previousMachines"} {
			delete(spec, key)
		}
		names := managedClusterMachineNames(machineInput)
		profiles := appendManagedMachineProfiles(nil, machineInput.ControlPlanes, machineInput.ControlPlaneCapacity)
		profiles = appendManagedMachineProfiles(profiles, machineInput.ManagementPool.Count, machineInput.ManagementPool.Capacity)
		for _, pool := range machineInput.ApplicationPools {
			profiles = appendManagedMachineProfiles(profiles, pool.Count, pool.Capacity)
		}
		spec["machineCount"] = len(names)
		spec["machineNames"] = names
		spec["machineProfiles"] = profiles
		spec["baseVMID"] = machineInput.BaseVMID
		spec["addressStart"] = machineInput.AddressStart
		spec["machineNodes"] = nodes
		stage.Spec, _ = json.Marshal(spec)
	}

	for i := range provisionDefinition.Stages {
		stage := &provisionDefinition.Stages[i]
		if stage.ID == "kubernetes" {
			var spec map[string]any
			_ = json.Unmarshal(stage.Spec, &spec)
			delete(spec, "reconcile")
			delete(spec, "previousMachineSetRef")
			stage.Spec, _ = json.Marshal(spec)
		}
	}
	if input.Mode != "multi" && !pipelineHasStage(provisionDefinition, "platform") {
		platformPlugin, err := s.pluginForCapability("", "platform.managed.install")
		if err != nil {
			writeError(response, http.StatusUnprocessableEntity, "recreation_invalid", err.Error())
			return
		}
		provisionDefinition = withManagedPlatformStage(provisionDefinition, platformPlugin.Manifest().ID)
	}
	provision, err := workflows.ValidateDeferred(request.Context(), s.registry, provisionDefinition)
	if err != nil || !provision.Validation.Valid {
		message := "The saved cluster configuration is no longer valid."
		if err != nil {
			message = err.Error()
		}
		writeError(response, http.StatusUnprocessableEntity, "recreation_invalid", message)
		return
	}
	for _, stage := range provision.Resolution.Stages {
		plugin, err := s.registry.Get(stage.PluginID)
		if err != nil || validatePlanEffects(plugin.Manifest(), stage.Plan) != nil {
			writeError(response, http.StatusUnprocessableEntity, "recreation_invalid", "A provisioning plugin no longer satisfies its saved contract.")
			return
		}
		if err := s.validateExternalArtifactInputs(request.Context(), resource.WorkspaceID, withoutPipelineBindings(stage.Plan)); err != nil {
			writeError(response, http.StatusUnprocessableEntity, "recreation_invalid", err.Error())
			return
		}
	}
	offset := len(definition.Stages)
	for index := range provision.Resolution.Stages {
		shiftPipelineTokens(&provision.Resolution.Stages[index], index+1, offset+index+1)
	}
	definition.Stages = append(definition.Stages, provision.Definition.Stages...)
	definition.Result = provision.Definition.Result
	resolution.Stages = append(resolution.Stages, provision.Resolution.Stages...)
	resolution.Result = provision.Resolution.Result
	validation.Issues = append(validation.Issues, provision.Validation.Issues...)
	validation.Issues = append(validation.Issues, domain.ValidationIssue{Level: "info", Message: "Provisioning health checks will run after the current cluster has been removed."})
	hash, err := workflows.Fingerprint(definition, resolution)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "planning_failed", "Could not fingerprint the recreation workflow.")
		return
	}
	pipelineID := id.New("pipe")
	pipeline := domain.Pipeline{ID: pipelineID, WorkspaceID: resource.WorkspaceID, Name: "recreate-managed-" + resource.ID + "-" + strings.TrimPrefix(pipelineID, "pipe_")[:8], Definition: definition, Resolution: resolution, Validation: validation, Hash: hash}
	run := pipelineRunForManagedPipeline(pipeline, "Recreate "+resource.Name)
	desiredSpec, _ := json.Marshal(input)
	if err := s.store.CreateManagedResourceRecreation(request.Context(), resource.ID, desiredSpec, pipeline, run); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "cluster_in_use", "The cluster is running an experiment or another lifecycle operation.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not queue the cluster recreation workflow.")
		return
	}
	resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the recreation workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

func clusterProvisioningStages(source domain.PipelineDefinition) domain.PipelineDefinition {
	byID := map[string]domain.PipelineStage{}
	for _, stage := range source.Stages {
		if !strings.HasPrefix(stage.ID, "cleanup-") && stage.ID != "prune-machines" {
			byID[stage.ID] = stage
		}
	}
	stages := []domain.PipelineStage{}
	for _, stageID := range []string{"machines", "kubernetes", "storage", "observability", "platform"} {
		if stage, exists := byID[stageID]; exists {
			stages = append(stages, stage)
			delete(byID, stageID)
		}
	}
	for _, stage := range source.Stages {
		if current, exists := byID[stage.ID]; exists {
			stages = append(stages, current)
			delete(byID, stage.ID)
		}
	}
	return domain.PipelineDefinition{Stages: stages, Result: source.Result}
}

func pipelineHasStage(definition domain.PipelineDefinition, stageID string) bool {
	for _, stage := range definition.Stages {
		if stage.ID == stageID {
			return true
		}
	}
	return false
}

func withManagedPlatformStage(definition domain.PipelineDefinition, pluginID string) domain.PipelineDefinition {
	if pipelineHasStage(definition, "platform") {
		return definition
	}
	definition.Stages = append(definition.Stages, domain.PipelineStage{ID: "platform", PluginID: pluginID, Title: "Install managed platform", Spec: json.RawMessage(`{"clusterConnectionRef":""}`), Bindings: []domain.PipelineBinding{{Path: "/clusterConnectionRef", FromStage: "kubernetes", FromOutput: "cluster-connection"}}})
	return definition
}

func shiftPipelineTokens(stage *domain.ResolvedPipelineStage, previousPosition, currentPosition int) {
	if previousPosition == currentPosition {
		return
	}
	for bindingIndex := range stage.Bindings {
		previous := fmt.Sprintf("art_pipeline_%d_%d", previousPosition, bindingIndex+1)
		current := fmt.Sprintf("art_pipeline_%d_%d", currentPosition, bindingIndex+1)
		stage.Spec = bytes.ReplaceAll(stage.Spec, []byte(previous), []byte(current))
		for stepIndex := range stage.Plan.Steps {
			step := &stage.Plan.Steps[stepIndex]
			step.Input = bytes.ReplaceAll(step.Input, []byte(previous), []byte(current))
			for inputIndex := range step.ArtifactInputs {
				if step.ArtifactInputs[inputIndex].ArtifactID == previous {
					step.ArtifactInputs[inputIndex].ArtifactID = current
				}
			}
		}
	}
}

func (s *Server) deleteManagedPipelineResource(response http.ResponseWriter, request *http.Request, resource domain.ManagedResource, label string) {
	if resource.Status == "deletion-failed" {
		if err := s.store.ResetFailedManagedResourceDeletion(request.Context(), resource.ID); err != nil {
			writeError(response, http.StatusConflict, "cleanup_unavailable", "The failed deletion workflow cannot be retried in its current state.")
			return
		}
		var err error
		resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
		if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not reload the managed lifecycle.")
			return
		}
	}
	sourceRunID := resource.PipelineRunID
	if resource.Status == "recreation-failed" || resource.Status == "maintenance-failed" {
		sourceRunID = resource.RecreationPipelineRunID
	}
	if resource.Status != "ready" && resource.Status != "failed" && resource.Status != "recreation-failed" && resource.Status != "maintenance-failed" || sourceRunID == "" {
		writeError(response, http.StatusConflict, label+"_not_ready", "Only a completed managed lifecycle can be deleted.")
		return
	}
	sourceRun, err := s.store.GetPipelineRun(request.Context(), sourceRunID)
	if err != nil || !terminal(sourceRun.Status) || len(sourceRun.Stages) == 0 {
		writeError(response, http.StatusConflict, "cleanup_unavailable", "The completed managed lifecycle is unavailable.")
		return
	}
	// Recovery runs may reinstall only the service. Resolve its dedicated machine
	// through the immutable artifact ID rather than the resource's display name.
	if resource.Kind == "harbor" || resource.Kind == "nfs" {
		hasMachine := false
		for _, stage := range sourceRun.Stages {
			hasMachine = hasMachine || stage.PluginID == "io.kubephos.infrastructure.proxmox.topology"
		}
		if !hasMachine {
			for _, stage := range sourceRun.Stages {
				source, err := s.store.GetOperation(request.Context(), stage.OperationID)
				if err != nil {
					writeError(response, http.StatusConflict, "cleanup_unavailable", "The service lifecycle is unavailable.")
					return
				}
				var input struct {
					MachineSetRef string `json:"machineSetRef"`
				}
				if json.Unmarshal(source.Spec, &input) != nil || input.MachineSetRef == "" {
					continue
				}
				artifact, err := s.store.GetArtifact(request.Context(), input.MachineSetRef)
				if err != nil || artifact.Type != "MachineSet" || artifact.VerifiedAt.IsZero() {
					writeError(response, http.StatusConflict, "cleanup_unavailable", "The original machine artifact is unavailable.")
					return
				}
				machine, err := s.store.GetOperation(request.Context(), artifact.OperationID)
				if err != nil || machine.WorkspaceID != resource.WorkspaceID || machine.PluginID != "io.kubephos.infrastructure.proxmox.topology" || machine.Status != domain.OperationSucceeded {
					writeError(response, http.StatusConflict, "cleanup_unavailable", "The original machine lifecycle is unavailable.")
					return
				}
				sourceRun.Stages = append([]domain.PipelineRunStage{{StageID: "machine", PluginID: machine.PluginID, Title: "Dedicated machine", OperationID: machine.ID}}, sourceRun.Stages...)
				break
			}
		}
	}
	definition := domain.PipelineDefinition{}
	resolution := domain.PipelineResolution{}
	validation := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC()}
	for sourcePosition := len(sourceRun.Stages) - 1; sourcePosition >= 0; sourcePosition-- {
		sourceStage := sourceRun.Stages[sourcePosition]
		if sourceStage.OperationID == "" {
			continue
		}
		source, err := s.store.GetOperation(request.Context(), sourceStage.OperationID)
		if err != nil {
			writeError(response, http.StatusConflict, "cleanup_unavailable", "A provisioning stage lifecycle is unavailable.")
			return
		}
		if _, err := cleanupPlan(source); err != nil {
			continue
		}
		cleanup, failure := s.validatedManagedCleanup(request.Context(), source)
		if failure != nil {
			failure.write(response)
			return
		}
		stageID := "cleanup-" + sourceStage.StageID
		definition.Stages = append(definition.Stages, domain.PipelineStage{ID: stageID, PluginID: cleanup.PluginID, Title: "Remove " + strings.ToLower(sourceStage.Title), Spec: cleanup.Spec})
		resolution.Stages = append(resolution.Stages, domain.ResolvedPipelineStage{ID: stageID, PluginID: cleanup.PluginID, PluginVersion: cleanup.PluginVersion, PluginDigest: cleanup.PluginDigest, Spec: cleanup.Spec, Plan: cleanup.Plan})
		validation.Issues = append(validation.Issues, cleanup.Validation.Issues...)
	}
	if len(definition.Stages) == 0 {
		if err := s.store.DeleteFailedManagedResource(request.Context(), resource.ID); errors.Is(err, storage.ErrConflict) {
			writeError(response, http.StatusConflict, "cleanup_unavailable", "The failed lifecycle cannot be dismissed in its current state.")
			return
		} else if err != nil {
			writeError(response, http.StatusInternalServerError, "database_error", "Could not dismiss the failed managed lifecycle.")
			return
		}
		response.WriteHeader(http.StatusNoContent)
		return
	}
	hash, err := workflows.Fingerprint(definition, resolution)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "planning_failed", "Could not fingerprint the deletion workflow.")
		return
	}
	pipelineID := id.New("pipe")
	pipeline := domain.Pipeline{ID: pipelineID, WorkspaceID: resource.WorkspaceID, Name: "delete-managed-" + resource.ID + "-" + strings.TrimPrefix(pipelineID, "pipe_")[:8], Definition: definition, Resolution: resolution, Validation: validation, Hash: hash}
	run := pipelineRunForManagedPipeline(pipeline, "Delete "+resource.Name)
	if err := s.store.CreateManagedPipelineResourceDeletion(request.Context(), resource.ID, pipeline, run); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, label+"_in_use", "The resource is still in use or already being deleted.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the managed deletion workflow.")
		return
	}
	resource, err = s.store.GetManagedResource(request.Context(), resource.ID)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the managed deletion workflow.")
		return
	}
	writeJSON(response, http.StatusAccepted, resource)
}

func (s *Server) downloadKubeconfig(response http.ResponseWriter, request *http.Request) {
	resource, err := s.store.GetManagedResource(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) || err == nil && resource.Kind != "kubernetes-cluster" {
		writeError(response, http.StatusNotFound, "not_found", "Kubernetes cluster not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the Kubernetes cluster.")
		return
	}
	if resource.Status != "ready" {
		writeError(response, http.StatusConflict, "cluster_not_ready", "Kubeconfig is available only after every cluster health gate passes.")
		return
	}
	if _, multi := multiClusterSpec(resource.Spec); multi {
		s.downloadMultiClusterKubeconfig(response, request, resource)
		return
	}
	artifact, err := s.store.GetPipelineRunArtifact(request.Context(), resource.PipelineRunID, "cluster-connection")
	if err != nil || artifact.Type != "ClusterConnection" || !artifact.Sensitive || artifact.VerifiedAt.IsZero() {
		writeError(response, http.StatusConflict, "kubeconfig_unavailable", "The verified cluster connection is unavailable.")
		return
	}
	raw, err := s.readArtifactPayload(request.Context(), artifact)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "artifact_error", "Could not decrypt the cluster connection.")
		return
	}
	var connection struct {
		Kind string `json:"kind"`
		Spec struct {
			Kubeconfig string `json:"kubeconfig"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &connection); err != nil || connection.Kind != "ClusterConnection" || strings.TrimSpace(connection.Spec.Kubeconfig) == "" {
		writeError(response, http.StatusConflict, "kubeconfig_invalid", "The stored cluster connection is invalid.")
		return
	}
	exportedKubeconfig, err := kubeconfigutil.WithIdentity(connection.Spec.Kubeconfig, resource.Name)
	if err != nil {
		writeError(response, http.StatusConflict, "kubeconfig_invalid", "The stored cluster connection has an invalid identity.")
		return
	}
	response.Header().Set("Content-Type", "application/yaml")
	response.Header().Set("Content-Disposition", `attachment; filename="`+resource.Name+`-kubeconfig.yaml"`)
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(response, exportedKubeconfig)
}

type managedOperationFailure struct {
	status  int
	code    string
	message string
	payload any
}

func (failure *managedOperationFailure) write(response http.ResponseWriter) {
	if failure.payload != nil {
		writeJSON(response, failure.status, failure.payload)
		return
	}
	writeError(response, failure.status, failure.code, failure.message)
}

func (s *Server) pluginForCapability(provider, capability string) (plugins.Plugin, error) {
	var selected plugins.Plugin
	for _, manifest := range s.registry.Manifests() {
		if provider != "" && manifest.Provider != provider || !manifest.HasCapability(capability) {
			continue
		}
		if selected != nil {
			return nil, fmt.Errorf("multiple %s implementations are installed for provider %s", capability, provider)
		}
		plugin, err := s.registry.Get(manifest.ID)
		if err != nil {
			return nil, err
		}
		selected = plugin
	}
	if selected == nil {
		return nil, fmt.Errorf("no installed plugin provides %s for provider %s", capability, provider)
	}
	return selected, nil
}

func (s *Server) validatedManagedPipeline(ctx context.Context, workspaceID string, definition domain.PipelineDefinition) (workflows.Result, *managedOperationFailure) {
	if _, err := s.store.GetWorkspace(ctx, workspaceID); errors.Is(err, storage.ErrNotFound) {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_workspace", message: "Select an existing workspace."}
	} else if err != nil {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate workspace."}
	}
	result, err := workflows.Validate(ctx, s.registry, definition)
	if err != nil {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_pipeline", message: err.Error()}
	}
	if !result.Validation.Valid {
		return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": result.Validation}}
	}
	for index := range result.Resolution.Stages {
		resolved := &result.Resolution.Stages[index]
		plugin, err := s.registry.Get(resolved.PluginID)
		if err != nil {
			return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plugin", message: err.Error()}
		}
		manifest := plugin.Manifest()
		if err := validatePlanEffects(manifest, resolved.Plan); err != nil {
			return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plan_effects", message: "Stage " + resolved.ID + ": " + err.Error()}
		}
		externalPlan := withoutPipelineBindings(resolved.Plan)
		if err := s.validateExternalArtifactInputs(ctx, workspaceID, externalPlan); err != nil {
			return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_reference", message: "Stage " + resolved.ID + ": " + err.Error()}
		}
		if err := s.validateResourceEffects(ctx, workspaceID, manifest, resolved.Plan); err != nil {
			return workflows.Result{}, &managedOperationFailure{status: http.StatusConflict, code: "resource_conflict", message: "Stage " + resolved.ID + ": " + err.Error()}
		}
		if hasPipelineBindings(resolved.Plan) {
			result.Validation.Issues = append(result.Validation.Issues, domain.ValidationIssue{Level: "info", Path: "stages." + resolved.ID, Message: "Runtime preflight will use verified outputs from earlier stages."})
			continue
		}
		preflight := preflightPlan(ctx, plugin, resolved.Plan, func(ctx context.Context, step domain.PlanStep) (domain.PlanStep, error) {
			return s.resolvePreflightArtifactInputs(ctx, workspaceID, step)
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
		return workflows.Result{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "preflight_failed", "validation": result.Validation}}
	}
	return result, nil
}

func (s *Server) validatedManagedOperation(ctx context.Context, workspaceID string, plugin plugins.Plugin, title string, spec json.RawMessage) (domain.Operation, *managedOperationFailure) {
	if _, err := s.store.GetWorkspace(ctx, workspaceID); errors.Is(err, storage.ErrNotFound) {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_workspace", message: "Select an existing workspace."}
	} else if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not validate workspace."}
	}
	manifest := plugin.Manifest()
	issues, err := schema.Validate(manifest.Schema, spec)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "invalid_schema", message: err.Error()}
	}
	if len(issues) > 0 {
		converted := make([]domain.ValidationIssue, 0, len(issues))
		for _, issue := range issues {
			converted = append(converted, domain.ValidationIssue{Level: "error", Path: issue.Path, Message: issue.Message})
		}
		validation := domain.ValidationReport{Valid: false, Issues: converted, CheckedAt: time.Now().UTC()}
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": validation}}
	}
	validation := plugin.Validate(ctx, spec)
	if !validation.Valid {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "validation_failed", "validation": validation}}
	}
	plan, err := plugin.Plan(ctx, spec)
	if err != nil || len(plan.Steps) == 0 {
		message := "The plugin produced an empty plan."
		if err != nil {
			message = err.Error()
		}
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "planning_failed", message: message}
	}
	if err := validatePlanEffects(manifest, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plan_effects", message: err.Error()}
	}
	if err := artifacts.ValidatePlan(plan, manifest.ArtifactInputs, manifest.ArtifactOutputs); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_graph", message: err.Error()}
	}
	if err := s.validateExternalArtifactInputs(ctx, workspaceID, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_reference", message: err.Error()}
	}
	if err := s.validateResourceEffects(ctx, workspaceID, manifest, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusConflict, code: "resource_conflict", message: err.Error()}
	}
	preflight := preflightPlan(ctx, plugin, plan, func(ctx context.Context, step domain.PlanStep) (domain.PlanStep, error) {
		return s.resolvePreflightArtifactInputs(ctx, workspaceID, step)
	})
	validation.Issues = append(validation.Issues, preflight...)
	for _, issue := range preflight {
		if issue.Level == "error" {
			validation.Valid = false
		}
	}
	validation.CheckedAt = time.Now().UTC()
	if !validation.Valid {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "preflight_failed", "validation": validation}}
	}
	planHash, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "planning_failed", message: "Could not fingerprint the validated plan."}
	}
	return domain.Operation{ID: id.New("op"), WorkspaceID: workspaceID, PluginID: manifest.ID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Title: title, Status: domain.OperationReady, Spec: spec, Plan: plan, Validation: validation, PlanHash: planHash}, nil
}

func (s *Server) validatedManagedCleanup(ctx context.Context, source domain.Operation) (domain.Operation, *managedOperationFailure) {
	plugin, err := s.registry.Get(source.PluginID)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plugin", message: err.Error()}
	}
	manifest := plugin.Manifest()
	if !managedCleanupCompatible(manifest, source) {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusConflict, code: "cleanup_unavailable", message: "A completed source lifecycle and a cleanup-compatible plugin are required."}
	}
	plan, err := cleanupPlan(source)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusConflict, code: "cleanup_unavailable", message: err.Error()}
	}
	if err := validatePlanEffects(manifest, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_plan_effects", message: err.Error()}
	}
	if err := artifacts.ValidatePlan(plan, manifest.ArtifactInputs, manifest.ArtifactOutputs); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_graph", message: err.Error()}
	}
	if err := s.validateExternalArtifactInputs(ctx, source.WorkspaceID, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: "invalid_artifact_reference", message: err.Error()}
	}
	if err := s.validateResourceEffects(ctx, source.WorkspaceID, manifest, plan); err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusConflict, code: "resource_conflict", message: err.Error()}
	}
	validation := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC(), Issues: []domain.ValidationIssue{{Level: "info", Message: "Deletion is restricted to the resource created by the original validated lifecycle."}}}
	preflight := preflightPlan(ctx, plugin, plan, func(ctx context.Context, step domain.PlanStep) (domain.PlanStep, error) {
		return s.resolvePreflightArtifactInputs(ctx, source.WorkspaceID, step)
	})
	validation.Issues = append(validation.Issues, preflight...)
	for _, issue := range preflight {
		if issue.Level == "error" {
			validation.Valid = false
		}
	}
	if !validation.Valid {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, payload: map[string]any{"code": "preflight_failed", "validation": validation}}
	}
	spec, _ := json.Marshal(map[string]string{"sourceOperationId": source.ID, "sourcePlanHash": source.PlanHash})
	planHash, err := resolvedPlanHash(manifest, spec, plan)
	if err != nil {
		return domain.Operation{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "planning_failed", message: "Could not fingerprint the cleanup plan."}
	}
	return domain.Operation{ID: id.New("op"), WorkspaceID: source.WorkspaceID, PluginID: source.PluginID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest, Title: "Delete " + source.Title, Status: domain.OperationReady, Spec: spec, Plan: plan, Validation: validation, PlanHash: planHash}, nil
}

func managedCleanupCompatible(manifest plugins.Manifest, source domain.Operation) bool {
	return terminal(source.Status) && manifest.HasCapability("lifecycle.cleanup") && (manifest.Matches(source.PluginVersion, source.PluginDigest) || manifest.HasCapability("lifecycle.cleanup.compatible"))
}

// Store explicit placement even for older clients that omit it.
func managedClusterMachineNodes(input *managedKubernetesClusterInput, defaultNode string) ([]string, error) {
	names := managedClusterMachineNames(*input)
	known := make(map[string]bool, len(names))
	for _, name := range names {
		known[name] = true
	}
	for name := range input.MachineNodes {
		if !known[name] {
			return nil, fmt.Errorf("Unknown machine %s in Proxmox server assignments.", name)
		}
	}
	if input.MachineNodes == nil {
		input.MachineNodes = map[string]string{}
	}
	nodes := make([]string, len(names))
	for index, name := range names {
		node, supplied := input.MachineNodes[name]
		if !supplied {
			node = defaultNode
		}
		node = strings.TrimSpace(node)
		if !regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`).MatchString(node) {
			return nil, fmt.Errorf("Select a valid Proxmox server for %s.", name)
		}
		input.MachineNodes[name] = node
		nodes[index] = node
	}
	return nodes, nil
}
