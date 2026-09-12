package networkinjection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID     = "io.kubephos.chaos.network.zones"
	artifactAPI  = "artifacts.kubephos.dev/v1alpha1"
	profileLabel = "kubephos.dev/network-profile"
	zoneLabel    = "topology.kubernetes.io/zone"
)

type Plugin struct {
	Runner commandRunner
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef     string `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string `json:"applicationDeploymentRef"`
	ManagedPlatformRef       string `json:"managedPlatformRef"`
	LatencyMilliseconds      int    `json:"latencyMilliseconds"`
	JitterMilliseconds       int    `json:"jitterMilliseconds"`
}

type clusterConnection struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Server     string `json:"server"`
		Kubeconfig string `json:"kubeconfig"`
	} `json:"spec"`
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
		ApplicationRef string `json:"applicationRef"`
		ClusterServer  string `json:"clusterServer"`
		Namespace      string `json:"namespace"`
	} `json:"spec"`
}

type managedPlatform struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		ClusterServer string `json:"clusterServer"`
		Components    []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"components"`
	} `json:"spec"`
}

type nodeList struct {
	Items []struct {
		Metadata struct {
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
	} `json:"items"`
}

type networkInjection struct {
	APIVersion string                   `json:"apiVersion"`
	Kind       string                   `json:"kind"`
	Metadata   networkInjectionMetadata `json:"metadata"`
	Spec       networkInjectionSpec     `json:"spec"`
}

type networkInjectionMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type networkInjectionSpec struct {
	ApplicationRef      string   `json:"applicationRef"`
	ClusterServer       string   `json:"clusterServer"`
	Namespace           string   `json:"namespace"`
	Zones               []string `json:"zones"`
	Links               int      `json:"links"`
	LatencyMilliseconds int      `json:"latencyMilliseconds"`
	JitterMilliseconds  int      `json:"jitterMilliseconds"`
}

type result struct {
	NetworkInjection networkInjection `json:"networkInjection"`
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Inter-zone network injection", Version: "0.1.0",
		Description:     "Applies and verifies a symmetric latency profile between every application zone.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","managedPlatformRef","latencyMilliseconds","jitterMilliseconds"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","title":"Application deployment","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"managedPlatformRef":{"type":"string","title":"Managed chaos runtime","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ManagedPlatformCapability","x-kubephos-artifact-version":"v1alpha1"},"latencyMilliseconds":{"type":"integer","title":"Cross-zone latency in ms","minimum":1,"maximum":5000,"default":50},"jitterMilliseconds":{"type":"integer","title":"Jitter in ms","minimum":0,"maximum":5000,"default":0}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "ManagedPlatformCapability", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "NetworkInjection", Version: "v1alpha1"}},
		Capabilities:    []string{"chaos.network.zones", "chaos.network.preflight", "chaos.network.cleanup", "lifecycle.cleanup"},
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
	for path, value := range map[string]string{"clusterConnectionRef": spec.ClusterConnectionRef, "applicationDeploymentRef": spec.ApplicationDeploymentRef, "managedPlatformRef": spec.ManagedPlatformRef} {
		if !strings.HasPrefix(value, "art_") {
			return invalid(report, path, "Select a verified artifact.")
		}
	}
	if spec.LatencyMilliseconds < 1 || spec.LatencyMilliseconds > 5000 {
		return invalid(report, "latencyMilliseconds", "Latency must be between 1 and 5000 milliseconds.")
	}
	if spec.JitterMilliseconds < 0 || spec.JitterMilliseconds > spec.LatencyMilliseconds {
		return invalid(report, "jitterMilliseconds", "Jitter must be between zero and the selected latency.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "Cluster readiness, Chaos Mesh, application ownership, zones and admission will be checked before injection."})
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
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "inject-inter-zone-network", Name: "Inject and verify inter-zone network latency", Input: raw, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}, {Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef}, {Name: "managed-platform", Type: "ManagedPlatformCapability", Version: "v1alpha1", ArtifactID: spec.ManagedPlatformRef}},
		Outputs:        []domain.ArtifactOutput{{Name: "network-injection", Type: "NetworkInjection", Version: "v1alpha1", MediaType: "application/json", Source: "/networkInjection"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, application, platform, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateArtifacts(cluster, application, platform); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "customresourcedefinition", "networkchaos.chaos-mesh.org"); err != nil {
		return unhealthy("Chaos Mesh NetworkChaos is unavailable", "chaosMesh", "unavailable"), nil
	}
	zones, err := discoverZones(ctx, runner, cluster.Spec.Kubeconfig)
	if err != nil || len(zones) < 2 {
		return unhealthy("At least two labeled application zones are required", "zones", "insufficient"), nil
	}
	manifest, err := networkManifest(application, zones, spec)
	if err != nil {
		return unhealthy(err.Error(), "manifest", "invalid"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return unhealthy("Network profile admission failed: "+err.Error(), "admission", "rejected"), nil
	}
	if err := log("info", fmt.Sprintf("Validated Chaos Mesh and %d directed links across %d application zones", len(zones)*(len(zones)-1), len(zones))); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Inter-zone network injection is ready", Checks: map[string]string{"api": "ready", "chaosMesh": "ready", "zones": fmt.Sprint(len(zones)), "admission": "accepted"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, cluster, application, _, err := resolve(step)
	if err != nil {
		return nil, err
	}
	zones, err := discoverZones(ctx, plugin.runner(), cluster.Spec.Kubeconfig)
	if err != nil {
		return nil, err
	}
	manifest, err := networkManifest(application, zones, spec)
	if err != nil {
		return nil, err
	}
	if err := log("info", "Applying the symmetric inter-zone network profile"); err != nil {
		return nil, err
	}
	if _, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "-f", "-"); err != nil {
		return nil, err
	}
	value := result{NetworkInjection: networkInjection{APIVersion: artifactAPI, Kind: "NetworkInjection", Metadata: networkInjectionMetadata{Name: application.Spec.Namespace, Version: "v1alpha1"}, Spec: networkInjectionSpec{ApplicationRef: application.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: application.Spec.Namespace, Zones: zones, Links: len(zones) * (len(zones) - 1), LatencyMilliseconds: spec.LatencyMilliseconds, JitterMilliseconds: spec.JitterMilliseconds}}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, application, _, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if step.Cleanup {
		count, err := profileCount(ctx, plugin.runner(), cluster.Spec.Kubeconfig, application)
		if err != nil || count != 0 {
			return unhealthy("Network injection resources remain after cleanup", "networkInjection", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Network injection was removed", Checks: map[string]string{"networkInjection": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	expected := len(value.NetworkInjection.Spec.Zones) * (len(value.NetworkInjection.Spec.Zones) - 1)
	if value.NetworkInjection.APIVersion != artifactAPI || value.NetworkInjection.Kind != "NetworkInjection" || value.NetworkInjection.Spec.ClusterServer != cluster.Spec.Server || value.NetworkInjection.Spec.Namespace != application.Spec.Namespace || value.NetworkInjection.Spec.Links != expected || value.NetworkInjection.Spec.LatencyMilliseconds != spec.LatencyMilliseconds || value.NetworkInjection.Spec.JitterMilliseconds != spec.JitterMilliseconds {
		return unhealthy("Network injection result is invalid", "artifact", "invalid"), nil
	}
	count, err := profileCount(ctx, plugin.runner(), cluster.Spec.Kubeconfig, application)
	if err != nil || count != expected {
		return unhealthy("Not every directed inter-zone link is active", "networkInjection", "incomplete"), nil
	}
	if err := log("info", fmt.Sprintf("Verified %d active directed inter-zone links", count)); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Inter-zone network injection is active", Checks: map[string]string{"links": fmt.Sprint(count), "latencyMs": fmt.Sprint(spec.LatencyMilliseconds), "jitterMs": fmt.Sprint(spec.JitterMilliseconds)}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	_, cluster, application, _, err := resolve(step)
	if err != nil {
		return err
	}
	if err := log("warning", "Removing the inter-zone network profile"); err != nil {
		return err
	}
	_, err = plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "delete", "networkchaos", "-n", application.Spec.Namespace, "-l", profileLabel+"="+application.Metadata.OwnershipMarker, "--ignore-not-found", "--wait=true", "--timeout=5m")
	return err
}

func resolve(step domain.PlanStep) (Spec, clusterConnection, applicationDeployment, managedPlatform, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, managedPlatform{}, err
	}
	clusterRaw, clusterOK := step.ResolvedInputs["cluster-connection"]
	applicationRaw, applicationOK := step.ResolvedInputs["application-deployment"]
	platformRaw, platformOK := step.ResolvedInputs["managed-platform"]
	if !clusterOK || !applicationOK || !platformOK {
		return Spec{}, clusterConnection{}, applicationDeployment{}, managedPlatform{}, errors.New("verified network injection inputs are unavailable")
	}
	var cluster clusterConnection
	var application applicationDeployment
	var platform managedPlatform
	if err := json.Unmarshal(clusterRaw.Value, &cluster); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, managedPlatform{}, err
	}
	if err := json.Unmarshal(applicationRaw.Value, &application); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, managedPlatform{}, err
	}
	if err := json.Unmarshal(platformRaw.Value, &platform); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, managedPlatform{}, err
	}
	return spec, cluster, application, platform, nil
}

