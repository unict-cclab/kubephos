package nfs

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"strings"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
)

func TestManagedSharedStorageLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner}
	spec := json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","storageName":"research-data"}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].ArtifactInputs) != 2 || len(plan.Steps[0].Outputs) != 1 || plan.Steps[0].Outputs[0].Type != "SharedStorageEndpoint" {
		t.Fatalf("unexpected plan %#v", plan)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	health, err := plugin.Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, value, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SharedStorageEndpoint.Spec.Server != "10.10.0.11" || decoded.SharedStorageEndpoint.Spec.ClientCIDR != "10.10.0.0/24" || decoded.SharedStorageEndpoint.Metadata.Version != "1:2.6.4-3ubuntu5.1" {
		t.Fatalf("unexpected endpoint %#v", decoded.SharedStorageEndpoint)
	}
	step.Cleanup = true
	health, err = plugin.Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup precheck failed %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), step, value, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Cleanup(context.Background(), step, value, func(string, string) error { return nil }); err != nil {
		t.Fatalf("cleanup must be idempotent: %v", err)
	}
	health, err = plugin.Verify(context.Background(), step, nil, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup verify failed %#v %v", health, err)
	}
}

func TestManagedSharedStorageRejectsNonDedicatedTopology(t *testing.T) {
	values := testArtifacts(t)
	var machines machineSet
	if err := json.Unmarshal(values["machines"].Value, &machines); err != nil {
		t.Fatal(err)
	}
	machines.Spec.Machines = append(machines.Spec.Machines, machines.Spec.Machines[0])
	value, _ := json.Marshal(machines)
	values["machines"] = domain.ResolvedArtifact{Value: value}
	step := domain.PlanStep{Input: json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","storageName":"storage","marker":"one"}`), ResolvedInputs: values}
	health, err := (Plugin{Runner: &fakeRunner{}}).Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected topology rejection %#v %v", health, err)
	}
}

func testArtifacts(t *testing.T) map[string]domain.ResolvedArtifact {
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
	privateValue := string(pem.EncodeToMemory(block))
	publicValue := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic)))
	machines := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"MachineSet","spec":{"networkCIDR":"10.10.0.0/24","machines":[{"id":"qemu/111","name":"storage-01","address":"10.10.0.11","sshPort":22,"sshUser":"ubuntu","state":"running"}]}}`)
	access, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "MachineAccess", "spec": map[string]string{"algorithm": "ssh-ed25519", "publicKey": publicValue, "privateKey": privateValue}})
	return map[string]domain.ResolvedArtifact{"machines": {Value: machines}, "machine-access": {Value: access}}
}

type fakeRunner struct {
	lock      sync.Mutex
	installed bool
}

func (runner *fakeRunner) Run(_ context.Context, _ machine, _ machineAccess, command string) (string, error) {
	runner.lock.Lock()
	defer runner.lock.Unlock()
	switch {
	case strings.HasPrefix(command, "sudo -n true && command -v apt-get"):
		if runner.installed {
			return "", errors.New("managed files already exist")
		}
		return "", nil
	case strings.HasPrefix(command, "sudo -n true && if test -e"):
		return "", nil
	case strings.Contains(command, "apt-get install -y nfs-kernel-server"):
		if !strings.Contains(command, "sudo install -d -m 0755 /etc/exports.d") {
			return "", errors.New("exports directory is not created")
		}
		runner.installed = true
		return "installation complete\n1:2.6.4-3ubuntu5.1", nil
	case strings.HasPrefix(command, "sudo test \"$(sudo cat"):
		if !runner.installed {
			return "", errors.New("service is absent")
		}
		return "", nil
	case strings.HasPrefix(command, "if test ! -e"):
		runner.installed = false
		return "", nil
	case strings.HasPrefix(command, "test ! -e"):
		if runner.installed {
			return "", errors.New("managed files remain")
		}
		return "", nil
	default:
		return "", errors.New("unexpected command")
	}
}
