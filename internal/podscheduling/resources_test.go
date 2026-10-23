package podscheduling

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObserverIsNamespacedAndHasNoImplicitComputeResources(t *testing.T) {
	raw, err := Manifest("run", "marker", []Target{{Name: "redis", Selector: map[string]string{"app": "redis"}}})
	if err != nil {
		t.Fatal(err)
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err = json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 5 {
		t.Fatal("missing observer resources")
	}
	for _, doc := range list.Items {
		meta := doc["metadata"].(map[string]any)
		if meta["namespace"] != "run" {
			t.Fatal("resource outside run namespace")
		}
		if strings.HasPrefix(doc["kind"].(string), "Cluster") {
			t.Fatal("cluster-wide observer permission")
		}
		if doc["kind"] == "Role" {
			rules := doc["rules"].([]any)
			rule := rules[0].(map[string]any)
			verbs, _ := json.Marshal(rule["verbs"])
			if string(verbs) != `["get","list","watch"]` {
				t.Fatal("observer may mutate cluster")
			}
		}
		if doc["kind"] == "Deployment" {
			template := doc["spec"].(map[string]any)["template"].(map[string]any)
			pod := template["spec"].(map[string]any)
			container := pod["containers"].([]any)[0].(map[string]any)
			if _, exists := container["resources"]; exists {
				t.Fatal("implicit observer CPU/memory")
			}
			if template["metadata"].(map[string]any)["annotations"].(map[string]any)["sidecar.istio.io/inject"] != "false" {
				t.Fatal("observer must not receive Envoy")
			}
		}
	}
}
