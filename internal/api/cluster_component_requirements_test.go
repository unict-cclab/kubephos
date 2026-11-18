package api

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func componentCluster(t *testing.T, spec map[string]any) domain.ManagedResource {
	t.Helper()
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	return domain.ManagedResource{Name: "research", Spec: raw}
}

func TestIstioModesNeedTheirClusterComponents(t *testing.T) {
	enabled := true
	sidecarOnly := componentCluster(t, map[string]any{"components": []string{"prometheus", "istio-sidecar"}})
	if failure := (&Server{}).validateClusterComponents(sidecarOnly, experimentConfigurationDefinitionInput{IstioDataPlane: "ambient"}); failure == nil || failure.code != "component_unavailable" {
		t.Fatalf("ambient accepted without the ambient component: %+v", failure)
	}
	if failure := (&Server{}).validateClusterComponents(sidecarOnly, experimentConfigurationDefinitionInput{IstioInjection: &enabled}); failure != nil {
		t.Fatalf("sidecar rejected: %+v", failure)
	}
	minimal := componentCluster(t, map[string]any{"components": []string{"prometheus"}})
	for _, definition := range []experimentConfigurationDefinitionInput{{IstioInjection: &enabled}, {IstioInjectionOverrides: map[string]bool{"frontend": true}}, {IstioProxyResources: map[string]any{"frontend": map[string]any{}}}} {
		if failure := (&Server{}).validateClusterComponents(minimal, definition); failure == nil {
			t.Fatalf("sidecar settings accepted without Istio: %+v", definition)
		}
	}
	if failure := (&Server{}).validateClusterComponents(minimal, experimentConfigurationDefinitionInput{IstioDataPlane: "ambient", DestinationMode: "external"}); failure != nil {
		t.Fatalf("external destination checked for Istio: %+v", failure)
	}
}

func TestLegacyClustersKeepEveryComponent(t *testing.T) {
	legacy := componentCluster(t, map[string]any{"name": "research"})
	installed := clusterComponentSet(legacy)
	for _, id := range []string{"istio-sidecar", "istio-ambient", "chaos-mesh", "cpa-operator"} {
		if !installed[id] {
			t.Fatalf("legacy cluster lost %s", id)
		}
	}
	if clusterComponentSet(componentCluster(t, map[string]any{"mode": "multi"}))["istio-sidecar"] {
		t.Fatal("multi cluster reports Istio")
	}
}

func TestSidecarInjectionDefaultsOffWithoutIstio(t *testing.T) {
	minimal := componentCluster(t, map[string]any{"components": []string{"prometheus"}})
	if injection := defaultIstioInjection(minimal, experimentConfigurationDefinitionInput{}); injection == nil || *injection {
		t.Fatalf("injection = %v", injection)
	}
	if injection := defaultIstioInjection(componentCluster(t, map[string]any{}), experimentConfigurationDefinitionInput{}); injection != nil {
		t.Fatalf("legacy cluster forced injection %v", *injection)
	}
}

func TestLegacyClusterResponsesListTheirComponents(t *testing.T) {
	legacy := withInstalledComponents(componentCluster(t, map[string]any{"mode": "multi", "members": []any{map[string]any{"name": "east"}}}))
	var spec struct {
		Components []string `json:"components"`
		Members    []struct {
			Components []string `json:"components"`
		} `json:"members"`
	}
	if err := json.Unmarshal(legacy.Spec, &spec); err != nil {
		t.Fatal(err)
	}
	if len(spec.Components) == 0 || len(spec.Members) != 1 || len(spec.Members[0].Components) == 0 || slices.Contains(spec.Components, "istio-sidecar") {
		t.Fatalf("legacy spec = %+v", spec)
	}
	selected := withInstalledComponents(componentCluster(t, map[string]any{"components": []string{"prometheus"}}))
	if !strings.Contains(string(selected.Spec), `"components":["prometheus"]`) {
		t.Fatalf("saved selection changed: %s", selected.Spec)
	}
}
