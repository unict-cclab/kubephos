package proxmoxvm

import (
	"context"
	"encoding/json"
	"kubephos.dev/kubephos/internal/domain"
	"testing"
)

func TestLiveMigrationPreservesVMIdentityAndAccess(t *testing.T) {
	server, state := newTopologyServer(t, 0)
	defer server.Close()
	invocation, _ := topologyInvocation(server.URL, false)
	plugin := TopologyPlugin{SSH: fakeSSHAccess{}}
	ctx := context.Background()
	log := func(string, string) error { return nil }
	plan, err := plugin.Plan(ctx, invocation)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := plugin.Execute(ctx, plan.Steps[0], invocation.Secrets, invocation.Connections, log)
	if err != nil {
		t.Fatal(err)
	}
	var result topologyResult
	_ = json.Unmarshal(raw, &result)
	var original topologyStepInput
	_ = json.Unmarshal(plan.Steps[0].Input, &original)
	var spec TopologySpec
	_ = json.Unmarshal(invocation.Input, &spec)
	spec.ReconcileOptions = ReconcileOptions{Reconcile: true, PreviousMachineSetRef: "art_previous", MachineAccessRef: "art_access", OwnershipMarker: original.Marker, PreviousMachines: original.Machines, PlannedMachines: append([]plannedMachine{}, original.Machines...)}
	spec.PlannedMachines[0].Node = "pve-2"
	// Original plans may leave their per-machine node empty; artifact identities are explicit.
	for i := range spec.PreviousMachines {
		spec.PreviousMachines[i].Node = "pve"
	}
	for i := 1; i < len(spec.PlannedMachines); i++ {
		spec.PlannedMachines[i].Node = "pve"
	}
	spec.PlannedMachines[0].Cores = 2
	spec.PlannedMachines[0].MemoryMiB = 4096
	spec.PlannedMachines[0].DiskGiB = 32
	for i := range spec.PlannedMachines {
		spec.PlannedMachines[i].Cores = 2
		spec.PlannedMachines[i].MemoryMiB = 4096
		spec.PlannedMachines[i].DiskGiB = 32
	}
	state.lock.Lock()
	state.machines[9003] = &topologyServerMachine{}
	state.lock.Unlock()
	spec.PlannedMachines[2].VMID = 9003
	spec.PlannedMachines[2].Name = "dev-new-3"
	spec.PlannedMachines[2].Address = "10.10.0.13"
	plan, err = planTopologyReconcile(spec)
	if err != nil {
		t.Fatal(err)
	}
	machines, _ := json.Marshal(result.MachineSet)
	access, _ := json.Marshal(result.MachineAccess)
	step := plan.Steps[0]
	step.ResolvedInputs = map[string]domain.ResolvedArtifact{"previous-machines": {ID: "art_previous", Type: "MachineSet", Value: machines}, "previous-access": {ID: "art_access", Type: "MachineAccess", Sensitive: true, Value: access}}
	raw, err = plugin.Execute(ctx, step, invocation.Secrets, invocation.Connections, log)
	if err != nil {
		t.Fatal(err)
	}
	var migrated topologyResult
	_ = json.Unmarshal(raw, &migrated)
	if migrated.MachineSet.Spec.Machines[0].ID != "qemu/9000" || migrated.MachineSet.Spec.Machines[0].Node != "pve-2" || migrated.MachineAccess.Spec.PrivateKey != result.MachineAccess.Spec.PrivateKey {
		t.Fatal("migration replaced identity or access")
	}
	health, err := plugin.Verify(ctx, step, raw, invocation.Secrets, invocation.Connections, log)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify: %+v %v", health, err)
	}
	spec.Prune = true
	spec.MachineCount = 3
	spec.ReadyClusterRef = "art_ready"
	prunePlan, err := planTopologyReconcile(spec)
	if err != nil {
		t.Fatal(err)
	}
	pruneStep := prunePlan.Steps[0]
	pruneStep.ResolvedInputs = step.ResolvedInputs
	if _, err := plugin.Execute(ctx, pruneStep, invocation.Secrets, invocation.Connections, log); err == nil {
		t.Fatal("deleted VM before Kubernetes reconciliation was verified")
	}
	state.lock.Lock()
	present := state.machines[9002].exists
	state.lock.Unlock()
	if !present {
		t.Fatal("VM removed without verified cluster")
	}
	pruneStep.ResolvedInputs["ready-cluster"] = domain.ResolvedArtifact{ID: "art_ready", Type: "ClusterConnection"}
	raw, err = plugin.Execute(ctx, pruneStep, invocation.Secrets, invocation.Connections, log)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(ctx, pruneStep, raw, invocation.Secrets, invocation.Connections, log)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("prune verification: %+v %v", health, err)
	}
	state.lock.Lock()
	defer state.lock.Unlock()
	if !state.machines[9000].exists || state.machines[9000].node != "pve-2" || !state.machines[9001].exists || !state.machines[9003].exists || state.machines[9002].exists {
		t.Fatal("prune affected retained VM or left removed VM behind")
	}
}
