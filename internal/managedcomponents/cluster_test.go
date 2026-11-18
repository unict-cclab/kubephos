package managedcomponents

import (
	"slices"
	"testing"
)

func TestLegacyAndUnspecifiedClustersGetEveryComponent(t *testing.T) {
	flannel := DefaultClusterComponents("")
	if len(flannel) != len(clusterCatalog) {
		t.Fatalf("flannel defaults = %v, want the whole catalog", flannel)
	}
	cilium := DefaultClusterComponents("cilium")
	for _, id := range []string{IstioSidecar, IstioAmbient, Kiali} {
		if slices.Contains(cilium, id) {
			t.Fatalf("cilium defaults include %s: %v", id, cilium)
		}
	}
	if resolved, err := ResolveClusterComponents(nil, ""); err != nil || !slices.Equal(resolved, flannel) {
		t.Fatalf("nil request = %v, %v", resolved, err)
	}
	if saved := SavedClusterComponents(nil, "cilium"); !slices.Equal(saved, cilium) {
		t.Fatalf("legacy cilium cluster = %v", saved)
	}
}

func TestSelectionAlwaysKeepsCoreInCatalogOrder(t *testing.T) {
	resolved, err := ResolveClusterComponents([]string{Headlamp, " " + ChaosMesh, ChaosMesh}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{Prometheus, MonAgent, Mentat, ClusterLens, ChaosMesh, Headlamp}
	if !slices.Equal(resolved, want) {
		t.Fatalf("resolved = %v, want %v", resolved, want)
	}
	if resolved, err := ResolveClusterComponents([]string{}, ""); err != nil || len(resolved) != 4 {
		t.Fatalf("empty request = %v, %v; want core only", resolved, err)
	}
}

func TestSelectionRejectsInvalidCombinations(t *testing.T) {
	for _, request := range []struct {
		components []string
		networking string
	}{
		{[]string{"unknown"}, ""},
		{[]string{Kiali}, ""},
		{[]string{IstioSidecar}, "cilium"},
		{[]string{IstioAmbient, Kiali}, "cilium"},
	} {
		if resolved, err := ResolveClusterComponents(request.components, request.networking); err == nil {
			t.Fatalf("accepted %v on %q networking: %v", request.components, request.networking, resolved)
		}
	}
	if _, err := ResolveClusterComponents([]string{IstioAmbient, Kiali}, ""); err != nil {
		t.Fatalf("kiali with ambient Istio rejected: %v", err)
	}
}

func TestClusterCatalogIsConsistent(t *testing.T) {
	ids := map[string]bool{}
	for _, component := range ClusterCatalog() {
		if component.ID == "" || component.Title == "" || ids[component.ID] {
			t.Fatalf("invalid or duplicate component %#v", component)
		}
		ids[component.ID] = true
	}
	for _, component := range clusterCatalog {
		for _, id := range component.RequiresAny {
			if !ids[id] {
				t.Fatalf("%s requires unknown component %s", component.ID, id)
			}
		}
	}
}
