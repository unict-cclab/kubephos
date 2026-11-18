package managedplatform

import (
	"slices"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/managedcomponents"
)

func chartIDs(charts []chartSpec) []string {
	ids := []string{}
	for _, chart := range charts {
		ids = append(ids, chart.ID)
	}
	return ids
}

func TestCiliumOmitsIstioAndPublishesHubble(t *testing.T) {
	selected := newSelection(Spec{}, "cilium")
	if got := chartIDs(selected.charts()); !slices.Equal(got, []string{"chaos-mesh", "cpa-operator", "loki", "alloy", "headlamp"}) {
		t.Fatalf("charts = %v", got)
	}
	capability := selected.capability("https://cluster.example.test")
	for _, component := range capability.Spec.Components {
		if component.Name == "istio" || component.Name == "kiali" {
			t.Fatalf("incompatible component: %+v", component)
		}
	}
	if !slices.ContainsFunc(capability.Spec.Endpoints, func(endpoint managedEndpoint) bool { return endpoint.Name == "hubble" && endpoint.NodePort == 31235 }) {
		t.Fatal("Hubble endpoint missing")
	}
	if slices.Contains(selected.namespaces(), istioNamespace) || strings.Contains(string(tracingManifest("test", selected.istio())), "telemetry.istio.io") {
		t.Fatal("Cilium selection kept Istio resources")
	}
}

func TestSelectionInstallsAndPublishesOnlySelectedComponents(t *testing.T) {
	selected := newSelection(Spec{Components: []string{managedcomponents.IstioAmbient, managedcomponents.Kiali}}, "")
	if got := chartIDs(selected.charts()); !slices.Equal(got, []string{"istio-base", "istiod", "kiali"}) {
		t.Fatalf("charts = %v", got)
	}
	capability := selected.capability("https://cluster.example.test")
	names := []string{}
	for _, component := range capability.Spec.Components {
		names = append(names, component.Name)
	}
	if !slices.Equal(names, []string{"istio", "mentat", "cluster-lens", "kiali"}) || len(capability.Spec.Endpoints) != 2 {
		t.Fatalf("capability = %+v", capability.Spec)
	}
	if !slices.Equal(selected.namespaces(), []string{istioNamespace, systemNamespace}) {
		t.Fatalf("namespaces = %v", selected.namespaces())
	}
	if minimal := newSelection(Spec{Components: []string{}}, ""); len(minimal.charts()) != 0 || len(minimal.releases()) != 0 {
		t.Fatalf("core-only selection installs charts: %v", chartIDs(minimal.charts()))
	}
}

// Every NodePort the platform publishes is listed as a dashboard of its catalog component.
func TestPublishedEndpointsMatchCatalogDashboards(t *testing.T) {
	ports := []int{}
	for _, component := range managedcomponents.ClusterCatalog() {
		for _, dashboard := range component.Dashboards {
			ports = append(ports, dashboard.Port)
		}
	}
	for _, endpoint := range newSelection(Spec{}, "").capability("https://cluster.example.test").Spec.Endpoints {
		if !slices.Contains(ports, endpoint.NodePort) {
			t.Fatalf("%s publishes NodePort %d, missing from the catalog dashboards", endpoint.Name, endpoint.NodePort)
		}
	}
}

func TestEveryChartIsPinned(t *testing.T) {
	for _, component := range platformComponents {
		for _, chart := range component.charts {
			if !strings.HasPrefix(chart.URL, "https://") || len(chart.Digest) != 64 {
				t.Fatalf("chart %s is not pinned", chart.ID)
			}
		}
	}
}
