package k3s

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
)

func TestPlanUsesExternalArtifactsAndProtectsKubeconfig(t *testing.T) {
	spec := json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","clusterName":"dev","controlPlanes":1}`)
	plan, err := (Plugin{}).Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	if !step.Mutating || len(step.ArtifactInputs) != 2 || step.ArtifactInputs[0].ArtifactID != "art_machines" {
		t.Fatalf("unexpected bootstrap step %#v", step)
	}
	if len(step.Outputs) != 2 || !step.Outputs[0].Sensitive || step.Outputs[0].Type != "ClusterConnection" {
		t.Fatalf("cluster connection must be protected %#v", step.Outputs)
	}
}

func TestBootstrapValidatesInstallsVerifiesAndCleansCluster(t *testing.T) {
	runner := &fakeRunner{installed: map[string]string{}}
	plugin := Plugin{Runner: runner}
	step := bootstrapStep(t, 1)
	health, err := plugin.Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	result, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, result, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}
	var decoded clusterResult
	if err := json.Unmarshal(result, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ClusterConnection.Spec.Server != "https://10.20.0.10:6443" || strings.Contains(decoded.ClusterConnection.Spec.Kubeconfig, "127.0.0.1") || len(decoded.ClusterInventory.Spec.Nodes) != 3 {
		t.Fatalf("unexpected cluster result %#v", decoded)
	}
	if err := plugin.Cleanup(context.Background(), step, result, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	runner.lock.Lock()
	defer runner.lock.Unlock()
	if len(runner.installed) != 0 {
		t.Fatalf("cleanup left installed nodes %#v", runner.installed)
	}
}

func TestPrecheckRejectsDirtyMachine(t *testing.T) {
	runner := &fakeRunner{installed: map[string]string{"machine-2": "agent"}}
	health, err := (Plugin{Runner: runner}).Precheck(context.Background(), bootstrapStep(t, 1), func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected unhealthy precheck %#v", health)
	}
}

type fakeRunner struct {
	lock      sync.Mutex
	installed map[string]string
}

func (r *fakeRunner) Run(_ context.Context, target machine, _ machineAccess, command string) (string, error) {
	r.lock.Lock()
	defer r.lock.Unlock()
	switch {
	case strings.HasPrefix(command, "sudo -n true"):
		if _, exists := r.installed[target.Name]; exists {
			return "", errors.New("k3s already installed")
		}
		return "", nil
	case strings.Contains(command, "sh -s - server"):
		r.installed[target.Name] = "server"
		return "installed", nil
	case strings.Contains(command, "sh -s - agent"):
		r.installed[target.Name] = "agent"
		return "installed", nil
	case command == "sudo k3s kubectl get --raw=/readyz":
		if _, exists := r.installed[target.Name]; !exists {
			return "", errors.New("not ready")
		}
		return "ok", nil
	case command == "sudo k3s kubectl get nodes -o json":
		items := make([]map[string]any, 0, len(r.installed))
		for name, role := range r.installed {
			labels := map[string]string{}
			if role == "server" {
				labels["node-role.kubernetes.io/control-plane"] = "true"
			}
			items = append(items, map[string]any{"metadata": map[string]any{"name": name, "labels": labels}, "status": map[string]any{"conditions": []map[string]string{{"type": "Ready", "status": "True"}}, "nodeInfo": map[string]string{"kubeletVersion": k3sVersion}}})
		}
		value, _ := json.Marshal(map[string]any{"items": items})
		return string(value), nil
	case command == "sudo cat /etc/rancher/k3s/k3s.yaml":
		return "apiVersion: v1\nkind: Config\nclusters:\n  - cluster:\n      server: https://127.0.0.1:6443\nusers:\n  - name: default\n    user:\n      token: test\ncontexts:\n  - name: default\n    context:\n      cluster: default\n      user: default\ncurrent-context: default\n", nil
	case strings.HasPrefix(command, "if [ -x /usr/local/bin/k3s-agent-uninstall.sh ]"):
		delete(r.installed, target.Name)
		return "", nil
	default:
		return "", fmt.Errorf("unexpected command %q", command)
	}
}

func bootstrapStep(t *testing.T, controlPlanes int) domain.PlanStep {
	t.Helper()
	access := testAccess(t)
	machines := machineSet{APIVersion: clusterAPIVersion, Kind: "MachineSet"}
	machines.Spec.Provider = "proxmox"
	for index := 0; index < 3; index++ {
		machines.Spec.Machines = append(machines.Spec.Machines, machine{ID: fmt.Sprintf("qemu/%d", 9000+index), Name: fmt.Sprintf("machine-%d", index+1), Address: fmt.Sprintf("10.20.0.%d", index+10), SSHPort: 22, SSHUser: "ubuntu", State: "running"})
	}
	machineRaw, _ := json.Marshal(machines)
	accessRaw, _ := json.Marshal(access)
	spec, _ := json.Marshal(Spec{MachineSetRef: "art_machines", MachineAccessRef: "art_access", ClusterName: "dev", ControlPlanes: controlPlanes})
	return domain.PlanStep{Input: spec, ResolvedInputs: map[string]domain.ResolvedArtifact{
		"machines":       {ID: "art_machines", Type: "MachineSet", Version: "v1alpha1", Value: machineRaw},
		"machine-access": {ID: "art_access", Type: "MachineAccess", Version: "v1alpha1", Sensitive: true, Value: accessRaw},
	}}
}

func testAccess(t *testing.T) machineAccess {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKey(private, "test")
	if err != nil {
		t.Fatal(err)
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		t.Fatal(err)
	}
	value := machineAccess{APIVersion: clusterAPIVersion, Kind: "MachineAccess"}
	value.Spec.Algorithm = "ssh-ed25519"
	value.Spec.PublicKey = strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic)))
	value.Spec.PrivateKey = string(pem.EncodeToMemory(block))
	return value
}
