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
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID           = "io.kubephos.platform.managed"
	artifactAPI        = "artifacts.kubephos.dev/v1alpha1"
	istioVersion       = "1.30.0"
	chaosVersion       = "2.8.3"
	istioNamespace     = "istio-system"
	chaosNamespace     = "chaos-mesh"
	istioBaseRelease   = "kubephos-istio-base"
	istiodRelease      = "kubephos-istiod"
	chaosRelease       = "kubephos-chaos"
	chaosDashboardPort = 32300
	ownershipKey       = "kubephos.dev/ownership-marker"
)

var managedCharts = []chartSpec{
	{ID: "istio-base", URL: "https://istio-release.storage.googleapis.com/charts/base-1.30.0.tgz", Digest: "aef833a87ed0022114c5759fe3ec6af7862c2a224d4c60e40ebaaef825e1e39d"},
	{ID: "istiod", URL: "https://istio-release.storage.googleapis.com/charts/istiod-1.30.0.tgz", Digest: "7a34c7688da34107841e9585b226b94357769749f82502fdc4fb43a6bc3db320"},
	{ID: "chaos-mesh", URL: "https://charts.chaos-mesh.org/chaos-mesh-2.8.3.tgz", Digest: "c96a2d6490c1fbb0693e18b04e2ab6f60ca45046c64e98ca774f72ac1eaf2a47"},
}

