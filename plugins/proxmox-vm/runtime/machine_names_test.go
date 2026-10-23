package proxmoxvm

import (
	"strings"
	"testing"
)

func TestMachineNameUniquenessAcrossProxmoxInventory(t *testing.T) {
	for _, resourceType := range []string{"qemu", "lxc"} {
		t.Run(resourceType, func(t *testing.T) {
			resources := []vmResource{{VMID: 8000, Name: "template", Node: "pve", Type: "qemu", Template: 1}, {VMID: 200, Name: "Registry", Node: "other-node", Type: resourceType}}
			if err := validateInventory(resources, Spec{TemplateVMID: 8000, TargetVMID: 9000, Node: "pve", Name: "registry"}); err == nil || !strings.Contains(err.Error(), "already exists") {
				t.Fatalf("VM accepted duplicate name: %v", err)
			}
			if err := validateTemplateInventory(resources, TemplateSpec{VMID: 9000, Name: "registry"}); err == nil {
				t.Fatal("template accepted duplicate name")
			}
			input := topologyStepInput{TemplateVMID: 8000, Node: "pve", Machines: []plannedMachine{{VMID: 9000, Name: "registry"}}}
			if err := validatePlannedInventory(resources, input); err == nil {
				t.Fatal("topology accepted duplicate name")
			}
			input.Machines[0].Name = "new-registry"
			if err := validatePlannedInventory(resources, input); err != nil {
				t.Fatalf("unique name rejected: %v", err)
			}
		})
	}
}

func TestTopologyRejectsRepeatedPlannedNames(t *testing.T) {
	resources := []vmResource{{VMID: 8000, Node: "pve", Type: "qemu", Template: 1}}
	input := topologyStepInput{TemplateVMID: 8000, Node: "pve", Machines: []plannedMachine{{VMID: 9000, Name: "registry"}, {VMID: 9001, Name: "registry"}}}
	if err := validatePlannedInventory(resources, input); err == nil {
		t.Fatal("repeated planned names accepted")
	}
}
