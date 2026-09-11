package plugins

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recordingContainerRunner struct {
	image   string
	command string
	network bool
}

type environmentContainerRunner struct {
	released bool
}

func (r *environmentContainerRunner) Ready(context.Context) error {
	return nil
}

func (r *environmentContainerRunner) Run(context.Context, string, string, []byte, bool, Logger) ([]byte, []string, error) {
	return nil, nil, nil
}

func (r *environmentContainerRunner) Environment(context.Context) ([]string, func(), error) {
	return []string{"KUBEPHOS_PLUGIN_RUNTIME_HOST=tcp://managed:2376"}, func() { r.released = true }, nil
}

func (r *recordingContainerRunner) Ready(context.Context) error {
	return nil
}

func (r *recordingContainerRunner) Run(_ context.Context, image, command string, _ []byte, network bool, _ Logger) ([]byte, []string, error) {
	r.image = image
	r.command = command
	r.network = network
	return []byte(`{"id":"dev.example.oci","name":"OCI example","version":"2.0.0","schema":{}}`), nil, nil
}

func TestValidatePackageChecksExecutableHandshake(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "example-plugin")
	script := `#!/bin/sh
if [ "$1" != "describe" ]; then
  exit 1
fi
printf '%s' '{"id":"dev.example.plugin","name":"Example","version":"1.2.3","schema":{},"artifactInputs":[{"type":"Input","version":"v1alpha1"}],"artifactOutputs":[{"type":"Output","version":"v1alpha1"}]}'
`
	if err := os.WriteFile(executable, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.plugin
  name: Example
  version: 1.2.3
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  runtime:
    executable: example-plugin
  artifacts:
    inputs: [{type: Input, version: v1alpha1}]
    outputs: [{type: Output, version: v1alpha1}]
`
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(descriptor), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := ValidatePackage(directory)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "dev.example.plugin" || manifest.Version != "1.2.3" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestValidatePackageRejectsIdentityMismatch(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "example-plugin")
	script := `#!/bin/sh
printf '%s' '{"id":"dev.example.other","name":"Example","version":"1.2.3","schema":{}}'
`
	if err := os.WriteFile(executable, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.plugin
  name: Example
  version: 1.2.3
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  runtime:
    executable: example-plugin
`
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(descriptor), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePackage(directory); err == nil {
		t.Fatal("expected identity mismatch")
	}
}

func TestValidatePackageRejectsUnknownDescriptorFields(t *testing.T) {
	directory := t.TempDir()
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.plugin
  name: Example
  version: 1.2.3
  unexpected: value
spec:
  protocol: v1alpha1
`
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(descriptor), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePackage(directory); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestValidatePackageUsesPinnedOCIImage(t *testing.T) {
	directory := t.TempDir()
	digest := strings.Repeat("a", 64)
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.oci
  name: OCI example
  version: 2.0.0
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  permissions: [network.egress]
  runtime:
    image: registry.example.test/kubephos/plugin@sha256:` + digest + `
`
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(descriptor), 0600); err != nil {
		t.Fatal(err)
	}
	runner := &recordingContainerRunner{}
	manifest, err := ValidatePackage(directory, runner)
	if err != nil {
		t.Fatal(err)
	}
	if runner.command != "describe" || runner.image != manifest.Runtime.Reference || !runner.network {
		t.Fatalf("unexpected invocation: %#v %#v", runner, manifest.Runtime)
	}
	if manifest.Runtime.Kind != "oci" || manifest.Runtime.Digest != "sha256:"+digest {
		t.Fatalf("unexpected runtime: %#v", manifest.Runtime)
	}
}

func TestValidatePackageRejectsMutableOCIImage(t *testing.T) {
	directory := t.TempDir()
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.oci
  name: OCI example
  version: 2.0.0
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  runtime:
    image: registry.example.test/kubephos/plugin:latest
`
	if err := os.WriteFile(filepath.Join(directory, "plugin.yaml"), []byte(descriptor), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePackage(directory, &recordingContainerRunner{}); err == nil {
		t.Fatal("expected mutable image rejection")
	}
}

func TestLoadDefinitionRejectsImportedExecutable(t *testing.T) {
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.plugin
  name: Example
  version: 1.2.3
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  runtime:
    executable: example-plugin
`
	if _, err := LoadDefinition([]byte(descriptor), nil, nil, nil); err == nil || !strings.Contains(err.Error(), "must use an OCI image") {
		t.Fatalf("expected imported executable rejection, got %v", err)
	}
}

