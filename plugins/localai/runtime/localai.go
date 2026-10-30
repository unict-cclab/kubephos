package localai

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/schema"
	"kubephos.dev/kubephos/internal/workloadresources"
)

const (
	materializerID      = "io.kubephos.applications.localai"
	materializerVersion = "0.4.0"
	sourceRevision      = "localai-v4.11.0"
	locustImage         = "locustio/locust:2.42.6"
)

type Invocation struct {
	Input   json.RawMessage            `json:"input"`
	Catalog map[string]json.RawMessage `json:"catalog"`
}

type MaterializerPlugin struct{}

type materializerSpec struct {
	ApplicationRef string         `json:"applicationRef"`
	Values         map[string]any `json:"values"`
}

type materializerInput struct {
	ApplicationRef string             `json:"applicationRef"`
	Values         map[string]any     `json:"values"`
	Descriptor     catalog.Descriptor `json:"descriptor"`
}

type model struct {
	ID           string `json:"id"`
	Service      string `json:"service,omitempty"`
	URL          string `json:"url"`
	SHA256       string `json:"sha256"`
	Size         int64  `json:"size"`
	Backend      string `json:"backend,omitempty"`
	Quantization string `json:"quantization,omitempty"`
}
type generatorConfig struct{ Models []model }

type materializerResult struct {
	ApplicationRef   string            `json:"applicationRef"`
	SourceRevision   string            `json:"sourceRevision"`
	ManifestDigest   string            `json:"manifestDigest"`
	ValuesDigest     string            `json:"valuesDigest"`
	ManifestSet      manifestSet       `json:"manifestSet"`
	WorkloadTargets  []workloadTarget  `json:"workloadTargets"`
	ServiceEndpoints []serviceEndpoint `json:"serviceEndpoints"`
	LoadScenarioSet  loadScenarioSet   `json:"loadScenarioSet"`
}

type manifestSet struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   manifestSetMetadata `json:"metadata"`
	Spec       manifestSetSpec     `json:"spec"`
}

type manifestSetMetadata struct {
	ApplicationRef string `json:"applicationRef"`
	Digest         string `json:"digest"`
	ValuesDigest   string `json:"valuesDigest"`
}

type manifestSetSpec struct {
	Renderer string         `json:"renderer"`
	Content  string         `json:"content"`
	Source   manifestSource `json:"source"`
}

type manifestSource struct {
	Type       string `json:"type"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Path       string `json:"path"`
	Entrypoint string `json:"entrypoint"`
}

type workloadTarget struct {
	ID           string            `json:"id"`
	APIVersion   string            `json:"apiVersion"`
	Kind         string            `json:"kind"`
	Name         string            `json:"name"`
	Selector     map[string]string `json:"selector"`
	Traits       []string          `json:"traits"`
	Index        *int              `json:"index,omitempty"`
	Dependencies []string          `json:"dependencies,omitempty"`
}

type serviceEndpoint struct {
	ID        string `json:"id"`
	Component string `json:"component"`
	Service   string `json:"service"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Path      string `json:"path,omitempty"`
}

type loadScenarioSet struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Metadata   loadScenarioSetMetadata `json:"metadata"`
	Spec       loadScenarioSetSpec     `json:"spec"`
}

type loadScenarioSetMetadata struct {
	ApplicationRef string `json:"applicationRef"`
	Digest         string `json:"digest"`
}

type loadScenarioSetSpec struct {
	Scenarios []loadScenario `json:"scenarios"`
}

type loadScenario struct {
	ID             string `json:"id"`
	Engine         string `json:"engine"`
	TargetEndpoint string `json:"targetEndpoint"`
	RuntimeImage   string `json:"runtimeImage"`
	Script         string `json:"script"`
	ScriptDigest   string `json:"scriptDigest"`
}

