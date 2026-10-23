package proxmoxvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type ReconcileOptions struct {
	Reconcile             bool             `json:"reconcile,omitempty"`
	Prune                 bool             `json:"prune,omitempty"`
	PreviousMachineSetRef string           `json:"previousMachineSetRef,omitempty"`
	MachineAccessRef      string           `json:"machineAccessRef,omitempty"`
	ReadyClusterRef       string           `json:"readyClusterRef,omitempty"`
	OwnershipMarker       string           `json:"ownershipMarker,omitempty"`
	PlannedMachines       []plannedMachine `json:"plannedMachines,omitempty"`
	PreviousMachines      []plannedMachine `json:"previousMachines,omitempty"`
}

func validateReconcileSpec(spec TopologySpec) error {
	if issue := validateTopologyValues(spec); issue != nil {
		return errors.New(issue.Message)
	}
	if !strings.HasPrefix(spec.PreviousMachineSetRef, "art_") || !strings.HasPrefix(spec.MachineAccessRef, "art_") || len(spec.OwnershipMarker) != 16 || len(spec.PlannedMachines) != spec.MachineCount || len(spec.PreviousMachines) == 0 {
		return errors.New("verified previous topology, access, marker and desired machines are required")
	}
	names, ids, addresses := map[string]bool{}, map[int]bool{}, map[string]bool{}
	for _, m := range spec.PlannedMachines {
		if m.VMID < 100 || m.VMID > 999999999 || m.VMID == spec.TemplateVMID || m.Name == "" || !regexpNode.MatchString(m.Node) || net.ParseIP(m.Address) == nil || names[m.Name] || ids[m.VMID] || addresses[m.Address] || !validMachineCapacity(machineCapacity{m.Cores, m.MemoryMiB, m.DiskGiB}) {
			return errors.New("desired machines contain invalid identities, addresses, servers or capacities")
		}
		names[m.Name] = true
		ids[m.VMID] = true
		addresses[m.Address] = true
	}
	return nil
}

