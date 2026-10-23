package api

import (
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
