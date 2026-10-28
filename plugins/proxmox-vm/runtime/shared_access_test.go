package proxmoxvm

import (
	"context"
	"encoding/json"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestTopologySharedKeyProvisioning(t *testing.T) {
	server, _ := newTopologyServer(t, 0)
	defer server.Close()
	invocation, _ := topologyInvocation(server.URL, false)
	var spec map[string]any
	json.Unmarshal(invocation.Input, &spec)
	spec["machineAccessRef"] = "art_shared"
	invocation.Input, _ = json.Marshal(spec)
	plugin := TopologyPlugin{SSH: fakeSSHAccess{}}
	plan, err := plugin.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	found := false
	for _, input := range step.ArtifactInputs {
		if input.Name == "shared-access" && input.ArtifactID == "art_shared" && input.Type == "MachineAccess" {
			found = true
		}
	}
	if !found {
		t.Fatal("shared key input missing from provisioning plan")
	}
	private, public, err := generateSSHKey()
	if err != nil {
		t.Fatal(err)
	}
	access := machineAccess{Kind: "MachineAccess", Spec: machineAccessSpec{PrivateKey: private, PublicKey: public}}
	raw, _ := json.Marshal(access)
	step.ResolvedInputs = map[string]domain.ResolvedArtifact{"shared-access": {ID: "art_shared", Type: "MachineAccess", Sensitive: true, Value: raw}}
	actual, err := plugin.Execute(context.Background(), step, invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var result topologyResult
	if err = json.Unmarshal(actual, &result); err != nil {
		t.Fatal(err)
	}
	if result.MachineAccess.Spec.PrivateKey != private {
		t.Fatal("provisioning generated a different private key")
	}
	key, pub, err := topologyProvisioningKey(step, "art_shared")
	if err != nil || key != private || pub != public {
		t.Fatal("shared key was not preserved")
	}
	for _, artifact := range []domain.ResolvedArtifact{{ID: "art_shared", Type: "MachineAccess", Sensitive: false, Value: raw}, {ID: "art_wrong", Type: "MachineAccess", Sensitive: true, Value: raw}, {ID: "art_shared", Type: "Other", Sensitive: true, Value: raw}, {ID: "art_shared", Type: "MachineAccess", Sensitive: true, Value: json.RawMessage(`{"kind":"MachineAccess","spec":{"privateKey":"invalid"}}`)}} {
		step.ResolvedInputs["shared-access"] = artifact
		if _, _, err = topologyProvisioningKey(step, "art_shared"); err == nil {
			t.Fatal("invalid shared key artifact accepted")
		}
	}
}
