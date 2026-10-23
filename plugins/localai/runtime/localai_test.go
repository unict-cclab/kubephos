package localai

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
	"kubephos.dev/kubephos/internal/catalog"
)

func invocation(t *testing.T, values map[string]any) Invocation {
	t.Helper()
	apps, err := catalog.LoadDirectory("../../../catalog/applications/localai")
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
		if object(doc["metadata"])["namespace"] != nil || doc["kind"] == "Namespace" {
			t.Fatal("manifest cannot be isolated")
		}
		resultDocs[doc["kind"].(string)+"/"+object(doc["metadata"])["name"].(string)] = doc
	}
	return resultDocs
}
func TestGeneratedTopologyAndSharedCache(t *testing.T) {
	result := generate(t, map[string]any{"models": []any{"Qwen2.5-1.5B-Instruct", "Qwen2.5-1.5B-Instruct"}, "initialReplicas": 2, "replicaOverrides": map[string]any{"m2": 3}})
	if len(result.WorkloadTargets) != 3 {
		t.Fatalf("unexpected targets: %d", len(result.WorkloadTargets))
	}
	docs := documents(t, result)
	for _, target := range result.WorkloadTargets {
		if !strings.Contains(strings.Join(target.Traits, ","), "deschedulable") {
			continue
		}
		doc := docs[target.Kind+"/"+target.Name]
		labels := object(object(object(object(doc["spec"])["template"])["metadata"])["labels"])
		if labels["kubephos.dev/component"] != target.ID {
			t.Errorf("deschedulable workload %s must match the managed eviction selector; labels: %v", target.ID, labels)
		}
	}
	for _, name := range []string{"m1", "m2"} {
		doc := docs["Deployment/"+name]
		pod := object(object(object(doc["spec"])["template"])["spec"])
		container := object(list(pod["containers"])[0])
		if _, exists := container["resources"]; exists {
			t.Fatal("resources must be absent by default")
		}
		if !strings.Contains(strings.Join(toStrings(list(container["args"])), " "), "--load-to-memory="+name) {
			t.Fatal("incorrect LocalAI alias")
		}
		found := false
		for _, mount := range list(container["volumeMounts"]) {
			m := object(mount)
			if m["mountPath"] == "/models/model.gguf" {
				found = true
				if m["subPath"] != "Qwen2.5-1.5B-Instruct/model.gguf" {
					t.Fatal("incorrect shared model path")
				}
			}
		}
		if !found {
			t.Fatal("model mount missing")
		}
	}
	if object(docs["Deployment/m2"]["spec"])["replicas"] != 3 {
		t.Fatal("replica override lost")
	}
	job := object(object(object(docs["Job/cache-prefetch"]["spec"])["template"])["spec"])
	if len(list(job["initContainers"])) != 1 {
		t.Fatal("backend must be downloaded by one job init container")
	}
	router := object(docs["ConfigMap/node-proxy-config"]["data"])["default.conf.template"].(string)
	if !strings.Contains(router, "location = / {") {
		t.Fatal("router must answer the deployer endpoint health gate without a model header")
	}
	if strings.Contains(router, "resolver ") || strings.Contains(router, "POD_NAMESPACE") || !strings.Contains(router, "server m1:80") || !strings.Contains(router, "m2 m2_backend") {
		t.Fatal("router must use generated static upstreams and Kubernetes DNS")
	}
	proxy := object(docs["ConfigMap/node-proxy-config"]["data"])
	if len(proxy) != 1 || !strings.Contains(proxy["default.conf.template"].(string), "X-Kubephos-Proxy-Node ${NODE_NAME}") || strings.Contains(result.ManifestSet.Spec.Content, "inference-router") {
		t.Fatal("node proxy must route directly and preserve source headers")
	}
	if len(result.LoadScenarioSet.Spec.Scenarios) != 1 || !strings.Contains(result.LoadScenarioSet.Spec.Scenarios[0].Script, "X-Model") {
		t.Fatal("inference load scenario missing")
	}
	if !strings.Contains(object(docs["ConfigMap/model-manifest"]["data"])["models.json"].(string), `"id": "Qwen2.5-1.5B-Instruct"`) {
		t.Fatal("physical model name lost")
	}
}
func toStrings(values []any) []string {
	out := make([]string, len(values))
	for i, v := range values {
		out[i] = v.(string)
	}
	return out
}
func TestOptionalResourcesAndScale(t *testing.T) {
	for _, values := range []map[string]any{{"cpuRequest": "500m"}, {"memoryLimit": "1Gi"}, {"cpuRequest": "500m", "cpuLimit": "1", "memoryRequest": "512Mi", "memoryLimit": "1Gi"}} {
		result := generate(t, values)
		docs := documents(t, result)
		pod := object(object(object(docs["Deployment/m1"]["spec"])["template"])["spec"])
		resources := object(object(list(pod["containers"])[0])["resources"])
		if len(resources) == 0 {
			t.Fatal("explicit resources lost")
		}
	}
	models := make([]any, 100)
	for i := range models {
		models[i] = "Qwen2.5-0.5B-Instruct"
	}
	result := generate(t, map[string]any{"models": models})
	if len(result.WorkloadTargets) != 101 {
		t.Fatal("large model topology not generated")
	}
	docs := documents(t, result)
	if docs["Deployment/m100"] == nil {
		t.Fatal("last service missing")
	}
}
func TestInvalidConfigurationAndReproducibility(t *testing.T) {
	plugin := MaterializerPlugin{}
	for _, values := range []map[string]any{{"models": []any{}}, {"models": []any{"unknown-model"}}, {"cpuRequest": "2", "cpuLimit": "1"}, {"replicaOverrides": map[string]any{"m99": 2}}, {"resourceOverrides": map[string]any{"m99": map[string]any{"cpuLimit": "1"}}}, {"cpuRequest": "1", "resourceOverrides": map[string]any{"m1": map[string]any{"cpuLimit": "500m"}}}} {
		if report := plugin.Validate(context.Background(), invocation(t, values)); report.Valid {
			t.Fatalf("invalid values accepted: %v", values)
		}
	}
	first, second := generate(t, nil), generate(t, nil)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if !bytes.Equal(a, b) {
		t.Fatal("materialization must be reproducible")
	}
}