func validateArtifacts(cluster clusterConnection, application applicationDeployment, platform managedPlatform) error {
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Metadata.Name == "" || cluster.Metadata.Version == "" || cluster.Spec.Kubeconfig == "" {
		return errors.New("cluster connection identity is invalid")
	}
	server, err := url.Parse(cluster.Spec.Server)
	if err != nil || server.Scheme != "https" || server.Host == "" || server.Path != "" {
		return errors.New("cluster server must be a valid HTTPS endpoint")
	}
	if application.APIVersion != artifactAPI || application.Kind != "ApplicationDeployment" || application.Metadata.Version != "v1alpha1" || application.Metadata.OwnershipMarker == "" || application.Spec.Namespace == "" || application.Spec.ClusterServer != cluster.Spec.Server {
		return errors.New("application deployment identity is invalid")
	}
	if platform.APIVersion != artifactAPI || platform.Kind != "ManagedPlatformCapability" || platform.Spec.ClusterServer != cluster.Spec.Server {
		return errors.New("managed platform identity is invalid")
	}
	for _, component := range platform.Spec.Components {
		if component.Name == "chaos-mesh" && component.Status == "ready" {
			return nil
		}
	}
	return errors.New("managed Chaos Mesh is not ready")
}

func discoverZones(ctx context.Context, runner commandRunner, kubeconfig string) ([]string, error) {
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "nodes", "-l", "kubephos.dev/role=application", "-o", "json")
	if err != nil {
		return nil, err
	}
	var nodes nodeList
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, node := range nodes.Items {
		if zone := strings.TrimSpace(node.Metadata.Labels[zoneLabel]); zone != "" {
			set[zone] = true
		}
	}
	zones := make([]string, 0, len(set))
	for zone := range set {
		zones = append(zones, zone)
	}
	sort.Strings(zones)
	return zones, nil
}

