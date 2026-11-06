package applicationdeployer

import (
	"encoding/json"
	"testing"
)

func TestPlacementDefaultsMatchManagedSelectorAndCatalogTolerations(t *testing.T) {
	manifest := placementManifest + `---
kind: Job
metadata: {name: bootstrap}
spec:
 template:
  spec:
   nodeSelector: {kubephos.dev/role: management}
   tolerations: [{key: batch, operator: Exists, effect: NoSchedule}]
   containers: []
`
	targets := json.RawMessage(`[{"id":"api","kind":"Deployment","name":"backend"}]`)
	defaults, err := PlacementDefaults(manifest, targets)
	if err != nil || len(defaults) != 2 {
		t.Fatal(defaults, err)
	}
	if defaults[0].ID != "api" || defaults[0].NodeSelector["kubephos.dev/role"] != "application" || len(defaults[0].Tolerations) != 1 || defaults[0].Tolerations[0].Key != "managed" {
		t.Fatal(defaults)
	}
	if defaults[1].ID != "Job/bootstrap" || defaults[1].NodeSelector["kubephos.dev/role"] != "management" {
		t.Fatal(defaults)
	}
	// Empty templates still receive the same selector as the runtime.
	defaults, err = PlacementDefaults("kind: Deployment\nmetadata: {name: backend}\nspec: {template: {spec: {containers: []}}}", targets)
	if err != nil || defaults[0].NodeSelector["kubephos.dev/role"] != "application" || len(defaults[0].Tolerations) != 0 {
		t.Fatal(defaults, err)
	}
}
func TestPlacementDefaultsDescribeTrafficBindingWithoutInventingHostname(t *testing.T) {
	defaults, err := PlacementDefaults("kind: DaemonSet\nmetadata: {name: proxy}\nspec: {template: {spec: {containers: []}}}", json.RawMessage(`[{"id":"node-proxy","kind":"DaemonSet","name":"proxy","traits":["traffic-entrypoint"]}]`))
	if err != nil || defaults[0].NodeBinding != "application-node" {
		t.Fatal(defaults, err)
	}
	if _, ok := defaults[0].NodeSelector["kubernetes.io/hostname"]; ok {
		t.Fatal("preview invented a runtime hostname")
	}
}

func TestPlacementPreviewDoesNotModifyTheSourceManifest(t *testing.T) {
	original := "kind: Deployment\nmetadata: {name: backend}\nspec: {template: {spec: {containers: []}}}"
	defaults, err := PlacementDefaults(original, json.RawMessage(`[{"id":"api","kind":"Deployment","name":"backend"}]`))
	if err != nil || len(defaults) != 1 {
		t.Fatal(defaults, err)
	}
	raw, err := configureApplicationPlacement(original, []workloadTarget{{ID: "api", Kind: "Deployment", Name: "backend"}}, nil)
	if err != nil || raw != original {
		t.Fatal("read-only preview changed the deployment input")
	}
}
