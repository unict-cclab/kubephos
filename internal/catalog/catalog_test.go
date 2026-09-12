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
	if len(descriptor.Spec.Interface.LoadScenarios) != 1 || descriptor.Spec.Interface.LoadScenarios[0].TargetEndpoint != "node-proxy-http" || len(descriptor.Spec.AdditionalManifests) != 1 || len(descriptor.Spec.ExcludeResources) != 2 {
		t.Fatalf("online boutique traffic contract is incomplete: %#v", descriptor.Spec)
	}
}
