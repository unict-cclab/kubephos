package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"strconv"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
)

type clusterMaintenanceInput struct {
	ApplicationPools []managedNodePoolInput `json:"applicationPools"`
	MachineNodes     map[string]string      `json:"machineNodes"`
	NewVMIDStart     int                    `json:"newVMIDStart"`
	NewAddressStart  string                 `json:"newAddressStart"`
}
type maintenanceMachine struct {
	VMID      int    `json:"vmid"`
	Name      string `json:"name"`
	Node      string `json:"node"`
	Address   string `json:"address"`
	Cores     int    `json:"cores"`
	MemoryMiB int    `json:"memoryMiB"`
	DiskGiB   int    `json:"diskGiB"`
}

// Preserve the identity and capacity of retained nodes. Only new application
// nodes receive an address and VMID from the explicitly selected allocation.
func maintenanceMachines(saved managedKubernetesClusterInput, input clusterMaintenanceInput, previous []maintenanceMachine, defaultNode string) (managedKubernetesClusterInput, []maintenanceMachine, error) {
	desired := saved
	desired.ApplicationPools = input.ApplicationPools
	desired.MachineNodes = input.MachineNodes
	seen := map[string]bool{"control-plane": true, saved.ManagementPool.Name: true}
	oldPools := map[string]managedNodePoolInput{}
	for _, p := range saved.ApplicationPools {
		oldPools[p.Name] = p
	}
	total := saved.ControlPlanes + saved.ManagementPool.Count
	for i := range desired.ApplicationPools {
		p := &desired.ApplicationPools[i]
		if !validManagedPool(*p, shortResourceNamePattern) || seen[p.Name] {
			return desired, nil, errors.New("Application pools require unique names, a positive node count and valid zones.")
		}
		if old, ok := oldPools[p.Name]; ok {
			if old.Capacity != p.Capacity || !reflect.DeepEqual(old.Zones, p.Zones) {
				return desired, nil, errors.New("Existing pool capacity and zones must remain unchanged during node maintenance.")
			}
		} else if !validManagedCapacity(p.Capacity, 8) {
			return desired, nil, errors.New("Select valid capacity for each new application pool.")
		}
		seen[p.Name] = true
		total += p.Count
	}
	if total > 12 {
		return desired, nil, errors.New("A cluster can contain at most 12 machines.")
	}
	nodes, err := managedClusterMachineNodes(&desired, defaultNode)
	if err != nil {
		return desired, nil, err
	}
	byName := map[string]maintenanceMachine{}
	usedIDs := map[int]bool{}
	usedIPs := map[string]bool{}
	for _, m := range previous {
		byName[m.Name] = m
		usedIDs[m.VMID] = true
		usedIPs[m.Address] = true
	}
	names := managedClusterMachineNames(desired)
	profiles := appendManagedMachineProfiles(nil, desired.ControlPlanes, desired.ControlPlaneCapacity)
	profiles = appendManagedMachineProfiles(profiles, desired.ManagementPool.Count, desired.ManagementPool.Capacity)
	for _, p := range desired.ApplicationPools {
		profiles = appendManagedMachineProfiles(profiles, p.Count, p.Capacity)
	}
	nextID := input.NewVMIDStart
	nextIP, _ := netip.ParseAddr(input.NewAddressStart)
	start, _ := netip.ParseAddr(saved.AddressStart)
	subnet := netip.PrefixFrom(start, saved.PrefixLength).Masked()
	result := make([]maintenanceMachine, 0, total)
	for i, name := range names {
		m, exists := byName[name]
		if !exists {
			if i < saved.ControlPlanes+saved.ManagementPool.Count {
				return desired, nil, errors.New("Control plane and management nodes cannot be replaced.")
			}
			if nextID < 100 || nextID > 999999999 || usedIDs[nextID] || !nextIP.Is4() || !subnet.Contains(nextIP) || usedIPs[nextIP.String()] || nextIP.String() == saved.Gateway || nextIP == subnet.Addr() || !subnet.Contains(nextIP.Next()) {
				return desired, nil, errors.New("New VMIDs and IPv4 addresses must be unused and within the cluster subnet.")
			}
			c := profiles[i]
			m = maintenanceMachine{VMID: nextID, Name: name, Address: nextIP.String(), Cores: c.Cores, MemoryMiB: c.MemoryMiB, DiskGiB: c.DiskGiB}
			usedIDs[nextID] = true
			usedIPs[nextIP.String()] = true
			nextID++
			nextIP = nextIP.Next()
		}
		m.Node = nodes[i]
		result = append(result, m)
	}
	if reflect.DeepEqual(saved.ApplicationPools, desired.ApplicationPools) && reflect.DeepEqual(previous, result) {
		return desired, nil, errors.New("No topology or placement changes were selected.")
	}
	return desired, result, nil
}

