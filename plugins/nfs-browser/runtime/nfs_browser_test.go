package nfsbrowser

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
)

func TestNFSBrowserLifecycle(t *testing.T) {
	tests := []struct {
		action      string
		path        string
		content     string
		state       string
		mutating    bool
		wantCommand string
	}{
		{action: "list", path: ".", state: "listed", wantCommand: "sudo find"},
		{action: "read", path: "results/run.json", state: "read", wantCommand: "base64 -w0"},
		{action: "write", path: "results/run.json", content: "hello", state: "written", mutating: true, wantCommand: ".kubephos-upload"},
		{action: "delete", path: "results/run.json", state: "deleted", mutating: true, wantCommand: "sudo rm"},
	}
	for _, current := range tests {
		t.Run(current.action, func(t *testing.T) {
			runner := &fakeRunner{}
			plugin := Plugin{Runner: runner}
			input, err := json.Marshal(Spec{MachineSetRef: "art_machines", MachineAccessRef: "art_access", SharedStorageEndpointRef: "art_storage", Action: current.action, Path: current.path, Content: current.content, ProtectOutput: true})
			if err != nil {
				t.Fatal(err)
			}
			report := plugin.Validate(context.Background(), Invocation{Input: input})
			if !report.Valid {
				t.Fatalf("unexpected validation failure %#v", report.Issues)
			}
			plan, err := plugin.Plan(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			if len(plan.Steps) != 1 || plan.Steps[0].Mutating != current.mutating || !plan.Steps[0].Outputs[0].Sensitive {
				t.Fatalf("unexpected plan %#v", plan)
			}
			step := plan.Steps[0]
			step.ResolvedInputs = nfsArtifacts(t)
			health, err := plugin.Precheck(context.Background(), step, discardLog)
			if err != nil || health.Status != domain.HealthHealthy {
				t.Fatalf("precheck failed %#v %v", health, err)
			}
			value, err := plugin.Execute(context.Background(), step, discardLog)
			if err != nil {
				t.Fatal(err)
			}
			health, err = plugin.Verify(context.Background(), step, value, discardLog)
			if err != nil || health.Status != domain.HealthHealthy {
				t.Fatalf("verify failed %#v %v", health, err)
			}
			view, err := unmarshalView(value)
			if err != nil || view.State != current.state {
				t.Fatalf("unexpected storage view %#v %v", view, err)
			}
			if current.action == "list" && (len(view.Entries) != 2 || view.Entries[0].Name != "alpha" || view.Entries[1].Name != "beta") {
				t.Fatalf("unexpected listing %#v", view.Entries)
			}
			if current.action == "read" && view.ContentBase64 != base64.StdEncoding.EncodeToString([]byte("hello")) {
				t.Fatalf("unexpected content %#v", view)
			}
			if !containsCommand(runner.commands, current.wantCommand) {
				t.Fatalf("expected command %q in %#v", current.wantCommand, runner.commands)
			}
		})
	}
}

func TestNFSBrowserRejectsUnsafePathsAndContent(t *testing.T) {
	values := []Spec{
		{MachineSetRef: "art_machines", MachineAccessRef: "art_access", SharedStorageEndpointRef: "art_storage", Action: "read", Path: "../secret", ProtectOutput: true},
		{MachineSetRef: "art_machines", MachineAccessRef: "art_access", SharedStorageEndpointRef: "art_storage", Action: "read", Path: "/etc/passwd", ProtectOutput: true},
		{MachineSetRef: "art_machines", MachineAccessRef: "art_access", SharedStorageEndpointRef: "art_storage", Action: "delete", Path: ".", ProtectOutput: true},
		{MachineSetRef: "art_machines", MachineAccessRef: "art_access", SharedStorageEndpointRef: "art_storage", Action: "write", Path: "safe", Content: strings.Repeat("x", writeSizeLimit+1), ProtectOutput: true},
	}
	for _, value := range values {
		input, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if report := (Plugin{}).Validate(context.Background(), Invocation{Input: input}); report.Valid {
			t.Fatalf("expected validation failure for %#v", value)
		}
	}
}

func nfsArtifacts(t *testing.T) map[string]domain.ResolvedArtifact {
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
	machines := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"MachineSet","spec":{"networkCIDR":"10.10.0.0/24","machines":[{"id":"qemu/111","name":"nfs-01","address":"10.10.0.11","sshPort":22,"sshUser":"ubuntu","state":"running"}]}}`)
	access, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "MachineAccess", "spec": map[string]string{"algorithm": "ssh-ed25519", "publicKey": strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))), "privateKey": string(pem.EncodeToMemory(block))}})
	storage := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"SharedStorageEndpoint","spec":{"protocol":"nfs","server":"10.10.0.11","exportPath":"/srv/kubephos","clientCIDR":"10.10.0.0/24"}}`)
	return map[string]domain.ResolvedArtifact{"machines": {Value: machines}, "machine-access": {Value: access}, "storage-endpoint": {Value: storage}}
}

type fakeRunner struct {
	commands []string
}

func (runner *fakeRunner) Run(_ context.Context, _ machine, _ machineAccess, command string) (string, error) {
	runner.commands = append(runner.commands, command)
	switch {
	case strings.Contains(command, "sudo find"):
		return "beta\x00f\x004\x0017.5\x00alpha\x00d\x000\x0016.5\x00", nil
	case strings.Contains(command, "base64 -w0"):
		return base64.StdEncoding.EncodeToString([]byte("hello")), nil
	case strings.Contains(command, ".kubephos-upload"):
		value := sha256.Sum256([]byte("hello"))
		return hex.EncodeToString(value[:]), nil
	default:
		return "", nil
	}
}

func discardLog(string, string) error {
	return nil
}

func containsCommand(values []string, expected string) bool {
	for _, value := range values {
		if strings.Contains(value, expected) {
			return true
		}
	}
	return false
}
