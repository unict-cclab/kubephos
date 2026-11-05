package api

import (
	"fmt"
	"kubephos.dev/kubephos/internal/placement"
	"reflect"
	"testing"
)

func TestMaintenancePreservesProtectedNodesAndRetainedIdentity(t *testing.T) {
	c := managedMachineCapacity{2, 4096, 80}
	saved := managedKubernetesClusterInput{Name: "k3s", ControlPlanes: 1, ManagementPool: managedNodePoolInput{Name: "management", Count: 1, Zones: []string{"zone-a"}, Capacity: c}, ApplicationPools: []managedNodePoolInput{{Name: "edge", Count: 2, Zones: []string{"zone-a"}, Capacity: c}}, AddressStart: "10.0.0.10", PrefixLength: 24, Gateway: "10.0.0.1"}
	old := []maintenanceMachine{}
	for i, n := range managedClusterMachineNames(saved) {
		old = append(old, maintenanceMachine{100 + i, n, "pve", "10.0.0." + []string{"10", "11", "12", "13"}[i], 2, 4096, 80})
	}
	pools := append([]managedNodePoolInput{}, saved.ApplicationPools...)
	pools[0].Count = 3
	nodes := map[string]string{}
	for _, m := range old {
		nodes[m.Name] = "pve-2"
	}
	input := clusterMaintenanceInput{ApplicationPools: pools, MachineNodes: nodes, NewVMIDStart: 200, NewAddressStart: "10.0.0.20"}
	desired, machines, err := maintenanceMachines(saved, input, old, "pve")
	if err != nil {
		t.Fatal(err)
	}
	if desired.ControlPlanes != saved.ControlPlanes || !reflect.DeepEqual(desired.ManagementPool, saved.ManagementPool) {
		t.Fatal("protected pools changed")
	}
	for i, m := range old {
		got := machines[i]
		got.Node = m.Node
		if got != m {
			t.Fatalf("retained VM changed: %+v", machines[i])
		}
	}
	if machines[4].VMID != 200 || machines[4].Address != "10.0.0.20" {
		t.Fatalf("new allocation: %+v", machines[4])
	}
	// Zero application pools is valid; only protected nodes remain.
	_, machines, err = maintenanceMachines(saved, clusterMaintenanceInput{}, old, "pve")
	if err != nil || len(machines) != 2 {
		t.Fatalf("remove application pools: %v %+v", err, machines)
	}
	for _, bad := range []clusterMaintenanceInput{
		{ApplicationPools: []managedNodePoolInput{{Name: "management", Count: 1, Zones: []string{"zone-a"}, Capacity: c}}},
		{ApplicationPools: pools, NewVMIDStart: 100, NewAddressStart: "10.0.0.20"},
		{ApplicationPools: pools, NewVMIDStart: 200, NewAddressStart: "10.0.1.20"},
		{ApplicationPools: pools, NewVMIDStart: 200, NewAddressStart: "10.0.0.13"},
		{ApplicationPools: pools, NewVMIDStart: 200, NewAddressStart: "10.0.0.255"},
	} {
		if _, _, err := maintenanceMachines(saved, bad, old, "pve"); err == nil {
			t.Fatalf("accepted invalid maintenance: %+v", bad)
		}
	}
	if _, _, err := maintenanceMachines(saved, input, old[1:], "pve"); err == nil {
		t.Fatal("replaced missing protected node")
	}
}

func TestMaintenanceCanChangePoolSchedulingWithoutChangingMachines(t *testing.T) {
	c := managedMachineCapacity{2, 4096, 80}
	saved := managedKubernetesClusterInput{Name: "demo", ControlPlanes: 1, ManagementPool: managedNodePoolInput{Name: "management", Count: 1, Zones: []string{"cloud"}, Capacity: c}, ApplicationPools: []managedNodePoolInput{{Name: "apps", Count: 1, Zones: []string{"edge"}, Capacity: c}}, AddressStart: "10.0.0.10", PrefixLength: 24, Gateway: "10.0.0.1"}
	previous := []maintenanceMachine{}
	for i, name := range managedClusterMachineNames(saved) {
		previous = append(previous, maintenanceMachine{VMID: 100 + i, Name: name, Node: "pve", Address: fmt.Sprintf("10.0.0.%d", 10+i), Cores: 2, MemoryMiB: 4096, DiskGiB: 80})
	}
	pools := append([]managedNodePoolInput{}, saved.ApplicationPools...)
	pools[0].Labels = map[string]string{"hardware": "gpu"}
	pools[0].Taints = []placement.Taint{{Key: "gpu", Effect: "NoSchedule"}}
	desired, machines, err := maintenanceMachines(saved, clusterMaintenanceInput{ApplicationPools: pools}, previous, "pve")
	if err != nil || !reflect.DeepEqual(machines, previous) || !reflect.DeepEqual(desired.ManagementPool, saved.ManagementPool) || len(desired.ApplicationPools[0].Taints) != 1 {
		t.Fatal("pool settings changed topology", err, desired, machines)
	}
	pools[0].Labels = map[string]string{"kubephos.dev/role": "management"}
	if _, _, err = maintenanceMachines(saved, clusterMaintenanceInput{ApplicationPools: pools}, previous, "pve"); err == nil {
		t.Fatal("accepted a managed label override")
	}
}
