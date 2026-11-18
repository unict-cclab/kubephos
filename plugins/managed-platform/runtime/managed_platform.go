package managedplatform

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/managedcomponents"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID               = "io.kubephos.platform.managed"
	artifactAPI            = "artifacts.kubephos.dev/v1alpha1"
	systemNamespace        = "kubephos-system"
	observabilityNamespace = "kubephos-observability"
	ownershipKey           = "kubephos.dev/ownership-marker"
)

type Plugin struct {
	Runner  clusterRunner
	Fetcher chartFetcher
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef string `json:"clusterConnectionRef"`
	// Components selects the optional platform components; nil installs the full set.
	Components []string `json:"components,omitempty"`
	// Reconcile changes the components of an installed platform owned by OwnershipMarker.
	Reconcile       bool   `json:"reconcile,omitempty"`
	OwnershipMarker string `json:"ownershipMarker,omitempty"`
}

type stepInput struct {
	Spec
	Marker string `json:"marker"`
}

type clusterConnection struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Networking      string `json:"networking,omitempty"`
		Distribution    string `json:"distribution"`
		Server          string `json:"server"`
		Kubeconfig      string `json:"kubeconfig"`
		RegistryHost    string `json:"registryHost,omitempty"`
		RegistryProject string `json:"registryProject,omitempty"`
	} `json:"spec"`
}

type managedPlatformCapability struct {
	APIVersion string                            `json:"apiVersion"`
	Kind       string                            `json:"kind"`
	Metadata   managedPlatformCapabilityMetadata `json:"metadata"`
	Spec       managedPlatformCapabilitySpec     `json:"spec"`
}

type managedPlatformCapabilityMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type managedPlatformCapabilitySpec struct {
	ClusterServer string             `json:"clusterServer"`
	Components    []managedComponent `json:"components"`
	Endpoints     []managedEndpoint  `json:"endpoints"`
}

type managedComponent struct {
	Name      string `json:"name"`
	Version   string `json:"version"`
	Namespace string `json:"namespace"`
	Status    string `json:"status"`
}

type managedEndpoint struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Scheme    string `json:"scheme"`
	NodePort  int    `json:"nodePort"`
}

type prometheusService struct {
	Name      string
	Namespace string
	Port      int
}

type serviceList struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Ports []struct {
				Name string `json:"name"`
				Port int    `json:"port"`
			} `json:"ports"`
		} `json:"spec"`
	} `json:"items"`
}

type nodeList struct {
	Items []struct {
		Metadata struct {
			Name        string            `json:"name"`
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	} `json:"items"`
}

type result struct {
	ManagedPlatformCapability managedPlatformCapability `json:"managedPlatformCapability"`
	HeadlampCredential        *serviceCredential        `json:"headlampCredential,omitempty"`
}

type serviceCredential struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Metadata   serviceCredentialMetadata `json:"metadata"`
	Spec       serviceCredentialSpec     `json:"spec"`
}

type serviceCredentialMetadata struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type serviceCredentialSpec struct {
	Namespace string `json:"namespace"`
	Service   string `json:"service"`
	Username  string `json:"username"`
	Password  string `json:"password"`
}

type chartSpec struct {
	ID     string
	URL    string
	Digest string
}

type clusterRunner interface {
	Kubectl(context.Context, string, []byte, ...string) (string, error)
	Helm(context.Context, string, []byte, []byte, ...string) (string, error)
}

type chartFetcher interface {
	Fetch(context.Context, chartSpec) ([]byte, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Managed Kubernetes platform", Version: "0.3.0",
		Description:     "Installs and verifies the selected managed service mesh, chaos, autoscaling, logging, tracing and console components on top of the cluster support baseline.",
		Schema:          json.RawMessage(platformSchema),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ManagedPlatformCapability", Version: "v1alpha1"}, {Type: "ServiceCredential", Version: "v1alpha1"}},
		Capabilities:    []string{"platform.managed.install", "platform.managed.preflight", "platform.managed.cleanup", "lifecycle.cleanup", "lifecycle.cleanup.compatible"},
		Permissions:     []string{"cluster.admin", "network.http"},
	}
}

