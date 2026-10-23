package redis

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/catalog"
)

func invocation(t *testing.T, values map[string]any) Invocation {
	t.Helper()
	apps, err := catalog.LoadDirectory("../../../catalog/applications/redis-cluster")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{"applicationRef": apps[0].Reference, "values": values})
	if err != nil {
		t.Fatal(err)
	}
	return Invocation{Input: raw, Catalog: map[string]json.RawMessage{apps[0].Reference: apps[0].Descriptor}}
}
func generate(t *testing.T, values map[string]any) materializerResult {
	t.Helper()
	plugin := MaterializerPlugin{}
	inv := invocation(t, values)
	if report := plugin.Validate(context.Background(), inv); !report.Valid {
		t.Fatalf("validation: %v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := plugin.Precheck(context.Background(), plan.Steps[0], nil); err != nil || health.Status != "healthy" {
		t.Fatalf("precheck: %v %v", health, err)
	}
	raw, err := plugin.Execute(context.Background(), plan.Steps[0], func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if health, err := plugin.Verify(context.Background(), plan.Steps[0], raw, nil); err != nil || health.Status != "healthy" {
		t.Fatalf("verify: %v %v", health, err)
	}
	var result materializerResult
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	return result
}
func documents(t *testing.T, result materializerResult) map[string]map[string]any {
	t.Helper()
	decoder := yaml.NewDecoder(strings.NewReader(result.ManifestSet.Spec.Content))
	resultDocs := map[string]map[string]any{}
	for {
		var doc map[string]any
		err := decoder.Decode(&doc)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if doc["metadata"].(map[string]any)["namespace"] != nil || doc["kind"] == "Namespace" {
			t.Fatal("manifest cannot be isolated")
		}
		resultDocs[doc["kind"].(string)+"/"+doc["metadata"].(map[string]any)["name"].(string)] = doc
	}
	return resultDocs
}
func TestRedisTopologyAndNativeWorkload(t *testing.T) {
	result := generate(t, nil)
	inv := invocation(t, nil)
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(inv.Catalog[result.ApplicationRef], &descriptor); err != nil {
		t.Fatal(err)
	}
	if len(descriptor.Spec.Interface.Components) != len(result.WorkloadTargets) {
		t.Fatal("catalog default topology differs from materialized nodes")
	}
	if len(result.WorkloadTargets) != 10 {
		t.Fatalf("expected paper topology, got %d", len(result.WorkloadTargets))
	}
	docs := documents(t, result)
	for _, target := range result.WorkloadTargets {
		doc := docs["StatefulSet/"+target.Name]
		if doc == nil {
			t.Fatalf("missing node %s", target.Name)
		}
		template := doc["spec"].(map[string]any)["template"].(map[string]any)
		annotations := template["metadata"].(map[string]any)["annotations"].(map[string]any)
		if annotations["sidecar.istio.io/inject"] != "false" {
			t.Fatal("Envoy injection must be disabled")
		}
		pod := template["spec"].(map[string]any)
		container := pod["containers"].([]any)[0].(map[string]any)
		security := container["securityContext"].(map[string]any)
		if security["runAsUser"] != 999 || security["runAsGroup"] != 999 || security["runAsNonRoot"] != true || security["allowPrivilegeEscalation"] != false {
			t.Fatal("Redis must start as its own user without chown on root-squashed NFS volumes")
		}
		if strings.HasPrefix(target.ID, "redis-primary") && (len(target.Dependencies) != 1 || *target.Index != 0) {
			t.Fatalf("incorrect replica dependency: %+v", target)
		}
		if strings.HasPrefix(target.ID, "redis-replica") && *target.Index != 1 {
			t.Fatal("replica must follow its primary in the topology")
		}
	}
	if result.ServiceEndpoints[0].Protocol != "tcp" || docs["Job/redis-bootstrap"] == nil {
		t.Fatal("native endpoint/bootstrap missing")
	}
	script := result.LoadScenarioSet.Spec.Scenarios[0].Script
	path := filepath.Join(t.TempDir(), "workload.py")
	if err := os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("python3", "-c", "import ast,sys; ast.parse(open(sys.argv[1]).read())", path).CombinedOutput(); err != nil {
		t.Fatalf("invalid Python: %s %v", output, err)
	}
}
func TestRedisValuesValidationAndReproducibility(t *testing.T) {
	result := generate(t, map[string]any{"primaries": 3, "replicasPerPrimary": 2, "payloadBytes": 64, "operation": "GET", "cpuLimit": "1"})
	if len(result.WorkloadTargets) != 9 || !strings.Contains(result.LoadScenarioSet.Spec.Scenarios[0].Script, `"payloadBytes":64`) {
		t.Fatal("settings were not propagated")
	}
	for _, values := range []map[string]any{{"primaries": 2}, {"replicasPerPrimary": 4}, {"operation": "UNKNOWN"}, {"keyCount": 100000, "payloadBytes": 1048576}, {"operation": "LRANGE_500", "keyCount": 100000, "listLength": 500}, {"cpuRequest": "2", "cpuLimit": "1"}, {"resourceOverrides": map[string]any{"unknown": map[string]any{"cpuRequest": "1"}}}} {
		if report := (MaterializerPlugin{}).Validate(context.Background(), invocation(t, values)); report.Valid {
			t.Fatalf("accepted invalid settings: %v", values)
		}
	}
	a, _ := json.Marshal(generate(t, nil))
	b, _ := json.Marshal(generate(t, nil))
	if !bytes.Equal(a, b) {
		t.Fatal("materialization is not reproducible")
	}
}

func TestRedisResourcesOnlyWhenExplicitlyConfigured(t *testing.T) {
	for _, values := range []map[string]any{nil, {"cpuLimit": "1"}, {"memoryRequest": "256Mi"}} {
		for name, doc := range documents(t, generate(t, values)) {
			if doc["kind"] != "StatefulSet" && doc["kind"] != "Job" {
				continue
			}
			pod := doc["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			container := pod["containers"].([]any)[0].(map[string]any)
			resources, exists := container["resources"]
			if values == nil || doc["kind"] == "Job" {
				if exists {
					t.Fatalf("unconfigured resources on %s: %v", name, resources)
				}
				continue
			}
			want := map[string]any{"limits": map[string]any{"cpu": "1"}}
			if values["memoryRequest"] != nil {
				want = map[string]any{"requests": map[string]any{"memory": "256Mi"}}
			}
			actualJSON, _ := json.Marshal(resources)
			wantJSON, _ := json.Marshal(want)
			if !bytes.Equal(actualJSON, wantJSON) {
				t.Fatalf("%s resources=%s want=%s", name, actualJSON, wantJSON)
			}
		}
	}
}