func (MaterializerPlugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: materializerID, Name: "LocalAI application materializer", Version: materializerVersion,
		Description:     "Materializes configurable LocalAI application resources into standard artifacts.",
		Schema:          json.RawMessage(`{"type":"object","required":["applicationRef"],"additionalProperties":false,"properties":{"applicationRef":{"type":"string","title":"Application","format":"kubephos-application-ref"},"values":{"type":"object","title":"Application settings","x-kubephos-schema-from-application":"applicationRef"}}}`),
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ManifestSet", Version: "v1alpha1"}, {Type: "WorkloadTargets", Version: "v1alpha1"}, {Type: "ServiceEndpoints", Version: "v1alpha1"}, {Type: "LoadScenarioSet", Version: "v1alpha1"}},
		Capabilities:    []string{"applications.materialize"},
		Permissions:     []string{"catalog.read:applications"},
	}
}

func (MaterializerPlugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	if err := ctx.Err(); err != nil {
		return invalid(err.Error())
	}
	_, _, config, err := resolveMaterializer(invocation)
	if err != nil {
		return invalid(err.Error())
	}
	return valid(fmt.Sprintf("Resolved %d model services with shared cache and inference routing.", len(config.Models)))
}

func (MaterializerPlugin) Plan(ctx context.Context, invocation Invocation) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	spec, descriptor, _, err := resolveMaterializer(invocation)
	if err != nil {
		return domain.Plan{}, err
	}
	raw, err := json.Marshal(materializerInput{ApplicationRef: spec.ApplicationRef, Values: spec.Values, Descriptor: descriptor})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: materializerID, Steps: []domain.PlanStep{{ID: "materialize-localai", Name: "Generate and verify LocalAI resources", Input: raw, Outputs: []domain.ArtifactOutput{{Name: "manifest-set", Type: "ManifestSet", Version: "v1alpha1", MediaType: "application/json", Source: "/manifestSet"}, {Name: "workload-targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json", Source: "/workloadTargets"}, {Name: "service-endpoints", Type: "ServiceEndpoints", Version: "v1alpha1", MediaType: "application/json", Source: "/serviceEndpoints"}, {Name: "load-scenario-set", Type: "LoadScenarioSet", Version: "v1alpha1", MediaType: "application/json", Source: "/loadScenarioSet"}}}}}, nil
}

func (MaterializerPlugin) Precheck(ctx context.Context, step domain.PlanStep, _ plugins.Logger) (domain.HealthReport, error) {
	input, config, err := decodeMaterializerStep(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if _, _, _, err := materialize(input, config); err != nil {
		return unhealthy(err.Error()), nil
	}
	if err := ctx.Err(); err != nil {
		return domain.HealthReport{}, err
	}
	return healthy("Generated manifests, targets and load scenario are consistent"), nil
}

func (MaterializerPlugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, config, err := decodeMaterializerStep(step)
	if err != nil {
		return nil, err
	}
	result, _, _, err := materialize(input, config)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := log("info", fmt.Sprintf("Materialized %d LocalAI services and the source-aware node proxy", len(config.Models))); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (MaterializerPlugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, _ plugins.Logger) (domain.HealthReport, error) {
	input, config, err := decodeMaterializerStep(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	expected, _, _, err := materialize(input, config)
	if err != nil {
		return domain.HealthReport{}, err
	}
	var actual materializerResult
	if err := json.Unmarshal(raw, &actual); err != nil {
		return unhealthy("Materialization result is invalid"), nil
	}
	expectedRaw, _ := json.Marshal(expected)
	actualRaw, _ := json.Marshal(actual)
	if string(expectedRaw) != string(actualRaw) {
		return unhealthy("Materialization result does not match the validated application"), nil
	}
	if err := ctx.Err(); err != nil {
		return domain.HealthReport{}, err
	}
	return healthy("LocalAI application resources are reproducible and internally consistent"), nil
}

func (MaterializerPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func clone(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}

func digest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(hash[:]), nil
}

func valid(messages ...string) domain.ValidationReport {
	issues := make([]domain.ValidationIssue, 0, len(messages))
	for _, message := range messages {
		issues = append(issues, domain.ValidationIssue{Level: "info", Message: message})
	}
	return domain.ValidationReport{Valid: true, Issues: issues, CheckedAt: time.Now().UTC()}
}

func invalid(message string) domain.ValidationReport {
	return domain.ValidationReport{Valid: false, Issues: []domain.ValidationIssue{{Level: "error", Path: "$", Message: message}}, CheckedAt: time.Now().UTC()}
}

func healthy(summary string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: summary, Checks: map[string]string{"contract": "valid"}}
}

func unhealthy(summary string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{"contract": "invalid"}}
}

//go:embed base.yaml
var baseManifest []byte

func resolveMaterializer(invocation Invocation) (materializerSpec, catalog.Descriptor, generatorConfig, error) {
	var spec materializerSpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return spec, catalog.Descriptor{}, generatorConfig{}, errors.New("configuration must be valid JSON")
	}
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(invocation.Catalog[spec.ApplicationRef], &descriptor); err != nil || descriptor.Spec.Materializer != materializerID {
		return spec, descriptor, generatorConfig{}, errors.New("catalog application is not a LocalAI generated application")
	}
	spec.Values = catalog.NormalizeApplicationValues(descriptor, spec.Values)
	values := clone(descriptor.Spec.Defaults)
	for key, value := range spec.Values {
		values[key] = value
	}
	spec.Values = values
	config, err := configuration(descriptor, values)
	return spec, descriptor, config, err
}

