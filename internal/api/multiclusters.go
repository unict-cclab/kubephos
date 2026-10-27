package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/provisioning"
)

func validateMultiClusterMembers(input managedKubernetesClusterInput) error {
	if !shortResourceNamePattern.MatchString(input.Name) || strings.HasSuffix(input.Name, "-") {
		return fmt.Errorf("Use a valid environment name.")
	}
	if len(input.Members) < 2 || len(input.Members) > 6 {
		return fmt.Errorf("A multi cluster environment requires two to six clusters.")
	}
	names, ids := map[string]bool{}, map[int]bool{}
	networks := []netip.Prefix{}
	for _, member := range input.Members {
		if !shortResourceNamePattern.MatchString(member.Name) || strings.HasSuffix(member.Name, "-") || names[member.Name] || member.Name == "default" {
			return fmt.Errorf("Each cluster requires a unique name that starts and ends with a letter or number.")
		}
		names[member.Name] = true
		if member.ClusterID < 1 || member.ClusterID > 255 || ids[member.ClusterID] {
			return fmt.Errorf("Each cluster requires a unique Cilium ID between 1 and 255.")
		}
		ids[member.ClusterID] = true
		if len(member.Members) > 0 {
			return fmt.Errorf("Clusters cannot contain nested environments.")
		}
		for _, value := range []string{member.PodCIDR, member.ServiceCIDR} {
			prefix, err := netip.ParsePrefix(value)
			if err != nil || !prefix.Addr().Is4() || prefix != prefix.Masked() || prefix.Bits() < 16 || prefix.Bits() > 24 {
				return fmt.Errorf("Each cluster requires aligned IPv4 pod and service CIDRs with a prefix between 16 and 24.")
			}
			for _, previous := range networks {
				if previous.Overlaps(prefix) {
					return fmt.Errorf("Cluster pod and service networks must not overlap: %s and %s.", prefix, previous)
				}
			}
			networks = append(networks, prefix)
		}
	}
	return nil
}
func (s *Server) createMultiCluster(response http.ResponseWriter, request *http.Request, input managedKubernetesClusterInput) {
	if err := validateMultiClusterMembers(input); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_multicluster", err.Error())
		return
	}
	meshPlugin, err := s.pluginForCapability("", "cluster.mesh.connect")
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "capability_unavailable", err.Error())
		return
	}
	preparedMembers := []*preparedManagedCluster{}
	definition := domain.PipelineDefinition{}
	meshMembers := []map[string]any{}
	bindings := []domain.PipelineBinding{}
	for index, member := range input.Members {
		member.WorkspaceID, member.ConnectionID = input.WorkspaceID, input.ConnectionID
		member.TemplateID, member.HarborID, member.NFSID = input.TemplateID, input.HarborID, input.NFSID
		member.ScrapeInterval, member.Retention = input.ScrapeInterval, input.Retention
		member.Mode, member.Networking = "single", "cilium"
		member.ControlPlaneZones = []string{member.Name}
		member.ManagementPool.Zones = []string{member.Name}
		for i := range member.ApplicationPools {
			member.ApplicationPools[i].Zones = []string{member.Name}
		}
		prepared := s.prepareManagedCluster(response, request, member)
		if prepared == nil {
			return
		}
		preparedMembers = append(preparedMembers, prepared)
		prefix := fmt.Sprintf("c%d-", index+1)
		for _, stage := range prepared.Definition.Stages {
			stage.ID = prefix + stage.ID
			stage.Title = member.Name + " · " + stage.Title
			for i := range stage.Bindings {
				stage.Bindings[i].FromStage = prefix + stage.Bindings[i].FromStage
			}
			definition.Stages = append(definition.Stages, stage)
		}
		meshMembers = append(meshMembers, map[string]any{"name": member.Name, "clusterId": member.ClusterID, "podCIDR": member.PodCIDR, "connectionRef": "", "observabilityRef": "", "platformRef": "", "inventoryRef": ""})
		for _, binding := range []struct{ field, stage, output string }{{"connectionRef", "kubernetes", "cluster-connection"}, {"observabilityRef", "observability", "observability-capability"}, {"platformRef", "platform", "managed-platform-capability"}, {"inventoryRef", "kubernetes", "cluster-inventory"}} {
			bindings = append(bindings, domain.PipelineBinding{Path: fmt.Sprintf("/members/%d/%s", index, binding.field), FromStage: prefix + binding.stage, FromOutput: binding.output})
		}
	}
	release, err := s.store.LockProvisioning(request.Context(), input.ConnectionID)
	if err != nil {
		writeError(response, http.StatusConflict, "allocation_busy", "Another provisioning request is being validated. Please retry.")
		return
	}
	defer release()
	inventory, _, err := s.provisioningInventory(request.Context(), input.ConnectionID, "", true)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "inventory_unavailable", err.Error())
		return
	}
	for _, prepared := range preparedMembers {
		allocation := provisioning.Allocation{VMID: prepared.Input.BaseVMID, Network: provisioning.Network{Address: prepared.Input.AddressStart, PrefixLength: prepared.Input.PrefixLength, Gateway: prepared.Input.Gateway, DNSServer: prepared.Input.DNSServer}}
		if err := inventory.Validate(allocation, prepared.MachineCount); err != nil {
			writeError(response, http.StatusUnprocessableEntity, "allocation_conflict", prepared.Input.Name+": "+err.Error())
			return
		}
		ip, _ := netip.ParseAddr(allocation.Address)
		prefix := netip.PrefixFrom(ip, allocation.PrefixLength)
		for _, member := range input.Members {
			for _, cidr := range []string{member.PodCIDR, member.ServiceCIDR} {
				network, _ := netip.ParsePrefix(cidr)
				if network.Overlaps(prefix) {
					writeError(response, http.StatusUnprocessableEntity, "network_conflict", "VM, pod and service networks must not overlap.")
					return
				}
			}
		}
		inventory.Reserve(allocation.VMID, prepared.MachineCount, allocation.Network)
	}
	definition.Stages = append(definition.Stages, domain.PipelineStage{ID: "mesh", PluginID: meshPlugin.Manifest().ID, Title: "Connect and verify Cilium ClusterMesh", Spec: marshalRaw(map[string]any{"name": input.Name, "members": meshMembers}), Bindings: bindings})
	definition.Result = domain.PipelineOutput{Stage: "mesh", Output: "cluster-environment"}
	input.Networking = "cilium"
	input.ManagedProfile = "2026.10.multicluster"
	input.Members = nil
	for _, prepared := range preparedMembers {
		input.Members = append(input.Members, prepared.Input)
	}
	prepared := *preparedMembers[0]
	prepared.Input = input
	prepared.Definition = definition
	prepared.Manifest = meshPlugin.Manifest()
	s.persistManagedCluster(response, request, &prepared)
}

func multiClusterSpec(raw json.RawMessage) (managedKubernetesClusterInput, bool) {
	var value managedKubernetesClusterInput
	err := json.Unmarshal(raw, &value)
	return value, err == nil && value.Mode == "multi"
}
