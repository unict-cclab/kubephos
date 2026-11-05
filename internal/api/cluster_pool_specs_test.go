package api

import (
	"encoding/json"
	"kubephos.dev/kubephos/internal/placement"
	"kubephos.dev/kubephos/internal/schema"
	k3s "kubephos.dev/kubephos/plugins/k3s/runtime"
	"testing"
)

func TestManagedClusterNodePoolsAcceptMissingAndEmptyLabels(t *testing.T) {
	for _, labels := range []map[string]string{nil, {}, {"hardware": "gpu"}} {
		input := managedKubernetesClusterInput{ManagementPool: managedNodePoolInput{Name: "management", Count: 1, Zones: []string{"zone-a"}}, ApplicationPools: []managedNodePoolInput{{Name: "applications", Count: 2, Zones: []string{"zone-b"}, Labels: labels}}}
		spec := k3s.Spec{MachineSetRef: "art_machines", MachineAccessRef: "art_access", ClusterName: "research", ControlPlanes: 1, ControlPlaneZones: []string{"zone-a"}, NodePools: managedClusterNodePools(input)}
		raw, err := json.Marshal(spec)
		if err != nil {
			t.Fatal(err)
		}
		issues, err := schema.Validate((k3s.Plugin{}).Manifest().Schema, raw)
		if err != nil || len(issues) != 0 {
			t.Fatalf("cluster pool JSON violates runtime schema: %v %#v", err, issues)
		}
		var value struct {
			NodePools []map[string]any `json:"nodePools"`
		}
		json.Unmarshal(raw, &value)
		for _, pool := range value.NodePools {
			if labels, exists := pool["labels"]; exists && labels == nil {
				t.Fatal("null labels leaked into the Kubernetes plan")
			}
		}
		if len(labels) > 0 && spec.NodePools[1].Labels["hardware"] != "gpu" {
			t.Fatal("configured labels were lost")
		}
	}
}

func TestApplicationPoolTaintsReachBothNetworkingModes(t *testing.T) {
	input := managedKubernetesClusterInput{Name: "demo", ManagementPool: managedNodePoolInput{Name: "management", Count: 1, Zones: []string{"cloud"}}, ApplicationPools: []managedNodePoolInput{{Name: "gpu", Count: 1, Zones: []string{"edge"}, Labels: map[string]string{"hardware": "gpu"}, Taints: []placement.Taint{{Key: "gpu", Effect: "NoSchedule"}}}}}
	pools := managedClusterNodePools(input)
	if len(pools[0].Taints) != 0 || len(pools[1].Taints) != 1 || pools[1].Labels["hardware"] != "gpu" {
		t.Fatal(pools)
	}
	settings := managedApplicationNodeSettings(input)
	if len(settings) != 1 || len(settings["demo-gpu-1"].Taints) != 1 {
		t.Fatal(settings)
	}
	raw, _ := json.Marshal(k3s.Spec{MachineSetRef: "art_machines", MachineAccessRef: "art_access", ClusterName: "demo", ControlPlanes: 1, ControlPlaneZones: []string{"cloud"}, NodePools: pools, PreviousNodeSettings: settings})
	issues, err := schema.Validate((k3s.Plugin{}).Manifest().Schema, raw)
	if err != nil || len(issues) > 0 {
		t.Fatal(issues, err)
	}
}
