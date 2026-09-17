package strategycontroller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const artifactAPI = "artifacts.kubephos.dev/v1alpha1"
const ownershipKey = "kubephos.dev/strategy-ownership"

type Plugin struct {
	Kind   string
	Runner commandRunner
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef     string `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string `json:"applicationDeploymentRef"`
	TargetBindingRef         string `json:"targetBindingRef"`
	Image                    string `json:"image"`
	ConfigFile               string `json:"configFile,omitempty"`
	IntervalSeconds          int    `json:"intervalSeconds,omitempty"`
	MinReplicas              int    `json:"minReplicas,omitempty"`
	MaxReplicas              int    `json:"maxReplicas,omitempty"`
	Parameters               string `json:"parameters,omitempty"`
	TargetOverrides          string `json:"targetOverrides,omitempty"`
}

type autoscalerSettings struct {
	IntervalSeconds int    `json:"intervalSeconds"`
	MinReplicas     int    `json:"minReplicas"`
	MaxReplicas     int    `json:"maxReplicas"`
	Parameters      string `json:"parameters"`
}

type stepInput struct {
	Spec
	Name   string `json:"name"`
	Marker string `json:"marker"`
}

type clusterConnection struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Server     string `json:"server"`
		Kubeconfig string `json:"kubeconfig"`
	} `json:"spec"`
}

type workloadTarget struct {
	ID         string            `json:"id"`
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Selector   map[string]string `json:"selector"`
	Traits     []string          `json:"traits"`
}

type applicationDeployment struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		OwnershipMarker string `json:"ownershipMarker"`
	} `json:"metadata"`
	Spec struct {
		ApplicationRef string           `json:"applicationRef"`
		ClusterServer  string           `json:"clusterServer"`
		Namespace      string           `json:"namespace"`
		Workloads      []workloadTarget `json:"workloads"`
	} `json:"spec"`
}

type targetBinding struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		RequiredTrait string           `json:"requiredTrait"`
		Targets       []workloadTarget `json:"targets"`
	} `json:"spec"`
}

type deploymentArtifact struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		StrategyKind  string   `json:"strategyKind"`
		ClusterServer string   `json:"clusterServer"`
		Namespace     string   `json:"namespace"`
		Image         string   `json:"image"`
		Targets       []string `json:"targets"`
	} `json:"spec"`
}

type result struct {
	StrategyDeployment deploymentArtifact `json:"strategyDeployment"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (plugin Plugin) Manifest() plugins.Manifest {
	if plugin.Kind == "descheduler" {
		return plugins.Manifest{ID: "io.kubephos.descheduler.kubernetes.managed", Name: "Managed descheduler", Version: "0.1.0", Description: "Runs a catalogued descheduler image with a validated policy against selected application workloads.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","targetBindingRef","image","configFile","intervalSeconds"],"properties":{"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"targetBindingRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"TargetBinding","x-kubephos-artifact-version":"v1alpha1"},"image":{"type":"string","format":"kubephos-strategy-image"},"configFile":{"type":"string","title":"Descheduler policy","description":"Complete DeschedulerPolicy YAML.","x-kubephos-multiline":true,"minLength":1,"maxLength":131072},"intervalSeconds":{"type":"integer","title":"Descheduling interval in seconds","minimum":10,"maximum":86400,"default":60}}}`), ArtifactInputs: contracts(), ArtifactOutputs: []domain.ArtifactContract{{Type: "StrategyDeployment", Version: "v1alpha1"}}, Targeting: &plugins.Targeting{RequiredTrait: "schedulable"}, Capabilities: []string{"descheduler.kubernetes.install", "descheduler.kubernetes.preflight", "descheduler.kubernetes.cleanup", "lifecycle.cleanup"}, Permissions: []string{"cluster.admin"}}
	}
	return plugins.Manifest{ID: "io.kubephos.autoscaler.kubernetes.cpa", Name: "Custom Pod Autoscaler", Version: "0.2.0", Description: "Creates one isolated CustomPodAutoscaler for every selected scalable workload.", Schema: json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","targetBindingRef","image","intervalSeconds","minReplicas","maxReplicas","parameters","targetOverrides"],"properties":{"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"targetBindingRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"TargetBinding","x-kubephos-artifact-version":"v1alpha1"},"image":{"type":"string","format":"kubephos-strategy-image"},"intervalSeconds":{"type":"integer","title":"Evaluation interval in seconds","minimum":5,"maximum":3600,"default":15},"minReplicas":{"type":"integer","title":"Minimum replicas","minimum":1,"maximum":1000,"default":1},"maxReplicas":{"type":"integer","title":"Maximum replicas","minimum":1,"maximum":1000,"default":10},"parameters":{"type":"string","title":"Autoscaler parameters","description":"JSON object passed unchanged to the autoscaler image for every selected workload.","x-kubephos-multiline":true,"minLength":2,"maxLength":131072,"default":"{}"},"targetOverrides":{"type":"string","title":"Per-microservice overrides","description":"JSON object keyed by application component ID.","minLength":2,"maxLength":524288,"default":"{}"}}}`), ArtifactInputs: contracts(), ArtifactOutputs: []domain.ArtifactContract{{Type: "StrategyDeployment", Version: "v1alpha1"}}, Targeting: &plugins.Targeting{RequiredTrait: "scalable"}, Capabilities: []string{"autoscaler.kubernetes.install", "autoscaler.kubernetes.preflight", "autoscaler.kubernetes.cleanup", "lifecycle.cleanup"}, Permissions: []string{"cluster.admin"}}
}

