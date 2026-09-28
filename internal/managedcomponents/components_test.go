package managedcomponents

import "testing"

func TestCatalogUsesVersionedKubePhosImages(t *testing.T) {
	values := Catalog()
	if len(values) != 3 {
		t.Fatalf("catalog size = %d, want 3", len(values))
	}
	for _, value := range values {
		if value.Name == "" || value.Version == "" || value.SourceImage == "" || value.Version == "latest" {
			t.Fatalf("invalid component %#v", value)
		}
		if got := Image("registry.internal", value.Name, value.Version); got != "registry.internal/"+Project+"/"+value.Name+":"+value.Version {
			t.Fatalf("managed image = %q", got)
		}
	}
}
