package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validDescriptor = `apiVersion: catalog.kubephos.dev/v1alpha1
kind: Application
metadata:
  id: dev.kubephos.test-app
  name: Test app
  version: 1.0.0
spec:
  package:
    type: git
    format: plain-yaml
    repository: https://example.test/application.git
    revision: 0123456789abcdef0123456789abcdef01234567
    path: deploy
    entrypoint: application.yaml
  interface:
    group: test-app
    components:
      - id: api
        workload:
          apiVersion: apps/v1
          kind: Deployment
          name: api
        selector:
          app: api
        traits: [scalable, schedulable]
    endpoints:
      - id: http
        component: api
        service: api
        port: 8080
        protocol: http
        path: /
  valuesSchema:
    type: object
    required: [replicas]
    additionalProperties: false
    properties:
      replicas:
        type: integer
        minimum: 1
  defaults:
    replicas: 1
`

func TestParseValidDescriptor(t *testing.T) {
	application, err := Parse([]byte(validDescriptor), "imported")
	if err != nil {
		t.Fatal(err)
	}
	if application.ID != "dev.kubephos.test-app" || !strings.HasPrefix(application.Digest, "sha256:") {
		t.Fatalf("unexpected application: %#v", application)
	}
}

func TestParseValidatesApplicationTopology(t *testing.T) {
	components := func(value string) string {
		start := strings.Index(validDescriptor, "    components:\n")
		end := strings.Index(validDescriptor[start:], "    endpoints:\n") + start
		return validDescriptor[:start] + "    components:\n" + value + validDescriptor[end:]
	}
	valid := components(`      - id: api
        workload: {apiVersion: apps/v1, kind: Deployment, name: api}
        selector: {app: api}
        traits: [scalable, schedulable]
        index: 0
        dependencies: [worker]
      - id: worker
        workload: {apiVersion: apps/v1, kind: Deployment, name: worker}
        selector: {app: worker}
        traits: [scalable, schedulable]
        index: 1
`)
	if _, err := Parse([]byte(valid), "imported"); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"missing index":      strings.Replace(valid, "        index: 1\n", "", 1),
		"unknown dependency": strings.Replace(valid, "dependencies: [worker]", "dependencies: [missing]", 1),
		"backward edge":      strings.Replace(strings.Replace(valid, "index: 0", "index: 2", 1), "index: 1", "index: 0", 1),
		"cycle":              strings.Replace(strings.Replace(valid, "index: 1", "index: 0", 1), "        index: 0\n    endpoints:", "        index: 0\n        dependencies: [api]\n    endpoints:", 1),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse([]byte(raw), "imported"); err == nil {
				t.Fatalf("expected %s to be rejected", name)
			}
		})
	}
}

func TestParseRequiresValidApplicationGroup(t *testing.T) {
	missing := strings.Replace(validDescriptor, "    group: test-app\n", "", 1)
	if _, err := Parse([]byte(missing), "imported"); err == nil {
		t.Fatal("expected a missing application group to be rejected")
	}
	invalid := strings.Replace(validDescriptor, "group: test-app", "group: invalid/group", 1)
	if _, err := Parse([]byte(invalid), "imported"); err == nil {
		t.Fatal("expected an invalid application group to be rejected")
	}
}

func TestParseRejectsMutableGitRevision(t *testing.T) {
	raw := strings.Replace(validDescriptor, "0123456789abcdef0123456789abcdef01234567", "main", 1)
	if _, err := Parse([]byte(raw), "imported"); err == nil {
		t.Fatal("expected mutable revision to be rejected")
	}
}

func TestParseRejectsInvalidDefaults(t *testing.T) {
	raw := strings.Replace(validDescriptor, "replicas: 1", "replicas: 0", 1)
	if _, err := Parse([]byte(raw), "imported"); err == nil {
		t.Fatal("expected invalid defaults to be rejected")
	}
}

