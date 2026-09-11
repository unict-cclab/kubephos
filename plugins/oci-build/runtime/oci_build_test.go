package ocibuild

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type fakeBuildRunner struct {
	prechecked bool
	built      bool
	verified   bool
	cleaned    bool
}

func (runner *fakeBuildRunner) Precheck(context.Context, Spec, registryEndpoint, registryCredential, plugins.Logger) error {
	runner.prechecked = true
	return nil
}

func (runner *fakeBuildRunner) Build(_ context.Context, spec Spec, endpoint registryEndpoint, credential registryCredential, _ plugins.Logger) (ociImage, error) {
	runner.built = true
	digest := "sha256:" + strings.Repeat("a", 64)
	repository := credential.Spec.Project + "/" + spec.ImageName
	return ociImage{
		APIVersion: artifactAPI,
		Kind:       "OCIImage",
		Metadata:   ociImageMetadata{Name: spec.ImageName, Version: tag(spec)},
		Spec: ociImageSpec{
			Reference: endpoint.Spec.Host + "/" + repository + "@" + digest, Registry: endpoint.Spec.Host,
			Repository: repository, Tag: tag(spec), Digest: digest,
			Source: ociImageSource{RepositoryURL: spec.RepositoryURL, Commit: spec.Commit, ContextPath: spec.ContextPath, Dockerfile: spec.Dockerfile},
		},
	}, nil
}

func (runner *fakeBuildRunner) Verify(context.Context, ociImage, registryEndpoint, registryCredential, plugins.Logger) error {
	runner.verified = true
	return nil
}

func (runner *fakeBuildRunner) Cleanup(context.Context, Spec, registryEndpoint, registryCredential, plugins.Logger) error {
	runner.cleaned = true
	return nil
}

func TestPluginBuildLifecycleProducesImmutableImage(t *testing.T) {
	spec, endpoint, credential := validInputs()
	raw, _ := json.Marshal(spec)
	runner := &fakeBuildRunner{}
	plugin := Plugin{Runner: runner}
	report := plugin.Validate(context.Background(), Invocation{Input: raw})
	if !report.Valid {
		t.Fatalf("expected valid configuration: %#v", report)
	}
	plan, err := plugin.Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	endpointRaw, _ := json.Marshal(endpoint)
	credentialRaw, _ := json.Marshal(credential)
	step.ResolvedInputs = map[string]domain.ResolvedArtifact{
		"registry-endpoint":   {Value: endpointRaw},
		"registry-credential": {Value: credentialRaw, Sensitive: true},
	}
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || !runner.prechecked {
		t.Fatalf("unexpected precheck: %#v %v", health, err)
	}
	result, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil || !runner.built {
		t.Fatalf("unexpected build: %s %v", result, err)
	}
	health, err = plugin.Verify(context.Background(), step, result, discardLog)
	if err != nil || health.Status != domain.HealthHealthy || !runner.verified {
		t.Fatalf("unexpected verification: %#v %v", health, err)
	}
	var output buildResult
	if err := json.Unmarshal(result, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.Image.Spec.Reference, "@sha256:") || output.Image.Spec.Source.Commit != spec.Commit {
		t.Fatalf("unexpected OCI artifact: %#v", output.Image)
	}
	if err := plugin.Cleanup(context.Background(), step, result, discardLog); err != nil || !runner.cleaned {
		t.Fatalf("unexpected cleanup: %v", err)
	}
}

func TestValidateRejectsUnsafeBuildInputs(t *testing.T) {
	valid, _, _ := validInputs()
	tests := map[string]Spec{
		"mutable revision":     withSpec(valid, func(value *Spec) { value.Commit = "main" }),
		"source credentials":   withSpec(valid, func(value *Spec) { value.RepositoryURL = "https://user@example.test/repo.git" }),
		"source query":         withSpec(valid, func(value *Spec) { value.RepositoryURL += "?token=value" }),
		"context traversal":    withSpec(valid, func(value *Spec) { value.ContextPath = "../outside" }),
		"dockerfile traversal": withSpec(valid, func(value *Spec) { value.Dockerfile = "../Dockerfile" }),
		"uppercase image":      withSpec(valid, func(value *Spec) { value.ImageName = "Team/Image" }),
	}
	plugin := Plugin{}
	for name, spec := range tests {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(spec)
			if report := plugin.Validate(context.Background(), Invocation{Input: raw}); report.Valid {
				t.Fatalf("expected invalid configuration: %#v", report)
			}
		})
	}
}

func TestValidateResolvedRequiresMatchingProjectCredential(t *testing.T) {
	spec, endpoint, credential := validInputs()
	credential.Spec.Server = "other.example.test"
	if err := validateResolved(spec, endpoint, credential); err == nil {
		t.Fatal("expected credential mismatch")
	}
}

