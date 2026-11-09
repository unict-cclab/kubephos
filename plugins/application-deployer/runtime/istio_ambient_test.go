package applicationdeployer

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAmbientDisablesEverySidecar(t *testing.T) {
	manifest := "apiVersion: apps/v1\nkind: Deployment\nmetadata:\n  name: api\nspec:\n  template:\n    metadata: {}\n    spec:\n      containers: []\n"
	targets := []workloadTarget{{ID: "api", Kind: "Deployment", Name: "api"}}
	on := true
	result, err := configureApplicationManifest(manifest, targets, stepInput{Spec: Spec{IstioDataPlane: istioAmbient, IstioInjection: &on}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result, "sidecar.istio.io/inject: \"false\"") {
		t.Fatalf("ambient workload can still receive a sidecar:\n%s", result)
	}
	if namespaceIstioInjection(stepInput{Spec: Spec{IstioDataPlane: istioAmbient, IstioInjection: &on}}, clusterConnection{}, nil) != "disabled" {
		t.Fatal("ambient namespace keeps sidecar injection enabled")
	}
}

func TestAmbientNamespaceUsesOneWaypoint(t *testing.T) {
	labels := map[string]string{}
	ambientNamespaceLabels(labels)
	if labels["istio.io/dataplane-mode"] != "ambient" || labels["istio.io/use-waypoint"] != ambientWaypoint || labels["istio-injection"] != "disabled" {
		t.Fatalf("unexpected ambient labels: %v", labels)
	}
	var gateway struct {
		Metadata struct {
			Name        string            `json:"name"`
			Namespace   string            `json:"namespace"`
			Labels      map[string]string `json:"labels"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
		Spec struct {
			GatewayClassName string `json:"gatewayClassName"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(waypointManifest("run", "marker"), &gateway); err != nil {
		t.Fatal(err)
	}
	if gateway.Metadata.Name != ambientWaypoint || gateway.Metadata.Namespace != "run" || gateway.Spec.GatewayClassName != "istio-waypoint" || gateway.Metadata.Labels["istio.io/waypoint-for"] != "service" || gateway.Metadata.Annotations[ownershipKey] != "marker" {
		t.Fatalf("unexpected waypoint: %+v", gateway)
	}
}

func TestIstioDataPlaneValidation(t *testing.T) {
	on := true
	for name, test := range map[string]struct {
		spec  Spec
		valid bool
	}{
		"default":            {Spec{}, true},
		"sidecar":            {Spec{IstioDataPlane: "sidecar", IstioInjection: &on}, true},
		"ambient":            {Spec{IstioDataPlane: istioAmbient}, true},
		"ambient injection":  {Spec{IstioDataPlane: istioAmbient, IstioInjection: &on}, false},
		"ambient overrides":  {Spec{IstioDataPlane: istioAmbient, IstioInjectionOverrides: map[string]bool{"api": true}}, false},
		"ambient resources":  {Spec{IstioDataPlane: istioAmbient, IstioProxyResources: map[string]any{"cpuRequest": "100m"}}, false},
		"unknown data plane": {Spec{IstioDataPlane: "proxyless"}, false},
	} {
		if err := validateIstioDataPlane(test.spec); (err == nil) != test.valid {
			t.Errorf("%s: valid=%v, error=%v", name, test.valid, err)
		}
	}
}
