package mubench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/schema"
)

const (
	factoryID           = "io.kubephos.applications.mubench.generate"
	materializerID      = "io.kubephos.applications.mubench"
	factoryVersion      = "0.1.3"
	materializerVersion = "0.1.2"
	sourceRevision      = "176c8f14f2740414436078d5dcd969d38dd4acd4"
	serviceImage        = "docker.io/msvcbench/microservice@sha256:26f486c7e546100c509501809aa4a36b283d3a2e45be09f56b059c6fe531f776"
	locustImage         = "locustio/locust:2.42.6"
)

var quantityPattern = regexp.MustCompile(`^[0-9]+(?:m|Ki|Mi|Gi)?$`)

type Invocation struct {
	Input   json.RawMessage            `json:"input"`
	Catalog map[string]json.RawMessage `json:"catalog"`
}

type FactoryPlugin struct{}

type MaterializerPlugin struct{}

type FactorySpec struct {
	Name            string `json:"name"`
	ServiceCount    int    `json:"serviceCount"`
	Topology        string `json:"topology"`
	Replicas        int    `json:"replicas"`
	Workers         int    `json:"workers"`
	Threads         int    `json:"threads"`
	WorkloadProfile string `json:"workloadProfile"`
	ResponseSizeKB  int    `json:"responseSizeKB"`
	CPURequest      string `json:"cpuRequest"`
	MemoryRequest   string `json:"memoryRequest"`
}

type materializerSpec struct {
	ApplicationRef string         `json:"applicationRef"`
	Values         map[string]any `json:"values"`
}

type materializerInput struct {
	ApplicationRef string             `json:"applicationRef"`
	Values         map[string]any     `json:"values"`
	Descriptor     catalog.Descriptor `json:"descriptor"`
}

type generatorConfig struct {
	ServiceCount int    `json:"serviceCount"`
	Topology     string `json:"topology"`
	ServiceImage string `json:"serviceImage"`
}

type factoryResult struct {
	Descriptor catalog.Descriptor `json:"descriptor"`
}

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

func (FactoryPlugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: factoryID, Name: "muBench application generator", Version: factoryVersion,
		Description:     "Creates reproducible muBench applications with source-aware traffic ingress.",
		Schema:          json.RawMessage(`{"type":"object","required":["name","serviceCount","topology","replicas","workers","threads","workloadProfile","responseSizeKB","cpuRequest","memoryRequest"],"additionalProperties":false,"properties":{"name":{"type":"string","title":"Application name","default":"muBench application","minLength":1,"maxLength":80},"serviceCount":{"type":"integer","title":"Services","default":6,"minimum":2,"maximum":100},"topology":{"type":"string","title":"Request topology","default":"chain","enum":["chain","fan-out","tree"]},"replicas":{"type":"integer","title":"Initial replicas","default":1,"minimum":1,"maximum":20},"workers":{"type":"integer","title":"Workers per service","default":2,"minimum":1,"maximum":32},"threads":{"type":"integer","title":"Threads per worker","default":4,"minimum":1,"maximum":64},"workloadProfile":{"type":"string","title":"Work per request","default":"balanced","enum":["light","balanced","heavy"]},"responseSizeKB":{"type":"integer","title":"Average response size (KB)","default":10,"minimum":1,"maximum":10240},"cpuRequest":{"type":"string","title":"CPU request","default":"250m","minLength":1,"maxLength":16},"memoryRequest":{"type":"string","title":"Memory request","default":"256Mi","minLength":1,"maxLength":16}}}`),
		ArtifactOutputs: []domain.ArtifactContract{{Type: "CatalogApplicationDescriptor", Version: "v1alpha1"}},
		Capabilities:    []string{"catalog.application.generate"},
	}
}

