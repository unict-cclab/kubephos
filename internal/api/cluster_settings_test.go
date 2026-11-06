package api

import (
	"encoding/json"
	"kubephos.dev/kubephos/internal/placement"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPoolSettingsUpdateChangesOnlyMetadataAndPreservesOtherSettings(t *testing.T) {
	raw := json.RawMessage(`{"name":"test","unknownFutureSetting":"keep","applicationPools":[{"name":"edge","count":2,"zones":["a"],"capacity":{"cores":2},"labels":{"hardware":"cpu"},"taints":[{"key":"dedicated","effect":"NoSchedule"}]}]}`)
	labels := map[string]string{"hardware": "gpu"}
	empty := []placement.Taint{}
	result, members, err := changedPoolSettings(raw, []poolSettingsUpdate{{Pool: "edge", Labels: &labels, Taints: &empty}})
	if err != nil || len(members) != 1 {
		t.Fatal(string(result), members, err)
	}
	var value map[string]any
	_ = json.Unmarshal(result, &value)
	pool := value["applicationPools"].([]any)[0].(map[string]any)
	if value["unknownFutureSetting"] != "keep" || pool["count"] != float64(2) || pool["labels"].(map[string]any)["hardware"] != "gpu" || pool["taints"] != nil {
		t.Fatal(string(result))
	}
	for _, update := range []poolSettingsUpdate{{Pool: "management", Labels: &labels}, {Pool: "new", Labels: &labels}, {Pool: "edge", Labels: &map[string]string{"kubephos.dev/role": "management"}}} {
		if _, _, err := changedPoolSettings(raw, []poolSettingsUpdate{update}); err == nil {
			t.Fatal("accepted protected or unknown pool", update)
		}
	}
	if _, _, err := changedPoolSettings(raw, []poolSettingsUpdate{{Pool: "edge", Labels: &labels}, {Pool: "edge", Labels: &labels}}); err == nil {
		t.Fatal("accepted duplicate pool updates")
	}
}
func TestMultiClusterPoolSettingsAreScopedToOneMember(t *testing.T) {
	raw := json.RawMessage(`{"mode":"multi","members":[{"name":"west","applicationPools":[{"name":"apps","count":2}]},{"name":"east","applicationPools":[{"name":"apps","count":2}]}]}`)
	labels := map[string]string{"hardware": "gpu"}
	data, members, err := changedPoolSettings(raw, []poolSettingsUpdate{{Member: "east", Pool: "apps", Labels: &labels}})
	if err != nil || len(members) != 1 || members[0] != "east" {
		t.Fatal(string(data), members, err)
	}
	var value managedKubernetesClusterInput
	_ = json.Unmarshal(data, &value)
	if len(value.Members[0].ApplicationPools[0].Labels) != 0 || value.Members[1].ApplicationPools[0].Labels["hardware"] != "gpu" {
		t.Fatal(string(data))
	}
}
func TestPoolSettingsRequestUsesTheExistingEditEndpointContract(t *testing.T) {
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"updatedAt":"2026-11-05T10:00:00Z","poolSettings":[{"pool":"edge","labels":{"hardware":"gpu"},"taints":[]}]}`))
	var input clusterMaintenanceInput
	if err := decodeJSON(request, &input); err != nil || input.PoolSettings == nil || input.UpdatedAt == nil {
		t.Fatal(input, err)
	}
	request = httptest.NewRequest("POST", "/", strings.NewReader(`{"poolSettings":[{"pool":"edge","count":3}]}`))
	if err := decodeJSON(request, &input); err == nil {
		t.Fatal("metadata-only edit accepted a topology field")
	}
}