func contracts() []domain.ArtifactContract {
	return []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "TargetBinding", Version: "v1alpha1"}}
}

func (plugin Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalid(report, "$", err.Error())
	}
	var spec Spec
	if json.Unmarshal(invocation.Input, &spec) != nil {
		return invalid(report, "$", "Configuration must be valid JSON.")
	}
	for path, reference := range map[string]string{"clusterConnectionRef": spec.ClusterConnectionRef, "applicationDeploymentRef": spec.ApplicationDeploymentRef, "targetBindingRef": spec.TargetBindingRef} {
		if !strings.HasPrefix(reference, "art_") {
			return invalid(report, path, "Select a verified artifact.")
		}
	}
	if !validImage(spec.Image) {
		return invalid(report, "image", "Select a verified image mirrored into Harbor.")
	}
	if plugin.Kind == "descheduler" {
		if spec.IntervalSeconds < 10 || spec.IntervalSeconds > 86400 {
			return invalid(report, "intervalSeconds", "Interval must be between 10 seconds and one day.")
		}
		var policy map[string]any
		if len(spec.ConfigFile) > 131072 || yaml.Unmarshal([]byte(spec.ConfigFile), &policy) != nil || policy["kind"] != "DeschedulerPolicy" {
			return invalid(report, "configFile", "Provide a valid DeschedulerPolicy YAML document up to 128 KiB.")
		}
	} else {
		if err := validateAutoscalerSettings(autoscalerSettings{IntervalSeconds: spec.IntervalSeconds, MinReplicas: spec.MinReplicas, MaxReplicas: spec.MaxReplicas, Parameters: spec.Parameters}); err != nil {
			return invalid(report, "minReplicas", err.Error())
		}
		overrides, err := parseTargetOverrides(spec.TargetOverrides)
		if err != nil {
			return invalid(report, "targetOverrides", err.Error())
		}
		for target, settings := range overrides {
			if strings.TrimSpace(target) == "" || len(target) > 63 {
				return invalid(report, "targetOverrides", "Override target IDs must contain between one and 63 characters.")
			}
			if err := validateAutoscalerSettings(settings); err != nil {
				return invalid(report, "targetOverrides."+target, err.Error())
			}
		}
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "Cluster health, target ownership, API admission, strategy image and runtime readiness will be checked before the next step."})
	return report
}