func (MaterializerPlugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: materializerID, Name: "muBench application materializer", Version: materializerVersion,
		Description:     "Materializes a generated muBench catalog application into the standard application artifacts.",
		Schema:          json.RawMessage(`{"type":"object","required":["applicationRef"],"additionalProperties":false,"properties":{"applicationRef":{"type":"string","title":"Application","format":"kubephos-application-ref"},"values":{"type":"object","title":"Application settings","x-kubephos-schema-from-application":"applicationRef"}}}`),
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ManifestSet", Version: "v1alpha1"}, {Type: "WorkloadTargets", Version: "v1alpha1"}, {Type: "ServiceEndpoints", Version: "v1alpha1"}, {Type: "LoadScenarioSet", Version: "v1alpha1"}},
		Capabilities:    []string{"applications.materialize"},
		Permissions:     []string{"catalog.read:applications"},
	}
}

func (FactoryPlugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	if err := ctx.Err(); err != nil {
		return invalid(err.Error())
	}
	var spec FactorySpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return invalid("Configuration must be valid JSON.")
	}
	if err := validateFactorySpec(spec); err != nil {
		return invalid(err.Error())
	}
	return valid(fmt.Sprintf("The generator will create %d services using a %s topology.", spec.ServiceCount, spec.Topology))
}

func (FactoryPlugin) Plan(ctx context.Context, invocation Invocation) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec FactorySpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return domain.Plan{}, err
	}
	if err := validateFactorySpec(spec); err != nil {
		return domain.Plan{}, err
	}
	raw, _ := json.Marshal(spec)
	return domain.Plan{PluginID: factoryID, Steps: []domain.PlanStep{{ID: "generate-application", Name: "Generate reproducible muBench application", Input: raw, Outputs: []domain.ArtifactOutput{{Name: "application-descriptor", Type: "CatalogApplicationDescriptor", Version: "v1alpha1", MediaType: "application/json", Source: "/descriptor"}}}}}, nil
}

func (FactoryPlugin) Precheck(ctx context.Context, step domain.PlanStep, _ plugins.Logger) (domain.HealthReport, error) {
	var spec FactorySpec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateFactorySpec(spec); err != nil {
		return unhealthy(err.Error()), nil
	}
	return healthy("Generation settings are complete and reproducible"), nil
}

func (FactoryPlugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var spec FactorySpec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return nil, err
	}
	descriptor, err := generateDescriptor(spec)
	if err != nil {
		return nil, err
	}
	if err := log("info", fmt.Sprintf("Generated %d muBench services with immutable source and image references", spec.ServiceCount)); err != nil {
		return nil, err
	}
	return json.Marshal(factoryResult{Descriptor: descriptor})
}

func (FactoryPlugin) Verify(ctx context.Context, _ domain.PlanStep, raw json.RawMessage, _ plugins.Logger) (domain.HealthReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.HealthReport{}, err
	}
	var result factoryResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return unhealthy("Generated descriptor is invalid"), nil
	}
	descriptor, err := json.Marshal(result.Descriptor)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := catalog.Parse(descriptor, "imported"); err != nil {
		return unhealthy(err.Error()), nil
	}
	return healthy("Generated application contract is valid"), nil
}

