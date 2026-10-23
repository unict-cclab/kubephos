package proxmoxvm

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/schema"
)

func TestPackagedTopologySchemaAcceptsServiceMachineNames(t *testing.T) {
	raw, err := os.ReadFile("../../proxmox-topology/plugin.yaml")
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
	if err := schema.ValidateDefinition(definition); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"harbor", "nfs"} {
		t.Run(kind, func(t *testing.T) {
			input, _ := json.Marshal(map[string]any{
				"connectionRef": "conn_test", "machineTemplateRef": "art_template", "node": "pve",
				"templateVMID": 8000, "baseVMID": 999999999, "namePrefix": strings.Repeat("n", 32), "machineCount": 1,
				"machineNodes": []string{"pve-2"}, "machineNames": []string{"registry-" + kind + "-1"}, "addressStart": "10.10.0.20", "prefixLength": 24,
				"gateway": "10.10.0.1", "dnsServer": "1.1.1.1", "cores": 4, "memoryMiB": 8192,
				"diskGiB": 100, "sshUser": "ubuntu", "cleanupAfterTest": false,
			})
			issues, err := schema.Validate(definition, input)
			if err != nil || len(issues) != 0 {
				t.Fatalf("service machine rejected: %v %+v", err, issues)
			}
			var spec TopologySpec
			if err := json.Unmarshal(input, &spec); err != nil {
				t.Fatal(err)
			}
			if issue := validateTopologyValues(spec); issue != nil {
				t.Fatalf("runtime rejected boundary values: %+v", issue)
			}

		})
	}
}