const platformSchema = `{"type":"object","additionalProperties":false,"required":["clusterConnectionRef"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"components":{"type":"array","title":"Cluster components","maxItems":32,"items":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,63}$"}},"reconcile":{"type":"boolean"},"ownershipMarker":{"type":"string","pattern":"^[0-9a-f]{32}$","x-kubephos-hidden":true}}}`

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalid(report, "$", err.Error())
	}
	var spec Spec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return invalid(report, "$", "Configuration must be valid JSON.")
	}
	if !strings.HasPrefix(spec.ClusterConnectionRef, "art_") {
		return invalid(report, "clusterConnectionRef", "Select a verified Kubernetes connection.")
	}
	if spec.Components != nil {
		if _, err := managedcomponents.ResolveClusterComponents(spec.Components, ""); err != nil {
			return invalid(report, "components", err.Error())
		}
	}
	if spec.Reconcile != (spec.OwnershipMarker != "") || spec.OwnershipMarker != "" && !ownershipMarkerPattern.MatchString(spec.OwnershipMarker) {
		return invalid(report, "ownershipMarker", "Reconciling the platform needs the ownership marker of the installed components.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will install Mentat, Cluster Lens and " + newSelection(spec, "").summary() + "."})
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
	marker := spec.OwnershipMarker
	if !spec.Reconcile || !ownershipMarkerPattern.MatchString(marker) {
		var err error
		if marker, err = randomHex(16); err != nil {
			return domain.Plan{}, err
		}
	}
	input, err := json.Marshal(stepInput{Spec: spec, Marker: marker})
	if err != nil {
		return domain.Plan{}, err
	}
	outputs := []domain.ArtifactOutput{{Name: "managed-platform-capability", Type: "ManagedPlatformCapability", Version: "v1alpha1", MediaType: "application/json", Source: "/managedPlatformCapability"}}
	if headlampComponent.enabled(newSelection(spec, "")) {
		outputs = append(outputs, domain.ArtifactOutput{Name: "headlamp-credential", Type: "ServiceCredential", Version: "v1alpha1", MediaType: "application/json", Source: "/headlampCredential", Sensitive: true})
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "install-managed-platform", Name: "Install and verify managed platform", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}},
		Outputs:        outputs,
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateCluster(cluster); err != nil {
		return unhealthy(err.Error(), "cluster", "invalid"), nil
	}
	if !step.Cleanup && !validManagedRegistryHost(cluster.Spec.RegistryHost) {
		return unhealthy("Managed Harbor address is missing from the verified cluster connection", "registry", "invalid"), nil
	}
	runner := plugin.runner()
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	authorized, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "auth", "can-i", "*", "*", "--all-namespaces")
	if err != nil || strings.TrimSpace(authorized) != "yes" {
		return unhealthy("The cluster connection does not have administrative permissions", "authorization", "denied"), nil
	}
	if step.Cleanup || input.Reconcile {
		istio := false
		for _, namespace := range []string{istioNamespace, chaosNamespace, systemNamespace} {
			owned, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
			if err != nil || present && !owned {
				return unhealthy("Managed namespace ownership cannot be verified", namespace, "invalid"), nil
			}
			istio = istio || namespace == istioNamespace && present
		}
		if err := verifyOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Marker, istio); err != nil {
			return unhealthy(err.Error(), "supportComponents", "invalid"), nil
		}
		summary := "Managed platform resources are safe to remove"
		if !step.Cleanup {
			summary = "Installed managed platform components are owned by this cluster and can be reconciled"
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: summary, Checks: map[string]string{"api": "ready", "ownership": "verified"}}, nil
	}
	selected := newSelection(input.Spec, cluster.Spec.Networking)
	for _, namespace := range selected.namespaces() {
		_, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
		if err != nil {
			return unhealthy(err.Error(), namespace, "unknown"), nil
		}
		if present {
			return unhealthy("Namespace "+namespace+" already exists", namespace, "conflict"), nil
		}
	}
	for _, resource := range selected.resources() {
		args := []string{"get", resource.name}
		if resource.namespace != "" {
			args = append(args, "-n", resource.namespace)
		}
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, args...); err == nil {
			return unhealthy("Managed resource "+resource.name+" already exists", resource.name, "conflict"), nil
		}
	}
	// Releases in the shared observability namespace are not covered by a namespace check.
	for _, release := range selected.releases() {
		if release.namespace != observabilityNamespace {
			continue
		}
		if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, nil, nil, "status", release.name, "--namespace", release.namespace); err == nil {
			return unhealthy("Managed release "+release.name+" already exists", release.name, "conflict"), nil
		}
	}
	if err := log("info", "Kubernetes API, permissions and managed namespace ownership are valid"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Cluster is ready for the managed platform baseline", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "namespaces": "available"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, cluster, err := resolve(step)
	if err != nil {
		return nil, err
	}
	run := plugin.componentRun(input, cluster, log)
	if err := log("info", "Downloading and verifying managed platform charts in parallel"); err != nil {
		return nil, err
	}
	if run.charts, err = plugin.fetchChartSet(ctx, run.selected.charts()); err != nil {
		return nil, err
	}
	for _, namespace := range run.selected.namespaces() {
		manifest, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace, "annotations": map[string]string{ownershipKey: input.Marker}}})
		if _, err := run.runner.Kubectl(ctx, run.kubeconfig, manifest, "apply", "-f", "-"); err != nil {
			return nil, fmt.Errorf("create namespace %s: %w", namespace, err)
		}
	}
	for _, component := range run.selected.components() {
		if err := component.install(ctx, run); err != nil {
			return nil, err
		}
	}
	if input.Reconcile {
		if err := log("info", "Removing managed platform components that are no longer selected"); err != nil {
			return nil, err
		}
		if err := removeComponents(ctx, run, func(component platformComponent) bool { return !component.enabled(run.selected) }); err != nil {
			return nil, err
		}
	}
	run.result.ManagedPlatformCapability = run.selected.capability(cluster.Spec.Server)
	return json.Marshal(run.result)
}