func (plugin Plugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return domain.Plan{}, err
	}
	marker := make([]byte, 8)
	if _, err := rand.Read(marker); err != nil {
		return domain.Plan{}, err
	}
	name := "kubephos-" + plugin.Kind + "-" + hex.EncodeToString(marker)[:10]
	input, _ := json.Marshal(stepInput{Spec: spec, Name: name, Marker: hex.EncodeToString(marker)})
	return domain.Plan{PluginID: plugin.Manifest().ID, Steps: []domain.PlanStep{{ID: "install-" + plugin.Kind, Name: "Install and verify " + plugin.Kind, Input: input, Mutating: true, ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}, {Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef}, {Name: "target-binding", Type: "TargetBinding", Version: "v1alpha1", ArtifactID: spec.TargetBindingRef}}, Outputs: []domain.ArtifactOutput{{Name: "strategy-deployment", Type: "StrategyDeployment", Version: "v1alpha1", MediaType: "application/json", Source: "/strategyDeployment"}}}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil || validateArtifacts(plugin.Kind, cluster, application, binding) != nil {
		return unhealthy("Resolved cluster, application or target artifacts are invalid", "artifacts", "invalid"), nil
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API is not ready: "+err.Error(), "api", "unhealthy"), nil
	}
	if step.Cleanup {
		if err := plugin.validateCleanupOwnership(ctx, cluster, application, binding, input); err != nil {
			return unhealthy(err.Error(), "ownership", "conflict"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Owned strategy resources are safe to remove", Checks: map[string]string{"ownership": input.Marker}}, nil
	}
	if plugin.Kind == "autoscaler" {
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "customresourcedefinition", "custompodautoscalers.custompodautoscaler.com"); err != nil {
			return unhealthy("Custom Pod Autoscaler operator is not installed", "cpaOperator", "unavailable"), nil
		}
	}
	manifest, err := plugin.manifest(input, application, binding)
	if err != nil {
		return unhealthy(err.Error(), "configuration", "invalid"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return unhealthy("Kubernetes rejected the generated resources: "+err.Error(), "admission", "rejected"), nil
	}
	_ = log("info", fmt.Sprintf("Validated %s image and %d selected application targets", plugin.Kind, len(binding.Spec.Targets)))
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Strategy passed every preflight gate", Checks: map[string]string{"api": "ready", "admission": "accepted", "targets": fmt.Sprint(len(binding.Spec.Targets))}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return nil, err
	}
	manifest, err := plugin.manifest(input, application, binding)
	if err != nil {
		return nil, err
	}
	if _, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "-f", "-"); err != nil {
		return nil, err
	}
	if plugin.Kind == "descheduler" {
		if _, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/descheduler", "-n", input.Name, "--timeout=5m"); err != nil {
			return nil, err
		}
	} else if err := plugin.verifyAutoscalers(ctx, cluster, application, binding, input); err != nil {
		return nil, err
	}
	_ = log("info", plugin.Kind+" runtime is ready")
	targets := make([]string, len(binding.Spec.Targets))
	for index, target := range binding.Spec.Targets {
		targets[index] = target.ID
	}
	value := result{StrategyDeployment: deploymentArtifact{APIVersion: artifactAPI, Kind: "StrategyDeployment"}}
	value.StrategyDeployment.Metadata.Name = input.Name
	value.StrategyDeployment.Metadata.Version = imageVersion(input.Image)
	value.StrategyDeployment.Spec.StrategyKind = plugin.Kind
	value.StrategyDeployment.Spec.ClusterServer = cluster.Spec.Server
	value.StrategyDeployment.Spec.Namespace = application.Spec.Namespace
	value.StrategyDeployment.Spec.Image = input.Image
	value.StrategyDeployment.Spec.Targets = targets
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, _ plugins.Logger) (domain.HealthReport, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if step.Cleanup {
		if err := plugin.verifyCleanup(ctx, cluster, application, binding, input); err != nil {
			return unhealthy(err.Error(), "cleanup", "incomplete"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Strategy resources are absent", Checks: map[string]string{"cleanup": "verified"}}, nil
	}
	var value result
	if json.Unmarshal(raw, &value) != nil || value.StrategyDeployment.Spec.Image != input.Image || value.StrategyDeployment.Spec.StrategyKind != plugin.Kind {
		return unhealthy("Strategy result artifact is invalid", "artifact", "invalid"), nil
	}
	if plugin.Kind == "descheduler" {
		if _, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/descheduler", "-n", input.Name, "--timeout=60s"); err != nil {
			return unhealthy(err.Error(), "runtime", "unhealthy"), nil
		}
	} else if err := plugin.verifyAutoscalers(ctx, cluster, application, binding, input); err != nil {
		return unhealthy(err.Error(), "runtime", "unhealthy"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Strategy runtime and selected targets are ready", Checks: map[string]string{"image": input.Image, "targets": fmt.Sprint(len(binding.Spec.Targets))}}, nil
}

func (plugin Plugin) validateCleanupOwnership(ctx context.Context, cluster clusterConnection, application applicationDeployment, binding targetBinding, input stepInput) error {
	if plugin.Kind == "descheduler" {
		value, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "namespace", input.Name, "-o", "json")
		if err != nil && isNotFound(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !strings.Contains(value, input.Marker) {
			return errors.New("descheduler namespace is not owned by this run")
		}
		return nil
	}
	for _, target := range binding.Spec.Targets {
		value, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "custompodautoscaler", resourceName(input.Name, target.ID), "-n", application.Spec.Namespace, "-o", "json")
		if err != nil && isNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		if !strings.Contains(value, input.Marker) {
			return fmt.Errorf("autoscaler for %s is not owned by this run", target.ID)
		}
	}
	return nil
}

func (plugin Plugin) verifyCleanup(ctx context.Context, cluster clusterConnection, application applicationDeployment, binding targetBinding, input stepInput) error {
	if plugin.Kind == "descheduler" {
		_, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "namespace", input.Name)
		if err == nil {
			return errors.New("descheduler namespace still exists")
		}
		if !isNotFound(err) {
			return err
		}
		_, err = plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "clusterrolebinding", input.Name)
		if err == nil {
			return errors.New("descheduler cluster role binding still exists")
		}
		if !isNotFound(err) {
			return err
		}
		return nil
	}
	for _, target := range binding.Spec.Targets {
		_, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "custompodautoscaler", resourceName(input.Name, target.ID), "-n", application.Spec.Namespace)
		if err == nil {
			return fmt.Errorf("autoscaler for %s still exists", target.ID)
		}
		if !isNotFound(err) {
			return err
		}
	}
	return nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, _ plugins.Logger) error {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return err
	}
	manifest, err := plugin.manifest(input, application, binding)
	if err != nil {
		return err
	}
	_, err = plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, manifest, "delete", "--ignore-not-found", "--wait=true", "-f", "-")
	return err
}

