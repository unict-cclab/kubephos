package applicationdeployer

import (
	"encoding/json"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/workloadresources"
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
	if err := json.Unmarshal(waypointManifest("run", "marker", workloadresources.Waypoint{}), &gateway); err != nil {
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
		"ambient waypoint":   {Spec{IstioDataPlane: istioAmbient, IstioWaypoint: map[string]any{"replicas": 2.0, "cpuRequest": "100m"}}, true},
		"bad waypoint":       {Spec{IstioDataPlane: istioAmbient, IstioWaypoint: map[string]any{"replicas": 0.0}}, false},
		"sidecar waypoint":   {Spec{IstioWaypoint: map[string]any{"replicas": 2.0}}, false},
	} {
		if err := validateIstioDataPlane(test.spec); (err == nil) != test.valid {
			t.Errorf("%s: valid=%v, error=%v", name, test.valid, err)
		}
	}
}

func TestSizedWaypointOverlaysTheGeneratedDeployment(t *testing.T) {
	settings, err := workloadresources.ParseWaypoint(map[string]any{"replicas": 3.0, "cpuRequest": "250m", "memoryLimit": "256Mi"})
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Kind  string `json:"kind"`
		Items []struct {
			Kind     string                `json:"kind"`
			Metadata struct{ Name string } `json:"metadata"`
			Data     map[string]string     `json:"data"`
			Spec     struct {
				Infrastructure struct {
					ParametersRef struct{ Kind, Name string } `json:"parametersRef"`
				} `json:"infrastructure"`
			} `json:"spec"`
		} `json:"items"`
	}
	if err := json.Unmarshal(waypointManifest("run", "marker", settings), &list); err != nil {
		t.Fatal(err)
	}
	if list.Kind != "List" || len(list.Items) != 2 || list.Items[0].Kind != "ConfigMap" || list.Items[1].Kind != "Gateway" {
		t.Fatalf("unexpected manifest: %+v", list)
	}
	if ref := list.Items[1].Spec.Infrastructure.ParametersRef; ref.Kind != "ConfigMap" || ref.Name != list.Items[0].Metadata.Name {
		t.Fatalf("waypoint does not reference its settings: %+v", ref)
	}
	var overlay struct {
		Spec struct {
			Replicas int `json:"replicas"`
			Template struct {
				Spec struct {
					Containers []struct {
						Name      string                       `json:"name"`
						Resources map[string]map[string]string `json:"resources"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(list.Items[0].Data["deployment"]), &overlay); err != nil {
		t.Fatal(err)
	}
	containers := overlay.Spec.Template.Spec.Containers
	if overlay.Spec.Replicas != 3 || len(containers) != 1 || containers[0].Name != "istio-proxy" || containers[0].Resources["requests"]["cpu"] != "250m" || containers[0].Resources["limits"]["memory"] != "256Mi" {
		t.Fatalf("unexpected deployment overlay: %+v", overlay)
	}
}
