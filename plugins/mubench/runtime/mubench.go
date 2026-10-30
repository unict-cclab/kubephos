package mubench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
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
	materializerID      = "io.kubephos.applications.mubench"
	materializerVersion = "0.4.0"
	sourceRevision      = "176c8f14f2740414436078d5dcd969d38dd4acd4"
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

type generatorConfig struct {
	ServiceCount int    `json:"serviceCount"`
	Topology     string `json:"topology"`
	ServiceImage string `json:"serviceImage"`
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

func (MaterializerPlugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: materializerID, Name: "muBench application materializer", Version: materializerVersion,
		Description:     "Materializes configurable muBench application resources into standard artifacts.",
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
	values := clone(descriptor.Spec.Defaults)
	properties, _ := descriptor.Spec.ValuesSchema["properties"].(map[string]any)
	if _, standardized := properties["initialReplicas"]; standardized {
		if _, exists := spec.Values["initialReplicas"]; !exists {
			if legacy, found := spec.Values["replicas"]; found {
				spec.Values["initialReplicas"] = legacy
			}
		}
		delete(spec.Values, "replicas")
	}
	for key, value := range spec.Values {
		values[key] = value
	}
	definition, _ := json.Marshal(descriptor.Spec.ValuesSchema)
	valueRaw, _ := json.Marshal(values)
	issues, err := schema.Validate(definition, valueRaw)
	if err != nil || len(issues) != 0 {
		if err != nil {
			return spec, descriptor, generatorConfig{}, err
		}
		return spec, descriptor, generatorConfig{}, errors.New(schema.IssueMessage(definition, issues[0]))
	}
	if _, err := workloadresources.Build(values); err != nil {
		return spec, descriptor, generatorConfig{}, err
	}

	if _, err := serviceReplicas(values, "s0"); err != nil {
		return spec, descriptor, generatorConfig{}, err
	}
	if overrides, ok := values["replicaOverrides"].(map[string]any); ok {
		serviceCount, _ := integerValue(values["serviceCount"])
		for name := range overrides {
			index, valid := strings.CutPrefix(name, "s")
			position, conversionErr := strconv.Atoi(index)
			if !valid || conversionErr != nil || position < 0 || position >= serviceCount {
				return spec, descriptor, generatorConfig{}, fmt.Errorf("replica override references unknown service %s", name)
			}
			if _, err := serviceReplicas(values, name); err != nil {
				return spec, descriptor, generatorConfig{}, err
			}
		}
	}
	config, err := materializerConfiguration(descriptor, values)
	if err != nil {
		return spec, descriptor, config, err
	}
	if err := workloadresources.Validate(values, serviceNames(config.ServiceCount)); err != nil {
		return spec, descriptor, config, err
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
	config, err := materializerConfiguration(input.Descriptor, input.Values)
	if err != nil {
		return input, config, err
	}
	return input, config, nil
}

func materializerConfiguration(descriptor catalog.Descriptor, values map[string]any) (generatorConfig, error) {
	topology := values["topology"]
	if topology == nil {
		topology = descriptor.Spec.MaterializerConfig["topology"]
	}
	serviceCountValue := values["serviceCount"]
	if serviceCountValue == nil {
		serviceCountValue = descriptor.Spec.MaterializerConfig["serviceCount"]
	}
	config := generatorConfig{ServiceImage: fmt.Sprint(descriptor.Spec.MaterializerConfig["serviceImage"]), Topology: fmt.Sprint(topology)}
	serviceCount, err := integerValue(serviceCountValue)
	if err != nil {
		return config, errors.New("service count must be an integer")
	}
	config.ServiceCount = serviceCount
	if config.ServiceCount < 2 || config.ServiceCount > 100 || config.ServiceImage == "" || config.Topology != "chain" && config.Topology != "fan-out" && config.Topology != "tree" {
		return config, errors.New("muBench application settings are invalid")
	}
	return config, nil
}

func integerValue(value any) (int, error) {
	switch number := value.(type) {
	case int:
		return number, nil
	case float64:
		if number == float64(int(number)) {
			return int(number), nil
		}
	case json.Number:
		parsed, err := strconv.Atoi(number.String())
		if err == nil {
			return parsed, nil
		}
	}
	return 0, errors.New("value is not an integer")
}

func materialize(input materializerInput, config generatorConfig) (materializerResult, []byte, []loadScenario, error) {
	topologyIndexes := map[string]*int{"node-proxy": intPointer(0)}
	for index := 0; index < config.ServiceCount; index++ {
		topologyIndexes[fmt.Sprintf("s%d", index)] = intPointer(graphIndex(index, config.Topology))
	}
	manifest, err := manifests(input.Descriptor.Spec.Interface.Group, config, input.Values, topologyIndexes)
	if err != nil {
		return materializerResult{}, nil, nil, err
	}
	workloads := make([]workloadTarget, 0, config.ServiceCount+1)
	for index := 0; index < config.ServiceCount; index++ {
		name := fmt.Sprintf("s%d", index)
		dependencies := make([]string, 0)
		for _, child := range graphChildren(index, config.ServiceCount, config.Topology) {
			dependencies = append(dependencies, fmt.Sprintf("s%d", child))
		}
		workloads = append(workloads, workloadTarget{ID: name, APIVersion: "apps/v1", Kind: "Deployment", Name: name, Selector: map[string]string{"app": name}, Traits: []string{"scalable", "schedulable", "deschedulable"}, Index: topologyIndexes[name], Dependencies: dependencies})
	}
	workloads = append(workloads, workloadTarget{ID: "node-proxy", APIVersion: "apps/v1", Kind: "DaemonSet", Name: "node-proxy", Selector: map[string]string{"app": "node-proxy"}, Traits: []string{"traffic-entrypoint"}, Index: topologyIndexes["node-proxy"], Dependencies: []string{"s0"}})
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
	if err := workloadresources.Validate(values, serviceNames(config.ServiceCount)); err != nil {
		return nil, err
	}

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
	replicas, _ := serviceReplicas(values, name)
	labels := map[string]any{"app": name, "group": group, "app.kubernetes.io/name": name, "app.kubernetes.io/part-of": group, "kubephos.dev/component": name}
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
		"readinessProbe": map[string]any{"tcpSocket": map[string]any{"port": "http"}, "initialDelaySeconds": 3, "periodSeconds": 5},
		"livenessProbe":  map[string]any{"tcpSocket": map[string]any{"port": "http"}, "initialDelaySeconds": 15, "periodSeconds": 10},
		"volumeMounts": []any{
			map[string]any{"name": "workmodel", "mountPath": "/app/MSConfig/workmodel.json", "subPath": "workmodel.json"},
			map[string]any{"name": "internal-services", "mountPath": "/app/MSConfig/InternalServiceFunctions"},
		},
	}
	resources, _ := workloadresources.ForService(values, name)
	if len(resources) > 0 {
		container["resources"] = resources
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
			"replicas": replicas, "selector": map[string]any{"matchLabels": map[string]any{"app": name}},
			"template": map[string]any{"metadata": map[string]any{"labels": labels, "annotations": map[string]any{"prometheus.io/scrape": "true", "prometheus.io/port": "8080"}}, "spec": podSpec},
		},
	}
	service := map[string]any{
		"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": name, "labels": labels},
		"spec": map[string]any{"selector": map[string]any{"app": name}, "ports": []any{map[string]any{"name": "http", "port": 80, "targetPort": "http"}, map[string]any{"name": "grpc", "port": 51313, "targetPort": "grpc"}}},
	}
	return []any{deployment, service}
}

