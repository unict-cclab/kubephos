package k3s

import (
	"encoding/base64"
	"encoding/json"
	"kubephos.dev/kubephos/internal/placement"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestInstallTaintsAreOnlyAddedToApplicationNodes(t *testing.T) {
	spec := Spec{ClusterName: "demo", ControlPlanes: 1, ControlPlaneZones: []string{"cloud"}, NodePools: []NodePool{{Name: "management", Role: "management", Count: 1, Zones: []string{"cloud"}}, {Name: "apps", Role: "application", Count: 1, Zones: []string{"edge"}, Labels: map[string]string{"hardware": "gpu"}, Taints: []placement.Taint{{Key: "gpu", Value: "true", Effect: "NoSchedule"}}}}}
	for index, name := range []string{"demo-control-plane-1", "demo-management-1", "demo-apps-1"} {
		role := "agent"
		if index == 0 {
			role = "server"
		}
		command := installCommandForSpec(spec, role, name, "https://server:6443", "token", index == 0, machineLabels(spec, index))
		if strings.Contains(command, "gpu=true:NoSchedule") != (index == 2) {
			t.Fatal(command)
		}
		if index == 0 && !strings.Contains(command, "node-role.kubernetes.io/control-plane:NoSchedule") {
			t.Fatal("default control plane taint lost")
		}
		if index == 2 && (!strings.Contains(command, "hardware=gpu") || !strings.Contains(command, "kubephos.dev/role=application")) {
			t.Fatal(command)
		}
	}
}
func TestNodeSettingsRemoveOnlyOwnedMetadata(t *testing.T) {
	labels := map[string]string{"old": "value", "external": "yes", "kubephos.dev/role": "application"}
	taints := []map[string]any{{"key": "old", "effect": "NoSchedule", "value": "value"}, {"key": "node.kubernetes.io/not-ready", "effect": "NoExecute", "timeAdded": "timestamp"}, {"key": "external", "effect": "NoSchedule"}}
	before := placement.PoolSettings{Labels: map[string]string{"old": "value", "kubephos.dev/role": "application"}, Taints: []placement.Taint{{Key: "old", Value: "value", Effect: "NoSchedule"}}}
	after := placement.PoolSettings{Labels: map[string]string{"new": ""}, Taints: []placement.Taint{{Key: "gpu", Effect: "NoSchedule"}}}
	// Even malformed historical input must not claim ownership of system taints.
	before.Taints = append(before.Taints, placement.Taint{Key: "node.kubernetes.io/not-ready", Effect: "NoExecute"})
	patch := nodeSettingsPatch(labels, taints, before, after)
	raw, _ := json.Marshal(patch)
	if strings.Contains(string(raw), "kubephos.dev/role") || strings.Contains(string(raw), "external\":null") {
		t.Fatal(string(raw))
	}
	next := patch["spec"].(map[string]any)["taints"].([]map[string]any)
	if len(next) != 3 || !reflect.DeepEqual(next[0], taints[1]) || !reflect.DeepEqual(next[1], taints[2]) {
		t.Fatal(next)
	}
	if value, exists := patch["metadata"].(map[string]any)["labels"].(map[string]any)["new"]; !exists || value != "" {
		t.Fatal("empty label not created")
	}
	if len(nodeSettingsPatch(labels, taints, placement.PoolSettings{}, placement.PoolSettings{})) != 0 {
		t.Fatal("unconfigured metadata changed")
	}
	// A controller modifying an owned label must not have its value removed.
	labels["old"] = "external-change"
	patch = nodeSettingsPatch(labels, taints, before, placement.PoolSettings{})
	if metadata, ok := patch["metadata"]; ok {
		if _, exists := metadata.(map[string]any)["labels"].(map[string]any)["old"]; exists {
			t.Fatal("external value removed")
		}
	}
}
func TestRegistrationSettingsPersistWithoutRestartOrSecretOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "k3s-agent.service")
	original := "[Service]\nEnvironmentFile=-/etc/systemd/system/k3s-agent.service.env\nExecStart=/usr/local/bin/k3s \\\n    agent \\\n    '--node-name' \\\n    'demo-apps-1' \\\n    '--node-label' \\\n    'old=value' \\\n    '--node-label=kubephos.dev/role=application' \\\n    '--node-taint' \\\n    'old:NoSchedule'\nRestart=always\n"
	if err := os.WriteFile(path, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	script := strings.Replace(nodeRegistrationScript, "/etc/systemd/system/k3s-agent.service", path, 1)
	args, _ := json.Marshal(map[string]any{"previous": placement.PoolSettings{Labels: map[string]string{"old": "value"}, Taints: []placement.Taint{{Key: "old", Effect: "NoSchedule"}}}, "args": []string{"--node-label", "kubephos.dev/role=application", "--node-label", "hardware=gpu", "--node-taint", "gpu:NoSchedule"}})
	command := exec.Command("python3", "-c", script, base64.StdEncoding.EncodeToString(args))
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
	result, _ := os.ReadFile(path)
	for _, want := range []string{"kubephos.dev/role=application", "hardware=gpu", "gpu:NoSchedule", "demo-apps-1", "EnvironmentFile=", "Restart=always"} {
		if !strings.Contains(string(result), want) {
			t.Fatalf("lost %s: %s", want, result)
		}
	}
	if strings.Contains(string(result), "old=value") || strings.Contains(string(result), "old:NoSchedule") {
		t.Fatal(string(result))
	}
	runtime := nodeRegistrationCommand(map[string]string{"hardware": "gpu"}, nil)
	if strings.Contains(runtime, "restart") || !strings.HasPrefix(runtime, "set -eu") {
		t.Fatal("must not restart service or swallow errors")
	}
}