func TestServiceResourceOverridesAndLegacyDNSSettings(t *testing.T) {
	result := generate(t, map[string]any{"cpuRequest": "250m", "memoryLimit": "2Gi", "dnsServer": "10.43.0.10", "clusterDomain": "cluster.local", "resourceOverrides": map[string]any{"m2": map[string]any{"cpuRequest": "500m"}}})
	docs := documents(t, result)
	for name, expected := range map[string]string{"m1": "250m", "m2": "500m"} {
		pod := object(object(object(docs["Deployment/"+name]["spec"])["template"])["spec"])
		resource := object(object(list(pod["containers"])[0])["resources"])
		if object(resource["requests"])["cpu"] != expected {
			t.Fatalf("incorrect override for %s", name)
		}
	}
	if strings.Contains(result.ManifestSet.Spec.Content, "resolver ") || strings.Contains(result.ManifestSet.Spec.Content, "10.43.0.10") {
		t.Fatal("generated application must not require cluster DNS settings")
	}
}
func TestExpandedModelRegistry(t *testing.T) {
	inv := invocation(t, nil)
	var descriptor catalog.Descriptor
	for _, raw := range inv.Catalog {
		if err := json.Unmarshal(raw, &descriptor); err != nil {
			t.Fatal(err)
		}
	}
	properties := descriptor.Spec.ValuesSchema["properties"].(map[string]any)
	if properties["dnsServer"] != nil || properties["clusterDomain"] != nil {
		t.Fatal("cluster DNS details must not be application settings")
	}
	available := properties["models"].(map[string]any)["items"].(map[string]any)["enum"].([]any)
	if len(available) != 9 {
		t.Fatalf("expected nine models, got %d", len(available))
	}
	for _, name := range available {
		result := generate(t, map[string]any{"models": []any{name}})
		if len(result.WorkloadTargets) != 2 {
			t.Fatal("model must produce an engine and proxy")
		}
	}
}

func TestCustomGGUFAndDirectProxy(t *testing.T) {
	inv := invocation(t, map[string]any{"models": []any{"Custom-1B"}, "initialReplicas": 3, "replicaOverrides": map[string]any{"m1": 4}})
	for ref, raw := range inv.Catalog {
		updated, _, err := catalog.WithModels(raw, []catalog.Model{{ID: "Custom-1B", URL: "https://models.example.org/custom.gguf", SHA256: strings.Repeat("a", 64), Size: 100, Backend: "llama-cpp", Quantization: "Q4_K_M"}})
		if err != nil {
			t.Fatal(err)
		}
		inv.Catalog[ref] = updated
	}
	spec, descriptor, config, err := resolveMaterializer(inv)
	if err != nil {
		t.Fatal(err)
	}
	result, _, _, err := materialize(materializerInput{ApplicationRef: spec.ApplicationRef, Values: spec.Values, Descriptor: descriptor}, config)
	if err != nil {
		t.Fatal(err)
	}
	docs := documents(t, result)
	if object(docs["Deployment/m1"]["spec"])["replicas"] != 4 {
		t.Fatal("model must use normal replica overrides")
	}
	if object(docs["PersistentVolumeClaim/model-store"]["spec"])["storageClassName"] != "shared-storage" {
		t.Fatal("managed shared storage class must be automatic")
	}
	if !strings.Contains(result.ManifestSet.Spec.Content, "https://models.example.org/custom.gguf") {
		t.Fatal("custom model URL missing")
	}
}

func TestDefaultManifestHasNoComputeResourcesOrRouter(t *testing.T) {
	result := generate(t, nil)
	for name, doc := range documents(t, result) {
		if strings.Contains(name, "inference-router") {
			t.Fatalf("obsolete router: %s", name)
		}
		spec, _ := doc["spec"].(map[string]any)
		template, _ := spec["template"].(map[string]any)
		pod, _ := template["spec"].(map[string]any)
		for _, field := range []string{"containers", "initContainers"} {
			containers, _ := pod[field].([]any)
			for _, item := range containers {
				if _, exists := object(item)["resources"]; exists {
					t.Fatalf("default compute resources on %s", name)
				}
			}
		}
		if doc["kind"] == "Deployment" && spec["replicas"] != 1 {
			t.Fatalf("default replicas: %v", spec["replicas"])
		}
	}
}