func planTopologyReconcile(spec TopologySpec) (domain.Plan, error) {
	if err := validateReconcileSpec(spec); err != nil {
		return domain.Plan{}, err
	}
	input := topologyStepInput{ReconcileOptions: spec.ReconcileOptions, Action: "reconcile", ConnectionRef: spec.ConnectionRef, MachineTemplateRef: spec.MachineTemplateRef, Node: spec.Node, TemplateVMID: spec.TemplateVMID, Cores: spec.Cores, MemoryMiB: spec.MemoryMiB, DiskGiB: spec.DiskGiB, SSHUser: spec.SSHUser, PrefixLength: spec.PrefixLength, Gateway: spec.Gateway, DNSServer: spec.DNSServer, Marker: spec.OwnershipMarker, Machines: spec.PlannedMachines}
	inputs := []domain.ArtifactInput{{Name: "previous-machines", Type: "MachineSet", Version: "v1alpha1", ArtifactID: spec.PreviousMachineSetRef}, {Name: "previous-access", Type: "MachineAccess", Version: "v1alpha1", ArtifactID: spec.MachineAccessRef}}
	if spec.MachineTemplateRef != "" {
		inputs = append(inputs, domain.ArtifactInput{Name: "machine-template", Type: "MachineTemplate", Version: "v1alpha1", ArtifactID: spec.MachineTemplateRef})
	}
	previous := map[int]bool{}
	desired := map[int]bool{}
	for _, m := range spec.PreviousMachines {
		previous[m.VMID] = true
	}
	for _, m := range input.Machines {
		desired[m.VMID] = true
	}
	effects := []domain.ResourceEffect{}
	outputs := []domain.ArtifactOutput{{Name: "machine-set", Type: "MachineSet", Version: "v1alpha1", MediaType: "application/json", Source: "/machineSet"}, {Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", MediaType: "application/json", Source: "/machineAccess", Sensitive: true}}
	if spec.Prune {
		input.Action = "prune"
		input.Machines = nil
		outputs = nil
		for _, m := range spec.PreviousMachines {
			if !desired[m.VMID] {
				input.Machines = append(input.Machines, m)
				effects = append(effects, domain.ResourceEffect{Action: "delete", ExternalID: externalID(m.VMID), Kind: "virtual-machine", Name: m.Name})
			}
		}
		inputs = append(inputs, domain.ArtifactInput{Name: "ready-cluster", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ReadyClusterRef})
	} else {
		for _, m := range input.Machines {
			action := "create"
			if previous[m.VMID] {
				action = "update"
			}
			effects = append(effects, domain.ResourceEffect{Action: action, ExternalID: externalID(m.VMID), Kind: "virtual-machine", Name: m.Name})
		}
	}
	if !spec.Prune {
		for _, m := range spec.PreviousMachines {
			if !desired[m.VMID] {
				effects = append(effects, domain.ResourceEffect{Action: "update", ExternalID: externalID(m.VMID), Kind: "virtual-machine", Name: m.Name})
			}
		}
	}
	raw, _ := json.Marshal(input)
	return domain.Plan{PluginID: topologyPluginID, Steps: []domain.PlanStep{{ID: input.Action + "-topology", Name: "Reconcile managed VM topology", Input: raw, Effects: effects, ArtifactInputs: inputs, Outputs: outputs}}}, nil
}

func previousTopology(step domain.PlanStep, input topologyStepInput) (machineAccess, error) {
	artifact, ok := step.ResolvedInputs["previous-machines"]
	if !ok || artifact.ID != input.PreviousMachineSetRef || artifact.Type != "MachineSet" {
		return machineAccess{}, errors.New("verified previous machines are unavailable")
	}
	var previous machineSet
	if err := json.Unmarshal(artifact.Value, &previous); err != nil {
		return machineAccess{}, err
	}
	if previous.Spec.ConnectionRef != input.ConnectionRef || len(previous.Spec.Machines) != len(input.PreviousMachines) {
		return machineAccess{}, errors.New("previous topology does not match the connection or machine count")
	}
	for _, expected := range input.PreviousMachines {
		found := false
		for _, actual := range previous.Spec.Machines {
			if actual.ID == externalID(expected.VMID) && actual.Name == expected.Name && actual.Address == expected.Address && actual.Node == expected.Node {
				found = true
			}
		}
		if !found {
			return machineAccess{}, errors.New("previous VM identity differs from the verified artifact")
		}
	}
	accessArtifact, ok := step.ResolvedInputs["previous-access"]
	if !ok || accessArtifact.ID != input.MachineAccessRef || !accessArtifact.Sensitive {
		return machineAccess{}, errors.New("verified machine access is unavailable")
	}
	var access machineAccess
	err := json.Unmarshal(accessArtifact.Value, &access)
	return access, err
}

func checkReconcile(ctx context.Context, connection *client, input topologyStepInput, step domain.PlanStep) error {
	if _, err := previousTopology(step, input); err != nil {
		return err
	}
	nodes, err := connection.nodes(ctx)
	if err != nil {
		return err
	}
	resources, err := connection.resources(ctx)
	if err != nil {
		return err
	}
	permissions, err := connection.permissions(ctx)
	if err != nil {
		return err
	}
	migrating := false
	for _, m := range input.Machines {
		if current, exists := findVM(resources, m.VMID); exists && current.Node != m.Node {
			migrating = true
		}
	}
	if migrating && !hasPrivilege(permissions, "VM.Migrate") {
		return errors.New("Token is missing required privilege VM.Migrate.")
	}
	for _, privilege := range []string{"Datastore.AllocateSpace", "VM.Allocate", "VM.Clone", "VM.Config.CPU", "VM.Config.Disk", "VM.Config.Memory", "VM.Config.Options", "VM.GuestAgent.Unrestricted", "VM.PowerMgmt"} {
		if !hasPrivilege(permissions, privilege) {
			return fmt.Errorf("Token is missing required privilege %s", privilege)
		}
	}
	for _, m := range input.Machines {
		if _, exists := findVM(resources, m.VMID); !exists && input.Action != "prune" {
			if !nodeIsOnline(nodes, input.Node) {
				return errors.New("template Proxmox server is offline")
			}
			config, err := connection.config(ctx, input.Node, input.TemplateVMID)
			if err != nil {
				return err
			}
			disk, err := topologyRootDisk(config)
			if err != nil {
				return err
			}
			size, err := topologyDiskSizeGiB(diskConfiguration(config, disk))
			if err != nil || float64(m.DiskGiB) < size {
				return errors.New("new VM disks cannot be smaller than the template disk")
			}
		}
	}
	previous := map[int]plannedMachine{}
	for _, m := range input.PreviousMachines {
		previous[m.VMID] = m
	}
	if err := checkReconcileCleanup(ctx, connection, input); err != nil {
		return err
	}
	for _, m := range input.Machines {
		if _, exists := findVM(resources, m.VMID); !exists {
			if err := validateMachineNames(resources, []string{m.Name}); err != nil {
				return err
			}
		}
	}
	for _, m := range input.Machines {
		if !nodeIsOnline(nodes, m.Node) {
			return fmt.Errorf("destination Proxmox server %s is offline", m.Node)
		}
		current, exists := findVM(resources, m.VMID)
		if !exists {
			if _, old := previous[m.VMID]; old && input.Action != "prune" {
				return fmt.Errorf("existing VM %d disappeared", m.VMID)
			}
			continue
		}
		configuration, err := connection.config(ctx, current.Node, m.VMID)
		if err != nil || !ownedTopology(configuration, input, m) {
			return fmt.Errorf("VM %d ownership could not be verified", m.VMID)
		}
		if current.Node != m.Node && current.Status != "running" {
			return fmt.Errorf("VM %d must be running for live migration", m.VMID)
		}
		if old, exists := previous[m.VMID]; exists && (old.Name != m.Name || old.Address != m.Address || old.Node != current.Node && m.Node != current.Node) {
			return fmt.Errorf("VM %d has moved outside the validated topology", m.VMID)
		}
	}
	if input.Action == "prune" {
		ready, ok := step.ResolvedInputs["ready-cluster"]
		if !ok || ready.ID != input.ReadyClusterRef || ready.Type != "ClusterConnection" {
			return errors.New("a verified reconciled cluster is required before VM removal")
		}
	}
	return nil
}

func (plugin TopologyPlugin) executeReconcile(ctx context.Context, connection *client, input topologyStepInput, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	access, err := previousTopology(step, input)
	if err != nil {
		return nil, err
	}
	if err := checkReconcile(ctx, connection, input, step); err != nil {
		return nil, err
	}
	if input.Action == "prune" {
		if err := cleanupTopology(ctx, connection, input, log); err != nil {
			return nil, err
		}
		return json.RawMessage(`{"resources":[]}`), nil
	}
	for _, m := range input.Machines {
		current, err := connection.vm(ctx, m.VMID)
		if err != nil {
			var response apiError
			if !errors.As(err, &response) || response.Status != 404 {
				return nil, err
			}
			if err := createTopologyMachine(ctx, connection, input, m, access.Spec.PublicKey, log); err != nil {
				return nil, err
			}
			continue
		}
		if current.Node != m.Node {
			if err := log("info", fmt.Sprintf("Live migration of %s from %s to %s", m.Name, current.Node, m.Node)); err != nil {
				return nil, err
			}
			task, err := connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/migrate", url.PathEscape(current.Node), m.VMID), url.Values{"target": {m.Node}, "online": {"1"}, "with-local-disks": {"1"}, "targetstorage": {"1"}})
			if err != nil {
				return nil, err
			}
			if err := connection.waitTask(ctx, current.Node, task); err != nil {
				return nil, err
			}
			migrated, err := connection.vm(ctx, m.VMID)
			if err != nil || migrated.Node != m.Node {
				return nil, fmt.Errorf("VM %d did not reach the destination", m.VMID)
			}
		}
	}
	if err := verifyTopologyGuestAddresses(ctx, connection, input, 3*time.Minute, log); err != nil {
		return nil, err
	}
	desiredCount := len(input.Machines)
	seen := map[int]bool{}
	for _, m := range input.Machines {
		seen[m.VMID] = true
	}
	for _, m := range input.PreviousMachines {
		if !seen[m.VMID] {
			input.Machines = append(input.Machines, m)
		}
	}
	result, err := topologyExecutionResult(ctx, connection, input, access.Spec.PrivateKey, access.Spec.PublicKey, plugin.sshAccess(), log)
	if err != nil {
		return nil, err
	}
	result.MachineSet.Spec.Machines = result.MachineSet.Spec.Machines[:desiredCount]
	return json.Marshal(result)
}

func cleanupReconcileInput(input topologyStepInput) topologyStepInput {
	seen := map[int]bool{}
	for _, m := range input.Machines {
		seen[m.VMID] = true
	}
	for _, m := range input.PreviousMachines {
		if !seen[m.VMID] {
			input.Machines = append(input.Machines, m)
		}
	}
	return input
}
func checkReconcileCleanup(ctx context.Context, connection *client, input topologyStepInput) error {
	input = cleanupReconcileInput(input)
	for _, m := range input.Machines {
		resource, err := connection.vm(ctx, m.VMID)
		if err != nil {
			var response apiError
			if errors.As(err, &response) && response.Status == 404 {
				continue
			}
			return err
		}
		configuration, err := connection.config(ctx, resource.Node, m.VMID)
		if err != nil || !ownedTopology(configuration, input, m) {
			return fmt.Errorf("cleanup ownership of %d could not be verified", m.VMID)
		}
	}
	return nil
}
