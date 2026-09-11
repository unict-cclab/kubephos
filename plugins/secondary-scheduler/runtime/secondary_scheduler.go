package secondaryscheduler

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID          = "io.kubephos.scheduler.kubernetes.secondary"
	artifactAPI       = "artifacts.kubephos.dev/v1alpha1"
	schedulerVersion  = "v1.36.0"
	schedulerImage    = "registry.k8s.io/kube-scheduler:v1.36.0"
	ownershipKey      = "kubephos.dev/scheduler-ownership"
	previousKey       = "kubephos.dev/previous-scheduler"
	defaultSentinel   = "__kubernetes_default__"
	applicationOwnKey = "kubephos.dev/ownership-marker"
)

type Plugin struct {
	Runner            commandRunner
	StabilityChecks   int
	StabilityInterval time.Duration
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef     string `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string `json:"applicationDeploymentRef"`
	TargetBindingRef         string `json:"targetBindingRef"`
}

type stepInput struct {
	Spec
	Marker        string `json:"marker"`
	Namespace     string `json:"namespace"`
	SchedulerName string `json:"schedulerName"`
}

type clusterConnection struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Distribution string `json:"distribution"`
		Server       string `json:"server"`
		Kubeconfig   string `json:"kubeconfig"`
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
		Name            string `json:"name"`
		Version         string `json:"version"`
		OwnershipMarker string `json:"ownershipMarker"`
	} `json:"metadata"`
	Spec struct {
		ApplicationRef string           `json:"applicationRef"`
		ClusterServer  string           `json:"clusterServer"`
		Namespace      string           `json:"namespace"`
		ManifestDigest string           `json:"manifestDigest"`
		Workloads      []workloadTarget `json:"workloads"`
	} `json:"spec"`
}

type targetBinding struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		SourceArtifactID string           `json:"sourceArtifactId"`
		SourceDigest     string           `json:"sourceDigest"`
		RequiredTrait    string           `json:"requiredTrait"`
		Targets          []workloadTarget `json:"targets"`
	} `json:"spec"`
}

type schedulerDeployment struct {
	APIVersion string                      `json:"apiVersion"`
	Kind       string                      `json:"kind"`
	Metadata   schedulerDeploymentMetadata `json:"metadata"`
	Spec       schedulerDeploymentSpec     `json:"spec"`
}

type schedulerDeploymentMetadata struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	OwnershipMarker string `json:"ownershipMarker"`
}

type schedulerDeploymentSpec struct {
	ClusterServer            string           `json:"clusterServer"`
	Namespace                string           `json:"namespace"`
	SchedulerName            string           `json:"schedulerName"`
	Image                    string           `json:"image"`
	ApplicationDeploymentRef string           `json:"applicationDeploymentRef"`
	TargetBindingRef         string           `json:"targetBindingRef"`
	Targets                  []workloadTarget `json:"targets"`
}

type result struct {
	SchedulerDeployment schedulerDeployment `json:"schedulerDeployment"`
}

type kubernetesWorkload struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Spec struct {
		Template struct {
			Metadata struct {
				Labels      map[string]string `json:"labels"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
			Spec struct {
				SchedulerName string `json:"schedulerName"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
}

type podList struct {
	Items []struct {
		Spec struct {
			SchedulerName string `json:"schedulerName"`
			NodeName      string `json:"nodeName"`
		} `json:"spec"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type schedulerPodList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
			ContainerStatuses []struct {
				State struct {
					Waiting *struct {
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"waiting"`
					Terminated *struct {
						Reason  string `json:"reason"`
						Message string `json:"message"`
					} `json:"terminated"`
				} `json:"state"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Managed secondary scheduler", Version: "0.1.0",
		Description:     "Installs an isolated Kubernetes scheduler and assigns only workloads selected by a validated target binding.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","targetBindingRef"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"targetBindingRef":{"type":"string","title":"Workload binding","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"TargetBinding","x-kubephos-artifact-version":"v1alpha1"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "TargetBinding", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "SchedulerDeployment", Version: "v1alpha1"}},
		Capabilities:    []string{"scheduler.kubernetes.install", "scheduler.kubernetes.bind", "scheduler.kubernetes.preflight", "scheduler.kubernetes.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"cluster.admin"},
	}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalid(report, "$", err.Error())
	}
	var spec Spec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return invalid(report, "$", "Configuration must be valid JSON.")
	}
	references := []struct{ path, value string }{{"clusterConnectionRef", spec.ClusterConnectionRef}, {"applicationDeploymentRef", spec.ApplicationDeploymentRef}, {"targetBindingRef", spec.TargetBindingRef}}
	seen := map[string]bool{}
	for _, reference := range references {
		if !strings.HasPrefix(reference.value, "art_") {
			return invalid(report, reference.path, "Select a verified artifact.")
		}
		if seen[reference.value] {
			return invalid(report, reference.path, "Each input must reference its own typed artifact.")
		}
		seen[reference.value] = true
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will validate the cluster, application ownership, target compatibility, RBAC, scheduler rollout and actual Pod assignment."})
	return report
}

func (Plugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return domain.Plan{}, err
	}
	markerBytes := make([]byte, 16)
	if _, err := rand.Read(markerBytes); err != nil {
		return domain.Plan{}, err
	}
	marker := hex.EncodeToString(markerBytes)
	name := "kubephos-" + marker[:10]
	input, err := json.Marshal(stepInput{Spec: spec, Marker: marker, Namespace: name, SchedulerName: name})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "install-secondary-scheduler", Name: "Install, bind and verify secondary scheduler", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef},
			{Name: "target-binding", Type: "TargetBinding", Version: "v1alpha1", ArtifactID: spec.TargetBindingRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "scheduler-deployment", Type: "SchedulerDeployment", Version: "v1alpha1", MediaType: "application/json", Source: "/schedulerDeployment"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateArtifacts(input, cluster, application, binding); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	runner := plugin.runner()
	if err := log("info", "Validating Kubernetes API, permissions, application ownership and scheduler target binding"); err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	authorized, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "auth", "can-i", "*", "*", "--all-namespaces")
	if err != nil || strings.TrimSpace(authorized) != "yes" {
		return unhealthy("The cluster connection does not have the required administrative permissions", "authorization", "denied"), nil
	}
	applicationOwned, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, application.Spec.Namespace, application.Metadata.OwnershipMarker, applicationOwnKey)
	if err != nil || !applicationOwned {
		return unhealthy("The application namespace failed ownership verification", "application", "unowned"), nil
	}
	if step.Cleanup {
		if err := validateCleanupOwnership(ctx, runner, cluster.Spec.Kubeconfig, input, application.Spec.Namespace, binding.Spec.Targets); err != nil {
			return unhealthy(err.Error(), "ownership", "blocked"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The secondary scheduler is safe to remove", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "application": "owned", "resources": "owned-or-absent"}}, nil
	}
	if err := ensureSchedulerResourcesAbsent(ctx, runner, cluster.Spec.Kubeconfig, input); err != nil {
		return unhealthy(err.Error(), "resources", "conflict"), nil
	}
	manifest, err := schedulerManifest(input)
	if err != nil {
		return domain.HealthReport{}, err
	}
	foundation, _, err := partitionSchedulerManifest(manifest)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, foundation, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return unhealthy("Scheduler namespace or RBAC failed server-side admission: "+err.Error(), "admission", "rejected"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "--dry-run=client", "--validate=true", "-f", "-"); err != nil {
		return unhealthy("Scheduler component manifests failed schema validation: "+err.Error(), "schema", "rejected"), nil
	}
	if category, target, err := precheckTargets(ctx, runner, cluster.Spec.Kubeconfig, application.Spec.Namespace, input, binding.Spec.Targets); err != nil {
		value := target
		if category == "admission" {
			value = "rejected"
		}
		return unhealthy(err.Error(), category, value), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The scheduler installation and every workload binding passed preflight", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "application": "owned", "admission": "accepted", "targets": fmt.Sprint(len(binding.Spec.Targets)), "version": schedulerVersion}}, nil
}

func precheckTargets(ctx context.Context, runner commandRunner, kubeconfig, namespace string, input stepInput, targets []workloadTarget) (string, string, error) {
	type targetResult struct {
		id       string
		category string
		err      error
	}
	results := make(chan targetResult, len(targets))
	var group sync.WaitGroup
	for _, target := range targets {
		target := target
		group.Add(1)
		go func() {
			defer group.Done()
			workload, err := readWorkload(ctx, runner, kubeconfig, namespace, target)
			if err != nil {
				results <- targetResult{target.ID, "target", err}
				return
			}
			if err := validateUnboundWorkload(workload, target); err != nil {
				results <- targetResult{target.ID, "target", err}
				return
			}
			if err := verifyTargetPods(ctx, runner, kubeconfig, namespace, target, effectiveScheduler(workload.Spec.Template.Spec.SchedulerName)); err != nil {
				results <- targetResult{target.ID, "target-health", fmt.Errorf("target %s is not healthy before scheduler mutation: %w", target.ID, err)}
				return
			}
			patch, err := bindPatch(input, workload.Spec.Template.Spec.SchedulerName)
			if err != nil {
				results <- targetResult{target.ID, "target", err}
				return
			}
			if _, err := runner.Run(ctx, kubeconfig, patch, "patch", resourceName(target), "-n", namespace, "--type=merge", "--dry-run=server", "-p", string(patch)); err != nil {
				results <- targetResult{target.ID, "admission", fmt.Errorf("target %s failed server-side patch validation: %w", target.ID, err)}
				return
			}
			results <- targetResult{id: target.ID}
		}()
	}
	group.Wait()
	close(results)
	failures := []targetResult{}
	for result := range results {
		if result.err != nil {
			failures = append(failures, result)
		}
	}
	if len(failures) == 0 {
		return "", "", nil
	}
	sort.Slice(failures, func(left, right int) bool { return failures[left].id < failures[right].id })
	messages := make([]string, 0, len(failures))
	for _, failure := range failures {
		messages = append(messages, failure.err.Error())
	}
	return failures[0].category, failures[0].id, errors.New(strings.Join(messages, "; "))
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return nil, err
	}
	manifest, err := schedulerManifest(input)
	if err != nil {
		return nil, err
	}
	foundation, components, err := partitionSchedulerManifest(manifest)
	if err != nil {
		return nil, err
	}
	runner := plugin.runner()
	if err := log("info", "Installing and verifying the isolated scheduler namespace and RBAC resources"); err != nil {
		return nil, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, foundation, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("apply scheduler namespace and RBAC: %w", err)
	}
	owned, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Namespace, input.Marker, ownershipKey)
	if err != nil || !owned {
		return nil, errors.New("scheduler namespace failed ownership verification before component installation")
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, components, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return nil, fmt.Errorf("scheduler components failed server-side admission after namespace creation: %w", err)
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, components, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("apply scheduler components: %w", err)
	}
	if err := waitForScheduler(ctx, runner, cluster.Spec.Kubeconfig, input, log); err != nil {
		return nil, fmt.Errorf("scheduler readiness failed before workload mutation: %w", err)
	}
	if err := log("info", fmt.Sprintf("Binding %d validated workloads to scheduler %s", len(binding.Spec.Targets), input.SchedulerName)); err != nil {
		return nil, err
	}
	for _, target := range binding.Spec.Targets {
		workload, err := readWorkload(ctx, runner, cluster.Spec.Kubeconfig, application.Spec.Namespace, target)
		if err != nil {
			return nil, err
		}
		if err := validateUnboundWorkload(workload, target); err != nil {
			return nil, err
		}
		patch, err := bindPatch(input, workload.Spec.Template.Spec.SchedulerName)
		if err != nil {
			return nil, err
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "patch", resourceName(target), "-n", application.Spec.Namespace, "--type=merge", "-p", string(patch)); err != nil {
			return nil, fmt.Errorf("bind workload %s: %w", target.ID, err)
		}
		if err := plugin.waitForTargetStable(ctx, runner, cluster.Spec.Kubeconfig, application.Spec.Namespace, target, input); err != nil {
			_ = logTargetDiagnostics(ctx, runner, cluster.Spec.Kubeconfig, application.Spec.Namespace, target, log)
			return nil, fmt.Errorf("workload %s did not stabilize on scheduler %s: %w", target.ID, input.SchedulerName, err)
		}
	}
	value := result{SchedulerDeployment: schedulerDeployment{
		APIVersion: artifactAPI, Kind: "SchedulerDeployment",
		Metadata: schedulerDeploymentMetadata{Name: input.SchedulerName, Version: schedulerVersion, OwnershipMarker: input.Marker},
		Spec:     schedulerDeploymentSpec{ClusterServer: cluster.Spec.Server, Namespace: input.Namespace, SchedulerName: input.SchedulerName, Image: schedulerImage, ApplicationDeploymentRef: input.ApplicationDeploymentRef, TargetBindingRef: input.TargetBindingRef, Targets: binding.Spec.Targets},
	}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if step.Cleanup {
		if err := verifyCleanup(ctx, runner, cluster.Spec.Kubeconfig, input, application.Spec.Namespace, binding.Spec.Targets); err != nil {
			return unhealthy(err.Error(), "cleanup", "incomplete"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The secondary scheduler was removed and workload scheduling was restored", Checks: map[string]string{"scheduler": "absent", "targets": "restored"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateResult(value, input, cluster, binding); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/scheduler", "-n", input.Namespace, "--timeout=5m"); err != nil {
		return unhealthy("Secondary scheduler is not ready: "+err.Error(), "scheduler", "unhealthy"), nil
	}
	if err := log("info", fmt.Sprintf("Verifying rollout and scheduler assignment for %d workloads in parallel", len(binding.Spec.Targets))); err != nil {
		return domain.HealthReport{}, err
	}
	if err := verifyTargets(ctx, runner, cluster.Spec.Kubeconfig, application.Spec.Namespace, input, binding.Spec.Targets); err != nil {
		return unhealthy(err.Error(), "targets", "unhealthy"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The secondary scheduler is ready and all selected Pods were assigned by it", Checks: map[string]string{"scheduler": input.SchedulerName, "version": schedulerVersion, "image": schedulerImage, "targets": fmt.Sprintf("%d verified", len(binding.Spec.Targets))}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, cluster, application, binding, err := resolve(step)
	if err != nil {
		return err
	}
	runner := plugin.runner()
	if err := log("warning", "Restoring owned workload scheduler settings before removing the secondary scheduler"); err != nil {
		return err
	}
	if err := validateCleanupOwnership(ctx, runner, cluster.Spec.Kubeconfig, input, application.Spec.Namespace, binding.Spec.Targets); err != nil {
		return err
	}
	for _, target := range binding.Spec.Targets {
		workload, err := readWorkload(ctx, runner, cluster.Spec.Kubeconfig, application.Spec.Namespace, target)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return err
		}
		if workload.Spec.Template.Metadata.Annotations[ownershipKey] != input.Marker {
			continue
		}
		patch, err := restorePatch(workload.Spec.Template.Metadata.Annotations[previousKey])
		if err != nil {
			return err
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "patch", resourceName(target), "-n", application.Spec.Namespace, "--type=merge", "-p", string(patch)); err != nil {
			return fmt.Errorf("restore workload %s: %w", target.ID, err)
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", resourceName(target), "-n", application.Spec.Namespace, "--timeout=10m"); err != nil {
			return fmt.Errorf("restored workload %s did not become ready: %w", target.ID, err)
		}
	}
	manifest, err := schedulerManifest(input)
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "delete", "--ignore-not-found", "--wait=true", "--timeout=5m", "-f", "-"); err != nil {
		return fmt.Errorf("delete scheduler resources: %w", err)
	}
	return nil
}

func resolve(step domain.PlanStep) (stepInput, clusterConnection, applicationDeployment, targetBinding, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, clusterConnection{}, applicationDeployment{}, targetBinding{}, err
	}
	values := []struct {
		name, id, artifactType string
	}{{"cluster-connection", input.ClusterConnectionRef, "ClusterConnection"}, {"application-deployment", input.ApplicationDeploymentRef, "ApplicationDeployment"}, {"target-binding", input.TargetBindingRef, "TargetBinding"}}
	for _, expected := range values {
		artifact, ok := step.ResolvedInputs[expected.name]
		if !ok || artifact.ID != expected.id || artifact.Type != expected.artifactType || artifact.Version != "v1alpha1" || len(artifact.Value) == 0 {
			return stepInput{}, clusterConnection{}, applicationDeployment{}, targetBinding{}, fmt.Errorf("verified %s input is unavailable or incompatible", expected.artifactType)
		}
	}
	var cluster clusterConnection
	var application applicationDeployment
	var binding targetBinding
	if err := json.Unmarshal(step.ResolvedInputs["cluster-connection"].Value, &cluster); err != nil {
		return stepInput{}, clusterConnection{}, applicationDeployment{}, targetBinding{}, err
	}
	if err := json.Unmarshal(step.ResolvedInputs["application-deployment"].Value, &application); err != nil {
		return stepInput{}, clusterConnection{}, applicationDeployment{}, targetBinding{}, err
	}
	if err := json.Unmarshal(step.ResolvedInputs["target-binding"].Value, &binding); err != nil {
		return stepInput{}, clusterConnection{}, applicationDeployment{}, targetBinding{}, err
	}
	return input, cluster, application, binding, nil
}

func validateArtifacts(input stepInput, cluster clusterConnection, application applicationDeployment, binding targetBinding) error {
	if input.Marker == "" || input.Namespace == "" || input.SchedulerName == "" || input.Namespace != input.SchedulerName {
		return errors.New("generated scheduler identity is invalid")
	}
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Metadata.Name == "" || cluster.Metadata.Version == "" || cluster.Spec.Distribution == "" {
		return errors.New("cluster connection identity is invalid")
	}
	server, err := url.Parse(cluster.Spec.Server)
	if err != nil || server.Scheme != "https" || server.Host == "" || server.Path != "" {
		return errors.New("cluster server must be a valid HTTPS endpoint")
	}
	if err := validateKubeconfig(cluster.Spec.Kubeconfig, cluster.Spec.Server); err != nil {
		return err
	}
	if application.APIVersion != artifactAPI || application.Kind != "ApplicationDeployment" || application.Metadata.Name == "" || application.Metadata.Version != "v1alpha1" || application.Metadata.OwnershipMarker == "" || application.Spec.ApplicationRef == "" || application.Spec.ClusterServer != cluster.Spec.Server || application.Spec.Namespace != application.Metadata.Name || len(application.Spec.Workloads) == 0 {
		return errors.New("application deployment is invalid or belongs to another cluster")
	}
	if binding.APIVersion != artifactAPI || binding.Kind != "TargetBinding" || binding.Metadata.Name == "" || binding.Metadata.Version != "v1alpha1" || binding.Spec.SourceArtifactID == "" || binding.Spec.SourceDigest == "" || binding.Spec.RequiredTrait != "schedulable" || len(binding.Spec.Targets) == 0 {
		return errors.New("target binding must be a verified schedulable workload selection")
	}
	workloads := map[string]workloadTarget{}
	for _, workload := range application.Spec.Workloads {
		if workload.ID == "" || workloads[workload.ID].ID != "" {
			return errors.New("application workload interface contains an invalid or duplicate target")
		}
		workloads[workload.ID] = workload
	}
	seen := map[string]bool{}
	for _, target := range binding.Spec.Targets {
		declared, ok := workloads[target.ID]
		if !ok || seen[target.ID] || !sameTarget(declared, target) || !contains(target.Traits, "schedulable") || !supportedKind(target.Kind) {
			return fmt.Errorf("bound target %q does not match a supported schedulable application workload", target.ID)
		}
		seen[target.ID] = true
	}
	return nil
}

func validateKubeconfig(value, server string) error {
	var config struct {
		APIVersion     string `yaml:"apiVersion"`
		Kind           string `yaml:"kind"`
		CurrentContext string `yaml:"current-context"`
		Clusters       []struct {
			Name    string `yaml:"name"`
			Cluster struct {
				Server string `yaml:"server"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Contexts []struct {
			Name    string `yaml:"name"`
			Context struct {
				Cluster string `yaml:"cluster"`
				User    string `yaml:"user"`
			} `yaml:"context"`
		} `yaml:"contexts"`
		Users []struct {
			Name string `yaml:"name"`
			User struct {
				ClientCertificateData string `yaml:"client-certificate-data"`
				ClientKeyData         string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal([]byte(value), &config); err != nil || config.APIVersion != "v1" || config.Kind != "Config" || config.CurrentContext == "" {
		return errors.New("cluster kubeconfig is invalid")
	}
	var clusterName, userName string
	for _, item := range config.Contexts {
		if item.Name == config.CurrentContext {
			clusterName, userName = item.Context.Cluster, item.Context.User
		}
	}
	clusterOK := false
	for _, item := range config.Clusters {
		clusterOK = clusterOK || item.Name == clusterName && item.Cluster.Server == server
	}
	userOK := false
	for _, item := range config.Users {
		if item.Name == userName {
			certificate, certificateErr := base64.StdEncoding.DecodeString(item.User.ClientCertificateData)
			key, keyErr := base64.StdEncoding.DecodeString(item.User.ClientKeyData)
			userOK = certificateErr == nil && keyErr == nil && len(certificate) > 0 && len(key) > 0
		}
	}
	if !clusterOK || !userOK {
		return errors.New("cluster kubeconfig target or client identity is invalid")
	}
	return nil
}

func schedulerManifest(input stepInput) ([]byte, error) {
	labels := map[string]string{"app.kubernetes.io/name": "kubephos-secondary-scheduler", "app.kubernetes.io/instance": input.SchedulerName, "app.kubernetes.io/managed-by": "kubephos"}
	annotations := map[string]string{ownershipKey: input.Marker}
	objects := []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": input.Namespace, "labels": labels, "annotations": annotations}},
		{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": "scheduler", "namespace": input.Namespace, "labels": labels, "annotations": annotations}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": map[string]any{"name": input.SchedulerName + "-scheduler", "labels": labels, "annotations": annotations}, "subjects": []map[string]string{{"kind": "ServiceAccount", "name": "scheduler", "namespace": input.Namespace}}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "system:kube-scheduler"}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": map[string]any{"name": input.SchedulerName + "-volume", "labels": labels, "annotations": annotations}, "subjects": []map[string]string{{"kind": "ServiceAccount", "name": "scheduler", "namespace": input.Namespace}}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": "system:volume-scheduler"}},
		{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "RoleBinding", "metadata": map[string]any{"name": input.SchedulerName + "-auth-reader", "namespace": "kube-system", "labels": labels, "annotations": annotations}, "subjects": []map[string]string{{"kind": "ServiceAccount", "name": "scheduler", "namespace": input.Namespace}}, "roleRef": map[string]string{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": "extension-apiserver-authentication-reader"}},
		{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": "scheduler-config", "namespace": input.Namespace, "labels": labels, "annotations": annotations}, "data": map[string]string{"scheduler.yaml": "apiVersion: kubescheduler.config.k8s.io/v1\nkind: KubeSchedulerConfiguration\nprofiles:\n  - schedulerName: " + input.SchedulerName + "\nleaderElection:\n  leaderElect: false\n"}},
		{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]any{"name": "scheduler", "namespace": input.Namespace, "labels": labels, "annotations": annotations}, "spec": map[string]any{"replicas": 1, "selector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/instance": input.SchedulerName}}, "template": map[string]any{"metadata": map[string]any{"labels": labels, "annotations": annotations}, "spec": map[string]any{"serviceAccountName": "scheduler", "automountServiceAccountToken": true, "containers": []map[string]any{{"name": "scheduler", "image": schedulerImage, "imagePullPolicy": "IfNotPresent", "command": []string{"/usr/local/bin/kube-scheduler"}, "args": []string{"--config=/etc/kubephos/scheduler.yaml"}, "ports": []map[string]any{{"name": "healthz", "containerPort": 10259, "protocol": "TCP"}}, "readinessProbe": map[string]any{"httpGet": map[string]any{"path": "/healthz", "port": "healthz", "scheme": "HTTPS"}, "initialDelaySeconds": 5, "periodSeconds": 5}, "livenessProbe": map[string]any{"httpGet": map[string]any{"path": "/healthz", "port": "healthz", "scheme": "HTTPS"}, "initialDelaySeconds": 15, "periodSeconds": 10}, "resources": map[string]any{"requests": map[string]string{"cpu": "100m", "memory": "128Mi"}, "limits": map[string]string{"cpu": "500m", "memory": "512Mi"}}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true}, "volumeMounts": []map[string]any{{"name": "config", "mountPath": "/etc/kubephos", "readOnly": true}}}}, "volumes": []map[string]any{{"name": "config", "configMap": map[string]string{"name": "scheduler-config"}}}}}}},
	}
	parts := make([]string, 0, len(objects))
	for _, object := range objects {
		value, err := json.Marshal(object)
		if err != nil {
			return nil, err
		}
		parts = append(parts, string(value))
	}
	return []byte(strings.Join(parts, "\n---\n")), nil
}

func partitionSchedulerManifest(manifest []byte) ([]byte, []byte, error) {
	foundation := []string{}
	components := []string{}
	for _, document := range strings.Split(string(manifest), "\n---\n") {
		var identity struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(document), &identity); err != nil {
			return nil, nil, err
		}
		switch identity.Kind {
		case "Namespace", "ClusterRoleBinding", "RoleBinding":
			foundation = append(foundation, document)
		default:
			components = append(components, document)
		}
	}
	if len(foundation) != 4 || len(components) != 3 {
		return nil, nil, errors.New("scheduler manifest partition is incomplete")
	}
	return []byte(strings.Join(foundation, "\n---\n")), []byte(strings.Join(components, "\n---\n")), nil
}

func ensureSchedulerResourcesAbsent(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput) error {
	for _, args := range schedulerResources(input) {
		value, err := runner.Run(ctx, kubeconfig, nil, append(args, "--ignore-not-found", "-o", "name")...)
		if err != nil {
			return err
		}
		if strings.TrimSpace(value) != "" {
			return fmt.Errorf("Kubernetes resource %s already exists", strings.Join(args[1:], "/"))
		}
	}
	return nil
}

func schedulerResources(input stepInput) [][]string {
	return [][]string{{"get", "namespace", input.Namespace}, {"get", "clusterrolebinding", input.SchedulerName + "-scheduler"}, {"get", "clusterrolebinding", input.SchedulerName + "-volume"}, {"get", "rolebinding", input.SchedulerName + "-auth-reader", "-n", "kube-system"}}
}

func validateCleanupOwnership(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput, applicationNamespace string, targets []workloadTarget) error {
	for _, args := range schedulerResources(input) {
		value, err := runner.Run(ctx, kubeconfig, nil, append(args, "--ignore-not-found", "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/scheduler-ownership}")...)
		if err != nil {
			return err
		}
		if strings.TrimSpace(value) != "" && strings.TrimSpace(value) != input.Marker {
			return fmt.Errorf("Kubernetes resource %s is not owned by this operation", strings.Join(args[1:], "/"))
		}
	}
	for _, target := range targets {
		workload, err := readWorkload(ctx, runner, kubeconfig, applicationNamespace, target)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return err
		}
		owner := workload.Spec.Template.Metadata.Annotations[ownershipKey]
		if owner != "" && owner != input.Marker {
			return fmt.Errorf("workload %s is owned by another scheduler operation", target.ID)
		}
	}
	return nil
}

func readWorkload(ctx context.Context, runner commandRunner, kubeconfig, namespace string, target workloadTarget) (kubernetesWorkload, error) {
	value, err := runner.Run(ctx, kubeconfig, nil, "get", resourceName(target), "-n", namespace, "-o", "json")
	if err != nil {
		return kubernetesWorkload{}, fmt.Errorf("read workload %s: %w", target.ID, err)
	}
	if strings.TrimSpace(value) == "" {
		return kubernetesWorkload{}, fmt.Errorf("read workload %s: not found", target.ID)
	}
	var workload kubernetesWorkload
	if err := json.Unmarshal([]byte(value), &workload); err != nil {
		return kubernetesWorkload{}, fmt.Errorf("decode workload %s: %w", target.ID, err)
	}
	return workload, nil
}

func validateUnboundWorkload(workload kubernetesWorkload, target workloadTarget) error {
	if workload.APIVersion != target.APIVersion || workload.Kind != target.Kind || workload.Metadata.Name != target.Name {
		return fmt.Errorf("workload %s identity does not match its target declaration", target.ID)
	}
	for key, value := range target.Selector {
		if workload.Spec.Template.Metadata.Labels[key] != value {
			return fmt.Errorf("workload %s Pod template does not match its declared selector", target.ID)
		}
	}
	owner := workload.Spec.Template.Metadata.Annotations[ownershipKey]
	if owner != "" {
		return fmt.Errorf("workload %s is already managed by a scheduler operation", target.ID)
	}
	current := workload.Spec.Template.Spec.SchedulerName
	if current != "" && current != "default-scheduler" {
		return fmt.Errorf("workload %s already uses unmanaged scheduler %q", target.ID, current)
	}
	return nil
}

func bindPatch(input stepInput, previous string) ([]byte, error) {
	if previous == "" {
		previous = defaultSentinel
	}
	return json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]string{ownershipKey: input.Marker, previousKey: previous}}, "spec": map[string]string{"schedulerName": input.SchedulerName}}}})
}

func restorePatch(previous string) ([]byte, error) {
	if previous == "" {
		return nil, errors.New("owned workload does not record its previous scheduler")
	}
	var scheduler any = previous
	if previous == defaultSentinel {
		scheduler = nil
	}
	return json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"annotations": map[string]any{ownershipKey: nil, previousKey: nil}}, "spec": map[string]any{"schedulerName": scheduler}}}})
}

func verifyTargets(ctx context.Context, runner commandRunner, kubeconfig, namespace string, input stepInput, targets []workloadTarget) error {
	type targetResult struct {
		id  string
		err error
	}
	results := make(chan targetResult, len(targets))
	var group sync.WaitGroup
	for _, target := range targets {
		target := target
		group.Add(1)
		go func() {
			defer group.Done()
			workload, err := readWorkload(ctx, runner, kubeconfig, namespace, target)
			if err != nil {
				results <- targetResult{target.ID, err}
				return
			}
			if workload.Spec.Template.Metadata.Annotations[ownershipKey] != input.Marker || workload.Spec.Template.Spec.SchedulerName != input.SchedulerName {
				results <- targetResult{target.ID, errors.New("workload template does not contain the verified scheduler binding")}
				return
			}
			if _, err := runner.Run(ctx, kubeconfig, nil, "rollout", "status", resourceName(target), "-n", namespace, "--timeout=10m"); err != nil {
				results <- targetResult{target.ID, err}
				return
			}
			if err := verifyTargetPods(ctx, runner, kubeconfig, namespace, target, input.SchedulerName); err != nil {
				results <- targetResult{target.ID, err}
				return
			}
			results <- targetResult{id: target.ID}
		}()
	}
	group.Wait()
	close(results)
	issues := []string{}
	for result := range results {
		if result.err != nil {
			issues = append(issues, result.id+": "+result.err.Error())
		}
	}
	if len(issues) > 0 {
		sort.Strings(issues)
		return errors.New(strings.Join(issues, "; "))
	}
	return nil
}

func (plugin Plugin) waitForTargetStable(ctx context.Context, runner commandRunner, kubeconfig, namespace string, target workloadTarget, input stepInput) error {
	if _, err := runner.Run(ctx, kubeconfig, nil, "rollout", "status", resourceName(target), "-n", namespace, "--timeout=10m"); err != nil {
		return err
	}
	checks := plugin.StabilityChecks
	if checks <= 0 {
		checks = 2
	}
	interval := plugin.StabilityInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	for check := 0; check < checks; check++ {
		workload, err := readWorkload(ctx, runner, kubeconfig, namespace, target)
		if err != nil {
			return err
		}
		if workload.Spec.Template.Metadata.Annotations[ownershipKey] != input.Marker || workload.Spec.Template.Spec.SchedulerName != input.SchedulerName {
			return errors.New("workload template lost its scheduler binding")
		}
		if err := verifyTargetPods(ctx, runner, kubeconfig, namespace, target, input.SchedulerName); err != nil {
			return err
		}
		if check+1 == checks {
			continue
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}

func verifyTargetPods(ctx context.Context, runner commandRunner, kubeconfig, namespace string, target workloadTarget, scheduler string) error {
	value, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selectorValue(target.Selector), "-o", "json")
	if err != nil {
		return err
	}
	var pods podList
	if err := json.Unmarshal([]byte(value), &pods); err != nil {
		return fmt.Errorf("decode selected Pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return errors.New("selected workload has no Pods")
	}
	for _, pod := range pods.Items {
		if pod.Spec.SchedulerName != scheduler || pod.Spec.NodeName == "" || !podReady(pod.Status.Conditions) {
			return fmt.Errorf("a selected Pod is not ready on scheduler %s", scheduler)
		}
	}
	return nil
}

func effectiveScheduler(value string) string {
	if value == "" {
		return "default-scheduler"
	}
	return value
}

func logTargetDiagnostics(ctx context.Context, runner commandRunner, kubeconfig, namespace string, target workloadTarget, log plugins.Logger) error {
	checks := []struct {
		name string
		args []string
	}{
		{"Pod inventory", []string{"get", "pods", "-n", namespace, "-l", selectorValue(target.Selector), "-o", "wide"}},
		{"Pod description", []string{"describe", "pods", "-n", namespace, "-l", selectorValue(target.Selector)}},
		{"Pod logs", []string{"logs", "-n", namespace, "-l", selectorValue(target.Selector), "--all-containers=true", "--tail=100"}},
	}
	for _, check := range checks {
		value, err := runner.Run(ctx, kubeconfig, nil, check.args...)
		if err != nil {
			value = err.Error()
		}
		if err := log("warning", fmt.Sprintf("Target %s %s:\n%s", target.ID, check.name, strings.TrimSpace(value))); err != nil {
			return err
		}
	}
	return nil
}

func waitForScheduler(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput, log plugins.Logger) error {
	for attempt := 0; attempt < 24; attempt++ {
		value, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", input.Namespace, "-l", "app.kubernetes.io/instance="+input.SchedulerName, "-o", "json")
		if err == nil {
			var pods schedulerPodList
			if json.Unmarshal([]byte(value), &pods) == nil {
				for _, pod := range pods.Items {
					for _, status := range pod.Status.ContainerStatuses {
						if status.State.Waiting != nil && terminalWaitingReason(status.State.Waiting.Reason) {
							_ = logSchedulerDiagnostics(ctx, runner, kubeconfig, input, log)
							return fmt.Errorf("scheduler Pod %s cannot start: %s: %s", pod.Metadata.Name, status.State.Waiting.Reason, status.State.Waiting.Message)
						}
						if status.State.Terminated != nil && status.State.Terminated.Reason != "Completed" {
							_ = logSchedulerDiagnostics(ctx, runner, kubeconfig, input, log)
							return fmt.Errorf("scheduler Pod %s terminated: %s: %s", pod.Metadata.Name, status.State.Terminated.Reason, status.State.Terminated.Message)
						}
					}
					if podReady(pod.Status.Conditions) {
						return nil
					}
				}
			}
		}
		if attempt > 0 && attempt%4 == 0 {
			if snapshot, snapshotErr := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", input.Namespace, "-o", "wide"); snapshotErr == nil {
				if logErr := log("info", "Scheduler readiness snapshot:\n"+snapshot); logErr != nil {
					return logErr
				}
			}
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	_ = logSchedulerDiagnostics(ctx, runner, kubeconfig, input, log)
	return errors.New("scheduler did not become ready within 2 minutes")
}

func terminalWaitingReason(reason string) bool {
	switch reason {
	case "CrashLoopBackOff", "CreateContainerConfigError", "CreateContainerError", "ErrImagePull", "ImagePullBackOff", "InvalidImageName", "RunContainerError":
		return true
	default:
		return false
	}
}

func logSchedulerDiagnostics(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput, log plugins.Logger) error {
	checks := []struct {
		name string
		args []string
	}{
		{"Pod inventory", []string{"get", "pods", "-n", input.Namespace, "-o", "wide"}},
		{"Pod description", []string{"describe", "pods", "-n", input.Namespace, "-l", "app.kubernetes.io/instance=" + input.SchedulerName}},
		{"Scheduler logs", []string{"logs", "deployment/scheduler", "-n", input.Namespace, "--all-containers=true", "--tail=100"}},
		{"Namespace events", []string{"get", "events", "-n", input.Namespace, "--sort-by=.lastTimestamp"}},
	}
	for _, check := range checks {
		value, err := runner.Run(ctx, kubeconfig, nil, check.args...)
		if err != nil {
			value = err.Error()
		}
		if err := log("warning", check.name+":\n"+strings.TrimSpace(value)); err != nil {
			return err
		}
	}
	return nil
}

func verifyCleanup(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput, namespace string, targets []workloadTarget) error {
	for _, args := range schedulerResources(input) {
		value, err := runner.Run(ctx, kubeconfig, nil, append(args, "--ignore-not-found", "-o", "name")...)
		if err != nil {
			return err
		}
		if strings.TrimSpace(value) != "" {
			return fmt.Errorf("Kubernetes resource %s remains after cleanup", strings.Join(args[1:], "/"))
		}
	}
	for _, target := range targets {
		workload, err := readWorkload(ctx, runner, kubeconfig, namespace, target)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return err
		}
		if workload.Spec.Template.Metadata.Annotations[ownershipKey] != "" || workload.Spec.Template.Spec.SchedulerName == input.SchedulerName {
			return fmt.Errorf("workload %s retains the removed scheduler binding", target.ID)
		}
	}
	return nil
}

func validateResult(value result, input stepInput, cluster clusterConnection, binding targetBinding) error {
	deployment := value.SchedulerDeployment
	if deployment.APIVersion != artifactAPI || deployment.Kind != "SchedulerDeployment" || deployment.Metadata.Name != input.SchedulerName || deployment.Metadata.Version != schedulerVersion || deployment.Metadata.OwnershipMarker != input.Marker || deployment.Spec.ClusterServer != cluster.Spec.Server || deployment.Spec.Namespace != input.Namespace || deployment.Spec.SchedulerName != input.SchedulerName || deployment.Spec.Image != schedulerImage || deployment.Spec.ApplicationDeploymentRef != input.ApplicationDeploymentRef || deployment.Spec.TargetBindingRef != input.TargetBindingRef || len(deployment.Spec.Targets) != len(binding.Spec.Targets) {
		return errors.New("scheduler deployment artifact does not match the validated plan")
	}
	for index := range binding.Spec.Targets {
		if !sameTarget(deployment.Spec.Targets[index], binding.Spec.Targets[index]) {
			return errors.New("scheduler deployment targets do not match the validated binding")
		}
	}
	return nil
}

func namespaceOwnership(ctx context.Context, runner commandRunner, kubeconfig, namespace, marker, key string) (bool, error) {
	jsonPath := "jsonpath={.metadata.annotations." + strings.ReplaceAll(key, ".", "\\.") + "}"
	value, err := runner.Run(ctx, kubeconfig, nil, "get", "namespace", namespace, "--ignore-not-found", "-o", jsonPath)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(value) == marker, nil
}

func sameTarget(left, right workloadTarget) bool {
	if left.ID != right.ID || left.APIVersion != right.APIVersion || left.Kind != right.Kind || left.Name != right.Name || len(left.Selector) != len(right.Selector) || len(left.Traits) != len(right.Traits) {
		return false
	}
	for key, value := range left.Selector {
		if right.Selector[key] != value {
			return false
		}
	}
	leftTraits := append([]string(nil), left.Traits...)
	rightTraits := append([]string(nil), right.Traits...)
	sort.Strings(leftTraits)
	sort.Strings(rightTraits)
	return strings.Join(leftTraits, "\x00") == strings.Join(rightTraits, "\x00")
}

func supportedKind(kind string) bool {
	return kind == "Deployment" || kind == "StatefulSet" || kind == "DaemonSet"
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func resourceName(target workloadTarget) string {
	return strings.ToLower(target.Kind) + "/" + target.Name
}

func selectorValue(selector map[string]string) string {
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+selector[key])
	}
	return strings.Join(values, ",")
}

func podReady(conditions []struct {
	Type   string `json:"type"`
	Status string `json:"status"`
}) bool {
	for _, condition := range conditions {
		if condition.Type == "Ready" && condition.Status == "True" {
			return true
		}
	}
	return false
}

func isNotFound(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "not found") || strings.Contains(strings.ToLower(err.Error()), "notfound")
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return localRunner{}
}

type localRunner struct{}

func (localRunner) Run(ctx context.Context, kubeconfig string, stdin []byte, args ...string) (string, error) {
	directory, err := os.MkdirTemp("", "kubephos-cluster-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	path := directory + "/kubeconfig"
	if err := os.WriteFile(path, []byte(kubeconfig), 0600); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", path}, args...)...)
	if stdin != nil {
		command.Stdin = bytes.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
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
