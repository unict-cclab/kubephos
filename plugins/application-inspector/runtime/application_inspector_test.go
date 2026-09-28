package applicationinspector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/catalog"
)

func TestInspectManifestMatchesDeclaredInterface(t *testing.T) {
	index := 2
	manifest := []byte(`# package header
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
  labels:
    group: test-app
    index: "2"
    kubephos.dev/component: api
spec:
  template:
    metadata:
      labels:
        app: api
        group: test-app
        index: "2"
        kubephos.dev/component: api
---
apiVersion: v1
kind: Service
metadata:
  name: api
  labels:
    group: test-app
spec:
  selector:
    app: api
  ports:
    - port: 8080
`)
	descriptor := catalog.Descriptor{}
	descriptor.Spec.Interface.Group = "test-app"
	descriptor.Spec.Interface.Components = []catalog.Component{{ID: "api", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"}, Selector: map[string]string{"app": "api"}, Traits: []string{"scalable"}, Index: &index}}
	descriptor.Spec.Interface.Endpoints = []catalog.Endpoint{{ID: "http", Component: "api", Service: "api", Port: 8080, Protocol: "http"}}
	workloads, endpoints, err := inspectManifest(manifest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(workloads) != 1 || len(endpoints) != 1 {
		t.Fatalf("unexpected interface: %#v %#v", workloads, endpoints)
	}
	if workloads[0].Index == nil || *workloads[0].Index != 2 {
		t.Fatalf("topology index was not propagated: %#v", workloads[0])
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
	apiIndex, proxyIndex := 1, 0
	manifest := []byte(`# package header
---
apiVersion: apps/v1
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
	descriptor.Spec.Interface.Group = "test-app"
	descriptor.Spec.Interface.Components = []catalog.Component{
		{ID: "api", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"}, Index: &apiIndex},
		{ID: "node-proxy", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "node-proxy"}, Index: &proxyIndex},
	}
	descriptor.Spec.ExcludeResources = []catalog.Workload{{APIVersion: "apps/v1", Kind: "Deployment", Name: "loadgenerator"}}
	descriptor.Spec.AdditionalManifests = []catalog.AdditionalManifest{{ID: "gateway", Content: "apiVersion: apps/v1\nkind: DaemonSet\nmetadata:\n  name: node-proxy\nspec:\n  template:\n    metadata:\n      labels:\n        app: node-proxy\n"}}
	application, err := materializeManifest(manifest, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(application), "loadgenerator") || !strings.Contains(string(application), "name: api") || !strings.Contains(string(application), "name: node-proxy") || strings.Count(string(application), "group: test-app") != 4 {
		t.Fatalf("unexpected application manifest: %s", application)
	}
	if strings.Count(string(application), `index: "1"`) != 2 || strings.Count(string(application), `index: "0"`) != 2 || strings.Count(string(application), `kubephos.dev/component: api`) != 2 || strings.Count(string(application), `kubephos.dev/component: node-proxy`) != 2 {
		t.Fatalf("topology indexes were not attached to workloads: %s", application)
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

func TestBuildLoadScenariosAcceptsInlineScript(t *testing.T) {
	descriptor := catalog.Descriptor{}
	descriptor.Spec.Interface.LoadScenarios = []catalog.LoadScenario{{ID: "journey", Engine: "locust", Script: "kubephos/journey.py", Content: "from locust import HttpUser\n", RuntimeImage: "example.test/locust:1.0.0", TargetEndpoint: "http"}}
	scenarios, err := buildLoadScenarios(map[string][]byte{"kubephos/journey.py": []byte(descriptor.Spec.Interface.LoadScenarios[0].Content)}, descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenarios) != 1 || scenarios[0].Script != descriptor.Spec.Interface.LoadScenarios[0].Content || !validLoadScenarios(scenarios, descriptor) {
		t.Fatalf("unexpected inline load scenario: %#v", scenarios)
	}
}

func TestApplyStorageClassMappingsUsesDynamicClaims(t *testing.T) {
	manifest := []byte(`apiVersion: v1
kind: PersistentVolume
metadata: {name: legacy}
spec:
  storageClassName: nfs
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata: {name: data}
spec:
  storageClassName: nfs
  accessModes: [ReadWriteMany]
  resources: {requests: {storage: 1Gi}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec:
  selector: {matchLabels: {app: api}}
  template:
    metadata: {labels: {app: api}}
    spec: {containers: [{name: api, image: example.test/api:1.0.0}]}
`)
	result, err := applyStorageClassMappings(manifest, []catalog.StorageClassMapping{{Source: "nfs", Target: "shared-storage", RemoveStaticVolumes: true}})
	if err != nil {
		t.Fatal(err)
	}
	value := string(result)
	if strings.Contains(value, "name: legacy") || !strings.Contains(value, "storageClassName: shared-storage") || !strings.Contains(value, "name: api") {
		t.Fatalf("unexpected storage-mapped manifest:\n%s", value)
	}
}

func TestApplyStorageClassMappingsRejectsDrift(t *testing.T) {
	manifest := []byte("apiVersion: v1\nkind: ConfigMap\nmetadata: {name: value}\n")
	_, err := applyStorageClassMappings(manifest, []catalog.StorageClassMapping{{Source: "nfs", Target: "shared-storage", RemoveStaticVolumes: true}})
	if err == nil || !strings.Contains(err.Error(), "did not match any PersistentVolumeClaim") {
		t.Fatalf("expected unmatched storage mapping rejection, got %v", err)
	}
}

func TestApplyImageMappingsPinsMatchingContainers(t *testing.T) {
	manifest := []byte(`apiVersion: apps/v1
kind: Deployment
metadata: {name: database}
spec:
  selector: {matchLabels: {app: database}}
  template:
    metadata: {labels: {app: database}}
    spec:
      initContainers: [{name: prepare, image: busybox:1.36}]
      containers: [{name: database, image: mongo}]
`)
	result, err := applyImageMappings(manifest, []catalog.ImageMapping{{Source: "mongo", Target: "mongo:4.0.28"}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(result), "image: mongo:4.0.28") || !strings.Contains(string(result), "image: busybox:1.36") {
		t.Fatalf("unexpected image-mapped manifest:\n%s", result)
	}
	if _, err := applyImageMappings(manifest, []catalog.ImageMapping{{Source: "missing", Target: "missing:1.0.0"}}); err == nil {
		t.Fatal("expected unmatched image mapping rejection")
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

func TestApplyReplicaSettingsUsesDefaultAndServiceOverrides(t *testing.T) {
	manifest := []byte("apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: frontend}\nspec: {replicas: 8}\n---\napiVersion: apps/v1\nkind: Deployment\nmetadata: {name: checkout}\nspec: {replicas: 8}\n")
	descriptor := catalog.Descriptor{}
	descriptor.Spec.Interface.Components = []catalog.Component{
		{ID: "frontend", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "frontend"}, Traits: []string{"scalable"}},
		{ID: "checkout", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "checkout"}, Traits: []string{"scalable"}},
	}
	result, err := applyReplicaSettings(manifest, descriptor, map[string]any{"initialReplicas": 2, "replicaOverrides": map[string]any{"checkout": 5}})
	if err != nil {
		t.Fatal(err)
	}
	value := string(result)
	if strings.Count(value, "replicas: 2") != 1 || strings.Count(value, "replicas: 5") != 1 {
		t.Fatalf("unexpected replica settings:\n%s", value)
	}
	if _, err := applyReplicaSettings(manifest, descriptor, map[string]any{"initialReplicas": 2, "replicaOverrides": map[string]any{"missing": 3}}); err == nil {
		t.Fatal("expected unknown service override to fail")
	}
}
func TestWorkloadTraitsAddsLegacyDeschedulingToStatelessTargets(t *testing.T) {
	stateless := workloadTraits([]string{"schedulable", "scalable"}, true)
	stateful := workloadTraits([]string{"schedulable", "scalable", "stateful"}, true)
	entrypoint := workloadTraits([]string{"schedulable", "scalable", "traffic-entrypoint"}, true)
	explicit := workloadTraits([]string{"schedulable", "scalable"}, false)
	if !contains(stateless, "deschedulable") || contains(stateful, "deschedulable") || contains(entrypoint, "deschedulable") || contains(explicit, "deschedulable") {
		t.Fatalf("unexpected descheduling traits: stateless=%v stateful=%v entrypoint=%v explicit=%v", stateless, stateful, entrypoint, explicit)
	}
}
