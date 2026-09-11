package ociexecutor

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
)

func TestManagedExecutorLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner, endpointVerifier: func(context.Context, machine, executorCredential) error { return nil }}
	spec := json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","executorName":"build-executor"}`)
	if report := plugin.Validate(context.Background(), Invocation{Input: spec}); !report.Valid {
		t.Fatalf("unexpected validation failure: %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].ArtifactInputs) != 2 || len(plan.Steps[0].Outputs) != 2 || plan.Steps[0].Outputs[0].Sensitive || !plan.Steps[0].Outputs[1].Sensitive {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed: %#v %v", health, err)
	}
	raw, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if value.ExecutorEndpoint.Spec.Host != "tcp://10.10.0.13:2376" || value.ExecutorEndpoint.Spec.Security != "rootless-mtls" || validateCredential(value.ExecutorCredential) != nil {
		t.Fatalf("unexpected result: %#v", value)
	}
	health, err = plugin.Verify(context.Background(), step, raw, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed: %#v %v", health, err)
	}
	step.Cleanup = true
	health, err = plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup precheck failed: %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), step, raw, discardLog); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Cleanup(context.Background(), step, raw, discardLog); err != nil {
		t.Fatalf("cleanup must be idempotent: %v", err)
	}
	health, err = plugin.Verify(context.Background(), step, nil, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup verify failed: %#v %v", health, err)
	}
}

func TestManagedExecutorPlanDoesNotContainCredentials(t *testing.T) {
	plan, err := (Plugin{}).Plan(context.Background(), json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","executorName":"executor"}`))
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(value), "privateKey") || strings.Contains(string(value), "certificate") {
		t.Fatalf("plan contains TLS material: %s", value)
	}
}

func TestManagedExecutorRejectsInvalidTopology(t *testing.T) {
	values := testArtifacts(t)
	var machines machineSet
	if err := json.Unmarshal(values["machines"].Value, &machines); err != nil {
		t.Fatal(err)
	}
	machines.Spec.Machines = append(machines.Spec.Machines, machines.Spec.Machines[0])
	value, _ := json.Marshal(machines)
	values["machines"] = domain.ResolvedArtifact{Value: value}
	step := domain.PlanStep{Input: json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","executorName":"executor","marker":"one"}`), ResolvedInputs: values}
	health, err := (Plugin{Runner: &fakeRunner{}}).Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected topology rejection: %#v %v", health, err)
	}
}

func TestExecutorInstallIsVersionedRootlessAndMutualTLS(t *testing.T) {
	command := installCommand("marker", testMachine())
	required := []string{dockerPackageVersion, dockerKeyFingerprint, aptNetworkOptions, "dockerd-rootless-setuptool.sh install", "socat", "OPENSSL-LISTEN:2376", "UNIX-CONNECT:%t/docker.sock", "kubephos-oci-proxy.service", "journalctl --user -u docker.service", "journalctl --user -u kubephos-oci-proxy.service", "disable --now docker.service docker.socket containerd.service"}
	for _, value := range required {
		if !strings.Contains(command, value) {
			t.Fatalf("install command does not contain %q", value)
		}
	}
	if strings.Contains(command, ":2375") || strings.Contains(command, "--tls=false") {
		t.Fatal("install command exposes an insecure Docker endpoint")
	}
	if strings.Contains(command, "dockerd-rootless.sh -H unix://%t/docker.sock -H tcp://") {
		t.Fatal("rootless daemon must not expose a plaintext TCP listener")
	}
	if !strings.Contains(command, "&& { attempt=0; until sudo -u") || !strings.Contains(command, `test "$attempt" -lt 61 || exit 1`) {
		t.Fatal("executor readiness retries are not bounded or chained to successful installation")
	}
}

