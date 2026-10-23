package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

const validDescriptor = `apiVersion: catalog.kubephos.dev/v1alpha1
kind: Application
metadata:
  id: dev.kubephos.test-app
  name: Test app
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

func TestCatalogComputeResourcesAreOptionalWithoutDefaults(t *testing.T) {
	apps, err := LoadDirectory("../../catalog/applications")
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		t.Run(app.Reference, func(t *testing.T) {
			var descriptor Descriptor
			if err := json.Unmarshal(app.Descriptor, &descriptor); err != nil {
				t.Fatal(err)
			}
			properties := descriptor.Spec.ValuesSchema["properties"].(map[string]any)
			for _, field := range []string{"cpuRequest", "cpuLimit", "memoryRequest", "memoryLimit"} {
				if _, exists := descriptor.Spec.Defaults[field]; exists {
					t.Errorf("catalog sets a default for %s", field)
				}
				property := properties[field].(map[string]any)
				if _, exists := property["default"]; exists {
					t.Errorf("schema sets a default for %s", field)
				}
				if required, ok := descriptor.Spec.ValuesSchema["required"].([]any); ok {
					for _, name := range required {
						if name == field {
							t.Errorf("%s is required", field)
						}
					}
				}
			}
		})
	}
}

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

func TestParseAcceptsMultipleEntrypointsAndInlineLoadScript(t *testing.T) {
	raw := strings.Replace(validDescriptor, "    entrypoint: application.yaml", "    entrypoint: application.yaml\n    additionalEntrypoints: [database.yaml, worker.yaml]", 1)
	raw = strings.Replace(raw, "  valuesSchema:", `    loadScenarios:
      - id: default-load
        engine: locust
        script: kubephos/default.py
        content: |
          from locust import HttpUser
        runtimeImage: example.test/load:1.0.0
        targetEndpoint: http
  valuesSchema:`, 1)
	application, err := Parse([]byte(raw), "imported")
	if err != nil {
		t.Fatal(err)
	}
	var descriptor Descriptor
	if err := json.Unmarshal(application.Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Spec.Package.AdditionalEntrypoints) != 2 || descriptor.Spec.Interface.LoadScenarios[0].Content == "" {
		t.Fatalf("generic package extensions were not retained: %#v", descriptor.Spec)
	}
	duplicate := strings.Replace(raw, "[database.yaml, worker.yaml]", "[database.yaml, application.yaml]", 1)
	if _, err := Parse([]byte(duplicate), "imported"); err == nil {
		t.Fatal("expected duplicate package entrypoint rejection")
	}
}

func TestParseValidatesStorageClassMappings(t *testing.T) {
	raw := strings.Replace(validDescriptor, "  valuesSchema:", "  storageClassMappings:\n    - source: nfs\n      target: shared-storage\n      removeStaticVolumes: true\n  valuesSchema:", 1)
	application, err := Parse([]byte(raw), "imported")
	if err != nil {
		t.Fatal(err)
	}
	var descriptor Descriptor
	if err := json.Unmarshal(application.Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Spec.StorageClassMappings) != 1 || !descriptor.Spec.StorageClassMappings[0].RemoveStaticVolumes {
		t.Fatalf("storage class mapping was not retained: %#v", descriptor.Spec.StorageClassMappings)
	}
	duplicate := strings.Replace(raw, "  valuesSchema:", "  storageClassMappings:\n    - source: nfs\n      target: other-storage\n  valuesSchema:", 1)
	if _, err := Parse([]byte(duplicate), "imported"); err == nil {
		t.Fatal("expected duplicate source storage class rejection")
	}
	same := strings.Replace(raw, "target: shared-storage", "target: nfs", 1)
	if _, err := Parse([]byte(same), "imported"); err == nil {
		t.Fatal("expected identity storage class mapping rejection")
	}
}

func TestParseValidatesImageMappings(t *testing.T) {
	raw := strings.Replace(validDescriptor, "  valuesSchema:", "  imageMappings:\n    - source: example.test/api\n      target: example.test/api:1.2.3\n  valuesSchema:", 1)
	application, err := Parse([]byte(raw), "imported")
	if err != nil {
		t.Fatal(err)
	}
	var descriptor Descriptor
	if err := json.Unmarshal(application.Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Spec.ImageMappings) != 1 || descriptor.Spec.ImageMappings[0].Target != "example.test/api:1.2.3" {
		t.Fatalf("image mapping was not retained: %#v", descriptor.Spec.ImageMappings)
	}
	mutable := strings.Replace(raw, "example.test/api:1.2.3", "example.test/api", 1)
	if _, err := Parse([]byte(mutable), "imported"); err == nil {
		t.Fatal("expected mutable target image rejection")
	}
}

func TestApplicationReferenceRoundTrip(t *testing.T) {
	value := Reference("dev.kubephos.test-app")
	applicationID, err := ParseReference(value)
	if err != nil {
		t.Fatal(err)
	}
	if applicationID != "dev.kubephos.test-app" {
		t.Fatalf("unexpected reference value: %s", applicationID)
	}
}

func TestLoadDirectoryRejectsDuplicateID(t *testing.T) {
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
		t.Fatal("expected duplicate ID to be rejected")
	}
}

func TestBuiltInCatalog(t *testing.T) {
	applications, err := LoadDirectory("../../catalog/applications")
	if err != nil {
		t.Fatal(err)
	}
	if len(applications) != 7 {
		t.Fatalf("unexpected built-in catalog: %#v", applications)
	}
	byID := map[string]domain.CatalogApplication{}
	for _, application := range applications {
		byID[application.ID] = application
	}
	for _, applicationID := range []string{"dev.kubephos.bank-of-anthos", "dev.kubephos.bookinfo", "dev.kubephos.mubench", "dev.kubephos.localai", "dev.kubephos.online-boutique", "dev.kubephos.train-ticket", "dev.kubephos.redis-cluster"} {
		if byID[applicationID].ID == "" {
			t.Fatalf("built-in application %s is missing", applicationID)
		}
	}
	var descriptor Descriptor
	if err := json.Unmarshal(byID["dev.kubephos.online-boutique"].Descriptor, &descriptor); err != nil {
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
	if err := json.Unmarshal(byID["dev.kubephos.bookinfo"].Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Spec.Interface.Components) != 7 || descriptor.Spec.Interface.Group != "bookinfo" || descriptor.Spec.Interface.LoadScenarios[0].Content == "" {
		t.Fatalf("bookinfo contract is incomplete: %#v", descriptor.Spec)
	}
	if err := json.Unmarshal(byID["dev.kubephos.bank-of-anthos"].Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Spec.Interface.Components) != 9 || descriptor.Spec.Interface.Group != "bank-of-anthos" || len(descriptor.Spec.Package.AdditionalEntrypoints) != 9 {
		t.Fatalf("bank of anthos contract is incomplete: %#v", descriptor.Spec)
	}
	if err := json.Unmarshal(byID["dev.kubephos.train-ticket"].Descriptor, &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Spec.Interface.Components) != 64 || descriptor.Spec.Interface.Group != "train-ticket" || len(descriptor.Spec.Package.AdditionalEntrypoints) != 2 || len(descriptor.Spec.StorageClassMappings) != 1 || len(descriptor.Spec.ImageMappings) != 1 {
		t.Fatalf("train ticket contract is incomplete: %#v", descriptor.Spec)
	}
}

func TestApplicationReferenceRejectsVersion(t *testing.T) {
	if _, err := ParseReference("app:dev.kubephos.test-app@1.0.0"); err == nil {
		t.Fatal("expected versioned reference rejection")
	}
}

func TestApplicationDescriptorRejectsVersion(t *testing.T) {
	if _, err := Parse([]byte(strings.Replace(validDescriptor, "metadata:\n", "metadata:\n  version: 1.0.0\n", 1)), "imported"); err == nil {
		t.Fatal("expected version field rejection")
	}
}