func TestInspectDefinitionDoesNotRequireRuntime(t *testing.T) {
	digest := strings.Repeat("d", 64)
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.inspect
  name: Inspect
  version: 1.0.0
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  permissions: [network.egress]
  capabilities: [example.inspect]
  runtime:
    image: registry.example.test/plugin@sha256:` + digest + `
`
	manifest, err := InspectDefinition([]byte(descriptor))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "dev.example.inspect" || manifest.Runtime.Digest != "sha256:"+digest || len(manifest.Permissions) != 1 {
		t.Fatalf("unexpected preview: %#v", manifest)
	}
}

func TestInspectDefinitionRejectsInvalidManifestMetadata(t *testing.T) {
	digest := strings.Repeat("f", 64)
	valid := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.policy
  name: Policy
  version: git-a1b2c3-k8s1.36
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  capabilities: [example.policy]
  permissions: [network.egress]
  runtime:
    image: registry.example.test/plugin@sha256:` + digest + `
    timeout: 1h
  artifacts:
    outputs: [{type: PolicyResult, version: v1alpha1}]
`
	tests := map[string]string{
		"id":         strings.Replace(valid, "dev.example.policy", "Dev Example", 1),
		"command":    strings.Replace(valid, "describe, validate", "describe, describe, validate", 1),
		"capability": strings.Replace(valid, "[example.policy]", "[example.policy, example.policy]", 1),
		"permission": strings.Replace(valid, "[network.egress]", "[network.egress, INVALID]", 1),
		"timeout":    strings.Replace(valid, "timeout: 1h", "timeout: 25h", 1),
		"artifact":   strings.Replace(valid, "outputs: [{type: PolicyResult, version: v1alpha1}]", "outputs: [{type: PolicyResult, version: v1alpha1}, {type: PolicyResult, version: v1alpha1}]", 1),
	}
	for name, descriptor := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := InspectDefinition([]byte(descriptor)); err == nil {
				t.Fatal("expected invalid manifest rejection")
			}
		})
	}
}