type Plugin struct {
	Runner  clusterRunner
	Fetcher chartFetcher
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef string `json:"clusterConnectionRef"`
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
		Distribution string `json:"distribution"`
		Server       string `json:"server"`
		Kubeconfig   string `json:"kubeconfig"`
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

type result struct {
	ManagedPlatformCapability managedPlatformCapability `json:"managedPlatformCapability"`
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
		ID: pluginID, Name: "Managed Kubernetes platform", Version: "0.1.0",
		Description:     "Installs and verifies the release-managed Istio and chaos runtime baseline.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ManagedPlatformCapability", Version: "v1alpha1"}},
		Capabilities:    []string{"platform.managed.install", "platform.managed.preflight", "platform.managed.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"cluster.admin", "network.http"},
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
	if !strings.HasPrefix(spec.ClusterConnectionRef, "art_") {
		return invalid(report, "clusterConnectionRef", "Select a verified Kubernetes connection.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("KubePhos will install managed Istio %s and Chaos Mesh %s.", istioVersion, chaosVersion)})
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
	marker, err := randomHex(16)
	if err != nil {
		return domain.Plan{}, err
	}
	input, err := json.Marshal(stepInput{Spec: spec, Marker: marker})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "install-managed-platform", Name: "Install and verify managed platform", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}},
		Outputs:        []domain.ArtifactOutput{{Name: "managed-platform-capability", Type: "ManagedPlatformCapability", Version: "v1alpha1", MediaType: "application/json", Source: "/managedPlatformCapability"}},
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
	runner := plugin.runner()
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	authorized, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "auth", "can-i", "*", "*", "--all-namespaces")
	if err != nil || strings.TrimSpace(authorized) != "yes" {
		return unhealthy("The cluster connection does not have administrative permissions", "authorization", "denied"), nil
	}
	if step.Cleanup {
		for _, namespace := range []string{istioNamespace, chaosNamespace} {
			owned, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
			if err != nil || present && !owned {
				return unhealthy("Managed namespace ownership cannot be verified", namespace, "invalid"), nil
			}
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed platform resources are safe to remove", Checks: map[string]string{"api": "ready", "ownership": "verified"}}, nil
	}
	for _, namespace := range []string{istioNamespace, chaosNamespace} {
		_, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
		if err != nil {
			return unhealthy(err.Error(), namespace, "unknown"), nil
		}
		if present {
			return unhealthy("Namespace "+namespace+" already exists", namespace, "conflict"), nil
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
	if err := log("info", "Downloading and verifying managed Istio and Chaos Mesh charts in parallel"); err != nil {
		return nil, err
	}
	charts, err := plugin.fetchCharts(ctx)
	if err != nil {
		return nil, err
	}
	runner := plugin.runner()
	for _, namespace := range []string{istioNamespace, chaosNamespace} {
		manifest, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace, "annotations": map[string]string{ownershipKey: input.Marker}}})
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "-f", "-"); err != nil {
			return nil, fmt.Errorf("create namespace %s: %w", namespace, err)
		}
	}
	if err := log("info", "Installing Istio base and control plane"); err != nil {
		return nil, err
	}
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, charts["istio-base"], []byte("defaultRevision: default\n"), "upgrade", "--install", istioBaseRelease, "@chart", "--namespace", istioNamespace, "--values", "@values", "--wait", "--timeout", "10m", "--atomic"); err != nil {
		return nil, fmt.Errorf("install Istio base: %w", err)
	}
	istiodValues := []byte("replicaCount: 1\nnodeSelector:\n  kubephos.dev/role: management\n")
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, charts["istiod"], istiodValues, "upgrade", "--install", istiodRelease, "@chart", "--namespace", istioNamespace, "--values", "@values", "--wait", "--timeout", "10m", "--atomic"); err != nil {
		return nil, fmt.Errorf("install Istio control plane: %w", err)
	}
	if err := log("info", "Installing the managed chaos runtime and dashboard"); err != nil {
		return nil, err
	}
	chaosValues := []byte("chaosDaemon:\n  runtime: containerd\n  socketPath: /run/k3s/containerd/containerd.sock\ncontrollerManager:\n  replicaCount: 1\n  nodeSelector:\n    kubephos.dev/role: management\ndashboard:\n  enabled: true\n  replicaCount: 1\n  nodeSelector:\n    kubephos.dev/role: management\n  securityMode: false\n  service:\n    type: NodePort\n    port: 2333\n    nodePort: 32300\n")
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, charts["chaos-mesh"], chaosValues, "upgrade", "--install", chaosRelease, "@chart", "--namespace", chaosNamespace, "--values", "@values", "--wait", "--wait-for-jobs", "--timeout", "15m", "--atomic"); err != nil {
		return nil, fmt.Errorf("install chaos runtime: %w", err)
	}
	value := result{ManagedPlatformCapability: managedPlatformCapability{
		APIVersion: artifactAPI, Kind: "ManagedPlatformCapability", Metadata: managedPlatformCapabilityMetadata{Name: "managed-platform", Version: istioVersion + "+chaos." + chaosVersion},
		Spec: managedPlatformCapabilitySpec{ClusterServer: cluster.Spec.Server, Components: []managedComponent{{Name: "istio", Version: istioVersion, Namespace: istioNamespace, Status: "ready"}, {Name: "chaos-mesh", Version: chaosVersion, Namespace: chaosNamespace, Status: "ready"}}, Endpoints: []managedEndpoint{{Name: "chaos-dashboard", Namespace: chaosNamespace, Service: "chaos-dashboard", Scheme: "http", NodePort: chaosDashboardPort}}},
	}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if step.Cleanup {
		for _, namespace := range []string{istioNamespace, chaosNamespace} {
			_, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
			if err != nil || present {
				return unhealthy("Managed namespace remains after cleanup", namespace, "present"), nil
			}
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed platform resources were removed", Checks: map[string]string{"istio": "absent", "chaos": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	capability := value.ManagedPlatformCapability
	if capability.APIVersion != artifactAPI || capability.Kind != "ManagedPlatformCapability" || capability.Spec.ClusterServer != cluster.Spec.Server || len(capability.Spec.Components) != 2 || len(capability.Spec.Endpoints) != 1 || capability.Spec.Endpoints[0].NodePort != chaosDashboardPort {
		return unhealthy("Managed platform capability is invalid", "artifact", "invalid"), nil
	}
	for _, release := range []struct{ name, namespace string }{{istioBaseRelease, istioNamespace}, {istiodRelease, istioNamespace}, {chaosRelease, chaosNamespace}} {
		if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, nil, nil, "status", release.name, "--namespace", release.namespace); err != nil {
			return unhealthy("Managed release is unhealthy: "+err.Error(), release.name, "unhealthy"), nil
		}
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/istiod", "-n", istioNamespace, "--timeout=5m"); err != nil {
		return unhealthy("Istio control plane is not ready: "+err.Error(), "istio", "unhealthy"), nil
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "daemonset/chaos-daemon", "-n", chaosNamespace, "--timeout=5m"); err != nil {
		return unhealthy("Chaos daemon is not ready on all nodes: "+err.Error(), "chaosDaemon", "unhealthy"), nil
	}
	port, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "service", "chaos-dashboard", "-n", chaosNamespace, "-o", "jsonpath={.spec.ports[0].nodePort}")
	if err != nil || strings.TrimSpace(port) != fmt.Sprint(chaosDashboardPort) {
		return unhealthy("Chaos dashboard NodePort is unavailable", "chaosDashboard", "unhealthy"), nil
	}
	if err := log("info", "Istio control plane, chaos daemon coverage and chaos dashboard endpoint are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed platform baseline is ready", Checks: map[string]string{"istio": istioVersion, "chaos": chaosVersion, "chaosDashboardNodePort": fmt.Sprint(chaosDashboardPort)}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, cluster, err := resolve(step)
	if err != nil {
		return err
	}
	runner := plugin.runner()
	for _, namespace := range []string{istioNamespace, chaosNamespace} {
		owned, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
		if err != nil {
			return err
		}
		if present && !owned {
			return fmt.Errorf("refusing to remove namespace %s without verified ownership", namespace)
		}
	}
	if err := log("warning", "Removing managed Chaos Mesh and Istio releases"); err != nil {
		return err
	}
	for _, release := range []struct{ name, namespace string }{{chaosRelease, chaosNamespace}, {istiodRelease, istioNamespace}, {istioBaseRelease, istioNamespace}} {
		if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, nil, nil, "uninstall", release.name, "--namespace", release.namespace, "--wait", "--timeout", "10m", "--ignore-not-found"); err != nil {
			return err
		}
	}
	for _, namespace := range []string{chaosNamespace, istioNamespace} {
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "delete", "namespace", namespace, "--ignore-not-found", "--wait=true", "--timeout=5m"); err != nil {
			return err
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

func (plugin Plugin) fetchCharts(ctx context.Context) (map[string][]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	values := map[string][]byte{}
	var lock sync.Mutex
	var first error
	var wait sync.WaitGroup
	for _, chart := range managedCharts {
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
