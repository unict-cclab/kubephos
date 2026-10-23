package catalog

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func exampleModel() Model {
	return Model{ID: "Custom-1B-Instruct", URL: "https://models.example.org/model.gguf", SHA256: strings.Repeat("a", 64), Size: 1024, Backend: "llama-cpp", Quantization: "Q4_K_M", Enabled: true}
}
func TestModelValidation(t *testing.T) {
	if err := ValidateModel(exampleModel()); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Model){func(m *Model) { m.ID = "../model" }, func(m *Model) { m.URL = "file:///models/model.gguf" }, func(m *Model) { m.SHA256 = "wrong" }, func(m *Model) { m.Size = -1 }, func(m *Model) { m.Backend = "unsupported" }} {
		m := exampleModel()
		change(&m)
		if err := ValidateModel(m); err == nil {
			t.Fatalf("invalid model accepted: %v", m)
		}
	}
}
func TestModelRegistrySnapshotsAndDefaultReplacement(t *testing.T) {
	raw, err := os.ReadFile("../../catalog/applications/localai/application.yaml")
	if err != nil {
		t.Fatal(err)
	}
	app, err := Parse(raw, "built-in")
	if err != nil {
		t.Fatal(err)
	}
	seeds, err := SeedModels(app.Descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != 9 {
		t.Fatal("seed models missing")
	}
	custom := exampleModel()
	updated, digest, err := WithModels(app.Descriptor, []Model{custom})
	if err != nil {
		t.Fatal(err)
	}
	var descriptor Descriptor
	if err := json.Unmarshal(updated, &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor.Spec.Defaults["models"].([]any)[0] != custom.ID {
		t.Fatal("default must reference an enabled model")
	}
	if !strings.Contains(string(updated), custom.URL) || digest == app.Digest {
		t.Fatal("registry must be part of the immutable descriptor digest")
	}
	if _, _, err := WithModels(app.Descriptor, nil); err == nil {
		t.Fatal("empty registry accepted")
	}
}

func TestSavedRouterReplicaMigration(t *testing.T) {
	raw, err := os.ReadFile("../../catalog/applications/localai/application.yaml")
	if err != nil {
		t.Fatal(err)
	}
	app, err := Parse(raw, "built-in")
	if err != nil {
		t.Fatal(err)
	}
	var descriptor Descriptor
	_ = json.Unmarshal(app.Descriptor, &descriptor)
	values := map[string]any{"routerReplicas": 3, "storageClass": "shared-storage", "dnsServer": "10.43.0.10", "clusterDomain": "cluster.local", "replicaOverrides": map[string]any{"m1": 2, "inference-router": 2}, "resourceOverrides": map[string]any{"inference-router": map[string]any{"cpuRequest": "100m"}}}
	normalized := NormalizeApplicationValues(descriptor, values)
	overrides := normalized["replicaOverrides"].(map[string]any)
	if overrides["inference-router"] != nil || overrides["m1"] != 2 {
		t.Fatal("replicas not retained")
	}
	if normalized["routerReplicas"] != nil || normalized["storageClass"] != nil {
		t.Fatal("obsolete settings retained")
	}
	if values["replicaOverrides"].(map[string]any)["inference-router"] != 2 {
		t.Fatal("source settings mutated")
	}
}