func TestInspectPackageFindsRepositoryDescriptor(t *testing.T) {
	directory := t.TempDir()
	packageDirectory := filepath.Join(directory, ".kubephos")
	if err := os.Mkdir(packageDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	descriptor := `apiVersion: plugins.kubephos.io/v1alpha1
kind: Plugin
metadata:
  id: dev.example.repository
  name: Repository plugin
  version: 1.0.0
spec:
  protocol: v1alpha1
  commands: [describe, validate, plan, precheck, execute, verify, status, cancel, cleanup]
  configurationSchema: {type: object}
  runtime:
    image: registry.example.test/plugin@sha256:` + strings.Repeat("a", 64) + `
`
	if err := os.WriteFile(filepath.Join(packageDirectory, "plugin.yaml"), []byte(descriptor), 0600); err != nil {
		t.Fatal(err)
	}
	manifest, err := InspectPackage(directory)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.ID != "dev.example.repository" || manifest.Runtime.Kind != "oci" {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
}

func TestInspectPackageRejectsAmbiguousDescriptor(t *testing.T) {
	directory := t.TempDir()
	if err := os.Mkdir(filepath.Join(directory, ".kubephos"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(directory, "plugin.yaml"), filepath.Join(directory, ".kubephos", "plugin.yaml")} {
		if err := os.WriteFile(path, []byte("invalid"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := InspectPackage(directory); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestResolveSecretsHonorsDeclaredKinds(t *testing.T) {
	process := &Process{
		secretKinds: map[string]bool{"allowed": true},
		resolver: func(context.Context, string) (string, json.RawMessage, error) {
			return "allowed", json.RawMessage(`{"value":"secret"}`), nil
		},
	}
	result, err := process.resolveSecrets(context.Background(), []byte(`{"credentialRef":"cred_one","message":"safe"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || string(result["cred_one"]) != `{"value":"secret"}` {
		t.Fatalf("unexpected resolved secrets: %#v", result)
	}
}

func TestResolveSecretsRejectsUndeclaredKinds(t *testing.T) {
	process := &Process{
		secretKinds: map[string]bool{"allowed": true},
		resolver: func(context.Context, string) (string, json.RawMessage, error) {
			return "forbidden", json.RawMessage(`{}`), nil
		},
	}
	if _, err := process.resolveSecrets(context.Background(), []byte(`{"credentialRef":"cred_one"}`)); err == nil {
		t.Fatal("expected permission error")
	}
}

func TestResolveConnectionsHonorsProviderBoundary(t *testing.T) {
	process := &Process{
		manifest: Manifest{Provider: "proxmox"},
		connections: func(context.Context, string) (string, json.RawMessage, error) {
			return "proxmox", json.RawMessage(`{"endpoint":"https://pve.test"}`), nil
		},
	}
	result, err := process.resolveConnections(context.Background(), []byte(`{"connectionRef":"conn_one"}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result["conn_one"]) != `{"endpoint":"https://pve.test"}` {
		t.Fatalf("unexpected connection: %#v", result)
	}
}

func TestResolveConnectionsRejectsAnotherProvider(t *testing.T) {
	process := &Process{
		manifest: Manifest{Provider: "proxmox"},
		connections: func(context.Context, string) (string, json.RawMessage, error) {
			return "vmware", json.RawMessage(`{}`), nil
		},
	}
	if _, err := process.resolveConnections(context.Background(), []byte(`{"connectionRef":"conn_one"}`)); err == nil {
		t.Fatal("expected provider boundary error")
	}
}

func TestResolveCatalogRequiresPermission(t *testing.T) {
	process := &Process{
		catalog: func(context.Context, string) (json.RawMessage, error) {
			return json.RawMessage(`{"kind":"Application"}`), nil
		},
	}
	if _, err := process.resolveCatalog(context.Background(), []byte(`{"applicationRef":"app:dev.example.app@1.0.0"}`)); err == nil {
		t.Fatal("expected catalog permission error")
	}
	process.manifest.Permissions = []string{"catalog.read:applications"}
	result, err := process.resolveCatalog(context.Background(), []byte(`{"applicationRef":"app:dev.example.app@1.0.0"}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 {
		t.Fatalf("unexpected catalog result: %#v", result)
	}
}

func TestReferenceResolutionExcludesVerifiedArtifactContents(t *testing.T) {
	payload, err := referenceResolutionPayload([]byte(`{"step":{"input":{"machineSetRef":"art_one"},"resolvedInputs":{"machines":{"value":{"connectionRef":"conn_provider"}},"access":{"value":{"credentialRef":"cred_private"}}}}}`))
	if err != nil {
		t.Fatal(err)
	}
	connectionRefs := map[string]bool{}
	credentialRefs := map[string]bool{}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	collectRefs(value, "conn_", connectionRefs)
	collectRefs(value, "cred_", credentialRefs)
	if len(connectionRefs) != 0 || len(credentialRefs) != 0 {
		t.Fatalf("artifact values leaked into reference resolution: %s", payload)
	}
}

func TestReferenceResolutionExcludesPluginResult(t *testing.T) {
	payload, err := referenceResolutionPayload([]byte(`{"step":{"input":{"applicationRef":"app:dev.example.input@1.0.0"}},"result":{"applicationRef":"app:dev.example.output@1.0.0","credentialRef":"cred_output"}}`))
	if err != nil {
		t.Fatal(err)
	}
	applicationRefs := map[string]bool{}
	credentialRefs := map[string]bool{}
	var value any
	if err := json.Unmarshal(payload, &value); err != nil {
		t.Fatal(err)
	}
	collectRefs(value, "app:", applicationRefs)
	collectRefs(value, "cred_", credentialRefs)
	if !applicationRefs["app:dev.example.input@1.0.0"] || applicationRefs["app:dev.example.output@1.0.0"] || len(credentialRefs) != 0 {
		t.Fatalf("plugin result leaked into reference resolution: %s", payload)
	}
}

func TestBundledPluginRuntimeEnvironmentIsScopedToBuildCommands(t *testing.T) {
	directory := t.TempDir()
	executable := filepath.Join(directory, "environment-plugin")
	script := "#!/bin/sh\nprintf '{\"host\":\"%s\"}' \"${KUBEPHOS_PLUGIN_RUNTIME_HOST:-missing}\"\n"
	if err := os.WriteFile(executable, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBEPHOS_PLUGIN_RUNTIME_HOST", "tcp://ambient:2376")
	runner := &environmentContainerRunner{}
	process := &Process{manifest: Manifest{Permissions: []string{"executor.build"}}, executable: executable, timeout: time.Second, runner: runner}
	var output map[string]string
	if err := process.invoke(context.Background(), "precheck", map[string]any{}, &output, nil); err != nil {
		t.Fatal(err)
	}
	if output["host"] != "tcp://managed:2376" || !runner.released {
		t.Fatalf("managed runtime environment was not scoped correctly: %#v released=%v", output, runner.released)
	}
	process.manifest.Permissions = nil
	output = nil
	if err := process.invoke(context.Background(), "precheck", map[string]any{}, &output, nil); err != nil {
		t.Fatal(err)
	}
	if output["host"] != "missing" {
		t.Fatalf("ambient runtime environment leaked to bundled plugin: %#v", output)
	}
}

func TestScanLinesBoundsLogsAndDiagnosticTail(t *testing.T) {
	var input strings.Builder
	for index := 0; index < pluginLogEventLimit+200; index++ {
		input.WriteString("runtime line\n")
	}
	result := make(chan []string, 1)
	events := 0
	scanLines(strings.NewReader(input.String()), result, func(_, _ string) error {
		events++
		return nil
	})
	lines := <-result
	if len(lines) != pluginErrorLineLimit || events != pluginLogEventLimit+1 {
		t.Fatalf("unexpected bounded log result: %d lines, %d events", len(lines), events)
	}
}

func TestPluginErrorMessageUsesBoundedFinalCause(t *testing.T) {
	message := pluginErrorMessage([]string{"setup detail", "diagnostic detail", " final cause "}, context.DeadlineExceeded)
	if message != "final cause" {
		t.Fatalf("unexpected error message %q", message)
	}
	message = pluginErrorMessage([]string{strings.Repeat("x", pluginErrorMessageLimit+100)}, nil)
	if len(message) != pluginErrorMessageLimit {
		t.Fatalf("error message was not bounded: %d", len(message))
	}
	if fallback := pluginErrorMessage(nil, context.DeadlineExceeded); fallback != context.DeadlineExceeded.Error() {
		t.Fatalf("unexpected fallback %q", fallback)
	}
}
