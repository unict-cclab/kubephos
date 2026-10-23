package nfs

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/schema"
)

func TestPackagedNFSSchemaMatchesRuntime(t *testing.T) {
	path := "../plugin.yaml"
	if live := os.Getenv("KUBEPHOS_TEST_NFS_SCHEMA"); live != "" {
		path = live
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var descriptor struct {
		Spec struct {
			Schema map[string]any `yaml:"configurationSchema"`
		} `yaml:"spec"`
	}
	if err := yaml.Unmarshal(raw, &descriptor); err != nil {
		t.Fatal(err)
	}
	definition, err := json.Marshal(descriptor.Spec.Schema)
	if err != nil {
		t.Fatal(err)
	}
	var packaged, runtime map[string]any
	if err := json.Unmarshal(definition, &packaged); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal((Plugin{}).Manifest().Schema, &runtime); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(packaged, runtime) {
		t.Fatal("packaged NFS schema differs from runtime")
	}
	input, _ := json.Marshal(Spec{MachineSetRef: "art_machines", MachineAccessRef: "art_access", StorageName: strings.Repeat("n", 32)})
	issues, err := schema.Validate(definition, input)
	if err != nil || len(issues) > 0 {
		t.Fatalf("NFS service input rejected: %v %+v", err, issues)
	}
	report := (Plugin{}).Validate(t.Context(), Invocation{Input: input})
	if !report.Valid {
		t.Fatalf("runtime validation failed: %+v", report)
	}
	plan, err := (Plugin{}).Plan(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Outputs[0].Name != "shared-storage-endpoint" {
		t.Fatalf("unexpected pipeline output: %+v", plan)
	}
}