func (plugin Plugin) manifest(input stepInput, application applicationDeployment, binding targetBinding) ([]byte, error) {
	if plugin.Kind == "descheduler" {
		return deschedulerManifest(input), nil
	}
	return autoscalerManifest(input, application, binding)
}

func deschedulerManifest(input stepInput) []byte {
	labels := map[string]string{"app.kubernetes.io/name": "kubephos-descheduler", "app.kubernetes.io/managed-by": "kubephos"}
	annotations := map[string]string{ownershipKey: input.Marker}
	objects := []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": input.Name, "labels": labels, "annotations": annotations}},
		{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "descheduler", "namespace": input.Name, "labels": labels, "annotations": annotations}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": map[string]any{"name": input.Name, "labels": labels, "annotations": annotations}, "subjects": []map[string]string{{"kind": "ServiceAccount", "name": "descheduler", "namespace": input.Name}}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "cluster-admin"}},
		{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "policy", "namespace": input.Name, "labels": labels, "annotations": annotations}, "data": map[string]string{"policy.yaml": input.ConfigFile}},
		{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "descheduler", "namespace": input.Name, "labels": labels, "annotations": annotations}, "spec": map[string]any{"replicas": 1, "selector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/name": "kubephos-descheduler"}}, "template": map[string]any{"metadata": map[string]any{"labels": labels, "annotations": annotations}, "spec": map[string]any{"serviceAccountName": "descheduler", "nodeSelector": map[string]string{"kubephos.dev/role": "management"}, "containers": []map[string]any{{"name": "descheduler", "image": input.Image, "imagePullPolicy": "IfNotPresent", "args": []string{"--policy-config-file=/policy-dir/policy.yaml", "--descheduling-interval=" + fmt.Sprint(input.IntervalSeconds) + "s"}, "volumeMounts": []map[string]any{{"name": "policy", "mountPath": "/policy-dir", "readOnly": true}}, "resources": map[string]any{"requests": map[string]string{"cpu": "50m", "memory": "64Mi"}, "limits": map[string]string{"cpu": "500m", "memory": "256Mi"}}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true}}}, "volumes": []map[string]any{{"name": "policy", "configMap": map[string]string{"name": "policy"}}}}}}},
	}
	return marshalDocuments(objects)
}