func serviceReplicas(values map[string]any, name string) (int, error) {
	value := values["initialReplicas"]
	if value == nil {
		value = values["replicas"]
	}
	if overrides, ok := values["replicaOverrides"].(map[string]any); ok {
		if override, exists := overrides[name]; exists {
			value = override
		}
	}
	replicas, err := integerValue(value)
	if err != nil || replicas < 1 || replicas > 1000 {
		return 0, fmt.Errorf("replicas for %s must be an integer between 1 and 1000", name)
	}
	return replicas, nil
}

func nodeProxyDocuments(group string, topologyIndex *int) []any {
	labels := map[string]any{"app": "node-proxy", "group": group, "app.kubernetes.io/name": "node-proxy", "app.kubernetes.io/part-of": group, "kubephos.dev/component": "node-proxy"}
	if topologyIndex != nil {
		labels["index"] = strconv.Itoa(*topologyIndex)
	}
	configuration := "server {\n  listen 8080;\n  location / {\n    proxy_http_version 1.1;\n    proxy_set_header X-Real-IP $remote_addr;\n    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;\n    proxy_set_header X-Forwarded-Proto $scheme;\n    proxy_set_header X-Kubephos-Proxy-Node ${NODE_NAME};\n    proxy_set_header X-Kubephos-Proxy-Zone ${NODE_ZONE};\n    proxy_pass http://s0:80/api/v1;\n  }\n}\n"
	configMap := map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "node-proxy", "labels": labels}, "data": map[string]any{"default.conf.template": configuration}}
	daemonSet := map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "metadata": map[string]any{"name": "node-proxy", "labels": labels}, "spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]any{"app": "node-proxy"}}, "template": map[string]any{"metadata": map[string]any{"labels": labels}, "spec": map[string]any{"nodeSelector": map[string]any{"kubephos.dev/role": "application"}, "containers": []any{map[string]any{"name": "nginx", "image": "nginx:1.27-alpine", "ports": []any{map[string]any{"name": "http", "containerPort": 8080}}, "env": []any{map[string]any{"name": "NODE_NAME", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "spec.nodeName"}}}, map[string]any{"name": "NODE_ZONE", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "metadata.labels['topology.kubernetes.io/zone']"}}}}, "volumeMounts": []any{map[string]any{"name": "config", "mountPath": "/etc/nginx/templates/default.conf.template", "subPath": "default.conf.template"}}}}, "volumes": []any{map[string]any{"name": "config", "configMap": map[string]any{"name": "node-proxy"}}}}}}}
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

func serviceNames(count int) []string {
	names := make([]string, count)
	for i := range names {
		names[i] = fmt.Sprintf("s%d", i)
	}
	return names
}