func TestExecutorRemoteCommandsHaveValidShellSyntax(t *testing.T) {
	commands := []string{
		installPrecheckCommand("ubuntu"),
		installCommand("marker", testMachine()),
		readinessCommand("marker", testMachine()),
		cleanupPrecheckCommand("marker", "ubuntu"),
		cleanupCommand("marker", "ubuntu"),
		cleanupVerifyCommand("ubuntu"),
	}
	for _, command := range commands {
		if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("invalid command syntax: %v: %s", err, output)
		}
	}
}

func TestExecutorCredentialRejectsMismatchedKey(t *testing.T) {
	credential := testCredential(t)
	other := testCredential(t)
	credential.Spec.PrivateKey = other.Spec.PrivateKey
	if err := validateCredential(credential); err == nil {
		t.Fatal("expected mismatched private key rejection")
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
	machines, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "MachineSet", "spec": map[string]any{"networkCIDR": "10.10.0.0/24", "machines": []machine{testMachine()}}})
	access, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "MachineAccess", "spec": map[string]string{"algorithm": "ssh-ed25519", "publicKey": publicValue, "privateKey": privateValue}})
	return map[string]domain.ResolvedArtifact{"machines": {Value: machines}, "machine-access": {Value: access}}
}

func testMachine() machine {
	return machine{ID: "qemu/113", Name: "executor-01", Address: "10.10.0.13", SSHPort: 22, SSHUser: "ubuntu", State: "running", Cores: 4, MemoryMiB: 8192, DiskGiB: 40}
}

type testingTB interface {
	Helper()
	Fatal(...any)
}

func testCredential(t testingTB) executorCredential {
	t.Helper()
	_, caKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KubePhos test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, caKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	_, clientKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientTemplate := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "KubePhos control plane"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	clientDER, err := x509.CreateCertificate(rand.Reader, clientTemplate, ca, clientKey.Public(), caKey)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(clientKey)
	if err != nil {
		t.Fatal(err)
	}
	return executorCredential{
		APIVersion: artifactAPI, Kind: "OCIExecutorCredential",
		Metadata: executorCredentialMetadata{Name: "executor-client", Role: "client"},
		Spec: executorCredentialSpec{
			CA:          string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})),
			Certificate: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER})),
			PrivateKey:  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})),
		},
	}
}

type fakeRunner struct {
	lock      sync.Mutex
	installed bool
}

func (runner *fakeRunner) Run(_ context.Context, _ machine, _ machineAccess, command string) (string, error) {
	runner.lock.Lock()
	defer runner.lock.Unlock()
	switch {
	case strings.Contains(command, "Ubuntu 24.04 is required"):
		if runner.installed {
			return "", errors.New("executor already exists")
		}
		return "", nil
	case strings.HasPrefix(command, "sudo -n true && if test -e"):
		return "", nil
	case strings.Contains(command, "dockerd-rootless-setuptool.sh install"):
		runner.installed = true
		credential := testCredentialForFake()
		return "installation complete\n" + caMarker + base64.StdEncoding.EncodeToString([]byte(credential.Spec.CA)) + "\n" + certMarker + base64.StdEncoding.EncodeToString([]byte(credential.Spec.Certificate)) + "\n" + keyMarker + base64.StdEncoding.EncodeToString([]byte(credential.Spec.PrivateKey)), nil
	case strings.Contains(command, "the executor ownership marker does not match"):
		if !runner.installed {
			return "", errors.New("executor is absent")
		}
		return "", nil
	case strings.HasPrefix(command, "if test ! -e"):
		runner.installed = false
		return "", nil
	case strings.HasPrefix(command, "executor_home="):
		if runner.installed {
			return "", errors.New("managed resources remain")
		}
		return "", nil
	default:
		return "", errors.New("unexpected command")
	}
}

func testCredentialForFake() executorCredential {
	testingT := &fakeTestingT{}
	return testCredential(testingT)
}

type fakeTestingT struct{}

func (*fakeTestingT) Helper() {}
func (*fakeTestingT) Fatal(args ...any) {
	panic(args)
}

func discardLog(string, string) error {
	return nil
}