func (FactoryPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func (MaterializerPlugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	if err := ctx.Err(); err != nil {
		return invalid(err.Error())
	}
	_, descriptor, config, err := resolveMaterializer(invocation)
	if err != nil {
		return invalid(err.Error())
	}
	return valid(fmt.Sprintf("Resolved %d generated muBench services using a %s topology.", config.ServiceCount, config.Topology), fmt.Sprintf("Application contract declares %d targetable components.", len(descriptor.Spec.Interface.Components)))
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
	return domain.Plan{PluginID: materializerID, Steps: []domain.PlanStep{{ID: "materialize-mubench", Name: "Generate and verify muBench resources", Input: raw, Outputs: []domain.ArtifactOutput{{Name: "manifest-set", Type: "ManifestSet", Version: "v1alpha1", MediaType: "application/json", Source: "/manifestSet"}, {Name: "workload-targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json", Source: "/workloadTargets"}, {Name: "service-endpoints", Type: "ServiceEndpoints", Version: "v1alpha1", MediaType: "application/json", Source: "/serviceEndpoints"}, {Name: "load-scenario-set", Type: "LoadScenarioSet", Version: "v1alpha1", MediaType: "application/json", Source: "/loadScenarioSet"}}}}}, nil
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
	if err := log("info", fmt.Sprintf("Materialized %d muBench services and the source-aware node proxy", config.ServiceCount)); err != nil {
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
	return healthy("muBench application resources are reproducible and internally consistent"), nil
}

func (MaterializerPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func generateDescriptor(spec FactorySpec) (catalog.Descriptor, error) {
	if err := validateFactorySpec(spec); err != nil {
		return catalog.Descriptor{}, err
	}
	fingerprintInput, _ := json.Marshal(struct {
		FactoryVersion string      `json:"factoryVersion"`
		SourceRevision string      `json:"sourceRevision"`
		ServiceImage   string      `json:"serviceImage"`
		Spec           FactorySpec `json:"spec"`
	}{FactoryVersion: factoryVersion, SourceRevision: sourceRevision, ServiceImage: serviceImage, Spec: spec})
	fingerprint := sha256.Sum256(fingerprintInput)
	short := hex.EncodeToString(fingerprint[:])[:10]
	slug := slugify(spec.Name)
	components := make([]catalog.Component, 0, spec.ServiceCount+1)
	for index := 0; index < spec.ServiceCount; index++ {
		name := fmt.Sprintf("s%d", index)
		dependencies := make([]string, 0)
		for _, child := range graphChildren(index, spec.ServiceCount, spec.Topology) {
			dependencies = append(dependencies, fmt.Sprintf("s%d", child))
		}
		components = append(components, catalog.Component{ID: name, Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "Deployment", Name: name}, Selector: map[string]string{"app": name}, Traits: []string{"scalable", "schedulable"}, Index: intPointer(graphIndex(index, spec.Topology)), Dependencies: dependencies})
	}
	components = append(components, catalog.Component{ID: "node-proxy", Workload: catalog.Workload{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "node-proxy"}, Selector: map[string]string{"app": "node-proxy"}, Traits: []string{"traffic-entrypoint"}, Index: intPointer(0), Dependencies: []string{"s0"}})
	valuesSchema := map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"replicas":        map[string]any{"type": "integer", "title": "Initial replicas", "minimum": 1, "maximum": 20},
		"workers":         map[string]any{"type": "integer", "title": "Workers per service", "minimum": 1, "maximum": 32},
		"threads":         map[string]any{"type": "integer", "title": "Threads per worker", "minimum": 1, "maximum": 64},
		"workloadProfile": map[string]any{"type": "string", "title": "Work per request", "enum": []any{"light", "balanced", "heavy"}},
		"responseSizeKB":  map[string]any{"type": "integer", "title": "Average response size (KB)", "minimum": 1, "maximum": 10240},
		"cpuRequest":      map[string]any{"type": "string", "title": "CPU request", "minLength": 1, "maxLength": 16},
		"memoryRequest":   map[string]any{"type": "string", "title": "Memory request", "minLength": 1, "maxLength": 16},
	}}
	return catalog.Descriptor{
		APIVersion: "catalog.kubephos.dev/v1alpha1", Kind: "Application",
		Metadata: catalog.Metadata{ID: "dev.kubephos.mubench." + slug, Name: spec.Name, Version: sourceRevision[:8] + "-" + short, Description: fmt.Sprintf("Generated muBench application with %d services and a %s request topology.", spec.ServiceCount, spec.Topology)},
		Spec: catalog.Spec{
			Package:            catalog.Package{Type: "git", Format: "plain-yaml", Repository: "https://github.com/mSvcBench/muBench.git", Revision: sourceRevision, Path: ".", Entrypoint: "generated.yaml"},
			Materializer:       materializerID,
			MaterializerConfig: map[string]any{"serviceCount": spec.ServiceCount, "topology": spec.Topology, "serviceImage": serviceImage},
			Interface:          catalog.Interface{Group: "mubench-" + short[:8], Components: components, Endpoints: []catalog.Endpoint{{ID: "entry-http", Component: "s0", Service: "s0", Port: 80, Protocol: "http", Path: "/api/v1"}, {ID: "node-proxy-http", Component: "node-proxy", Service: "node-proxy", Port: 80, Protocol: "http", Path: "/"}}, LoadScenarios: []catalog.LoadScenario{{ID: "default-journey", Engine: "locust", Script: "generated/locustfile.py", RuntimeImage: locustImage, TargetEndpoint: "node-proxy-http"}}},
			ValuesSchema:       valuesSchema,
			Defaults:           map[string]any{"replicas": spec.Replicas, "workers": spec.Workers, "threads": spec.Threads, "workloadProfile": spec.WorkloadProfile, "responseSizeKB": spec.ResponseSizeKB, "cpuRequest": spec.CPURequest, "memoryRequest": spec.MemoryRequest},
		},
	}, nil
}