// removeComponents removes the matching components in reverse installation order.
func removeComponents(ctx context.Context, run *componentRun, matches func(platformComponent) bool) error {
	for index := len(platformComponents) - 1; index >= 0; index-- {
		if component := platformComponents[index]; matches(component) {
			if err := component.remove(ctx, run); err != nil {
				return err
			}
		}
	}
	return nil
}

func (plugin Plugin) componentRun(input stepInput, cluster clusterConnection, log plugins.Logger) *componentRun {
	return &componentRun{runner: plugin.runner(), cluster: cluster, kubeconfig: cluster.Spec.Kubeconfig, marker: input.Marker, selected: newSelection(input.Spec, cluster.Spec.Networking), reconcile: input.Reconcile, log: log, result: &result{}, checks: map[string]string{}}
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	kubeconfig := cluster.Spec.Kubeconfig
	if step.Cleanup {
		for _, namespace := range []string{istioNamespace, chaosNamespace, systemNamespace} {
			_, present, err := namespaceOwnership(ctx, runner, kubeconfig, namespace, input.Marker)
			if err != nil || present {
				return unhealthy("Managed namespace remains after cleanup", namespace, "present"), nil
			}
		}
		for _, resource := range []string{"clusterrole/kubephos-mentat", "clusterrolebinding/kubephos-mentat", "clusterrole/kubephos-cluster-lens", "clusterrolebinding/kubephos-cluster-lens"} {
			if _, err := runner.Kubectl(ctx, kubeconfig, nil, "get", resource); err == nil {
				return unhealthy("Managed support resource remains after cleanup", resource, "present"), nil
			}
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed platform resources were removed", Checks: map[string]string{"istio": "absent", "chaos": "absent"}}, nil
	}
	run := plugin.componentRun(input, cluster, log)
	if err := json.Unmarshal(raw, run.result); err != nil {
		return domain.HealthReport{}, err
	}
	capability := run.result.ManagedPlatformCapability
	expected := run.selected.capability(cluster.Spec.Server)
	if capability.APIVersion != artifactAPI || capability.Kind != "ManagedPlatformCapability" || capability.Spec.ClusterServer != cluster.Spec.Server || len(capability.Spec.Components) != len(expected.Spec.Components) || len(capability.Spec.Endpoints) != len(expected.Spec.Endpoints) {
		return unhealthy("Managed platform capability is invalid", "artifact", "invalid"), nil
	}
	for _, release := range run.selected.releases() {
		if _, err := runner.Helm(ctx, kubeconfig, nil, nil, "status", release.name, "--namespace", release.namespace); err != nil {
			return unhealthy("Managed release is unhealthy: "+err.Error(), release.name, "unhealthy"), nil
		}
	}
	if err := verifyOwnership(ctx, runner, kubeconfig, input.Marker, run.selected.istio()); err != nil {
		return unhealthy(err.Error(), "supportOwnership", "invalid"), nil
	}
	prometheus, err := run.managedPrometheus(ctx)
	if err != nil {
		return unhealthy("Managed Prometheus discovery failed: "+err.Error(), "prometheusDiscovery", "unhealthy"), nil
	}
	if _, err := runner.Kubectl(ctx, kubeconfig, nil, "get", fmt.Sprintf("--raw=/api/v1/namespaces/%s/services/http:%s:%d/proxy/-/ready", prometheus.Namespace, prometheus.Name, prometheus.Port)); err != nil {
		return unhealthy("Managed Prometheus endpoint is not ready: "+err.Error(), "prometheusEndpoint", "unhealthy"), nil
	}
	for _, component := range run.selected.components() {
		report, err := component.verify(ctx, run)
		if err != nil {
			return domain.HealthReport{}, err
		}
		if report != nil {
			return *report, nil
		}
	}
	for _, endpoint := range capability.Spec.Endpoints {
		ports, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "service", endpoint.Service, "-n", endpoint.Namespace, "-o", "jsonpath={.spec.ports[*].nodePort}")
		if err != nil || !slices.Contains(strings.Fields(ports), fmt.Sprint(endpoint.NodePort)) {
			return unhealthy(endpoint.Name+" NodePort is unavailable", endpoint.Name, "unhealthy"), nil
		}
	}
	if err := log("info", "Selected managed platform components and their endpoints are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed platform baseline is ready", Checks: run.checks}, nil
}

