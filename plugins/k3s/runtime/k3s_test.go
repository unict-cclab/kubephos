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
	if decoded.ClusterConnection.Spec.Server != "https://10.20.0.10:6443" || strings.Contains(decoded.ClusterConnection.Spec.Kubeconfig, "127.0.0.1") || !strings.Contains(decoded.ClusterConnection.Spec.Kubeconfig, "current-context: dev") || !strings.Contains(decoded.ClusterConnection.Spec.Kubeconfig, "name: dev-admin") || len(decoded.ClusterInventory.Spec.Nodes) != 3 {
		t.Fatalf("unexpected cluster result %#v", decoded)
	}
	step.Cleanup = true
	health, err = plugin.Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup precheck failed %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), step, result, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Cleanup(context.Background(), step, result, func(string, string) error { return nil }); err != nil {
		t.Fatalf("repeated cleanup must succeed: %v", err)
	}
	health, err = plugin.Verify(context.Background(), step, nil, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup verification failed %#v %v", health, err)
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

func TestManagedPoolsAssignStableRolesAndZones(t *testing.T) {
	spec := Spec{ControlPlanes: 3, ControlPlaneZones: []string{"zone-a", "zone-b"}, NodePools: []NodePool{{Name: "management", Role: "management", Count: 2, Zones: []string{"zone-a", "zone-b"}}, {Name: "applications", Role: "application", Count: 3, Zones: []string{"zone-a", "zone-b"}}}}
	if err := validatePoolConfiguration(spec); err != nil {
		t.Fatal(err)
	}
	if err := validatePoolCapacity(spec, 8); err != nil {
		t.Fatal(err)
	}
	expected := []struct{ pool, role, zone string }{{"control-plane", "control-plane", "zone-a"}, {"control-plane", "control-plane", "zone-b"}, {"control-plane", "control-plane", "zone-a"}, {"management", "management", "zone-a"}, {"management", "management", "zone-b"}, {"applications", "application", "zone-a"}, {"applications", "application", "zone-b"}, {"applications", "application", "zone-a"}}
	for position, value := range expected {
		labels := machineLabels(spec, position)
		if labels["kubephos.dev/pool"] != value.pool || labels["kubephos.dev/role"] != value.role || labels["topology.kubernetes.io/zone"] != value.zone {
			t.Fatalf("machine %d has unexpected labels %#v", position, labels)
		}
	}
}

func TestPlanIncludesOptionalManagedRegistryContracts(t *testing.T) {
	spec := json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","clusterName":"dev","controlPlanes":1,"registryEndpointRef":"art_registry","registryCredentialRef":"art_registry_credential"}`)
	plan, err := (Plugin{}).Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps[0].ArtifactInputs) != 4 || plan.Steps[0].ArtifactInputs[2].Type != "RegistryEndpoint" || plan.Steps[0].ArtifactInputs[3].Type != "RegistryCredential" {
		t.Fatalf("unexpected registry inputs %#v", plan.Steps[0].ArtifactInputs)
	}
}

func TestRegistryConfigurationRoutesManagedUpstreamsThroughDeclaredMirrors(t *testing.T) {
	profile := registryProfile{Endpoint: registryEndpoint{}, Credential: registryCredential{}}
	profile.Endpoint.Spec.Host = "registry.internal"
	profile.Endpoint.Spec.URL = "https://registry.internal"
	profile.Endpoint.Spec.Mirrors = []registryMirror{{Source: "docker.io", Endpoint: profile.Endpoint.Spec.URL, RewritePrefix: "dockerhub-proxy/", Probe: "library/alpine:3.23"}, {Source: "ghcr.io", Endpoint: profile.Endpoint.Spec.URL, RewritePrefix: "ghcr-proxy/", Probe: "unict-cclab/mon-agent:v0.0.7"}}
	profile.Credential.Spec.Username = "robot"
	profile.Credential.Spec.Password = "secret"
	raw, err := registryConfiguration(profile)
	if err != nil {
		t.Fatal(err)
	}
	value := string(raw)
	for _, expected := range []string{"docker.io:", "ghcr.io:", "https://registry.internal", "dockerhub-proxy/$1", "ghcr-proxy/$1", "registry.internal:", "kubephos-registry-ca.crt"} {
		if !strings.Contains(value, expected) {
			t.Fatalf("registry configuration is missing %q from %s", expected, value)
		}
	}
}

func TestLegacyManagedRegistryReceivesProxyProfile(t *testing.T) {
	mirrors := legacyManagedMirrors("https://registry.internal")
	if len(mirrors) != 5 || mirrors[0].Source != "docker.io" || mirrors[0].RewritePrefix != "dockerhub-proxy/" || mirrors[0].Probe != "library/alpine:3.23" || mirrors[4].Source != "quay.io" {
		t.Fatalf("unexpected legacy mirror profile %#v", mirrors)
	}
}

func TestInstallDisablesDirectRegistryFallback(t *testing.T) {
	command := installCommand("server", "node-1", "https://192.0.2.1:6443", "token", true)
	if !strings.Contains(command, "--disable-default-registry-endpoint") {
		t.Fatalf("registry fallback remains enabled: %s", command)
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
	case command == "sudo -n true":
		return "", nil
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
		return "apiVersion: v1\nkind: Config\nclusters:\n  - name: default\n    cluster:\n      server: https://127.0.0.1:6443\nusers:\n  - name: default\n    user:\n      token: test\ncontexts:\n  - name: default\n    context:\n      cluster: default\n      user: default\ncurrent-context: default\n", nil
	case strings.HasPrefix(command, "if [ -x /usr/local/bin/k3s-agent-uninstall.sh ]"):
		delete(r.installed, target.Name)
		return "", nil
	case strings.HasPrefix(command, "test ! -x /usr/local/bin/k3s && test ! -e /etc/systemd/system/k3s.service"):
		if _, exists := r.installed[target.Name]; exists {
			return "", errors.New("k3s remains installed")
		}
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