func resolveMaterializer(invocation Invocation) (materializerSpec, catalog.Descriptor, generatorConfig, error) {
	var spec materializerSpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return spec, catalog.Descriptor{}, generatorConfig{}, errors.New("configuration must be valid JSON")
	}
	raw, ok := invocation.Catalog[spec.ApplicationRef]
	if !ok {
		return spec, catalog.Descriptor{}, generatorConfig{}, errors.New("catalog application could not be resolved")
	}
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil || descriptor.Spec.Materializer != materializerID {
		return spec, descriptor, generatorConfig{}, errors.New("catalog application is not a muBench generated application")
	}
	configRaw, _ := json.Marshal(descriptor.Spec.MaterializerConfig)
	var config generatorConfig
	if err := json.Unmarshal(configRaw, &config); err != nil || config.ServiceCount < 2 || config.ServiceImage == "" {
		return spec, descriptor, config, errors.New("muBench generator metadata is invalid")
	}
	values := clone(descriptor.Spec.Defaults)
	for key, value := range spec.Values {
		values[key] = value
	}
	definition, _ := json.Marshal(descriptor.Spec.ValuesSchema)
	valueRaw, _ := json.Marshal(values)
	issues, err := schema.Validate(definition, valueRaw)
	if err != nil || len(issues) != 0 {
		if err != nil {
			return spec, descriptor, config, err
		}
		return spec, descriptor, config, fmt.Errorf("application setting %s is invalid: %s", issues[0].Path, issues[0].Message)
	}
	if !quantityPattern.MatchString(fmt.Sprint(values["cpuRequest"])) || !quantityPattern.MatchString(fmt.Sprint(values["memoryRequest"])) {
		return spec, descriptor, config, errors.New("CPU and memory requests must be Kubernetes quantities")
	}
	spec.Values = values
	return spec, descriptor, config, nil
}

func decodeMaterializerStep(step domain.PlanStep) (materializerInput, generatorConfig, error) {
	var input materializerInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return input, generatorConfig{}, err
	}
	if input.ApplicationRef == "" || input.Descriptor.Spec.Materializer != materializerID {
		return input, generatorConfig{}, errors.New("materializer step is incomplete")
	}
	raw, _ := json.Marshal(input.Descriptor.Spec.MaterializerConfig)
	var config generatorConfig
	if err := json.Unmarshal(raw, &config); err != nil {
		return input, config, err
	}
	return input, config, nil
}