func decodeMaterializerStep(step domain.PlanStep) (materializerInput, generatorConfig, error) {
	var input materializerInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return input, generatorConfig{}, err
	}
	if input.ApplicationRef == "" || input.Descriptor.Spec.Materializer != materializerID {
		return input, generatorConfig{}, errors.New("materializer step is incomplete")
	}
	config, err := configuration(input.Descriptor, input.Values)
	return input, config, err
}

func configuration(descriptor catalog.Descriptor, values map[string]any) (generatorConfig, error) {
	var config generatorConfig
	definition, _ := json.Marshal(descriptor.Spec.ValuesSchema)
	raw, _ := json.Marshal(values)
	issues, err := schema.Validate(definition, raw)
	if err != nil {
		return config, err
	}
	if len(issues) > 0 {
		return config, errors.New(schema.IssueMessage(definition, issues[0]))
	}
	if _, err := workloadresources.Build(values); err != nil {
		return config, err
	}
	var registry []model
	raw, _ = json.Marshal(descriptor.Spec.MaterializerConfig["models"])
	if err := json.Unmarshal(raw, &registry); err != nil {
		return config, errors.New("model registry is invalid")
	}
	available := map[string]model{}
	for _, entry := range registry {
		available[entry.ID] = entry
	}
	raw, _ = json.Marshal(values["models"])
	var selected []string
	if err := json.Unmarshal(raw, &selected); err != nil || len(selected) < 1 || len(selected) > 100 {
		return config, errors.New("select between 1 and 100 model services")
	}
	for i, name := range selected {
		entry, ok := available[name]
		if !ok {
			return config, fmt.Errorf("model %s is not in the supported registry", name)
		}
		if entry.Backend == "" {
			entry.Backend = "llama-cpp"
		}
		if entry.Quantization == "" {
			entry.Quantization = "unknown"
		}
		if err := catalog.ValidateModel(catalog.Model{ID: entry.ID, URL: entry.URL, SHA256: entry.SHA256, Size: entry.Size, Backend: entry.Backend, Quantization: entry.Quantization}); err != nil {
			return config, err
		}

		entry.Service = fmt.Sprintf("m%d", i+1)
		config.Models = append(config.Models, entry)
	}
	if overrides, ok := values["replicaOverrides"].(map[string]any); ok {
		for name, value := range overrides {
			index, err := strconv.Atoi(strings.TrimPrefix(name, "m"))
			if err != nil || name != fmt.Sprintf("m%d", index) || index < 1 || index > len(selected) {
				return config, fmt.Errorf("replica override references unknown service %s", name)
			}
			count, err := integerValue(value)
			if err != nil || count < 1 || count > 1000 {
				return config, fmt.Errorf("replicas for %s must be between 1 and 1000", name)
			}
		}
	}
	services := []string{}
	for _, entry := range config.Models {
		services = append(services, entry.Service)
	}
	if err := workloadresources.Validate(values, services); err != nil {
		return config, err
	}

	return config, nil
}

func integerValue(value any) (int, error) {
	switch n := value.(type) {
	case int:
		return n, nil
	case float64:
		if n == float64(int(n)) {
			return int(n), nil
		}
	case json.Number:
		return strconv.Atoi(n.String())
	}
	return 0, errors.New("value must be an integer")
}