func (s *Server) reconfigureKubernetesCluster(w http.ResponseWriter, r *http.Request) {
	resource, err := s.store.GetManagedResource(r.Context(), r.PathValue("id"))
	if err != nil || resource.Kind != "kubernetes-cluster" {
		writeError(w, 404, "not_found", "Kubernetes cluster not found.")
		return
	}
	if resource.Status != "ready" {
		writeError(w, 409, "cluster_not_ready", "Only a ready cluster can be maintained.")
		return
	}
	var input clusterMaintenanceInput
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	var saved managedKubernetesClusterInput
	if json.Unmarshal(resource.Spec, &saved) != nil {
		writeError(w, 409, "configuration_invalid", "Saved cluster configuration is invalid.")
		return
	}
	if saved.Mode == "multi" {
		writeError(w, 409, "multi_cluster_maintenance_unavailable", "Node maintenance is not available for multi cluster environments.")
		return
	}

	source, err := s.store.GetPipeline(r.Context(), resource.PipelineID)
	if err != nil {
		writeError(w, 409, "workflow_unavailable", "Cluster workflow is unavailable.")
		return
	}
	definition := clusterProvisioningStages(source.Definition)
	var machineStage *domain.PipelineStage
	for i := range definition.Stages {
		if definition.Stages[i].ID == "machines" {
			machineStage = &definition.Stages[i]
		}
	}
	if machineStage == nil {
		writeError(w, 409, "workflow_unavailable", "Machine workflow is unavailable.")
		return
	}
	var topology map[string]any
	if json.Unmarshal(machineStage.Spec, &topology) != nil {
		writeError(w, 409, "workflow_unavailable", "Machine configuration is invalid.")
		return
	}
	machineArtifact, err := s.store.GetPipelineRunArtifact(r.Context(), resource.PipelineRunID, "machine-set")
	if err != nil {
		writeError(w, 409, "topology_unavailable", "Verified machines are unavailable.")
		return
	}
	raw, err := s.readArtifactPayload(r.Context(), machineArtifact)
	if err != nil {
		writeError(w, 409, "topology_unavailable", "Verified machines could not be read.")
		return
	}
	var set struct {
		Spec struct {
			Machines []struct {
				ID        string `json:"id"`
				Name      string `json:"name"`
				Node      string `json:"node"`
				Address   string `json:"address"`
				Cores     int    `json:"cores"`
				MemoryMiB int    `json:"memoryMiB"`
				DiskGiB   int    `json:"diskGiB"`
			} `json:"machines"`
		} `json:"spec"`
	}
	if json.Unmarshal(raw, &set) != nil || len(set.Spec.Machines) == 0 {
		writeError(w, 409, "topology_unavailable", "Verified machines are invalid.")
		return
	}
	previous := []maintenanceMachine{}
	for _, m := range set.Spec.Machines {
		vmid, e := strconv.Atoi(strings.TrimPrefix(m.ID, "qemu/"))
		if e != nil {
			writeError(w, 409, "topology_unavailable", "Verified VM identity is invalid.")
			return
		}
		previous = append(previous, maintenanceMachine{vmid, m.Name, m.Node, m.Address, m.Cores, m.MemoryMiB, m.DiskGiB})
	}
	node, _ := topology["node"].(string)
	desired, machines, err := maintenanceMachines(saved, input, previous, node)
	if err != nil {
		writeError(w, 422, "invalid_maintenance", err.Error())
		return
	}
	access, err := s.store.GetPipelineRunArtifact(r.Context(), resource.PipelineRunID, "machine-access")
	if err != nil {
		writeError(w, 409, "access_unavailable", "Verified machine access is unavailable.")
		return
	}
	run, err := s.store.GetPipelineRun(r.Context(), resource.PipelineRunID)
	if err != nil {
		writeError(w, 409, "workflow_unavailable", "Cluster lifecycle is unavailable.")
		return
	}
	marker := ""
	for _, stage := range run.Stages {
		if stage.StageID == "machines" {
			op, e := s.store.GetOperation(r.Context(), stage.OperationID)
			if e == nil && len(op.Plan.Steps) > 0 {
				var data struct {
					Marker string `json:"marker"`
				}
				_ = json.Unmarshal(op.Plan.Steps[0].Input, &data)
				marker = data.Marker
			}
		}
	}
	if len(marker) != 16 {
		writeError(w, 409, "ownership_unavailable", "The VM ownership marker is unavailable.")
		return
	}
	topology["reconcile"] = true
	topology["prune"] = false
	topology["previousMachineSetRef"] = machineArtifact.ID
	topology["machineAccessRef"] = access.ID
	topology["ownershipMarker"] = marker
	topology["plannedMachines"] = machines
	topology["previousMachines"] = previous
	topology["machineCount"] = len(machines)
	delete(topology, "machineNames")
	delete(topology, "machineNodes")
	delete(topology, "machineProfiles")
	machineStage.Spec, _ = json.Marshal(topology)
	machineStage.Title = "Reconcile VM placement and capacity"
	pools := []map[string]any{{"name": desired.ManagementPool.Name, "role": "management", "count": desired.ManagementPool.Count, "zones": desired.ManagementPool.Zones}}
	for _, p := range desired.ApplicationPools {
		pools = append(pools, map[string]any{"name": p.Name, "role": "application", "count": p.Count, "zones": p.Zones})
	}
	for i := range definition.Stages {
		if definition.Stages[i].ID == "kubernetes" {
			stage := &definition.Stages[i]
			var spec map[string]any
			if err := json.Unmarshal(stage.Spec, &spec); err != nil || spec == nil {
				writeError(w, 409, "workflow_unavailable", "Kubernetes configuration is invalid.")
				return
			}
			spec["reconcile"] = true
			spec["previousMachineSetRef"] = machineArtifact.ID
			spec["nodePools"] = pools
			stage.Spec, _ = json.Marshal(spec)
			stage.Title = "Reconcile Kubernetes nodes"
		}
	}
	prune := domain.PipelineStage{ID: "prune-machines", PluginID: machineStage.PluginID, Title: "Remove drained virtual machines", Bindings: []domain.PipelineBinding{{Path: "/readyClusterRef", FromStage: "kubernetes", FromOutput: "cluster-connection"}}}
	topology["prune"] = true
	topology["readyClusterRef"] = ""
	prune.Spec, _ = json.Marshal(topology)
	stages := []domain.PipelineStage{}
	for _, stage := range definition.Stages {
		stages = append(stages, stage)
		if stage.ID == "kubernetes" {
			stages = append(stages, prune)
		}
	}
	definition.Stages = stages
	validated, failure := s.validatedManagedPipeline(r.Context(), resource.WorkspaceID, definition)
	if failure != nil {
		failure.write(w)
		return
	}
	pipeline := domain.Pipeline{ID: id.New("pipe"), WorkspaceID: resource.WorkspaceID, Name: fmt.Sprintf("maintain-%s-%s", resource.ID, id.New("change")), Definition: validated.Definition, Resolution: validated.Resolution, Validation: validated.Validation, Hash: validated.Hash}
	lifecycle := pipelineRunForManagedPipeline(pipeline, "Maintain "+resource.Name)
	spec, _ := json.Marshal(desired)
	if err := s.store.CreateManagedResourceRecreation(r.Context(), resource.ID, resource.PipelineRunID, spec, pipeline, lifecycle); err != nil {
		if errors.Is(err, storage.ErrConflict) {
			writeError(w, 409, "cluster_in_use", "Maintenance requires no queued or running experiments or other lifecycle operations.")
		} else {
			writeError(w, 500, "database_error", "Could not queue cluster maintenance.")
		}
		return
	}
	updated, err := s.store.GetManagedResource(r.Context(), resource.ID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not read cluster maintenance.")
		return
	}
	writeJSON(w, 202, updated)
}