func materialize(input materializerInput, config generatorConfig) (materializerResult, []byte, []loadScenario, error) {
	topologyIndexes := map[string]*int{}
	for _, component := range input.Descriptor.Spec.Interface.Components {
		topologyIndexes[component.ID] = component.Index
	}
	manifest, err := manifests(input.Descriptor.Spec.Interface.Group, config, input.Values, topologyIndexes)
	if err != nil {
		return materializerResult{}, nil, nil, err
	}
	workloads := make([]workloadTarget, 0, len(input.Descriptor.Spec.Interface.Components))
	for _, component := range input.Descriptor.Spec.Interface.Components {
		workloads = append(workloads, workloadTarget{ID: component.ID, APIVersion: component.Workload.APIVersion, Kind: component.Workload.Kind, Name: component.Workload.Name, Selector: component.Selector, Traits: component.Traits, Index: component.Index, Dependencies: component.Dependencies})
	}
	endpoints := make([]serviceEndpoint, 0, len(input.Descriptor.Spec.Interface.Endpoints))
	for _, endpoint := range input.Descriptor.Spec.Interface.Endpoints {
		endpoints = append(endpoints, serviceEndpoint{ID: endpoint.ID, Component: endpoint.Component, Service: endpoint.Service, Port: endpoint.Port, Protocol: endpoint.Protocol, Path: endpoint.Path})
	}
	script := "from locust import HttpUser, task\n\nclass MuBenchUser(HttpUser):\n    @task\n    def request(self):\n        self.client.get(\"/\", name=\"muBench request\")\n"
	scriptHash := sha256.Sum256([]byte(script))
	scenarios := []loadScenario{{ID: "default-journey", Engine: "locust", TargetEndpoint: "node-proxy-http", RuntimeImage: locustImage, Script: script, ScriptDigest: "sha256:" + hex.EncodeToString(scriptHash[:])}}
	manifestHash := sha256.Sum256(manifest)
	manifestDigest := "sha256:" + hex.EncodeToString(manifestHash[:])
	valuesDigest, _ := digest(input.Values)
	loadDigest, _ := digest(scenarios)
	result := materializerResult{
		ApplicationRef: input.ApplicationRef, SourceRevision: sourceRevision, ManifestDigest: manifestDigest, ValuesDigest: valuesDigest,
		ManifestSet:     manifestSet{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "ManifestSet", Metadata: manifestSetMetadata{ApplicationRef: input.ApplicationRef, Digest: manifestDigest, ValuesDigest: valuesDigest}, Spec: manifestSetSpec{Renderer: "plain-yaml", Content: string(manifest), Source: manifestSource{Type: "generated", Repository: input.Descriptor.Spec.Package.Repository, Revision: sourceRevision, Path: ".", Entrypoint: "generated.yaml"}}},
		WorkloadTargets: workloads, ServiceEndpoints: endpoints,
		LoadScenarioSet: loadScenarioSet{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "LoadScenarioSet", Metadata: loadScenarioSetMetadata{ApplicationRef: input.ApplicationRef, Digest: loadDigest}, Spec: loadScenarioSetSpec{Scenarios: scenarios}},
	}
	return result, manifest, scenarios, nil
}

func manifests(group string, config generatorConfig, values map[string]any, topologyIndexes map[string]*int) ([]byte, error) {
	workmodel := map[string]any{}
	complexity := map[string][]int{"light": {25, 50}, "balanced": {100, 200}, "heavy": {500, 1000}}[fmt.Sprint(values["workloadProfile"])]
	if len(complexity) == 0 {
		return nil, errors.New("workload profile is invalid")
	}
	for index := 0; index < config.ServiceCount; index++ {
		name := fmt.Sprintf("s%d", index)
		children := graphChildren(index, config.ServiceCount, config.Topology)
		external := []any{}
		if len(children) != 0 {
			names := make([]any, len(children))
			for position, child := range children {
				names[position] = fmt.Sprintf("s%d", child)
			}
			external = append(external, map[string]any{"seq_len": 1, "services": names})
		}
		workmodel[name] = map[string]any{"external_services": external, "internal_service": map[string]any{"compute_pi": map[string]any{"range_complexity": complexity, "mean_response_size": values["responseSizeKB"]}}, "request_method": "rest", "workers": values["workers"], "threads": values["threads"], "url": name, "path": "/api/v1", "image": config.ServiceImage}
	}
	workmodelRaw, err := json.MarshalIndent(workmodel, "", "  ")
	if err != nil {
		return nil, err
	}
	documents := []any{
		map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "workmodel", "labels": map[string]any{"group": group}}, "data": map[string]any{"workmodel.json": string(workmodelRaw)}},
		map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "internal-services", "labels": map[string]any{"group": group}}, "data": map[string]any{}},
	}
	for index := 0; index < config.ServiceCount; index++ {
		name := fmt.Sprintf("s%d", index)
		documents = append(documents, serviceDocuments(name, group, config.ServiceImage, values, topologyIndexes[name])...)
	}
	documents = append(documents, nodeProxyDocuments(group, topologyIndexes["node-proxy"])...)
	var output strings.Builder
	for index, document := range documents {
		if index != 0 {
			output.WriteString("---\n")
		}
		raw, err := yaml.Marshal(document)
		if err != nil {
			return nil, err
		}
		output.Write(raw)
	}
	return []byte(output.String()), nil
}

