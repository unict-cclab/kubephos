package applicationdeployer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	pluginID         = "io.kubephos.applications.kubernetes.deploy"
	artifactAPI      = "artifacts.kubephos.dev/v1alpha1"
	ownershipKey     = "kubephos.dev/ownership-marker"
	maxManifestBytes = 10 * 1024 * 1024
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
	ClusterConnectionRef string `json:"clusterConnectionRef"`
	ManifestSetRef       string `json:"manifestSetRef"`
	WorkloadTargetsRef   string `json:"workloadTargetsRef"`
	ServiceEndpointsRef  string `json:"serviceEndpointsRef"`
}

type stepInput struct {
	Spec
	Marker    string `json:"marker"`
	Namespace string `json:"namespace"`
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

type manifestSet struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		ApplicationRef string `json:"applicationRef"`
		Digest         string `json:"digest"`
	} `json:"metadata"`
	Spec struct {
		Renderer string `json:"renderer"`
		Content  string `json:"content"`
		Source   struct {
			Type       string `json:"type"`
			Repository string `json:"repository"`
			Revision   string `json:"revision"`
			Path       string `json:"path"`
			Entrypoint string `json:"entrypoint"`
		} `json:"source"`
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

type serviceEndpoint struct {
	ID        string `json:"id"`
	Component string `json:"component"`
	Service   string `json:"service"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Path      string `json:"path,omitempty"`
}

type applicationDeployment struct {
	APIVersion string                        `json:"apiVersion"`
	Kind       string                        `json:"kind"`
	Metadata   applicationDeploymentMetadata `json:"metadata"`
	Spec       applicationDeploymentSpec     `json:"spec"`
}

type applicationDeploymentMetadata struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	OwnershipMarker string `json:"ownershipMarker"`
}

type applicationDeploymentSpec struct {
	ApplicationRef string            `json:"applicationRef"`
	ClusterServer  string            `json:"clusterServer"`
	Namespace      string            `json:"namespace"`
	ManifestDigest string            `json:"manifestDigest"`
	Workloads      []workloadTarget  `json:"workloads"`
	Endpoints      []serviceEndpoint `json:"endpoints"`
}

type result struct {
	ApplicationDeployment applicationDeployment `json:"applicationDeployment"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Kubernetes application deployer", Version: "0.2.1",
		Description:     "Deploys any validated application package into an isolated Kubernetes namespace.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","manifestSetRef","workloadTargetsRef","serviceEndpointsRef"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"manifestSetRef":{"type":"string","title":"Application manifests","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ManifestSet","x-kubephos-artifact-version":"v1alpha1"},"workloadTargetsRef":{"type":"string","title":"Application workloads","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"WorkloadTargets","x-kubephos-artifact-version":"v1alpha1"},"serviceEndpointsRef":{"type":"string","title":"Application endpoints","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ServiceEndpoints","x-kubephos-artifact-version":"v1alpha1"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ManifestSet", Version: "v1alpha1"}, {Type: "WorkloadTargets", Version: "v1alpha1"}, {Type: "ServiceEndpoints", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ApplicationDeployment", Version: "v1alpha1"}},
		Capabilities:    []string{"applications.kubernetes.deploy", "applications.kubernetes.preflight", "applications.kubernetes.cleanup", "lifecycle.cleanup"},
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
	references := []struct {
		path  string
		value string
	}{{"clusterConnectionRef", spec.ClusterConnectionRef}, {"manifestSetRef", spec.ManifestSetRef}, {"workloadTargetsRef", spec.WorkloadTargetsRef}, {"serviceEndpointsRef", spec.ServiceEndpointsRef}}
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
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will validate isolation, server-side admission, workload readiness and service endpoints before completing the deployment."})
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
	marker := domain.RuntimeExecutionIDToken
	input, err := json.Marshal(stepInput{Spec: spec, Marker: marker, Namespace: "kubephos-app-" + marker})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "deploy-application", Name: "Deploy and verify isolated application", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "manifest-set", Type: "ManifestSet", Version: "v1alpha1", ArtifactID: spec.ManifestSetRef},
			{Name: "workload-targets", Type: "WorkloadTargets", Version: "v1alpha1", ArtifactID: spec.WorkloadTargetsRef},
			{Name: "service-endpoints", Type: "ServiceEndpoints", Version: "v1alpha1", ArtifactID: spec.ServiceEndpointsRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", MediaType: "application/json", Source: "/applicationDeployment"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, manifests, workloads, endpoints, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateArtifacts(cluster, manifests, workloads, endpoints); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	runner := plugin.runner()
	if err := log("info", "Validating Kubernetes API, isolation policy, admission and deployment ownership"); err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	authorized, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "auth", "can-i", "*", "*", "--all-namespaces")
	if err != nil || strings.TrimSpace(authorized) != "yes" {
		return unhealthy("The cluster connection does not have the required administrative permissions", "authorization", "denied"), nil
	}
	owned, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Namespace, input.Marker)
	if step.Cleanup {
		if err != nil {
			return unhealthy(err.Error(), "ownership", "blocked"), nil
		}
		state := "absent"
		if owned {
			state = "verified"
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The isolated application namespace is safe to remove", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "ownership": state}}, nil
	}
	if err != nil {
		return unhealthy(err.Error(), "namespace", "conflict"), nil
	}
	if owned {
		return unhealthy("The generated application namespace already exists", "namespace", "conflict"), nil
	}
	clusterScoped, err := clusterScopedKinds(ctx, runner, cluster.Spec.Kubeconfig)
	if err != nil {
		return unhealthy("Kubernetes API resource discovery failed: "+err.Error(), "discovery", "failed"), nil
	}
	if err := validateManifestIsolation([]byte(manifests.Spec.Content), clusterScoped); err != nil {
		return unhealthy(err.Error(), "isolation", "rejected"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, []byte(manifests.Spec.Content), "apply", "--dry-run=server", "--namespace", "default", "-f", "-"); err != nil {
		return unhealthy("Server-side manifest admission failed: "+err.Error(), "admission", "rejected"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The application passed every pre-deployment gate", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "namespace": "available", "isolation": "verified", "admission": "accepted", "workloads": fmt.Sprint(len(workloads)), "endpoints": fmt.Sprint(len(endpoints))}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, cluster, manifests, workloads, endpoints, err := resolve(step)
	if err != nil {
		return nil, err
	}
	runner := plugin.runner()
	namespaceManifest, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": input.Namespace, "annotations": map[string]string{ownershipKey: input.Marker}, "labels": map[string]string{"app.kubernetes.io/managed-by": "kubephos", "istio-injection": "enabled"}}})
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, namespaceManifest, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("create isolated namespace: %w", err)
	}
	if err := log("info", "Applying the immutable manifest set to the isolated namespace"); err != nil {
		return nil, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, []byte(manifests.Spec.Content), "apply", "--namespace", input.Namespace, "-f", "-"); err != nil {
		return nil, fmt.Errorf("apply application manifests: %w", err)
	}
	value := result{ApplicationDeployment: applicationDeployment{
		APIVersion: artifactAPI, Kind: "ApplicationDeployment",
		Metadata: applicationDeploymentMetadata{Name: input.Namespace, Version: "v1alpha1", OwnershipMarker: input.Marker},
		Spec:     applicationDeploymentSpec{ApplicationRef: manifests.Metadata.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: input.Namespace, ManifestDigest: manifests.Metadata.Digest, Workloads: workloads, Endpoints: endpoints},
	}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, manifests, workloads, endpoints, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if step.Cleanup {
		value, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "namespace", input.Namespace, "--ignore-not-found", "-o", "name")
		if err != nil {
			return domain.HealthReport{}, err
		}
		if strings.TrimSpace(value) != "" {
			return unhealthy("The isolated application namespace remains after cleanup", "namespace", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The isolated application deployment was removed", Checks: map[string]string{"namespace": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateResult(value, input, cluster, manifests, workloads, endpoints); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	owned, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Namespace, input.Marker)
	if err != nil || !owned {
		return unhealthy("The isolated namespace failed ownership verification", "ownership", "invalid"), nil
	}
	if err := log("info", fmt.Sprintf("Waiting for %d declared workloads in parallel", len(workloads))); err != nil {
		return domain.HealthReport{}, err
	}
	if err := plugin.verifyStableWorkloads(ctx, runner, cluster.Spec.Kubeconfig, input.Namespace, workloads, log); err != nil {
		return unhealthy(err.Error(), "workloads", "unhealthy"), nil
	}
	if err := verifyEndpoints(ctx, runner, cluster.Spec.Kubeconfig, input.Namespace, endpoints); err != nil {
		return unhealthy(err.Error(), "endpoints", "unhealthy"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The isolated application deployment is ready", Checks: map[string]string{"namespace": input.Namespace, "ownership": "verified", "workloads": fmt.Sprintf("%d ready", len(workloads)), "endpoints": fmt.Sprintf("%d ready", len(endpoints)), "manifest": "verified"}}, nil
}

func (plugin Plugin) verifyStableWorkloads(ctx context.Context, runner commandRunner, kubeconfig, namespace string, workloads []workloadTarget, log plugins.Logger) error {
	checks := plugin.StabilityChecks
	if checks <= 0 {
		checks = 3
	}
	interval := plugin.StabilityInterval
	if interval <= 0 {
		interval = 15 * time.Second
	}
	for check := 1; check <= checks; check++ {
		if err := verifyWorkloads(ctx, runner, kubeconfig, namespace, workloads, log); err != nil {
			return err
		}
		if check == checks {
			break
		}
		if err := log("info", fmt.Sprintf("Workload stability sample %d/%d passed", check, checks)); err != nil {
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return log("info", fmt.Sprintf("Workload stability window passed with %d samples", checks))
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, cluster, _, _, _, err := resolve(step)
	if err != nil {
		return err
	}
	runner := plugin.runner()
	owned, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Namespace, input.Marker)
	if err != nil {
		return err
	}
	if !owned {
		return nil
	}
	if err := log("warning", "Removing only the owned isolated application namespace"); err != nil {
		return err
	}
	_, err = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "delete", "namespace", input.Namespace, "--ignore-not-found", "--wait=true", "--timeout=10m")
	return err
}

func resolve(step domain.PlanStep) (stepInput, clusterConnection, manifestSet, []workloadTarget, []serviceEndpoint, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, clusterConnection{}, manifestSet{}, nil, nil, err
	}
	values := step.ResolvedInputs
	clusterRaw, clusterOK := values["cluster-connection"]
	manifestRaw, manifestOK := values["manifest-set"]
	workloadsRaw, workloadsOK := values["workload-targets"]
	endpointsRaw, endpointsOK := values["service-endpoints"]
	if !clusterOK || !manifestOK || !workloadsOK || !endpointsOK {
		return stepInput{}, clusterConnection{}, manifestSet{}, nil, nil, errors.New("one or more verified application deployment inputs are unavailable")
	}
	var cluster clusterConnection
	var manifests manifestSet
	var workloads []workloadTarget
	var endpoints []serviceEndpoint
	if err := json.Unmarshal(clusterRaw.Value, &cluster); err != nil {
		return stepInput{}, clusterConnection{}, manifestSet{}, nil, nil, err
	}
	if err := json.Unmarshal(manifestRaw.Value, &manifests); err != nil {
		return stepInput{}, clusterConnection{}, manifestSet{}, nil, nil, err
	}
	if err := json.Unmarshal(workloadsRaw.Value, &workloads); err != nil {
		return stepInput{}, clusterConnection{}, manifestSet{}, nil, nil, err
	}
	if err := json.Unmarshal(endpointsRaw.Value, &endpoints); err != nil {
		return stepInput{}, clusterConnection{}, manifestSet{}, nil, nil, err
	}
	return input, cluster, manifests, workloads, endpoints, nil
}

func validateArtifacts(cluster clusterConnection, manifests manifestSet, workloads []workloadTarget, endpoints []serviceEndpoint) error {
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
	if manifests.APIVersion != artifactAPI || manifests.Kind != "ManifestSet" || manifests.Metadata.ApplicationRef == "" || manifests.Spec.Renderer != "plain-yaml" || manifests.Spec.Content == "" || len(manifests.Spec.Content) > maxManifestBytes {
		return errors.New("manifest set identity or renderer is invalid")
	}
	digest := sha256.Sum256([]byte(manifests.Spec.Content))
	if manifests.Metadata.Digest != "sha256:"+hex.EncodeToString(digest[:]) {
		return errors.New("manifest set digest is invalid")
	}
	if len(workloads) == 0 {
		return errors.New("at least one workload target is required")
	}
	componentIDs := map[string]bool{}
	for _, workload := range workloads {
		if workload.ID == "" || workload.APIVersion == "" || workload.Kind == "" || workload.Name == "" || len(workload.Selector) == 0 || componentIDs[workload.ID] {
			return errors.New("workload target interface is invalid")
		}
		componentIDs[workload.ID] = true
		for key, value := range workload.Selector {
			if key == "" || value == "" || strings.ContainsAny(key+value, "\n\r,=") {
				return errors.New("workload selector is invalid")
			}
		}
	}
	endpointIDs := map[string]bool{}
	for _, endpoint := range endpoints {
		if endpoint.ID == "" || endpointIDs[endpoint.ID] || !componentIDs[endpoint.Component] || endpoint.Service == "" || endpoint.Port < 1 || endpoint.Port > 65535 || (endpoint.Protocol != "http" && endpoint.Protocol != "https" && endpoint.Protocol != "grpc" && endpoint.Protocol != "tcp") {
			return errors.New("service endpoint interface is invalid")
		}
		endpointIDs[endpoint.ID] = true
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

func clusterScopedKinds(ctx context.Context, runner commandRunner, kubeconfig string) (map[string]bool, error) {
	value, err := runner.Run(ctx, kubeconfig, nil, "api-resources", "--namespaced=false", "-o", "wide")
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, line := range strings.Split(value, "\n") {
		fields := strings.Fields(line)
		for index, field := range fields {
			if field == "false" && index+1 < len(fields) {
				result[fields[index+1]] = true
				break
			}
		}
	}
	if len(result) == 0 {
		return nil, errors.New("cluster-scoped API resource inventory is empty")
	}
	return result, nil
}

func validateManifestIsolation(value []byte, clusterScoped map[string]bool) error {
	decoder := yaml.NewDecoder(bytes.NewReader(value))
	resources := 0
	for {
		var resource struct {
			APIVersion string `yaml:"apiVersion"`
			Kind       string `yaml:"kind"`
			Metadata   struct {
				Name      string `yaml:"name"`
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("decode application manifest: %w", err)
		}
		if resource.APIVersion == "" && resource.Kind == "" && resource.Metadata.Name == "" {
			continue
		}
		resources++
		if resource.APIVersion == "" || resource.Kind == "" || resource.Metadata.Name == "" {
			return errors.New("every application manifest resource must have apiVersion, kind and metadata.name")
		}
		if resource.Metadata.Namespace != "" {
			return fmt.Errorf("resource %s/%s declares a namespace and cannot be isolated automatically", resource.Kind, resource.Metadata.Name)
		}
		if resource.Kind == "List" || strings.HasSuffix(resource.Kind, "List") {
			return fmt.Errorf("list resource %s/%s is not allowed in an isolated application package", resource.Kind, resource.Metadata.Name)
		}
		if clusterScoped[resource.Kind] {
			return fmt.Errorf("cluster-scoped resource %s/%s is not allowed in an isolated application package", resource.Kind, resource.Metadata.Name)
		}
	}
	if resources == 0 {
		return errors.New("application manifest does not contain resources")
	}
	return nil
}

func namespaceOwnership(ctx context.Context, runner commandRunner, kubeconfig, namespace, marker string) (bool, error) {
	value, err := runner.Run(ctx, kubeconfig, nil, "get", "namespace", namespace, "--ignore-not-found", "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(value) == "" {
		return false, nil
	}
	if strings.TrimSpace(value) != marker {
		return false, errors.New("application namespace is not owned by this operation")
	}
	return true, nil
}

func verifyWorkloads(ctx context.Context, runner commandRunner, kubeconfig, namespace string, workloads []workloadTarget, log plugins.Logger) error {
	type workloadResult struct {
		id  string
		err error
	}
	results := make(chan workloadResult, len(workloads))
	var group sync.WaitGroup
	for _, workload := range workloads {
		workload := workload
		group.Add(1)
		go func() {
			defer group.Done()
			resource := strings.ToLower(workload.Kind) + "/" + workload.Name
			if _, err := runner.Run(ctx, kubeconfig, nil, "get", resource, "-n", namespace, "-o", "name"); err != nil {
				results <- workloadResult{workload.ID, fmt.Errorf("declared workload %s is unavailable: %w", workload.ID, err)}
				return
			}
			selector := selectorValue(workload.Selector)
			pods, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-l", selector, "-o", "name")
			if err != nil || strings.TrimSpace(pods) == "" {
				results <- workloadResult{workload.ID, fmt.Errorf("declared workload %s has no selected pods", workload.ID)}
				return
			}
			if _, err := runner.Run(ctx, kubeconfig, nil, "wait", "--for=condition=Ready", "pod", "-n", namespace, "-l", selector, "--timeout=10m"); err != nil {
				results <- workloadResult{workload.ID, fmt.Errorf("declared workload %s did not become ready: %w", workload.ID, err)}
				return
			}
			results <- workloadResult{id: workload.ID}
		}()
	}
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			close(results)
			goto completed
		case <-ticker.C:
			pods, err := runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-o", "wide")
			if err != nil {
				if logErr := log("warning", "Workload readiness diagnostics failed: "+err.Error()); logErr != nil {
					return logErr
				}
				continue
			}
			if logErr := log("info", "Workload readiness snapshot:\n"+strings.TrimSpace(pods)); logErr != nil {
				return logErr
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}

completed:
	issues := []string{}
	for result := range results {
		if result.err != nil {
			issues = append(issues, result.err.Error())
		}
	}
	if len(issues) > 0 {
		sort.Strings(issues)
		return errors.New(strings.Join(issues, "; "))
	}
	return nil
}

func verifyEndpoints(ctx context.Context, runner commandRunner, kubeconfig, namespace string, endpoints []serviceEndpoint) error {
	for _, endpoint := range endpoints {
		addresses, err := runner.Run(ctx, kubeconfig, nil, "get", "endpoints", endpoint.Service, "-n", namespace, "-o", "jsonpath={.subsets[*].addresses[*].ip}")
		if err != nil || strings.TrimSpace(addresses) == "" {
			return fmt.Errorf("service endpoint %s has no ready backend", endpoint.ID)
		}
		if endpoint.Protocol == "http" || endpoint.Protocol == "https" {
			path := endpoint.Path
			if path == "" {
				path = "/"
			}
			proxy := fmt.Sprintf("/api/v1/namespaces/%s/services/%s:%s:%d/proxy%s", namespace, endpoint.Protocol, endpoint.Service, endpoint.Port, path)
			if _, err := runner.Run(ctx, kubeconfig, nil, "get", "--raw="+proxy); err != nil {
				return fmt.Errorf("service endpoint %s health request failed: %w", endpoint.ID, err)
			}
		}
	}
	return nil
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

func validateResult(value result, input stepInput, cluster clusterConnection, manifests manifestSet, workloads []workloadTarget, endpoints []serviceEndpoint) error {
	deployment := value.ApplicationDeployment
	if deployment.APIVersion != artifactAPI || deployment.Kind != "ApplicationDeployment" || deployment.Metadata.Name != input.Namespace || deployment.Metadata.Version != "v1alpha1" || deployment.Metadata.OwnershipMarker != input.Marker || deployment.Spec.ApplicationRef != manifests.Metadata.ApplicationRef || deployment.Spec.ClusterServer != cluster.Spec.Server || deployment.Spec.Namespace != input.Namespace || deployment.Spec.ManifestDigest != manifests.Metadata.Digest || len(deployment.Spec.Workloads) != len(workloads) || len(deployment.Spec.Endpoints) != len(endpoints) {
		return errors.New("application deployment artifact does not match the validated plan")
	}
	return nil
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
