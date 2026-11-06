package applicationdeployer

import (
	"gopkg.in/yaml.v3"
	"strings"
	"testing"
)

func TestProxyResourcesAreSeparateFromApplicationContainerResources(t *testing.T) {
	manifest := `kind: Deployment
metadata: {name: api}
spec:
 template:
  metadata: {annotations: {existing: value}}
  spec:
   containers: [{name: app, image: app, resources: {requests: {cpu: 500m, memory: 512Mi}, limits: {cpu: '1'}}}]
`
	workloads := []workloadTarget{{ID: "api", Kind: "Deployment", Name: "api"}}
	unchanged, err := configureProxyResources(manifest, workloads, nil)
	if err != nil || unchanged != manifest {
		t.Fatal("changed an unspecified manifest", err)
	}
	result, err := configureProxyResources(manifest, workloads, map[string]any{"cpuRequest": "100m", "memoryLimit": "256Mi"})
	if err != nil {
		t.Fatal(err)
	}
	var resource map[string]any
	_ = yaml.Unmarshal([]byte(result), &resource)
	spec := resource["spec"].(map[string]any)["template"].(map[string]any)
	annotations := spec["metadata"].(map[string]any)["annotations"].(map[string]any)
	if annotations["sidecar.istio.io/proxyCPU"] != "100m" || annotations["sidecar.istio.io/proxyMemoryLimit"] != "256Mi" || annotations["existing"] != "value" {
		t.Fatal(result)
	}
	if _, exists := annotations["sidecar.istio.io/proxyCPULimit"]; exists {
		t.Fatal("created an unspecified CPU limit")
	}
	app := spec["spec"].(map[string]any)["containers"].([]any)[0].(map[string]any)
	if app["resources"].(map[string]any)["requests"].(map[string]any)["cpu"] != "500m" {
		t.Fatal("overwrote application requests", result)
	}
	result, err = configureProxyResources(manifest, workloads, map[string]any{"cpuRequest": "100m", "resourceOverrides": map[string]any{"api": map[string]any{"cpuRequest": "200m", "cpuLimit": "1"}}})
	if err != nil || !strings.Contains(result, "sidecar.istio.io/proxyCPU: 200m") || !strings.Contains(result, "sidecar.istio.io/proxyCPULimit:") {
		t.Fatal(result, err)
	}
}
func TestDisabledInjectionDoesNotReceiveProxyResources(t *testing.T) {
	manifest := "kind: Deployment\nmetadata: {name: api}\nspec: {template: {metadata: {labels: {sidecar.istio.io/inject: 'false'}}, spec: {containers: []}}}"
	result, err := configureProxyResources(manifest, []workloadTarget{{ID: "api", Kind: "Deployment", Name: "api"}}, map[string]any{"cpuRequest": "100m"})
	if err != nil || strings.Contains(result, "proxyCPU") {
		t.Fatal(result, err)
	}
	for _, values := range []map[string]any{{"cpuRequest": "2", "cpuLimit": "1"}, {"memoryLimit": "wrong"}, {"unknown": "value"}, {"resourceOverrides": map[string]any{"missing": map[string]any{"cpuLimit": "1"}}}} {
		if _, err := configureProxyResources(manifest, []workloadTarget{{ID: "api", Kind: "Deployment", Name: "api"}}, values); err == nil {
			t.Fatal("accepted invalid proxy resources", values)
		}
	}
}