func serviceDocuments(name, group, image string, values map[string]any, topologyIndex *int) []any {
	labels := map[string]any{"app": name, "group": group, "app.kubernetes.io/name": name, "app.kubernetes.io/part-of": group}
	if topologyIndex != nil {
		labels["index"] = strconv.Itoa(*topologyIndex)
	}
	container := map[string]any{
		"name": name, "image": image, "imagePullPolicy": "IfNotPresent",
		"ports": []any{map[string]any{"name": "http", "containerPort": 8080}, map[string]any{"name": "grpc", "containerPort": 51313}},
		"env": []any{
			map[string]any{"name": "APP", "value": name},
			map[string]any{"name": "ZONE", "value": "default"},
			map[string]any{"name": "K8S_APP", "value": name},
			map[string]any{"name": "PN", "value": fmt.Sprint(values["workers"])},
			map[string]any{"name": "TN", "value": fmt.Sprint(values["threads"])},
		},
		"resources":      map[string]any{"requests": map[string]any{"cpu": values["cpuRequest"], "memory": values["memoryRequest"]}},
		"readinessProbe": map[string]any{"tcpSocket": map[string]any{"port": "http"}, "initialDelaySeconds": 3, "periodSeconds": 5},
		"livenessProbe":  map[string]any{"tcpSocket": map[string]any{"port": "http"}, "initialDelaySeconds": 15, "periodSeconds": 10},
		"volumeMounts": []any{
			map[string]any{"name": "workmodel", "mountPath": "/app/MSConfig/workmodel.json", "subPath": "workmodel.json"},
			map[string]any{"name": "internal-services", "mountPath": "/app/MSConfig/InternalServiceFunctions"},
		},
	}
	podSpec := map[string]any{
		"terminationGracePeriodSeconds": 5,
		"containers":                    []any{container},
		"volumes": []any{
			map[string]any{"name": "workmodel", "configMap": map[string]any{"name": "workmodel"}},
			map[string]any{"name": "internal-services", "configMap": map[string]any{"name": "internal-services"}},
		},
	}
	deployment := map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": name, "labels": labels},
		"spec": map[string]any{
			"replicas": values["replicas"], "selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			"template": map[string]any{"metadata": map[string]any{"labels": labels, "annotations": map[string]any{"prometheus.io/scrape": "true", "prometheus.io/port": "8080"}}, "spec": podSpec},
		},
	}
	service := map[string]any{
		"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": name, "labels": labels},
		"spec": map[string]any{"selector": map[string]any{"app": name}, "ports": []any{map[string]any{"name": "http", "port": 80, "targetPort": "http"}, map[string]any{"name": "grpc", "port": 51313, "targetPort": "grpc"}}},
	}
	return []any{deployment, service}
}

