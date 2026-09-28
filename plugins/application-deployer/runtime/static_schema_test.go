package applicationdeployer

import (
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
