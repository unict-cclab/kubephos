package redis

import (
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	materializerID      = "io.kubephos.applications.redis"
	materializerVersion = "0.1.2"
	sourceRevision      = "redis-7.4.2"
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
	Primaries      int    `json:"primaries"`
	Replicas       int    `json:"replicasPerPrimary"`
	KeyCount       int    `json:"keyCount"`
	PayloadBytes   int    `json:"payloadBytes"`
	ListLength     int    `json:"listLength"`
	Operation      string `json:"operation"`
	ReadPercentage int    `json:"readPercentage"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
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
		ID: materializerID, Name: "Redis Cluster application materializer", Version: materializerVersion,
		Description:     "Materializes configurable Redis Cluster application resources into standard artifacts.",
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
	return valid(fmt.Sprintf("Resolved %d Redis nodes.", config.Primaries*(1+config.Replicas)))
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
	return domain.Plan{PluginID: materializerID, Steps: []domain.PlanStep{{ID: "materialize-redis", Name: "Generate and verify Redis Cluster resources", Input: raw, Outputs: []domain.ArtifactOutput{{Name: "manifest-set", Type: "ManifestSet", Version: "v1alpha1", MediaType: "application/json", Source: "/manifestSet"}, {Name: "workload-targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json", Source: "/workloadTargets"}, {Name: "service-endpoints", Type: "ServiceEndpoints", Version: "v1alpha1", MediaType: "application/json", Source: "/serviceEndpoints"}, {Name: "load-scenario-set", Type: "LoadScenarioSet", Version: "v1alpha1", MediaType: "application/json", Source: "/loadScenarioSet"}}}}}, nil
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
	if err := log("info", fmt.Sprintf("Materialized %d Redis nodes", config.Primaries*(1+config.Replicas))); err != nil {
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
	return healthy("Redis Cluster application resources are reproducible and internally consistent"), nil
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

//go:embed workload.py
var workloadScript string

func resolveMaterializer(invocation Invocation) (materializerSpec, catalog.Descriptor, generatorConfig, error) {
	var spec materializerSpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return spec, catalog.Descriptor{}, generatorConfig{}, errors.New("configuration must be valid JSON")
	}
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(invocation.Catalog[spec.ApplicationRef], &descriptor); err != nil || descriptor.Spec.Materializer != materializerID {
		return spec, descriptor, generatorConfig{}, errors.New("catalog application is not a Redis Cluster generated application")
	}
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
		return config, fmt.Errorf("application setting %s is invalid: %s", issues[0].Path, issues[0].Message)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		return config, err
	}
	// Keep initialization finite and prevent accidental multi-gigabyte test fixtures.
	size := int64(config.KeyCount) * int64(config.PayloadBytes)
	if config.Operation == "LPUSH" || config.Operation == "LPOP" || config.Operation == "LRANGE_100" || config.Operation == "LRANGE_500" {
		size *= int64(config.ListLength)
		if int64(config.KeyCount)*int64(config.ListLength) > 1000000 {
			return config, errors.New("initial list data must contain at most 1,000,000 elements")
		}
	}
	if size > 64*1024*1024 {
		return config, errors.New("initial Redis payload must not exceed 64 MiB; reduce keys, value size or list length")
	}
	return config, workloadresources.Validate(values, nodeNames(config))
}
func nodeNames(config generatorConfig) []string {
	var names []string
	for p := 1; p <= config.Primaries; p++ {
		names = append(names, fmt.Sprintf("redis-primary-%d", p))
		for r := 1; r <= config.Replicas; r++ {
			names = append(names, fmt.Sprintf("redis-replica-%d-%d", p, r))
		}
	}
	return names
}
func materialize(input materializerInput, config generatorConfig) (materializerResult, []byte, []loadScenario, error) {
	values := input.Values
	configJSON, _ := json.Marshal(config)
	script := "CONFIG = " + string(configJSON) + "\n" + workloadScript
	names := nodeNames(config)
	group := input.Descriptor.Spec.Interface.Group
	docs := []map[string]any{}
	workloads := []workloadTarget{}
	labels := func(name string) map[string]any {
		return map[string]any{"app": name, "group": group, "kubephos.dev/component": name}
	}
	meta := func(name string) map[string]any {
		return map[string]any{"name": name, "labels": map[string]any{"group": group}}
	}
	docs = append(docs, map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": meta("redis-bootstrap"), "data": map[string]any{"workload.py": script}})
	docs = append(docs, map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": meta("redis-nodes"), "spec": map[string]any{"clusterIP": "None", "publishNotReadyAddresses": true, "selector": map[string]any{"group": group}, "ports": []any{map[string]any{"name": "redis", "port": 6379}, map[string]any{"name": "cluster-bus", "port": 16379}}}})
	docs = append(docs, map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": meta("redis"), "spec": map[string]any{"selector": map[string]any{"app": "redis-primary-1"}, "ports": []any{map[string]any{"name": "redis", "port": 6379, "targetPort": 6379}}}})
	for _, name := range names {
		index := 0
		deps := []string{}
		for p := 1; p <= config.Primaries; p++ {
			if name == fmt.Sprintf("redis-primary-%d", p) {
				for r := 1; r <= config.Replicas; r++ {
					deps = append(deps, fmt.Sprintf("redis-replica-%d-%d", p, r))
				}
			}
		}
		// Replica names are paired explicitly; the bootstrap does not rely on redis-cli heuristics.
		if strings.HasPrefix(name, "redis-replica-") {
			index = 1
		}
		resources, err := workloadresources.ForService(values, name)
		if err != nil {
			return materializerResult{}, nil, nil, err
		}
		appendOnly := "no"
		if values["appendOnly"] == true {
			appendOnly = "yes"
		}
		container := map[string]any{"name": "redis", "image": "redis:7.4.2", "imagePullPolicy": "IfNotPresent", "securityContext": map[string]any{"runAsUser": 999, "runAsGroup": 999, "runAsNonRoot": true, "allowPrivilegeEscalation": false}, "args": []any{"redis-server", "--cluster-enabled", "yes", "--cluster-config-file", "/data/nodes.conf", "--cluster-node-timeout", "5000", "--cluster-announce-ip", "$(POD_IP)", "--appendonly", appendOnly, "--save", "", "--protected-mode", "no"}, "env": []any{map[string]any{"name": "POD_IP", "valueFrom": map[string]any{"fieldRef": map[string]any{"fieldPath": "status.podIP"}}}}, "ports": []any{map[string]any{"name": "redis", "containerPort": 6379}, map[string]any{"name": "cluster-bus", "containerPort": 16379}}, "volumeMounts": []any{map[string]any{"name": "data", "mountPath": "/data"}}, "readinessProbe": map[string]any{"exec": map[string]any{"command": []any{"redis-cli", "ping"}}, "periodSeconds": 2, "timeoutSeconds": 1}, "livenessProbe": map[string]any{"exec": map[string]any{"command": []any{"redis-cli", "ping"}}, "initialDelaySeconds": 30, "periodSeconds": 10}}
		if len(resources) > 0 {
			container["resources"] = resources
		}
		pod := map[string]any{"containers": []any{container}, "terminationGracePeriodSeconds": 30}
		podLabels := labels(name)
		podLabels["index"] = fmt.Sprint(index)
		docs = append(docs, map[string]any{"apiVersion": "apps/v1", "kind": "StatefulSet", "metadata": meta(name), "spec": map[string]any{"serviceName": "redis-nodes", "replicas": 1, "selector": map[string]any{"matchLabels": map[string]any{"app": name}}, "template": map[string]any{"metadata": map[string]any{"labels": podLabels, "annotations": map[string]any{"sidecar.istio.io/inject": "false"}}, "spec": pod}, "volumeClaimTemplates": []any{map[string]any{"metadata": map[string]any{"name": "data"}, "spec": map[string]any{"accessModes": []any{"ReadWriteOnce"}, "storageClassName": "shared-storage", "resources": map[string]any{"requests": map[string]any{"storage": values["storageSize"]}}}}}}})
		workloads = append(workloads, workloadTarget{ID: name, APIVersion: "apps/v1", Kind: "StatefulSet", Name: name, Selector: map[string]string{"app": name}, Traits: []string{"schedulable", "deschedulable"}, Index: &index, Dependencies: deps})
	}
	docs = append(docs, map[string]any{"apiVersion": "batch/v1", "kind": "Job", "metadata": map[string]any{"name": "redis-bootstrap", "labels": map[string]any{"group": group}, "annotations": map[string]any{"kubephos.dev/bootstrap": "true"}}, "spec": map[string]any{"backoffLimit": 1, "activeDeadlineSeconds": 600, "template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{"sidecar.istio.io/inject": "false"}}, "spec": map[string]any{"restartPolicy": "Never", "nodeSelector": map[string]any{"kubephos.dev/role": "management"}, "containers": []any{map[string]any{"name": "bootstrap", "image": locustImage, "command": []any{"python", "/scripts/workload.py", "--bootstrap"}, "volumeMounts": []any{map[string]any{"name": "script", "mountPath": "/scripts", "readOnly": true}}}}, "volumes": []any{map[string]any{"name": "script", "configMap": map[string]any{"name": "redis-bootstrap"}}}}}}})
	var output bytes.Buffer
	encoder := yaml.NewEncoder(&output)
	encoder.SetIndent(2)
	for _, doc := range docs {
		if err := encoder.Encode(doc); err != nil {
			return materializerResult{}, nil, nil, err
		}
	}
	if err := encoder.Close(); err != nil {
		return materializerResult{}, nil, nil, err
	}
	manifest := output.Bytes()
	scriptDigest, _ := digestBytes([]byte(script))
	scenarios := []loadScenario{{ID: "redis-operations", Engine: "locust", TargetEndpoint: "redis", RuntimeImage: locustImage, Script: script, ScriptDigest: scriptDigest}}
	manifestDigest, _ := digestBytes(manifest)
	valuesDigest, _ := digest(values)
	loadDigest, _ := digest(scenarios)
	result := materializerResult{ApplicationRef: input.ApplicationRef, SourceRevision: sourceRevision, ManifestDigest: manifestDigest, ValuesDigest: valuesDigest, ManifestSet: manifestSet{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "ManifestSet", Metadata: manifestSetMetadata{ApplicationRef: input.ApplicationRef, Digest: manifestDigest, ValuesDigest: valuesDigest}, Spec: manifestSetSpec{Renderer: "plain-yaml", Content: string(manifest), Source: manifestSource{Type: "generated", Revision: sourceRevision, Path: ".", Entrypoint: "generated.yaml"}}}, WorkloadTargets: workloads, ServiceEndpoints: []serviceEndpoint{{ID: "redis", Component: "redis-primary-1", Service: "redis", Port: 6379, Protocol: "tcp"}}, LoadScenarioSet: loadScenarioSet{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "LoadScenarioSet", Metadata: loadScenarioSetMetadata{ApplicationRef: input.ApplicationRef, Digest: loadDigest}, Spec: loadScenarioSetSpec{Scenarios: scenarios}}}
	return result, manifest, scenarios, nil
}
func digestBytes(raw []byte) (string, error) {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