func networkManifest(application applicationDeployment, zones []string, spec Spec) ([]byte, error) {
	if len(zones) < 2 {
		return nil, errors.New("at least two application zones are required")
	}
	resources := make([]map[string]any, 0, len(zones)*(len(zones)-1))
	for _, source := range zones {
		for _, target := range zones {
			if source == target {
				continue
			}
			name := resourceName("zone-" + source + "-to-" + target)
			resources = append(resources, map[string]any{
				"apiVersion": "chaos-mesh.org/v1alpha1", "kind": "NetworkChaos",
				"metadata": map[string]any{"name": name, "namespace": application.Spec.Namespace, "labels": map[string]string{profileLabel: application.Metadata.OwnershipMarker}},
				"spec": map[string]any{
					"action": "delay", "mode": "all", "direction": "to",
					"selector": map[string]any{"namespaces": []string{application.Spec.Namespace}, "nodeSelectors": map[string]string{zoneLabel: source}},
					"target":   map[string]any{"mode": "all", "selector": map[string]any{"namespaces": []string{application.Spec.Namespace}, "nodeSelectors": map[string]string{zoneLabel: target}}},
					"delay":    map[string]string{"latency": fmt.Sprintf("%dms", spec.LatencyMilliseconds), "jitter": fmt.Sprintf("%dms", spec.JitterMilliseconds), "correlation": "0"},
				},
			})
		}
	}
	return json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": resources})
}

func profileCount(ctx context.Context, runner commandRunner, kubeconfig string, application applicationDeployment) (int, error) {
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "networkchaos", "-n", application.Spec.Namespace, "-l", profileLabel+"="+application.Metadata.OwnershipMarker, "-o", "json")
	if err != nil {
		return 0, err
	}
	var list struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return 0, err
	}
	return len(list.Items), nil
}

func resourceName(value string) string {
	value = strings.ToLower(value)
	var result strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' {
			result.WriteRune(character)
		} else {
			result.WriteByte('-')
		}
	}
	return strings.Trim(strings.TrimSpace(result.String()), "-")
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
	file, err := os.CreateTemp("", "kubephos-kubeconfig-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	defer os.Remove(path)
	if err := file.Chmod(0600); err != nil {
		file.Close()
		return "", err
	}
	if _, err := file.WriteString(kubeconfig); err != nil {
		file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", path}, args...)...)
	command.Stdin = bytes.NewReader(stdin)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