func TestValidateResolvedRequiresVerifiedTLS(t *testing.T) {
	spec, endpoint, credential := validInputs()
	endpoint.Spec.Insecure = true
	endpoint.Spec.URL = "http://harbor.example.test"
	if err := validateResolved(spec, endpoint, credential); err == nil {
		t.Fatal("expected insecure registry rejection")
	}
	_, endpoint, credential = validInputs()
	endpoint.Spec.CABundle = "not a certificate"
	if err := validateResolved(spec, endpoint, credential); err == nil {
		t.Fatal("expected invalid certificate authority rejection")
	}
}

func TestBuildPathsRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Dockerfile"), []byte("FROM scratch"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "context")); err != nil {
		t.Fatal(err)
	}
	spec, _, _ := validInputs()
	spec.ContextPath = "context"
	if _, _, err := buildPaths(root, spec); err == nil {
		t.Fatal("expected escaped context rejection")
	}
}

func TestRegistryClientMaterializesOnlyThePublicCA(t *testing.T) {
	_, endpoint, _ := validInputs()
	files, err := prepareRegistryClient(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(files.root)
	info, err := os.Stat(files.root)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("unexpected registry directory permissions: %v %v", info, err)
	}
	value, err := os.ReadFile(filepath.Join(files.certDir, "ca.crt"))
	if err != nil || string(value) != endpoint.Spec.CABundle {
		t.Fatal("registry CA was not materialized exactly")
	}
	if _, err := os.Stat(files.authFile); !os.IsNotExist(err) {
		t.Fatal("credential file must not exist before authenticated login")
	}
}

func TestGitHostPolicyDefaultsToGitHub(t *testing.T) {
	t.Setenv("KUBEPHOS_BUILD_ALLOWED_GIT_HOSTS", "")
	if err := validateGitHost("https://github.com/example/repository.git"); err != nil {
		t.Fatal(err)
	}
	if err := validateGitHost("https://github.com.evil/repository.git"); err == nil {
		t.Fatal("expected deceptive source host rejection")
	}
}

func TestLoggedPushCancellationTerminatesChildrenAndCleansState(t *testing.T) {
	directory := t.TempDir()
	program := filepath.Join(directory, "skopeo")
	pidFile := filepath.Join(directory, "child.pid")
	marker := filepath.Join(directory, "push.tmp")
	script := `#!/bin/sh
trap 'kill "$child" 2>/dev/null; wait "$child" 2>/dev/null; rm -f "$2"; exit 143' TERM
sleep 30 &
child=$!
printf '%s' "$child" > "$1"
touch "$2"
wait "$child"
`
	if err := os.WriteFile(program, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- runLoggedProgram(ctx, program, []string{pidFile, marker}, nil, discardLog)
	}()
	var child int
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		value, err := os.ReadFile(pidFile)
		if err == nil {
			child, err = strconv.Atoi(string(value))
			if err != nil {
				t.Fatal(err)
			}
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("push process did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected canceled push")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("push cancellation timed out")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("temporary push state remains: %v", err)
	}
	if err := syscall.Kill(child, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("push child remains alive: %v", err)
	}
}

func validInputs() (Spec, registryEndpoint, registryCredential) {
	spec := Spec{
		RegistryEndpointRef: "art_endpoint", RegistryCredentialRef: "art_credential",
		RepositoryURL: "https://github.com/example/repository.git", Commit: strings.Repeat("a", 40),
		ContextPath: ".", Dockerfile: "Dockerfile", ImageName: "scheduler/network-aware", TagSuffix: "k8s1.36",
	}
	var endpoint registryEndpoint
	endpoint.APIVersion = artifactAPI
	endpoint.Kind = "RegistryEndpoint"
	endpoint.Metadata.Name = "managed"
	endpoint.Metadata.Version = "v2.15.1"
	endpoint.Spec.Protocol = "oci"
	endpoint.Spec.Host = "harbor.example.test"
	endpoint.Spec.URL = "https://harbor.example.test"
	endpoint.Spec.APIURL = "https://harbor.example.test/api/v2.0"
	endpoint.Spec.CABundle = testRegistryCABundle()
	endpoint.Spec.Projects = []string{"kubephos-dev", "kubephos-releases"}
	var credential registryCredential
	credential.APIVersion = artifactAPI
	credential.Kind = "RegistryCredential"
	credential.Metadata.Name = "push"
	credential.Metadata.Role = "push"
	credential.Spec.Server = endpoint.Spec.Host
	credential.Spec.Username = "robot$build"
	credential.Spec.Password = "secret"
	credential.Spec.Project = "kubephos-dev"
	credential.Spec.Scopes = []string{"pull", "push"}
	return spec, endpoint, credential
}

func testRegistryCABundle() string {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}
	now := time.Now()
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "KubePhos registry test CA"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, private.Public(), private)
	if err != nil {
		panic(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func withSpec(value Spec, mutate func(*Spec)) Spec {
	mutate(&value)
	return value
}

func discardLog(string, string) error {
	return nil
}