// Cleanup removes every component the platform may own, whatever was selected.
func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, cluster, err := resolve(step)
	if err != nil {
		return err
	}
	runner := plugin.runner()
	for _, namespace := range []string{istioNamespace, chaosNamespace, systemNamespace} {
		owned, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
		if err != nil {
			return err
		}
		if present && !owned {
			return fmt.Errorf("refusing to remove namespace %s without verified ownership", namespace)
		}
	}
	if err := log("warning", "Removing managed platform releases"); err != nil {
		return err
	}
	run := plugin.componentRun(input, cluster, log)
	run.selected = selection{ids: map[string]bool{}, networking: cluster.Spec.Networking}
	if err := removeComponents(ctx, run, func(platformComponent) bool { return true }); err != nil {
		return err
	}
	return run.deleteNamespace(ctx, systemNamespace)
}

func validManagedRegistryHost(host string) bool {
	parsed, err := url.Parse("https://" + host)
	return err == nil && parsed.Hostname() != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.User == nil
}

func (service prometheusService) URL() string {
	return fmt.Sprintf("http://%s:%d", service.Hostname(), service.Port)
}

func (service prometheusService) Hostname() string {
	return fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, service.Namespace)
}

func discoverPrometheusService(ctx context.Context, runner clusterRunner, kubeconfig string) (prometheusService, error) {
	raw, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "services", "-n", observabilityNamespace, "-o", "json")
	if err != nil {
		return prometheusService{}, err
	}
	var services serviceList
	if err := json.Unmarshal([]byte(raw), &services); err != nil {
		return prometheusService{}, err
	}
	for _, service := range services.Items {
		if service.Metadata.Labels["app.kubernetes.io/name"] != "prometheus" && !strings.HasSuffix(service.Metadata.Name, "-prometheus") {
			continue
		}
		for _, port := range service.Spec.Ports {
			if port.Port == 9090 || port.Name == "http-web" {
				return prometheusService{Name: service.Metadata.Name, Namespace: observabilityNamespace, Port: port.Port}, nil
			}
		}
	}
	return prometheusService{}, errors.New("managed Prometheus service is unavailable")
}