func autoscalerManifest(input stepInput, application applicationDeployment, binding targetBinding) ([]byte, error) {
	overrides, err := parseTargetOverrides(input.TargetOverrides)
	if err != nil {
		return nil, err
	}
	targets := map[string]bool{}
	for _, target := range binding.Spec.Targets {
		targets[target.ID] = true
	}
	for target := range overrides {
		if !targets[target] {
			return nil, fmt.Errorf("autoscaler override target %s is not selected", target)
		}
	}
	objects := []map[string]any{}
	for _, target := range binding.Spec.Targets {
		settings := autoscalerSettings{IntervalSeconds: input.IntervalSeconds, MinReplicas: input.MinReplicas, MaxReplicas: input.MaxReplicas, Parameters: input.Parameters}
		if override, ok := overrides[target.ID]; ok {
			settings = override
		}
		configuration, err := autoscalerConfiguration(settings)
		if err != nil {
			return nil, fmt.Errorf("autoscaler configuration for %s: %w", target.ID, err)
		}
		name := resourceName(input.Name, target.ID)
		metadata := map[string]any{"name": name, "namespace": application.Spec.Namespace, "labels": map[string]string{"app.kubernetes.io/managed-by": "kubephos", "kubephos.dev/application-component": target.ID}, "annotations": map[string]string{ownershipKey: input.Marker}}
		container := map[string]any{"name": "custom-pod-autoscaler", "image": input.Image, "imagePullPolicy": "IfNotPresent", "volumeMounts": []map[string]any{{"name": "configuration", "mountPath": "/etc/custom-pod-autoscaler", "readOnly": true}}, "resources": map[string]any{"requests": map[string]string{"cpu": "25m", "memory": "64Mi"}, "limits": map[string]string{"cpu": "500m", "memory": "256Mi"}}}
		podSpec := map[string]any{"nodeSelector": map[string]string{"kubephos.dev/role": "management"}, "containers": []map[string]any{container}, "volumes": []map[string]any{{"name": "configuration", "configMap": map[string]string{"name": name}}}}
		autoscalerSpec := map[string]any{"scaleTargetRef": map[string]string{"apiVersion": target.APIVersion, "kind": target.Kind, "name": target.Name}, "config": []map[string]string{{"name": "interval", "value": fmt.Sprint(settings.IntervalSeconds * 1000)}, {"name": "minReplicas", "value": fmt.Sprint(settings.MinReplicas)}, {"name": "maxReplicas", "value": fmt.Sprint(settings.MaxReplicas)}}, "template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{ownershipKey: input.Marker}}, "spec": podSpec}}
		objects = append(objects,
			map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": metadata, "data": map[string]string{"config.json": string(configuration)}},
			map[string]any{"apiVersion": "custompodautoscaler.com/v1", "kind": "CustomPodAutoscaler", "metadata": metadata, "spec": autoscalerSpec},
		)
	}
	return marshalDocuments(objects), nil
}

func parseTargetOverrides(raw string) (map[string]autoscalerSettings, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if len(raw) > 524288 {
		return nil, errors.New("per-microservice overrides must not exceed 512 KiB")
	}
	value := map[string]autoscalerSettings{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
		return nil, errors.New("per-microservice overrides must be a JSON object")
	}
	return value, nil
}

func validateAutoscalerSettings(settings autoscalerSettings) error {
	if settings.IntervalSeconds < 5 || settings.IntervalSeconds > 3600 || settings.MinReplicas < 1 || settings.MaxReplicas < settings.MinReplicas || settings.MaxReplicas > 1000 {
		return errors.New("interval and replica bounds are invalid")
	}
	var parameters map[string]any
	if len(settings.Parameters) > 131072 || json.Unmarshal([]byte(settings.Parameters), &parameters) != nil || parameters == nil {
		return errors.New("autoscaler parameters must be a JSON object up to 128 KiB")
	}
	return nil
}

func autoscalerConfiguration(settings autoscalerSettings) ([]byte, error) {
	if err := validateAutoscalerSettings(settings); err != nil {
		return nil, err
	}
	parameters := map[string]any{}
	if err := json.Unmarshal([]byte(settings.Parameters), &parameters); err != nil {
		return nil, err
	}
	parameters["minReplicas"] = settings.MinReplicas
	parameters["maxReplicas"] = settings.MaxReplicas
	return json.Marshal(parameters)
}

func (plugin Plugin) verifyAutoscalers(ctx context.Context, cluster clusterConnection, application applicationDeployment, binding targetBinding, input stepInput) error {
	for _, target := range binding.Spec.Targets {
		value, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "custompodautoscaler", resourceName(input.Name, target.ID), "-n", application.Spec.Namespace, "-o", "json")
		if err != nil || !strings.Contains(value, input.Image) || !strings.Contains(value, input.Marker) {
			return fmt.Errorf("autoscaler for %s is not ready or has unexpected ownership", target.ID)
		}
	}
	return nil
}

