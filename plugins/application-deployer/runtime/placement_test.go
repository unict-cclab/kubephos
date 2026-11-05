package applicationdeployer

import (
	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/placement"
	"strings"
	"testing"
)

const placementManifest = `apiVersion: apps/v1
kind: Deployment
metadata: {name: backend}
spec:
  template:
    spec:
      nodeSelector: {kubephos.dev/role: application}
      tolerations: [{key: managed, operator: Exists, effect: NoSchedule}]
      containers: [{name: app, image: example/app}]
`

func TestPlacementPreservesBaselineAndServiceModes(t *testing.T) {
	workloads := []workloadTarget{{ID: "svc", Kind: "Deployment", Name: "backend"}}
	content, err := configureApplicationPlacement(placementManifest, workloads, nil)
	if err != nil || content != placementManifest {
		t.Fatal("legacy manifest changed", err)
	}
	settings := &placement.ApplicationSettings{NodeSelector: map[string]string{"hardware": "gpu"}, Tolerations: []placement.Toleration{{Key: "gpu", Operator: "Exists"}, {Key: "managed", Operator: "Exists", Effect: "NoSchedule"}}}
	content, err = configureApplicationPlacement(placementManifest, workloads, settings)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	_ = yaml.Unmarshal([]byte(content), &manifest)
	spec := applicationPodSpec(manifest)
	selector := spec["nodeSelector"].(map[string]any)
	if selector["kubephos.dev/role"] != "application" || selector["hardware"] != "gpu" || len(spec["tolerations"].([]any)) != 2 {
		t.Fatalf("baseline lost or tolerations duplicated: %s", content)
	}
	settings.ServiceOverrides = map[string]placement.ServiceOverride{"svc": {NodeSelectorMode: "none", TolerationsMode: "none"}}
	content, err = configureApplicationPlacement(placementManifest, workloads, settings)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(content, "hardware") || strings.Contains(content, "key: gpu") || !strings.Contains(content, "managed") || !strings.Contains(content, "kubephos.dev/role: application") {
		t.Fatal(content)
	}
	settings.ServiceOverrides["svc"] = placement.ServiceOverride{NodeSelectorMode: "custom", NodeSelector: map[string]string{"disk": "ssd"}, TolerationsMode: "custom"}
	content, err = configureApplicationPlacement(placementManifest, workloads, settings)
	if err != nil || !strings.Contains(content, "disk: ssd") || strings.Contains(content, "hardware") {
		t.Fatal(content, err)
	}
	settings.ServiceOverrides = nil
	settings.NodeSelector = map[string]string{"kubephos.dev/role": "management"}
	if _, err = configureApplicationPlacement(placementManifest, workloads, settings); err == nil {
		t.Fatal("overrode baseline selector")
	}
	settings.NodeSelector = nil
	settings.ServiceOverrides = map[string]placement.ServiceOverride{"missing": {}}
	if _, err = configureApplicationPlacement(placementManifest, workloads, settings); err == nil {
		t.Fatal("accepted unknown service")
	}
}
func TestPlacementHandlesAuxiliaryPodsJobsAndCronJobs(t *testing.T) {
	for _, resource := range []string{"kind: Pod\nspec: {containers: []}", "kind: Job\nspec: {template: {spec: {containers: []}}}", "kind: CronJob\nspec: {jobTemplate: {spec: {template: {spec: {containers: []}}}}}"} {
		content, err := configureApplicationPlacement(resource, nil, &placement.ApplicationSettings{Tolerations: []placement.Toleration{{Key: "gpu", Operator: "Exists"}}})
		if err != nil || !strings.Contains(content, "key: gpu") {
			t.Fatal(content, err)
		}
	}
}