// verifyOwnership rejects platform resources that another installation owns. The mesh
// telemetry exists only on clusters running Istio, whose API serves its kind.
func verifyOwnership(ctx context.Context, runner clusterRunner, kubeconfig, marker string, istio bool) error {
	resources := []resourceRef{}
	for _, component := range platformComponents {
		resources = append(resources, component.resources...)
	}
	if istio {
		resources = append(resources, resourceRef{"telemetry/kubephos-tracing", istioNamespace})
	}
	for _, resource := range resources {
		args := []string{"get", resource.name, "--ignore-not-found", "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}"}
		if resource.namespace != "" {
			args = append(args, "-n", resource.namespace)
		}
		value, err := runner.Kubectl(ctx, kubeconfig, nil, args...)
		if err != nil {
			return err
		}
		if owner := strings.TrimSpace(value); owner != "" && owner != marker {
			return fmt.Errorf("managed resource %s is not owned by this operation", resource.name)
		}
	}
	return nil
}

func resolve(step domain.PlanStep) (stepInput, clusterConnection, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, clusterConnection{}, err
	}
	value, exists := step.ResolvedInputs["cluster-connection"]
	if !exists {
		return stepInput{}, clusterConnection{}, errors.New("verified ClusterConnection input is unavailable")
	}
	var cluster clusterConnection
	if err := json.Unmarshal(value.Value, &cluster); err != nil {
		return stepInput{}, clusterConnection{}, err
	}
	return input, cluster, nil
}

func validateCluster(cluster clusterConnection) error {
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Metadata.Name == "" || cluster.Metadata.Version == "" || cluster.Spec.Distribution != "k3s" {
		return errors.New("cluster connection identity is invalid")
	}
	server, err := url.Parse(cluster.Spec.Server)
	if err != nil || server.Scheme != "https" || server.Host == "" || server.Path != "" {
		return errors.New("cluster server must be a valid HTTPS endpoint")
	}
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
				Certificate string `yaml:"client-certificate-data"`
				Key         string `yaml:"client-key-data"`
			} `yaml:"user"`
		} `yaml:"users"`
	}
	if err := yaml.Unmarshal([]byte(cluster.Spec.Kubeconfig), &config); err != nil || config.APIVersion != "v1" || config.Kind != "Config" || config.CurrentContext == "" {
		return errors.New("cluster kubeconfig is invalid")
	}
	var clusterName, userName string
	for _, context := range config.Contexts {
		if context.Name == config.CurrentContext {
			clusterName, userName = context.Context.Cluster, context.Context.User
		}
	}
	clusterOK := false
	for _, item := range config.Clusters {
		clusterOK = clusterOK || item.Name == clusterName && item.Cluster.Server == cluster.Spec.Server
	}
	userOK := false
	for _, item := range config.Users {
		if item.Name == userName {
			certificate, certificateErr := base64.StdEncoding.DecodeString(item.User.Certificate)
			key, keyErr := base64.StdEncoding.DecodeString(item.User.Key)
			userOK = certificateErr == nil && keyErr == nil && len(certificate) > 0 && len(key) > 0
		}
	}
	if !clusterOK || !userOK {
		return errors.New("cluster kubeconfig target or identity is invalid")
	}
	return nil
}

