package applicationinspector

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/catalog"
)

const resourceManifest = `apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec:
  template:
    spec:
      initContainers:
        - name: init
          resources: {requests: {cpu: 10m}}
      containers:
        - name: api
          image: example/api
          resources:
            requests: {cpu: 100m, memory: 64Mi}
            limits: {cpu: "2", memory: 256Mi, ephemeral-storage: 1Gi}
        - name: helper
          resources: {requests: {memory: 32Mi}}
`

func resourceDescriptor() catalog.Descriptor {
	d := catalog.Descriptor{}
	d.Spec.Interface.Components = []catalog.Component{{ID: "service", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: "api"}, Traits: []string{"scalable"}}}
	return d
}
func TestResourceSettingsMergeDefaultsOverridesAndPreserveOtherResources(t *testing.T) {
	result, err := applyResourceSettings([]byte(resourceManifest), resourceDescriptor(), map[string]any{"cpuRequest": "250m", "resourceOverrides": map[string]any{"service": map[string]any{"cpuRequest": "500m", "memoryLimit": "512Mi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(result, &doc); err != nil {
		t.Fatal(err)
	}
	pod := doc["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	containers := pod["containers"].([]any)
	resources := containers[0].(map[string]any)["resources"].(map[string]any)
	want := map[string]any{"requests": map[string]any{"cpu": "500m"}, "limits": map[string]any{"memory": "512Mi", "ephemeral-storage": "1Gi"}}
	if !reflect.DeepEqual(resources, want) {
		t.Fatalf("resources = %#v", resources)
	}
	helper := containers[1].(map[string]any)["resources"].(map[string]any)
	if helper["requests"].(map[string]any)["cpu"] != "500m" {
		t.Fatal("second container override missing")
	}
	if strings.Contains(string(result), "cpu: 10m") {
		t.Fatal("unconfigured init container resources retained")
	}
}
func TestResourceSettingsRejectInvalidEffectiveValuesAndUnknownServices(t *testing.T) {
	for _, values := range []map[string]any{
		{"cpuRequest": "3", "cpuLimit": "2"},
		{"memoryRequest": "64Mi", "memoryLimit": "32Mi"},
		{"cpuLimit": "invalid"},
		{"resourceOverrides": map[string]any{"missing": map[string]any{"cpuRequest": "1"}}},
		{"resourceOverrides": map[string]any{"service": map[string]any{"unknown": "1"}}},
	} {
		if _, err := applyResourceSettings([]byte(resourceManifest), resourceDescriptor(), values); err == nil {
			t.Fatalf("accepted %#v", values)
		}
	}
	if _, err := applyResourceSettings([]byte("apiVersion: v1\nkind: Service\nmetadata: {name: api}\n"), resourceDescriptor(), map[string]any{"cpuRequest": "1"}); err == nil {
		t.Fatal("missing workload accepted")
	}
}
func TestResourceSettingsWithoutValuesRemovesPackageCPUAndMemory(t *testing.T) {
	manifest := []byte(resourceManifest)
	result, err := applyResourceSettings(manifest, resourceDescriptor(), map[string]any{"resourceOverrides": map[string]any{"service": map[string]any{}}})
	if err != nil || strings.Contains(string(result), "100m") || strings.Contains(string(result), "64Mi") || strings.Contains(string(result), "256Mi") || strings.Contains(string(result), "10m") || !strings.Contains(string(result), "ephemeral-storage: 1Gi") {
		t.Fatalf("changed unspecified resources: %v", err)
	}
}

func TestAuxiliaryJobsOmitUnconfiguredComputeResources(t *testing.T) {
	manifest := resourceManifest + `
---
apiVersion: batch/v1
kind: Job
metadata: {name: seed}
spec:
  template:
    spec:
      containers:
      - name: seed
        resources: {requests: {cpu: 25m}, limits: {memory: 128Mi}}
`
	result, err := applyResourceSettings([]byte(manifest), resourceDescriptor(), map[string]any{"cpuRequest": "250m"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(result), "25m") || strings.Contains(string(result), "128Mi") || strings.Contains(string(result), "10m") {
		t.Fatalf("implicit resources retained: %s", result)
	}
	if !strings.Contains(string(result), "250m") {
		t.Fatal("explicit application request lost")
	}
}

// Every built-in application uses the same CPU/memory semantics, including its gateway.
func TestBuiltinApplicationsOmitUnconfiguredResources(t *testing.T) {
	apps, err := catalog.LoadDirectory("../../../catalog/applications")
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		t.Run(app.Reference, func(t *testing.T) {
			var descriptor catalog.Descriptor
			if err := json.Unmarshal(app.Descriptor, &descriptor); err != nil {
				t.Fatal(err)
			}
			var manifest strings.Builder
			for _, component := range descriptor.Spec.Interface.Components {
				fmt.Fprintf(&manifest, "---\napiVersion: %s\nkind: %s\nmetadata: {name: %s}\nspec:\n  template:\n    spec:\n      containers:\n      - name: app\n        resources: {requests: {cpu: 100m, memory: 64Mi}, limits: {cpu: '1', memory: 1Gi}}\n", component.Workload.APIVersion, component.Workload.Kind, component.Workload.Name)
			}
			result, err := applyResourceSettings([]byte(manifest.String()), descriptor, map[string]any{})
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(result), "resources:") {
				t.Fatalf("unconfigured CPU/memory retained: %s", result)
			}
		})
	}
}
