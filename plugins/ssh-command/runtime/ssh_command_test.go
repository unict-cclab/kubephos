package sshcommand

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
)

func TestAuditedCommandLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner}
	spec := json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","machineIndex":1,"command":"printf 'alpha\\nbeta\\n'","timeoutSeconds":30,"protectOutput":false}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid || len(report.Issues) != 2 {
		t.Fatalf("unexpected validation report %#v", report)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Outputs[0].Sensitive {
		t.Fatalf("unexpected plan %#v", plan)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	logs := []string{}
	logger := func(_ string, message string) error {
		logs = append(logs, message)
		return nil
	}
	health, err := plugin.Precheck(context.Background(), step, logger)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, logger)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, value, logger)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.CommandResult.Spec.MachineID != "qemu/112" || decoded.CommandResult.Spec.Output != "alpha\nbeta" || runner.target.Name != "node-02" {
		t.Fatalf("unexpected result %#v", decoded.CommandResult)
	}
	if !contains(logs, "alpha") || !contains(logs, "beta") {
		t.Fatalf("expected public command output in logs, got %#v", logs)
	}
}

func TestAuditedCommandProtectsOutputByDefault(t *testing.T) {
	spec := json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","machineIndex":0,"command":"uptime","timeoutSeconds":60,"protectOutput":true}`)
	plan, err := (Plugin{}).Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Steps[0].Outputs[0].Sensitive {
		t.Fatal("expected protected output")
	}
}

func TestAuditedCommandRejectsControlCharactersAndInvalidTarget(t *testing.T) {
	for _, value := range []json.RawMessage{
		json.RawMessage("{\"machineSetRef\":\"art_machines\",\"machineAccessRef\":\"art_access\",\"machineIndex\":0,\"command\":\"bad\\u0000command\",\"timeoutSeconds\":60,\"protectOutput\":true}"),
		json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","machineIndex":12,"command":"true","timeoutSeconds":60,"protectOutput":true}`),
	} {
		if report := (Plugin{}).Validate(context.Background(), Invocation{Input: value}); report.Valid {
			t.Fatalf("expected validation failure for %s", value)
		}
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
	machines := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"MachineSet","spec":{"networkCIDR":"10.10.0.0/24","machines":[{"id":"qemu/111","name":"node-01","address":"10.10.0.11","sshPort":22,"sshUser":"ubuntu","state":"running"},{"id":"qemu/112","name":"node-02","address":"10.10.0.12","sshPort":22,"sshUser":"ubuntu","state":"running"}]}}`)
	access, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "MachineAccess", "spec": map[string]string{"algorithm": "ssh-ed25519", "publicKey": publicValue, "privateKey": privateValue}})
	return map[string]domain.ResolvedArtifact{"machines": {Value: machines}, "machine-access": {Value: access}}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

type fakeRunner struct {
	target machine
}

func (runner *fakeRunner) Run(_ context.Context, target machine, _ machineAccess, command string) (string, error) {
	runner.target = target
	if strings.Contains(command, "timeout --signal=TERM") {
		return "alpha\nbeta", nil
	}
	return "", nil
}