func namespaceOwnership(ctx context.Context, runner clusterRunner, kubeconfig, namespace, marker string) (bool, bool, error) {
	value, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "namespace", namespace, "--ignore-not-found", "-o", "json")
	if err != nil {
		return false, false, err
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return false, false, nil
	}
	var object struct {
		Metadata struct {
			Annotations map[string]string `json:"annotations"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal([]byte(value), &object); err != nil {
		return false, true, err
	}
	return object.Metadata.Annotations[ownershipKey] == marker, true, nil
}

// fetchChartSet downloads charts in parallel and verifies each digest.
func (plugin Plugin) fetchChartSet(ctx context.Context, charts []chartSpec) (map[string][]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	values := map[string][]byte{}
	var lock sync.Mutex
	var first error
	var wait sync.WaitGroup
	for _, chart := range charts {
		chart := chart
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, err := plugin.fetcher().Fetch(ctx, chart)
			lock.Lock()
			defer lock.Unlock()
			if err != nil && first == nil {
				first = fmt.Errorf("fetch %s chart: %w", chart.ID, err)
				cancel()
				return
			}
			if err == nil {
				values[chart.ID] = value
			}
		}()
	}
	wait.Wait()
	return values, first
}

func (plugin Plugin) runner() clusterRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return localRunner{}
}

func (plugin Plugin) fetcher() chartFetcher {
	if plugin.Fetcher != nil {
		return plugin.Fetcher
	}
	return httpChartFetcher{client: &http.Client{Timeout: 2 * time.Minute}}
}

type localRunner struct{}

func (localRunner) Kubectl(ctx context.Context, kubeconfig string, stdin []byte, args ...string) (string, error) {
	return runClusterCommand(ctx, kubeconfig, stdin, nil, "kubectl", args...)
}

func (localRunner) Helm(ctx context.Context, kubeconfig string, chart, values []byte, args ...string) (string, error) {
	return runClusterCommand(ctx, kubeconfig, chart, values, "helm", args...)
}

func runClusterCommand(ctx context.Context, kubeconfig string, primary, secondary []byte, executable string, args ...string) (string, error) {
	directory, err := os.MkdirTemp("", "kubephos-managed-platform-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	kubeconfigPath := directory + "/kubeconfig"
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return "", err
	}
	resolved := append([]string{}, args...)
	var stdin []byte
	if executable == "kubectl" {
		resolved = append([]string{"--kubeconfig", kubeconfigPath}, resolved...)
		stdin = primary
	} else {
		chartPath := directory + "/chart.tgz"
		valuesPath := directory + "/values.yaml"
		if primary != nil {
			if err := os.WriteFile(chartPath, primary, 0600); err != nil {
				return "", err
			}
		}
		if secondary != nil {
			if err := os.WriteFile(valuesPath, secondary, 0600); err != nil {
				return "", err
			}
		}
		for index := range resolved {
			if resolved[index] == "@chart" {
				resolved[index] = chartPath
			}
			if resolved[index] == "@values" {
				resolved[index] = valuesPath
			}
		}
		resolved = append(resolved, "--kubeconfig", kubeconfigPath)
	}
	command := exec.CommandContext(ctx, executable, resolved...)
	command.Stdin = bytes.NewReader(stdin)
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", executable, err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

type httpChartFetcher struct {
	client *http.Client
}

func (fetcher httpChartFetcher) Fetch(ctx context.Context, chart chartSpec) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, chart.URL, nil)
	if err != nil {
		return nil, err
	}
	response, err := fetcher.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("chart returned HTTP status %d", response.StatusCode)
	}
	value, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(value)
	if hex.EncodeToString(digest[:]) != chart.Digest {
		return nil, errors.New("chart digest does not match the managed release")
	}
	return value, nil
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}