func nodeProxyDocuments(group string, topologyIndex *int) []any {
	labels := map[string]any{"app": "node-proxy", "group": group, "app.kubernetes.io/name": "node-proxy", "app.kubernetes.io/part-of": group}
	if topologyIndex != nil {
		labels["index"] = strconv.Itoa(*topologyIndex)
	}
	configuration := "server {\n  listen 8080;\n  location / {\n    proxy_http_version 1.1;\n    proxy_set_header X-Real-IP $remote_addr;\n    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n    proxy_set_header X-Forwarded-Proto $scheme;\n    proxy_set_header X-Kubephos-Proxy-Node ${NODE_NAME};\n    proxy_set_header X-Kubephos-Proxy-Zone ${NODE_ZONE};\n    proxy_pass http://s0:80/api/v1;\n  }\n}\n"
	configMap := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "node-proxy", "labels": labels}, "data": map[string]any{"default.conf.template": configuration}}
	daemonSet := map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "metadata": map[string]any{"name": "node-proxy", "labels": labels}, "spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "node-proxy"}}, "template": map[string]any{"metadata": map[string]any{"labels": labels}, "spec": map[string]any{"nodeSelector": map[string]any{"kubephos.dev/role": "application"}, "containers": []any{map[string]any{"name": "nginx", "image": "nginx:1.27-alpine", "ports": []any{map[string]any{"name": "http", "containerPort": 8080}}, "env": []any{map[string]any{"name": "NODE_NAME", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "spec.nodeName"}}}, map[string]any{"name": "NODE_ZONE", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.labels['topology.kubernetes.io/zone']"}}}}, "volumeMounts": []any{map[string]any{"name": "config", "mountPath": "/etc/nginx/templates/default.conf.template", "subPath": "default.conf.template"}}, "resources": map[string]any{"requests": map[string]any{"cpu": "25m", "memory": "32Mi"}, "limits": map[string]any{"cpu": "250m", "memory": "128Mi"}}}}, "volumes": []any{map[string]any{"name": "config", "configMap": map[string]any{"name": "node-proxy"}}}}}}}
	service := map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "node-proxy", "labels": labels}, "spec": map[string]any{"type": "NodePort", "externalTrafficPolicy": "Local", "selector": map[string]any{"app": "node-proxy"}, "ports": []any{map[string]any{"name": "http", "port": 80, "targetPort": "http"}}}}
	return []any{configMap, daemonSet, service}
}

func graphChildren(index, count int, topology string) []int {
	switch topology {
	case "chain":
		if index+1 < count {
			return []int{index + 1}
		}
	case "fan-out":
		if index == 0 {
			children := make([]int, count-1)
			for child := 1; child < count; child++ {
				children[child-1] = child
			}
			return children
		}
	case "tree":
		children := []int{}
		for _, child := range []int{index*2 + 1, index*2 + 2} {
			if child < count {
				children = append(children, child)
			}
		}
		return children
	}
	return nil
}

func graphIndex(index int, topology string) int {
	if topology == "fan-out" {
		if index == 0 {
			return 0
		}
		return 1
	}
	if topology == "tree" {
		level := 0
		for value := index + 1; value > 1; value /= 2 {
			level++
		}
		return level
	}
	return index
}

func intPointer(value int) *int {
	return &value
}

func validateFactorySpec(spec FactorySpec) error {
	if strings.TrimSpace(spec.Name) != spec.Name || len([]rune(spec.Name)) < 1 || len([]rune(spec.Name)) > 80 {
		return errors.New("Application name must contain between 1 and 80 characters.")
	}
	if spec.ServiceCount < 2 || spec.ServiceCount > 100 {
		return errors.New("Choose between 2 and 100 services.")
	}
	if spec.Topology != "chain" && spec.Topology != "fan-out" && spec.Topology != "tree" {
		return errors.New("Choose a supported request topology.")
	}
	if spec.Replicas < 1 || spec.Replicas > 20 || spec.Workers < 1 || spec.Workers > 32 || spec.Threads < 1 || spec.Threads > 64 || spec.ResponseSizeKB < 1 || spec.ResponseSizeKB > 10240 {
		return errors.New("One or more generation values are outside the supported range.")
	}
	if spec.WorkloadProfile != "light" && spec.WorkloadProfile != "balanced" && spec.WorkloadProfile != "heavy" {
		return errors.New("Choose a supported work profile.")
	}
	if !quantityPattern.MatchString(spec.CPURequest) || !quantityPattern.MatchString(spec.MemoryRequest) {
		return errors.New("CPU and memory requests must be Kubernetes quantities.")
	}
	return nil
}

func slugify(value string) string {
	var result strings.Builder
	lastDash := false
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			result.WriteRune(character)
			lastDash = false
		} else if !lastDash && result.Len() != 0 {
			result.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(result.String(), "-")
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	if len(slug) < 3 {
		slug = "application"
	}
	return slug
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

func SortedComponentIDs(descriptor catalog.Descriptor) []string {
	result := make([]string, 0, len(descriptor.Spec.Interface.Components))
	for _, component := range descriptor.Spec.Interface.Components {
		result = append(result, component.ID)
	}
	sort.Strings(result)
	return result
}