func TestParseValidatesDeclarativeOverlays(t *testing.T) {
	overlay := `  overlays:
    - target: {apiVersion: apps/v1, kind: Deployment, name: api}
      operations:
        - op: replace
          path: /spec/replicas
          valueFrom: /replicas
`
	raw := strings.Replace(validDescriptor, "  defaults:\n    replicas: 1", "  defaults:\n    replicas: 1\n"+overlay, 1)
	application, err := Parse([]byte(raw), "imported")
	if err != nil {
		t.Fatal(err)
	}
	if application.Descriptor == nil || !strings.Contains(string(application.Descriptor), `"overlays"`) {
		t.Fatal("validated overlay is missing from the canonical descriptor")
	}
	unsafe := strings.Replace(raw, "/spec/replicas", "/metadata/name", 1)
	if _, err := Parse([]byte(unsafe), "imported"); err == nil {
		t.Fatal("expected identity-changing overlay rejection")
	}
	unknown := strings.Replace(raw, "name: api}", "name: missing}", 1)
	if _, err := Parse([]byte(unknown), "imported"); err == nil {
		t.Fatal("expected undeclared overlay target rejection")
	}
}

func TestParseAcceptsRepositoryRoot(t *testing.T) {
	raw := strings.Replace(validDescriptor, "path: deploy", "path: .", 1)
	if _, err := Parse([]byte(raw), "imported"); err != nil {
		t.Fatal(err)
	}
}

func TestParseAcceptsGenericApplicationMaterializer(t *testing.T) {
	raw := strings.Replace(validDescriptor, "  interface:", "  materializer: io.example.application.materializer\n  materializerConfig:\n    generator: example\n  interface:", 1)
	application, err := Parse([]byte(raw), "imported")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(application.Descriptor), `"materializer":"io.example.application.materializer"`) {
		t.Fatal("materializer was not retained")
	}
}

func TestParseValidatesLoadScenarioEndpoint(t *testing.T) {
	raw := strings.Replace(validDescriptor, "  valuesSchema:", "    loadScenarios:\n      - id: default-load\n        engine: locust\n        script: load/locustfile.py\n        runtimeImage: example.test/load:1.0.0\n        targetEndpoint: missing\n  valuesSchema:", 1)
	if _, err := Parse([]byte(raw), "imported"); err == nil {
		t.Fatal("expected unknown load target endpoint to be rejected")
	}
}

func TestApplicationReferenceRoundTrip(t *testing.T) {
	value := Reference("dev.kubephos.test-app", "1.0.0")
	applicationID, version, err := ParseReference(value)
	if err != nil {
		t.Fatal(err)
	}
	if applicationID != "dev.kubephos.test-app" || version != "1.0.0" {
		t.Fatalf("unexpected reference values: %s %s", applicationID, version)
	}
}

func TestLoadDirectoryRejectsDuplicateVersion(t *testing.T) {
	root := t.TempDir()
	for _, directory := range []string{"one", "two"} {
		path := filepath.Join(root, directory)
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "application.yaml"), []byte(validDescriptor), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadDirectory(root); err == nil {
		t.Fatal("expected duplicate version to be rejected")
	}
}

func TestBuiltInCatalog(t *testing.T) {
	applications, err := LoadDirectory("../../catalog/applications")
	if err != nil {
		t.Fatal(err)
	}
	if len(applications) != 1 || applications[0].ID != "dev.kubephos.online-boutique" {
		t.Fatalf("unexpected built-in catalog: %#v", applications)
	}
	var descriptor Descriptor
	if err := json.Unmarshal(applications[0].Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Spec.Interface.Group != "onlineboutique" || len(descriptor.Spec.Interface.LoadScenarios) != 1 || descriptor.Spec.Interface.LoadScenarios[0].TargetEndpoint != "node-proxy-http" || len(descriptor.Spec.AdditionalManifests) != 1 || len(descriptor.Spec.ExcludeResources) != 2 {
		t.Fatalf("online boutique traffic contract is incomplete: %#v", descriptor.Spec)
	}
	components := map[string]Component{}
	for _, component := range descriptor.Spec.Interface.Components {
		components[component.ID] = component
	}
	if components["node-proxy"].Index == nil || *components["node-proxy"].Index != 0 || len(components["node-proxy"].Dependencies) != 1 || components["node-proxy"].Dependencies[0] != "frontend" || components["redis-cart"].Index == nil || *components["redis-cart"].Index != 3 {
		t.Fatalf("online boutique topology contract is incomplete: %#v", descriptor.Spec.Interface.Components)
	}
}