func resolve(step domain.PlanStep) (stepInput, clusterConnection, applicationDeployment, targetBinding, error) {
	var input stepInput
	var cluster clusterConnection
	var application applicationDeployment
	var binding targetBinding
	if json.Unmarshal(step.Input, &input) != nil {
		return input, cluster, application, binding, errors.New("strategy step input is invalid")
	}
	values := []struct {
		name  string
		value any
	}{{"cluster-connection", &cluster}, {"application-deployment", &application}, {"target-binding", &binding}}
	for _, value := range values {
		artifact, ok := step.ResolvedInputs[value.name]
		if !ok || json.Unmarshal(artifact.Value, value.value) != nil {
			return input, cluster, application, binding, fmt.Errorf("verified %s artifact is unavailable", value.name)
		}
	}
	return input, cluster, application, binding, nil
}

func validateArtifacts(kind string, cluster clusterConnection, application applicationDeployment, binding targetBinding) error {
	trait := "schedulable"
	if kind == "autoscaler" {
		trait = "scalable"
	}
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Spec.Kubeconfig == "" || application.APIVersion != artifactAPI || application.Kind != "ApplicationDeployment" || application.Spec.ClusterServer != cluster.Spec.Server || application.Metadata.OwnershipMarker == "" || binding.APIVersion != artifactAPI || binding.Kind != "TargetBinding" || binding.Spec.RequiredTrait != trait || len(binding.Spec.Targets) == 0 {
		return errors.New("artifact contract mismatch")
	}
	for _, target := range binding.Spec.Targets {
		if target.ID == "" || target.Name == "" || !contains(target.Traits, trait) {
			return errors.New("target contract mismatch")
		}
	}
	return nil
}

func marshalDocuments(objects []map[string]any) []byte {
	parts := make([]string, 0, len(objects))
	for _, object := range objects {
		value, _ := json.Marshal(object)
		parts = append(parts, string(value))
	}
	return []byte(strings.Join(parts, "\n---\n"))
}

func resourceName(prefix, target string) string {
	value := prefix + "-" + target
	if len(value) > 63 {
		digest := sha256.Sum256([]byte(value))
		value = strings.TrimRight(value[:54], "-") + "-" + hex.EncodeToString(digest[:4])
	}
	return strings.TrimRight(value, "-")
}

func validImage(value string) bool {
	parts := strings.Split(value, "@sha256:")
	if len(parts) != 2 || parts[0] == "" || len(parts[1]) != 64 || strings.ContainsAny(value, " \t\r\n") {
		return false
	}
	_, err := hex.DecodeString(parts[1])
	return err == nil
}

func imageVersion(value string) string {
	parts := strings.Split(value, "@sha256:")
	if len(parts) == 2 && len(parts[1]) >= 12 {
		return parts[1][:12]
	}
	return "unknown"
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	return err != nil && (strings.Contains(strings.ToLower(err.Error()), "notfound") || strings.Contains(strings.ToLower(err.Error()), "not found"))
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(message, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: message, Checks: map[string]string{key: value}}
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return kubectlRunner{}
}

type kubectlRunner struct{}

func (kubectlRunner) Run(ctx context.Context, kubeconfig string, stdin []byte, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "kubectl", args...)
	command.Env = append(os.Environ(), "KUBECONFIG="+kubeconfig)
	command.Stdin = bytes.NewReader(stdin)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}
