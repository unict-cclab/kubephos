package applicationdeployer

import (
	"encoding/json"
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStaticSchemaSupportsPipelineDeploymentPhases(t *testing.T) {
	value, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Spec struct {
			ConfigurationSchema struct {
				Properties map[string]any `yaml:"properties"`
			} `yaml:"configurationSchema"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(value, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mode", "schedulerName", "schedulerInclude", "schedulerExclude", "podDisruptionBudgetMinAvailable", "controlNamespace"} {
		if _, ok := manifest.Spec.ConfigurationSchema.Properties[name]; !ok {
			t.Fatalf("static plugin schema is missing %s", name)
		}
	}
}

// The registry validates stage configuration against plugin.yaml, so it must
// accept exactly the properties the runtime manifest declares.
func TestStaticSchemaMatchesTheRuntimeManifest(t *testing.T) {
	value, err := os.ReadFile("../plugin.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var static struct {
		Spec struct {
			ConfigurationSchema struct {
				Properties map[string]any `yaml:"properties"`
			} `yaml:"configurationSchema"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(value, &static); err != nil {
		t.Fatal(err)
	}
	var runtime struct {
		Properties map[string]any `json:"properties"`
	}
	if err := json.Unmarshal(Plugin{}.Manifest().Schema, &runtime); err != nil {
		t.Fatal(err)
	}
	for name := range runtime.Properties {
		if _, ok := static.Spec.ConfigurationSchema.Properties[name]; !ok {
			t.Errorf("plugin.yaml does not accept %s, which the runtime manifest declares", name)
		}
	}
	for name := range static.Spec.ConfigurationSchema.Properties {
		if _, ok := runtime.Properties[name]; !ok {
			t.Errorf("plugin.yaml accepts %s, which the runtime manifest does not declare", name)
		}
	}
}
