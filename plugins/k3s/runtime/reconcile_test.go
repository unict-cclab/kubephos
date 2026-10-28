package k3s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"kubephos.dev/kubephos/internal/domain"
	"strings"
	"testing"
)

func TestRemovalBlocksLocalPersistentData(t *testing.T) {
	for _, test := range []struct {
		raw     string
		blocked bool
	}{
		{`{"items":[]}`, false},
		{`{"items":[{"spec":{"nfs":{"server":"nfs"}}}]}`, false},
		{`{"items":[{"metadata":{"name":"data"},"spec":{"local":{"path":"/data"},"nodeAffinity":{"required":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"kubernetes.io/hostname","values":["edge-1"]}]}]}}}}]}`, true},
		{`{"items":[{"spec":{"hostPath":{"path":"/data"}}}]}`, true},
		{`{"items":[{"spec":{"local":{"path":"/data"},"nodeAffinity":{"required":{"nodeSelectorTerms":[{"matchExpressions":[{"key":"kubernetes.io/hostname","values":["other"]}]}]}}}}]}`, false},
		{`invalid`, true},
	} {
		err := validateRemovableVolumes([]byte(test.raw), "edge-1")
		if (err != nil) != test.blocked {
			t.Fatalf("volume guard: %s: %v", test.raw, err)
		}
	}
}

type maintenanceRunner struct {
	base      *fakeRunner
	commands  []string
	failDrain bool
	version   string
}

func (r *maintenanceRunner) Run(ctx context.Context, target machine, access machineAccess, command string) (string, error) {
	r.commands = append(r.commands, command)
	switch {
	case command == "sudo k3s --version":
		if r.version != "" {
			return r.version, nil
		}
		return k3sVersion, nil
	case command == "sudo cat /var/lib/rancher/k3s/server/node-token":
		return "test-join-token", nil
	case command == "sudo k3s kubectl get pv -o json":
		return `{"items":[]}`, nil
	case strings.HasPrefix(command, "sudo k3s kubectl drain "):
		if r.failDrain {
			return "", errors.New("PDB blocks drain")
		}
		return "", nil
	case strings.HasPrefix(command, "sudo k3s kubectl delete node "):
		return "", nil
	case strings.HasPrefix(command, "if test -x /usr/local/bin/k3s-uninstall.sh;"):
		delete(r.base.installed, target.Name)
		delete(r.base.nodeNames, target.Name)
		return "", nil
	default:
		value, err := r.base.Run(ctx, target, access, command)
		if r.version != "" {
			value = strings.ReplaceAll(value, k3sVersion, r.version)
		}
		return value, err
	}
}
func TestMaintenanceDrainsBeforeRemovingApplicationNodes(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint(fail), func(t *testing.T) {
			step := bootstrapStep(t, 1)
			var old machineSet
			_ = json.Unmarshal(step.ResolvedInputs["machines"].Value, &old)
			// Names match the nodes actually registered with Kubernetes.
			for i := range old.Spec.Machines {
				old.Spec.Machines[i].Name = managedNodeName(Spec{ClusterName: "dev", ControlPlanes: 1}, i)
			}
			previous, _ := json.Marshal(old)
			desired := old
			desired.Spec.Machines = desired.Spec.Machines[:2]
			raw, _ := json.Marshal(desired)
			a := step.ResolvedInputs["machines"]
			a.Value = raw
			step.ResolvedInputs["machines"] = a
			step.ResolvedInputs["previous-machines"] = domain.ResolvedArtifact{ID: "art_previous", Type: "MachineSet", Value: previous}
			spec := Spec{MachineSetRef: "art_machines", MachineAccessRef: "art_access", ClusterName: "dev", ControlPlanes: 1, Reconcile: true, PreviousMachineSetRef: "art_previous"}
			step.Input, _ = json.Marshal(spec)
			runner := &maintenanceRunner{base: &fakeRunner{installed: map[string]string{"dev-control-plane-1": "server", "dev-worker-1": "agent", "dev-worker-2": "agent"}, nodeNames: map[string]string{"dev-control-plane-1": "dev-control-plane-1", "dev-worker-1": "dev-worker-1", "dev-worker-2": "dev-worker-2"}}, failDrain: fail}
			_, err := (Plugin{Runner: runner}).Execute(context.Background(), step, func(string, string) error { return nil })
			if fail {
				if err == nil {
					t.Fatal("ignored drain failure")
				}
				if _, exists := runner.base.installed["dev-worker-2"]; !exists {
					t.Fatal("removed node after failed drain")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, exists := runner.base.installed["dev-worker-2"]; exists {
				t.Fatal("removed node still installed")
			}
			drain, uninstall := -1, -1
			for i, c := range runner.commands {
				if strings.Contains(c, "kubectl drain ") {
					drain = i
				}
				if strings.HasPrefix(c, "if test -x /usr/local/bin/k3s-uninstall.sh;") {
					uninstall = i
				}
			}
			if drain < 0 || uninstall <= drain {
				t.Fatalf("incorrect drain/uninstall order: %v", runner.commands)
			}
		})
	}
}

func TestMaintenanceKeepsLegacyReleaseWhenAddingNode(t *testing.T) {
	step := bootstrapStep(t, 1)
	var old machineSet
	_ = json.Unmarshal(step.ResolvedInputs["machines"].Value, &old)
	for i := range old.Spec.Machines {
		old.Spec.Machines[i].Name = managedNodeName(Spec{ClusterName: "dev", ControlPlanes: 1}, i)
	}
	previous, _ := json.Marshal(old)
	desired := old
	added := old.Spec.Machines[2]
	added.Name = "dev-worker-3"
	added.ID = "machine-4"
	added.Address = "10.20.0.13"
	desired.Spec.Machines = append(desired.Spec.Machines, added)
	raw, _ := json.Marshal(desired)
	a := step.ResolvedInputs["machines"]
	a.Value = raw
	step.ResolvedInputs["machines"] = a
	step.ResolvedInputs["previous-machines"] = domain.ResolvedArtifact{ID: "art_previous", Type: "MachineSet", Value: previous}
	spec := Spec{MachineSetRef: "art_machines", MachineAccessRef: "art_access", ClusterName: "dev", ControlPlanes: 1, Reconcile: true, PreviousMachineSetRef: "art_previous"}
	step.Input, _ = json.Marshal(spec)
	runner := &maintenanceRunner{version: "v1.36.0+k3s1", base: &fakeRunner{installed: map[string]string{}, nodeNames: map[string]string{}}}
	for i, m := range old.Spec.Machines {
		role := "agent"
		if i == 0 {
			role = "server"
		}
		runner.base.installed[m.Name] = role
		runner.base.nodeNames[m.Name] = m.Name
	}
	plugin := Plugin{Runner: runner}
	result, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	health, err := plugin.Verify(context.Background(), step, result, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("legacy maintenance verification: %#v %v", health, err)
	}
	var decoded clusterResult
	_ = json.Unmarshal(result, &decoded)
	if decoded.ClusterConnection.Metadata.Version != runner.version || decoded.ClusterInventory.Spec.Version != runner.version {
		t.Fatal("maintenance changed reported version")
	}
	found := false
	for _, command := range runner.commands {
		if strings.Contains(command, "sh -s - agent") {
			found = true
			if !strings.Contains(command, "INSTALL_K3S_VERSION='"+runner.version+"'") {
				t.Fatal("new node would upgrade cluster")
			}
		}
	}
	if !found {
		t.Fatal("new node not installed")
	}
}
