// Package hpa runs the Kubernetes HorizontalPodAutoscaler as an experiment autoscaler:
// one autoscaling/v2 HorizontalPodAutoscaler per selected scalable workload, scaling
// on the average CPU utilization of its pods.
package hpa

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	kubeconfigutil "kubephos.dev/kubephos/internal/kubeconfig"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID      = "io.kubephos.autoscaler.kubernetes.hpa"
	artifactAPI   = "artifacts.kubephos.dev/v1alpha1"
	ownershipKey  = "kubephos.dev/strategy-ownership"
	componentKey  = "kubephos.dev/application-component"
	scalableTrait = "scalable"
)

type Plugin struct {
	Runner commandRunner
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

// Settings are the scaling bounds of one workload; the spec values apply to every
// target without an override.
type Settings struct {
	MinReplicas    int `json:"minReplicas"`
	MaxReplicas    int `json:"maxReplicas"`
	CPUUtilization int `json:"cpuUtilization"`
}

type Spec struct {
	ClusterConnectionRef     string `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string `json:"applicationDeploymentRef"`
	TargetBindingRef         string `json:"targetBindingRef"`
	Settings
	TargetOverrides string `json:"targetOverrides,omitempty"`
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

type applicationDeployment struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		OwnershipMarker string `json:"ownershipMarker"`
	} `json:"metadata"`
	Spec struct {
		ClusterServer string `json:"clusterServer"`
		Namespace     string `json:"namespace"`
	} `json:"spec"`
}

type workloadTarget struct {
	ID         string   `json:"id"`
	APIVersion string   `json:"apiVersion"`
	Kind       string   `json:"kind"`
	Name       string   `json:"name"`
	Traits     []string `json:"traits"`
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
		Targets       []string `json:"targets"`
	} `json:"spec"`
}

type result struct {
	StrategyDeployment deploymentArtifact `json:"strategyDeployment"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Horizontal Pod Autoscaler", Version: "0.1.0",
		Description:     "Creates one Kubernetes HorizontalPodAutoscaler for every selected scalable workload, scaling on CPU utilization.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","targetBindingRef","minReplicas","maxReplicas","cpuUtilization","targetOverrides"],"properties":{"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"targetBindingRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"TargetBinding","x-kubephos-artifact-version":"v1alpha1"},"minReplicas":{"type":"integer","title":"Minimum replicas","minimum":1,"maximum":1000,"default":1,"x-kubephos-order":1},"maxReplicas":{"type":"integer","title":"Maximum replicas","minimum":1,"maximum":1000,"default":10,"x-kubephos-order":2},"cpuUtilization":{"type":"integer","title":"Target CPU (%)","description":"Average CPU utilization the autoscaler keeps, relative to the CPU requests of the pods.","minimum":1,"maximum":500,"default":70,"x-kubephos-order":3},"targetOverrides":{"type":"string","title":"Per-microservice overrides","description":"JSON object keyed by application component ID.","minLength":2,"maxLength":524288,"default":"{}"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "TargetBinding", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "StrategyDeployment", Version: "v1alpha1"}},
		Targeting:       &plugins.Targeting{RequiredTrait: scalableTrait},
		Capabilities:    []string{"autoscaler.kubernetes.install", "autoscaler.kubernetes.preflight", "autoscaler.kubernetes.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"cluster.admin"},
	}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
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
	if err := spec.Settings.validate(); err != nil {
		return invalid(report, "minReplicas", err.Error())
	}
	overrides, err := parseOverrides(spec.TargetOverrides)
	if err != nil {
		return invalid(report, "targetOverrides", err.Error())
	}
	for target, settings := range overrides {
		if strings.TrimSpace(target) == "" || len(target) > 63 {
			return invalid(report, "targetOverrides", "Override target IDs must contain between one and 63 characters.")
		}
		if err := settings.validate(); err != nil {
			return invalid(report, "targetOverrides."+target, err.Error())
		}
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "Cluster health, the metrics API, target ownership and API admission will be checked before the next step."})
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
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return domain.Plan{}, err
	}
	marker := hex.EncodeToString(random)
	input, _ := json.Marshal(stepInput{Spec: spec, Name: "kubephos-hpa-" + marker[:10], Marker: marker})
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "install-hpa", Name: "Install and verify horizontal pod autoscalers", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}, {Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef}, {Name: "target-binding", Type: "TargetBinding", Version: "v1alpha1", ArtifactID: spec.TargetBindingRef}},
		Outputs:        []domain.ArtifactOutput{{Name: "strategy-deployment", Type: "StrategyDeployment", Version: "v1alpha1", MediaType: "application/json", Source: "/strategyDeployment"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil || validateArtifacts(cluster, application, binding) != nil {
		return unhealthy("Resolved cluster, application or target artifacts are invalid", "artifacts", "invalid"), nil
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API is not ready: "+err.Error(), "api", "unhealthy"), nil
	}
	if step.Cleanup {
		if err := plugin.verifyOwnership(ctx, cluster, application, binding, input, true); err != nil {
			return unhealthy(err.Error(), "ownership", "conflict"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Owned autoscalers are safe to remove", Checks: map[string]string{"ownership": input.Marker}}, nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/apis/metrics.k8s.io/v1beta1"); err != nil {
		return unhealthy("The Kubernetes metrics API is unavailable; HorizontalPodAutoscalers need metrics-server", "metricsAPI", "unavailable"), nil
	}
	manifest, err := autoscalerManifest(input, application, binding)
	if err != nil {
		return unhealthy(err.Error(), "configuration", "invalid"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return unhealthy("Kubernetes rejected the generated autoscalers: "+err.Error(), "admission", "rejected"), nil
	}
	_ = log("info", fmt.Sprintf("Validated horizontal pod autoscalers for %d selected application targets", len(binding.Spec.Targets)))
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Autoscalers passed every preflight gate", Checks: map[string]string{"api": "ready", "metricsAPI": "ready", "admission": "accepted", "targets": fmt.Sprint(len(binding.Spec.Targets))}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return nil, err
	}
	manifest, err := autoscalerManifest(input, application, binding)
	if err != nil {
		return nil, err
	}
	if _, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "-f", "-"); err != nil {
		return nil, err
	}
	if err := plugin.verifyOwnership(ctx, cluster, application, binding, input, false); err != nil {
		return nil, err
	}
	_ = log("info", "horizontal pod autoscalers are ready")
	value := result{StrategyDeployment: deploymentArtifact{APIVersion: artifactAPI, Kind: "StrategyDeployment"}}
	value.StrategyDeployment.Metadata.Name = input.Name
	value.StrategyDeployment.Metadata.Version = "autoscaling/v2"
	value.StrategyDeployment.Spec.StrategyKind = "autoscaler"
	value.StrategyDeployment.Spec.ClusterServer = cluster.Spec.Server
	value.StrategyDeployment.Spec.Namespace = application.Spec.Namespace
	for _, target := range binding.Spec.Targets {
		value.StrategyDeployment.Spec.Targets = append(value.StrategyDeployment.Spec.Targets, target.ID)
	}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, _ plugins.Logger) (domain.HealthReport, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if step.Cleanup {
		if err := plugin.verifyAbsent(ctx, cluster, application, binding, input); err != nil {
			return unhealthy(err.Error(), "cleanup", "incomplete"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Autoscalers are absent", Checks: map[string]string{"cleanup": "verified"}}, nil
	}
	var value result
	if json.Unmarshal(raw, &value) != nil || value.StrategyDeployment.Metadata.Name != input.Name || value.StrategyDeployment.Spec.StrategyKind != "autoscaler" {
		return unhealthy("Autoscaler result artifact is invalid", "artifact", "invalid"), nil
	}
	if err := plugin.verifyOwnership(ctx, cluster, application, binding, input, false); err != nil {
		return unhealthy(err.Error(), "runtime", "unhealthy"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Autoscalers of the selected targets are ready", Checks: map[string]string{"targets": fmt.Sprint(len(binding.Spec.Targets))}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, _ plugins.Logger) error {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return err
	}
	manifest, err := autoscalerManifest(input, application, binding)
	if err != nil {
		return err
	}
	_, err = plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, manifest, "delete", "--ignore-not-found", "--wait=true", "-f", "-")
	return err
}

// verifyOwnership checks that the autoscaler of every target carries this run's marker;
// with allowMissing, absent autoscalers are accepted too.
func (plugin Plugin) verifyOwnership(ctx context.Context, cluster clusterConnection, application applicationDeployment, binding targetBinding, input stepInput, allowMissing bool) error {
	for _, target := range binding.Spec.Targets {
		marker, err := plugin.ownershipMarker(ctx, cluster, application, resourceName(input.Name, target.ID))
		if err != nil {
			return err
		}
		if marker == "" && allowMissing {
			continue
		}
		if marker != input.Marker {
			return fmt.Errorf("autoscaler for %s is missing or not owned by this run", target.ID)
		}
	}
	return nil
}

func (plugin Plugin) verifyAbsent(ctx context.Context, cluster clusterConnection, application applicationDeployment, binding targetBinding, input stepInput) error {
	for _, target := range binding.Spec.Targets {
		value, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "horizontalpodautoscaler", resourceName(input.Name, target.ID), "-n", application.Spec.Namespace, "--ignore-not-found", "-o", "name")
		if err != nil {
			return err
		}
		if strings.TrimSpace(value) != "" {
			return fmt.Errorf("autoscaler for %s still exists", target.ID)
		}
	}
	return nil
}

func (plugin Plugin) ownershipMarker(ctx context.Context, cluster clusterConnection, application applicationDeployment, name string) (string, error) {
	jsonPath := "jsonpath={.metadata.annotations." + strings.ReplaceAll(ownershipKey, ".", "\\.") + "}"
	value, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "horizontalpodautoscaler", name, "-n", application.Spec.Namespace, "--ignore-not-found", "-o", jsonPath)
	return strings.TrimSpace(value), err
}

// autoscalerManifest renders one HorizontalPodAutoscaler per target, applying the
// per-target overrides over the spec settings.
func autoscalerManifest(input stepInput, application applicationDeployment, binding targetBinding) ([]byte, error) {
	overrides, err := parseOverrides(input.TargetOverrides)
	if err != nil {
		return nil, err
	}
	for target := range overrides {
		if !slices.ContainsFunc(binding.Spec.Targets, func(selected workloadTarget) bool { return selected.ID == target }) {
			return nil, fmt.Errorf("autoscaler override target %s is not selected", target)
		}
	}
	documents := make([]string, 0, len(binding.Spec.Targets))
	for _, target := range binding.Spec.Targets {
		settings := input.Settings
		if override, ok := overrides[target.ID]; ok {
			settings = override
		}
		if err := settings.validate(); err != nil {
			return nil, fmt.Errorf("autoscaler settings for %s: %w", target.ID, err)
		}
		object := map[string]any{
			"apiVersion": "autoscaling/v2",
			"kind":       "HorizontalPodAutoscaler",
			"metadata": map[string]any{
				"name":        resourceName(input.Name, target.ID),
				"namespace":   application.Spec.Namespace,
				"labels":      map[string]string{"app.kubernetes.io/managed-by": "kubephos", componentKey: target.ID},
				"annotations": map[string]string{ownershipKey: input.Marker},
			},
			"spec": map[string]any{
				"scaleTargetRef": map[string]string{"apiVersion": target.APIVersion, "kind": target.Kind, "name": target.Name},
				"minReplicas":    settings.MinReplicas,
				"maxReplicas":    settings.MaxReplicas,
				"metrics": []map[string]any{{"type": "Resource", "resource": map[string]any{
					"name":   "cpu",
					"target": map[string]any{"type": "Utilization", "averageUtilization": settings.CPUUtilization},
				}}},
			},
		}
		value, _ := json.Marshal(object)
		documents = append(documents, string(value))
	}
	return []byte(strings.Join(documents, "\n---\n")), nil
}

func (settings Settings) validate() error {
	if settings.MinReplicas < 1 || settings.MaxReplicas < settings.MinReplicas || settings.MaxReplicas > 1000 {
		return errors.New("replica bounds must satisfy 1 <= minimum <= maximum <= 1000")
	}
	if settings.CPUUtilization < 1 || settings.CPUUtilization > 500 {
		return errors.New("target CPU utilization must be between 1% and 500%")
	}
	return nil
}

func parseOverrides(raw string) (map[string]Settings, error) {
	if strings.TrimSpace(raw) == "" {
		raw = "{}"
	}
	if len(raw) > 524288 {
		return nil, errors.New("per-microservice overrides must not exceed 512 KiB")
	}
	value := map[string]Settings{}
	if err := json.Unmarshal([]byte(raw), &value); err != nil || value == nil {
		return nil, errors.New("per-microservice overrides must be a JSON object")
	}
	return value, nil
}

func resolve(step domain.PlanStep) (stepInput, clusterConnection, applicationDeployment, targetBinding, error) {
	var input stepInput
	var cluster clusterConnection
	var application applicationDeployment
	var binding targetBinding
	if json.Unmarshal(step.Input, &input) != nil {
		return input, cluster, application, binding, errors.New("autoscaler step input is invalid")
	}
	for name, value := range map[string]any{"cluster-connection": &cluster, "application-deployment": &application, "target-binding": &binding} {
		artifact, ok := step.ResolvedInputs[name]
		if !ok || json.Unmarshal(artifact.Value, value) != nil {
			return input, cluster, application, binding, fmt.Errorf("verified %s artifact is unavailable", name)
		}
	}
	return input, cluster, application, binding, nil
}

func validateArtifacts(cluster clusterConnection, application applicationDeployment, binding targetBinding) error {
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Spec.Kubeconfig == "" ||
		application.APIVersion != artifactAPI || application.Kind != "ApplicationDeployment" || application.Spec.ClusterServer != cluster.Spec.Server || application.Spec.Namespace == "" || application.Metadata.OwnershipMarker == "" ||
		binding.APIVersion != artifactAPI || binding.Kind != "TargetBinding" || binding.Spec.RequiredTrait != scalableTrait || len(binding.Spec.Targets) == 0 {
		return errors.New("artifact contract mismatch")
	}
	for _, target := range binding.Spec.Targets {
		if target.ID == "" || target.Name == "" || !slices.Contains(target.Traits, scalableTrait) {
			return errors.New("target contract mismatch")
		}
	}
	return nil
}

func resourceName(prefix, target string) string {
	value := prefix + "-" + target
	if len(value) > 63 {
		digest := sha256.Sum256([]byte(value))
		value = strings.TrimRight(value[:54], "-") + "-" + hex.EncodeToString(digest[:4])
	}
	return strings.TrimRight(value, "-")
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
	output, err := kubeconfigutil.RunKubectl(ctx, kubeconfig, stdin, args...)
	message := strings.TrimSpace(string(output))
	if err != nil {
		if message == "" {
			message = err.Error()
		}
		if len(message) > 2000 {
			message = message[:2000]
		}
		return "", fmt.Errorf("kubectl %s: %s", strings.Join(args, " "), message)
	}
	return message, nil
}
