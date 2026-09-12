package plugins

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

type publicPluginSchema struct {
	Properties struct {
		Metadata struct {
			Properties map[string]struct {
				Pattern string `json:"pattern"`
			} `json:"properties"`
		} `json:"metadata"`
		Spec struct {
			Properties struct {
				Provider struct {
					Pattern string `json:"pattern"`
				} `json:"provider"`
				Commands struct {
					Items struct {
						Enum []string `json:"enum"`
					} `json:"items"`
					MinItems int `json:"minItems"`
					MaxItems int `json:"maxItems"`
				} `json:"commands"`
				Runtime struct {
					Properties map[string]json.RawMessage `json:"properties"`
					OneOf      []struct {
						Required []string `json:"required"`
					} `json:"oneOf"`
				} `json:"runtime"`
				Targeting struct {
					Properties map[string]struct {
						Pattern string `json:"pattern"`
					} `json:"properties"`
				} `json:"targeting"`
			} `json:"properties"`
		} `json:"spec"`
	} `json:"properties"`
	Definitions map[string]struct {
		Pattern string `json:"pattern"`
		Items   struct {
			Properties map[string]struct {
				Pattern string `json:"pattern"`
			} `json:"properties"`
		} `json:"items"`
	} `json:"$defs"`
}

func TestPublicPluginSchemaMatchesRuntimeContract(t *testing.T) {
	value, err := os.ReadFile("../../contracts/v1alpha1/plugin.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract publicPluginSchema
	if err := json.Unmarshal(value, &contract); err != nil {
		t.Fatal(err)
	}
	requiredCommands := []string{"describe", "validate", "plan", "precheck", "execute", "verify", "status", "cancel", "cleanup"}
	commands := contract.Properties.Spec.Properties.Commands
	if !reflect.DeepEqual(commands.Items.Enum, requiredCommands) || commands.MinItems != len(requiredCommands) || commands.MaxItems != len(requiredCommands) {
		t.Fatalf("public commands differ from runtime contract: %#v", commands)
	}
	if contract.Properties.Metadata.Properties["id"].Pattern != pluginIDExpression.String() {
		t.Fatal("public plugin id pattern differs from runtime contract")
	}
	if contract.Properties.Spec.Properties.Provider.Pattern != providerExpression.String() {
		t.Fatal("public provider pattern differs from runtime contract")
	}
	if contract.Properties.Metadata.Properties["version"].Pattern != versionExpression.String() {
		t.Fatal("public version pattern differs from runtime contract")
	}
	if contract.Definitions["manifestValue"].Pattern != capabilityExpression.String() {
		t.Fatal("public capability pattern differs from runtime contract")
	}
	if contract.Properties.Spec.Properties.Targeting.Properties["requiredTrait"].Pattern != targetTraitExpression.String() {
		t.Fatal("public target trait pattern differs from runtime contract")
	}
	artifactProperties := contract.Definitions["artifactContracts"].Items.Properties
	if artifactProperties["type"].Pattern != artifactTypeExpression.String() || artifactProperties["version"].Pattern != artifactVersionExpression.String() {
		t.Fatal("public artifact patterns differ from runtime contract")
	}
	runtime := contract.Properties.Spec.Properties.Runtime
	if _, found := runtime.Properties["executable"]; !found {
		t.Fatal("public runtime does not support bundled executables")
	}
	image, found := runtime.Properties["image"]
	if !found {
		t.Fatal("public runtime does not support OCI images")
	}
	var imageProperty struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(image, &imageProperty); err != nil {
		t.Fatal(err)
	}
	expectedImagePattern := ociNameExpression.String()[:len(ociNameExpression.String())-1] + "@sha256:[0-9a-f]{64}$"
	if imageProperty.Pattern != expectedImagePattern {
		t.Fatalf("public OCI image pattern differs from runtime contract: %s", imageProperty.Pattern)
	}
	if len(runtime.OneOf) != 2 || !reflect.DeepEqual(runtime.OneOf[0].Required, []string{"executable"}) || !reflect.DeepEqual(runtime.OneOf[1].Required, []string{"image"}) {
		t.Fatal("public runtime must require exactly one executable or image")
	}
}
