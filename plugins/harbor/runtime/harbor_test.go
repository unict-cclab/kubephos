package harbor

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
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
)

func TestManagedRegistryLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner}
	spec := json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","registryName":"build-registry"}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].ArtifactInputs) != 2 || len(plan.Steps[0].Outputs) != 3 {
		t.Fatalf("unexpected plan %#v", plan)
	}
	if plan.Steps[0].Outputs[0].Type != "RegistryEndpoint" || plan.Steps[0].Outputs[0].Sensitive || plan.Steps[0].Outputs[1].Type != "RegistryCredential" || !plan.Steps[0].Outputs[1].Sensitive || !plan.Steps[0].Outputs[2].Sensitive {
		t.Fatalf("unexpected output policy %#v", plan.Steps[0].Outputs)
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
	if decoded.RegistryEndpoint.Metadata.Version != harborVersion || decoded.RegistryEndpoint.Spec.Host != "10.10.0.12" || decoded.RegistryEndpoint.Spec.URL != "https://10.10.0.12" || decoded.RegistryEndpoint.Spec.Insecure || validateCABundle(decoded.RegistryEndpoint.Spec.CABundle) != nil || decoded.RegistryPushCredential.Metadata.Role != "push" || decoded.RegistryPushCredential.Spec.Project != development || decoded.RegistryManagementCredential.Metadata.Role != "management" {
		t.Fatalf("unexpected registry result %#v", decoded)
	}
	if decoded.RegistryPushCredential.Spec.Password == "" || decoded.RegistryManagementCredential.Spec.Password == "" || decoded.RegistryPushCredential.Spec.Password == decoded.RegistryManagementCredential.Spec.Password {
		t.Fatal("registry credentials are missing or reused")
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

func TestManagedRegistryPlanDoesNotContainCredentials(t *testing.T) {
	plan, err := (Plugin{}).Plan(context.Background(), json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","registryName":"registry"}`))
	if err != nil {
		t.Fatal(err)
	}
	value, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(value), "password") || strings.Contains(string(value), "secret") {
		t.Fatalf("plan contains credential material: %s", value)
	}
}

func TestManagedRegistryAcceptsOmittedRobotSecretAndVerifiesKnownSecret(t *testing.T) {
	plugin := Plugin{Runner: &fakeRunner{omitSecret: true}}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","registryName":"registry"}`))
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	value, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RegistryPushCredential.Spec.Password == "" {
		t.Fatal("known robot secret was not preserved")
	}
	health, err := plugin.Verify(context.Background(), step, value, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("robot secret was not verified %#v %v", health, err)
	}
}

func TestManagedRegistryUsesServerGeneratedRobotSecret(t *testing.T) {
	plugin := Plugin{Runner: &fakeRunner{robotSecret: "server-generated-secret"}}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","registryName":"registry"}`))
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	value, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RegistryPushCredential.Spec.Password != "server-generated-secret" {
		t.Fatal("server-generated robot secret was not preserved")
	}
}

func TestManagedRegistryRejectsNonDedicatedTopology(t *testing.T) {
	values := testArtifacts(t)
	var machines machineSet
	if err := json.Unmarshal(values["machines"].Value, &machines); err != nil {
		t.Fatal(err)
	}
	machines.Spec.Machines = append(machines.Spec.Machines, machines.Spec.Machines[0])
	value, _ := json.Marshal(machines)
	values["machines"] = domain.ResolvedArtifact{Value: value}
	step := domain.PlanStep{Input: json.RawMessage(`{"machineSetRef":"art_machines","machineAccessRef":"art_access","registryName":"registry","marker":"one"}`), ResolvedInputs: values}
	health, err := (Plugin{Runner: &fakeRunner{}}).Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthUnhealthy {
		t.Fatalf("expected topology rejection %#v %v", health, err)
	}
}

func TestInstallerIsVersionedAndDigestVerified(t *testing.T) {
	command := installCommand("marker", "10.10.0.12", "admin-password", "database-password", "robot-secret")
	if !strings.Contains(command, "harbor-online-installer-"+harborVersion+".tgz") || !strings.Contains(command, installerDigest) || !strings.Contains(command, "sha256sum -c -") {
		t.Fatal("installer command is not pinned and digest verified")
	}
	if !strings.Contains(command, "docker-compose-v2") || !strings.Contains(command, "docker compose version") {
		t.Fatal("installer command does not validate the compose runtime")
	}
	if strings.Contains(command, "http://") || strings.Contains(command, "curl -k") || strings.Contains(command, "curl --insecure") || !strings.Contains(command, "subjectAltName=IP:10.10.0.12") || !strings.Contains(command, "--cacert "+tlsPath+"/ca.crt") || !strings.Contains(command, caMarker) {
		t.Fatal("installer command does not enforce verifiable registry TLS")
	}
}

func TestRemoteCommandsHaveValidShellSyntax(t *testing.T) {
	commands := []string{
		installPrecheckCommand(),
		installCommand("marker", "10.10.0.12", "admin-password", "database-password", "robot-secret"),
		readinessCommand("marker", "10.10.0.12", "admin-password", "robot$name", "robot-secret"),
		cleanupPrecheckCommand("marker"),
		cleanupCommand("marker"),
		cleanupVerifyCommand(),
	}
	for _, command := range commands {
		if output, err := exec.Command("sh", "-n", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("invalid command syntax: %v: %s", err, output)
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
	machines := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"MachineSet","spec":{"networkCIDR":"10.10.0.0/24","machines":[{"id":"qemu/112","name":"registry-01","address":"10.10.0.12","sshPort":22,"sshUser":"ubuntu","state":"running","cores":4,"memoryMiB":8192,"diskGiB":32}]}}`)
	access, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "MachineAccess", "spec": map[string]string{"algorithm": "ssh-ed25519", "publicKey": publicValue, "privateKey": privateValue}})
	return map[string]domain.ResolvedArtifact{"machines": {Value: machines}, "machine-access": {Value: access}}
}

type fakeRunner struct {
	lock        sync.Mutex
	installed   bool
	omitSecret  bool
	robotSecret string
}

func (runner *fakeRunner) Run(_ context.Context, _ machine, _ machineAccess, command string) (string, error) {
	runner.lock.Lock()
	defer runner.lock.Unlock()
	switch {
	case strings.Contains(command, "non-interactive sudo is unavailable"):
		if runner.installed {
			return "", errors.New("managed registry already exists")
		}
		return "", nil
	case strings.HasPrefix(command, "sudo -n true && if test -e"):
		return "", nil
	case strings.Contains(command, "harbor-online-installer-"):
		match := regexp.MustCompile(`"secret":"([0-9a-f]+)"`).FindStringSubmatch(command)
		if len(match) != 2 {
			return "", errors.New("robot secret is unavailable")
		}
		runner.installed = true
		secret := match[1]
		if runner.omitSecret {
			secret = ""
		} else if runner.robotSecret != "" {
			secret = runner.robotSecret
		}
		return "installation complete\n" + robotMarker + `{"id":7,"name":"robot$kubephos-dev+builder","secret":"` + secret + `"}` + "\n" + caMarker + base64.StdEncoding.EncodeToString(testCABundle()), nil
	case strings.Contains(command, "the registry ownership marker does not match"):
		if !runner.installed {
			return "", errors.New("registry is absent")
		}
		return "", nil
	case strings.HasPrefix(command, "if test ! -e"):
		runner.installed = false
		return "", nil
	case strings.HasPrefix(command, "test ! -e"):
		if runner.installed {
			return "", errors.New("managed resources remain")
		}
		return "", nil
	default:
		return "", errors.New("unexpected command")
	}
}

func testCABundle() []byte {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KubePhos test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if err != nil {
		panic(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
