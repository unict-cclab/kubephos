package applicationinspector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/catalog"
)

func TestInspectManifestMatchesDeclaredInterface(t *testing.T) {
	manifest := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    metadata:
      labels:
        app: api
---
apiVersion: v1
kind: Service
metadata:
  name: api
spec:
  selector:
    app: api
  ports:
    - port: 8080
`)
	descriptor := catalog.Descriptor{}
	descriptor.Spec.Interface.Components = []catalog.Component{{ID: "api", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"}, Selector: map[string]string{"app": "api"}, Traits: []string{"scalable"}}}
	descriptor.Spec.Interface.Endpoints = []catalog.Endpoint{{ID: "http", Component: "api", Service: "api", Port: 8080, Protocol: "http"}}
	workloads, endpoints, err := inspectManifest(manifest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(workloads) != 1 || len(endpoints) != 1 {
		t.Fatalf("unexpected interface: %#v %#v", workloads, endpoints)
	}
}

func TestValidateRequiresResolvedCatalogEntry(t *testing.T) {
	input, _ := json.Marshal(specification{ApplicationRef: "app:dev.example.app@1.0.0"})
	report := (Plugin{}).Validate(t.Context(), Invocation{Input: input, Catalog: map[string]json.RawMessage{}})
	if report.Valid {
		t.Fatal("expected unresolved application to fail")
	}
}

func TestMaterializeManifestExcludesAndAddsDeclaredResources(t *testing.T) {
	manifest := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    metadata:
      labels:
        app: api
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: loadgenerator
spec:
  template:
    metadata:
      labels:
        app: loadgenerator
# package footer
`)
	descriptor := catalog.Descriptor{}
	descriptor.Spec.ExcludeResources = []catalog.Workload{{APIVersion: "apps/v1", Kind: "Deployment", Name: "loadgenerator"}}
	descriptor.Spec.AdditionalManifests = []catalog.AdditionalManifest{{ID: "gateway", Content: "apiVersion: apps/v1\nkind: DaemonSet\nmetadata:\n  name: node-proxy\nspec:\n  template:\n    metadata:\n      labels:\n        app: node-proxy\n"}}
	application, err := materializeManifest(manifest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(application), "loadgenerator") || !strings.Contains(string(application), "name: api") || !strings.Contains(string(application), "name: node-proxy") {
		t.Fatalf("unexpected application manifest: %s", application)
	}
	if _, err := materializeManifest([]byte("apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\n"), descriptor); err == nil {
		t.Fatal("expected a missing exclusion to fail")
	}
}

func TestBuildLoadScenariosKeepsApplicationScriptImmutable(t *testing.T) {
	descriptor := catalog.Descriptor{}
	descriptor.Spec.Interface.LoadScenarios = []catalog.LoadScenario{{ID: "journey", Engine: "locust", Script: "load/locustfile.py", RuntimeImage: "example.test/locust:1.0.0", TargetEndpoint: "http"}}
	scenarios, err := buildLoadScenarios(map[string][]byte{"load/locustfile.py": []byte("from locust import HttpUser\n")}, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 || scenarios[0].ID != "journey" || scenarios[0].ScriptDigest == "" || !validLoadScenarios(scenarios, descriptor) {
		t.Fatalf("unexpected load scenarios: %#v", scenarios)
	}
}

func TestPlanDeclaresTypedInterfaceArtifacts(t *testing.T) {
	descriptor := catalog.Descriptor{}
	descriptor.Metadata.ID = "dev.example.app"
	descriptor.Metadata.Version = "1.0.0"
	raw, _ := json.Marshal(descriptor)
	input, _ := json.Marshal(specification{ApplicationRef: "app:dev.example.app@1.0.0"})
	plan, err := (Plugin{}).Plan(context.Background(), Invocation{Input: input, Catalog: map[string]json.RawMessage{"app:dev.example.app@1.0.0": raw}})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].Outputs) != 4 {
		t.Fatalf("unexpected typed outputs: %#v", plan.Steps)
	}
	if plan.Steps[0].Outputs[0].Type != "ManifestSet" || plan.Steps[0].Outputs[1].Type != "WorkloadTargets" || plan.Steps[0].Outputs[2].Type != "ServiceEndpoints" || plan.Steps[0].Outputs[3].Type != "LoadScenarioSet" {
		t.Fatalf("unexpected artifact types: %#v", plan.Steps[0].Outputs)
	}
}

func TestApplyOverlaysUsesValidatedApplicationValues(t *testing.T) {
	manifest := []byte(`apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  replicas: 1
  template:
    spec:
      containers:
        - name: api
          image: example.test/api:1
`)
	overlays := []catalog.Overlay{{
		Target: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"},
		Operations: []catalog.OverlayOperation{
			{Operation: "replace", Path: "/spec/replicas", ValueFrom: "/replicas"},
			{Operation: "replace", Path: "/spec/template/spec/containers/0/image", ValueFrom: "/image"},
		},
	}}
	result, err := applyOverlays(manifest, overlays, map[string]any{"replicas": 3, "image": "example.test/api:2"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "replicas: 3") || !strings.Contains(string(result), "example.test/api:2") {
		t.Fatalf("overlay values were not applied: %s", result)
	}
}

func TestResolveValuesMergesDefaultsAndRejectsUnknownSettings(t *testing.T) {
	definition := map[string]any{"type": "object", "additionalProperties": false, "required": []any{"replicas"}, "properties": map[string]any{"replicas": map[string]any{"type": "integer", "minimum": 1}}}
	values, err := resolveValues(definition, map[string]any{"replicas": 1}, map[string]any{"replicas": 4})
	if err != nil || values["replicas"] != 4 {
		t.Fatalf("unexpected resolved values %#v %v", values, err)
	}
	if _, err := resolveValues(definition, map[string]any{"replicas": 1}, map[string]any{"unknown": true}); err == nil {
		t.Fatal("expected unknown setting rejection")
	}
}