func copyObject(object map[string]any) map[string]any {
	raw, _ := json.Marshal(object)
	var copy map[string]any
	_ = json.Unmarshal(raw, &copy)
	return copy
}
func object(value any) map[string]any { return value.(map[string]any) }
func list(value any) []any            { return value.([]any) }

func materialize(input materializerInput, config generatorConfig) (materializerResult, []byte, []loadScenario, error) {
	values := input.Values
	group := input.Descriptor.Spec.Interface.Group
	decoder := yaml.NewDecoder(bytes.NewReader(baseManifest))
	var documents []map[string]any
	var engine, engineService map[string]any
	for {
		var doc map[string]any
		if err := decoder.Decode(&doc); err == io.EOF {
			break
		} else if err != nil {
			return materializerResult{}, nil, nil, err
		}
		name := object(doc["metadata"])["name"]
		if name == "m1" {
			if doc["kind"] == "Deployment" {
				engine = doc
			} else {
				engineService = doc
			}
			continue
		}
		documents = append(documents, doc)
	}
	modelConfig := map[string]any{}
	for _, doc := range documents {
		name := object(doc["metadata"])["name"]
		switch name {
		case "model-store":
			spec := object(doc["spec"])
			object(object(spec["resources"])["requests"])["storage"] = values["storageSize"]
		case "model-manifest":
			raw, _ := json.MarshalIndent(config.Models, "", "  ")
			object(doc["data"])["models.json"] = string(raw)
		case "localai-model-config":
			doc["data"] = modelConfig
		case "node-proxy-config":
			data := object(doc["data"])
			var upstreams strings.Builder
			for _, entry := range config.Models {
				fmt.Fprintf(&upstreams, "upstream %s_backend { server %s:80; keepalive 16; }\n", entry.Service, entry.Service)
			}
			upstreams.WriteString("map $http_x_model $model_backend {\n  default \"\";\n")
			for _, entry := range config.Models {
				fmt.Fprintf(&upstreams, "  %s %s_backend;\n", entry.Service, entry.Service)
			}
			upstreams.WriteString("}\n")
			data["default.conf.template"] = upstreams.String() + data["default.conf.template"].(string)

		}
	}
	workloads := make([]workloadTarget, 0, len(config.Models)+1)
	for _, entry := range config.Models {
		name := entry.Service
		deployment, service := copyObject(engine), copyObject(engineService)
		object(deployment["metadata"])["name"], object(service["metadata"])["name"] = name, name
		spec := object(deployment["spec"])
		spec["replicas"] = values["initialReplicas"]
		if overrides, ok := values["replicaOverrides"].(map[string]any); ok {
			if count, exists := overrides[name]; exists {
				spec["replicas"] = count
			}
		}
		spec["selector"] = map[string]any{"matchLabels": map[string]any{"app": name}}
		template := object(spec["template"])
		template["metadata"] = map[string]any{"labels": map[string]any{"app": name, "group": group, "index": "1", "kubephos.dev/component": name}}
		pod := object(template["spec"])
		init := object(list(pod["initContainers"])[0])
		init["command"] = []any{"python3", "-u", "/scripts/wait.py", name}
		container := object(list(pod["containers"])[0])
		args := list(container["args"])
		for i, arg := range args {
			if strings.HasPrefix(fmt.Sprint(arg), "--load-to-memory=") {
				args[i] = "--load-to-memory=" + name
			}
		}
		resources, err := workloadresources.ForService(values, name)
		if err != nil {
			return materializerResult{}, nil, nil, err
		}
		if len(resources) > 0 {
			container["resources"] = resources
		}
		for _, mount := range list(container["volumeMounts"]) {
			m := object(mount)
			switch m["mountPath"] {
			case "/models/model.gguf":
				m["subPath"] = entry.ID + "/model.gguf"
			case "/config/models.yaml":
				m["subPath"] = name + ".yaml"
			}
		}
		object(service["spec"])["selector"] = map[string]any{"app": name}
		cfg := []any{map[string]any{"name": name, "backend": entry.Backend, "parameters": map[string]any{"model": "model.gguf"}, "context_size": values["contextSize"], "threads": values["threads"], "gpu_layers": 0, "template": map[string]any{"use_tokenizer_template": true}}}
		raw, err := yaml.Marshal(cfg)
		if err != nil {
			return materializerResult{}, nil, nil, err
		}
		modelConfig[name+".yaml"] = string(raw)
		documents = append(documents, deployment, service)
		index := 1
		workloads = append(workloads, workloadTarget{ID: name, APIVersion: "apps/v1", Kind: "Deployment", Name: name, Selector: map[string]string{"app": name}, Traits: []string{"scalable", "schedulable", "deschedulable"}, Index: &index})
	}
	dependencies := make([]string, len(config.Models))
	for i, m := range config.Models {
		dependencies[i] = m.Service
	}
	proxyIndex := 0
	workloads = append(workloads, workloadTarget{ID: "node-proxy", APIVersion: "apps/v1", Kind: "DaemonSet", Name: "node-proxy", Selector: map[string]string{"app": "node-proxy"}, Traits: []string{"traffic-entrypoint"}, Index: &proxyIndex, Dependencies: dependencies})
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	for _, doc := range documents {
		object(doc["metadata"])["labels"] = map[string]any{"group": group}
		if spec, ok := doc["spec"].(map[string]any); ok {
			if template, ok := spec["template"].(map[string]any); ok {
				labels := object(object(template["metadata"])["labels"])
				labels["group"] = group
				name := fmt.Sprint(object(doc["metadata"])["name"])
				if name == "node-proxy" {
					labels["index"] = "0"
					labels["component"] = "node-proxy"

				}
			}
		}
		if err := encoder.Encode(doc); err != nil {
			return materializerResult{}, nil, nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return materializerResult{}, nil, nil, err
	}
	manifest := output.Bytes()
	names, _ := json.Marshal(dependencies)
	prompt, _ := json.Marshal(fmt.Sprint(values["prompt"]))
	script := fmt.Sprintf(`import random
from locust import HttpUser, task

SERVICES = %s
PROMPT = %s

class InferenceUser(HttpUser):
    @task
    def infer(self):
        service = random.choice(SERVICES)
        with self.client.post("/v1/chat/completions", headers={"X-Model": service}, json={"model": service, "messages": [{"role": "user", "content": PROMPT}], "max_tokens": %v, "stream": False}, name=service, timeout=600, catch_response=True) as response:
            if response.status_code != 200:
                response.failure("Inference HTTP status: " + str(response.status_code))
                return
            try:
                if not response.json().get("choices"):
                    response.failure("Inference returned no choices")
            except ValueError:
                response.failure("Inference returned invalid JSON")
`, names, prompt, values["maxTokens"])
	scriptDigest, _ := digestBytes([]byte(script))
	scenarios := []loadScenario{{ID: "inference", Engine: "locust", TargetEndpoint: "node-proxy-http", RuntimeImage: locustImage, Script: script, ScriptDigest: scriptDigest}}
	manifestDigest, _ := digestBytes(manifest)
	valuesDigest, _ := digest(values)
	loadDigest, _ := digest(scenarios)
	result := materializerResult{ApplicationRef: input.ApplicationRef, SourceRevision: sourceRevision, ManifestDigest: manifestDigest, ValuesDigest: valuesDigest, ManifestSet: manifestSet{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "ManifestSet", Metadata: manifestSetMetadata{ApplicationRef: input.ApplicationRef, Digest: manifestDigest, ValuesDigest: valuesDigest}, Spec: manifestSetSpec{Renderer: "plain-yaml", Content: string(manifest), Source: manifestSource{Type: "generated", Revision: sourceRevision, Path: ".", Entrypoint: "generated.yaml"}}}, WorkloadTargets: workloads, ServiceEndpoints: []serviceEndpoint{{ID: "node-proxy-http", Component: "node-proxy", Service: "node-proxy", Port: 80, Protocol: "http", Path: "/"}}, LoadScenarioSet: loadScenarioSet{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "LoadScenarioSet", Metadata: loadScenarioSetMetadata{ApplicationRef: input.ApplicationRef, Digest: loadDigest}, Spec: loadScenarioSetSpec{Scenarios: scenarios}}}
	return result, manifest, scenarios, nil
}

func digestBytes(raw []byte) (string, error) {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
