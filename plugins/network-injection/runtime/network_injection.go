package networkinjection

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"os"
	"os/exec"
	"regexp"
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
	chaosImage   = "ghcr.io/chaos-mesh/chaos-daemon:v2.8.3"
	chaosNS      = "chaos-mesh"
)

type Plugin struct{ Runner commandRunner }
type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef     string         `json:"clusterConnectionRef"`
	ApplicationDeploymentRef string         `json:"applicationDeploymentRef"`
	ManagedPlatformRef       string         `json:"managedPlatformRef"`
	NodeGroupLabel           string         `json:"nodeGroupLabel"`
	NodeSelector             string         `json:"nodeSelector,omitempty"`
	NetworkInterface         string         `json:"networkInterface"`
	HostNetwork              bool           `json:"hostNetwork"`
	EnableLatency            bool           `json:"enableLatency"`
	LatencyMilliseconds      int            `json:"latencyMilliseconds"`
	JitterMilliseconds       int            `json:"jitterMilliseconds"`
	CorrelationPercent       float64        `json:"correlationPercent"`
	EnableBandwidth          bool           `json:"enableBandwidth"`
	BandwidthBytesPerSecond  int64          `json:"bandwidthBytesPerSecond"`
	EnablePacketLoss         bool           `json:"enablePacketLoss"`
	PacketLossPercent        float64        `json:"packetLossPercent"`
	LinkOverrides            []LinkOverride `json:"linkOverrides,omitempty"`
}

type LinkOverride struct {
	SourceZone              string   `json:"sourceZone"`
	TargetZone              string   `json:"targetZone"`
	LatencyMilliseconds     *int     `json:"latencyMilliseconds,omitempty"`
	JitterMilliseconds      *int     `json:"jitterMilliseconds,omitempty"`
	CorrelationPercent      *float64 `json:"correlationPercent,omitempty"`
	BandwidthBytesPerSecond *int64   `json:"bandwidthBytesPerSecond,omitempty"`
	PacketLossPercent       *float64 `json:"packetLossPercent,omitempty"`
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
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			PodCIDR string `json:"podCIDR"`
		} `json:"spec"`
		Status struct {
			Addresses []struct {
				Type    string `json:"type"`
				Address string `json:"address"`
			} `json:"addresses"`
		} `json:"status"`
	} `json:"items"`
}

type node struct{ Name, Zone, InternalIP, PodCIDR string }
type effectiveLink struct {
	EnableLatency, EnableBandwidth, EnablePacketLoss bool
	Latency, Jitter                                  int
	Correlation                                      float64
	Bandwidth                                        int64
	PacketLoss                                       float64
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
	ApplicationRef string   `json:"applicationRef"`
	ClusterServer  string   `json:"clusterServer"`
	Namespace      string   `json:"namespace"`
	Zones          []string `json:"zones"`
	Nodes          int      `json:"nodes"`
	Links          int      `json:"links"`
	Configuration  Spec     `json:"configuration"`
}
type result struct {
	NetworkInjection networkInjection `json:"networkInjection"`
}
type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Inter-zone network injection", Version: "0.3.0",
		Description:     "Applies latency, jitter, bandwidth and packet loss to directed links between application zones.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","applicationDeploymentRef","managedPlatformRef","nodeGroupLabel","networkInterface","hostNetwork","enableLatency","latencyMilliseconds","jitterMilliseconds","correlationPercent","enableBandwidth","bandwidthBytesPerSecond","enablePacketLoss","packetLossPercent"],"properties":{"clusterConnectionRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"applicationDeploymentRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ApplicationDeployment","x-kubephos-artifact-version":"v1alpha1"},"managedPlatformRef":{"type":"string","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ManagedPlatformCapability","x-kubephos-artifact-version":"v1alpha1"},"nodeGroupLabel":{"type":"string","title":"Node group label","default":"topology.kubernetes.io/zone"},"nodeSelector":{"type":"string","title":"Affected node selector","default":"kubephos.dev/role=application"},"networkInterface":{"type":"string","title":"Network interface","default":"flannel.1"},"hostNetwork":{"type":"boolean","title":"Shape host-network traffic","default":false},"enableLatency":{"type":"boolean","title":"Inject latency","default":true},"latencyMilliseconds":{"type":"integer","title":"Latency in ms","minimum":1,"maximum":60000,"default":50},"jitterMilliseconds":{"type":"integer","title":"Jitter in ms","minimum":0,"maximum":60000,"default":0},"correlationPercent":{"type":"number","title":"Correlation %","minimum":0,"maximum":100,"default":0},"enableBandwidth":{"type":"boolean","title":"Limit bandwidth","default":false},"bandwidthBytesPerSecond":{"type":"integer","title":"Bandwidth bytes / second","minimum":1,"maximum":1000000000000,"default":12500000},"enablePacketLoss":{"type":"boolean","title":"Inject packet loss","default":false},"packetLossPercent":{"type":"number","title":"Packet loss %","minimum":0,"maximum":100,"default":0},"linkOverrides":{"type":"array","title":"Directed zone overrides","maxItems":225,"items":{"type":"object"}}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ApplicationDeployment", Version: "v1alpha1"}, {Type: "ManagedPlatformCapability", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "NetworkInjection", Version: "v1alpha1"}},
		Capabilities:    []string{"chaos.network.zones", "chaos.network.preflight", "chaos.network.cleanup", "lifecycle.cleanup"}, Permissions: []string{"cluster.admin"},
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
	if err := validateConfiguration(withDefaults(spec)); err != nil {
		return invalid(report, "networkInjection", err.Error())
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "Every selected node, zone, destination network, qdisc and server-side admission will be checked before traffic shaping."})
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
	spec = withDefaults(spec)
	if err := validateConfiguration(spec); err != nil {
		return domain.Plan{}, err
	}
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{ID: "inject-inter-zone-network", Name: "Inject and verify directed inter-zone network conditions", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}, {Name: "application-deployment", Type: "ApplicationDeployment", Version: "v1alpha1", ArtifactID: spec.ApplicationDeploymentRef}, {Name: "managed-platform", Type: "ManagedPlatformCapability", Version: "v1alpha1", ArtifactID: spec.ManagedPlatformRef}},
		Outputs:        []domain.ArtifactOutput{{Name: "network-injection", Type: "NetworkInjection", Version: "v1alpha1", MediaType: "application/json", Source: "/networkInjection"}}}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, application, platform, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateArtifacts(cluster, application, platform); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateConfiguration(spec); err != nil {
		return unhealthy(err.Error(), "configuration", "invalid"), nil
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "namespace", chaosNS); err != nil {
		return unhealthy("Managed chaos namespace is unavailable", "chaosRuntime", "unavailable"), nil
	}
	nodes, zones, err := discoverNodes(ctx, runner, cluster.Spec.Kubeconfig, spec)
	if err != nil {
		return unhealthy(err.Error(), "nodes", "invalid"), nil
	}
	if err := validateOverrides(spec.LinkOverrides, zones, spec); err != nil {
		return unhealthy(err.Error(), "linkOverrides", "invalid"), nil
	}
	activeNodes, links := profileDimensions(nodes, zones, spec)
	manifest, err := networkManifest(application, nodes, spec)
	if err != nil {
		return unhealthy(err.Error(), "manifest", "invalid"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return unhealthy("Network profile admission failed: "+err.Error(), "admission", "rejected"), nil
	}
	if err := log("info", fmt.Sprintf("Validated %d node shapers and %d directed zone links", activeNodes, links)); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Inter-zone network injection is ready", Checks: map[string]string{"api": "ready", "chaosRuntime": "ready", "nodes": fmt.Sprint(activeNodes), "zones": fmt.Sprint(len(zones)), "links": fmt.Sprint(links), "admission": "accepted"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, cluster, application, _, err := resolve(step)
	if err != nil {
		return nil, err
	}
	nodes, zones, err := discoverNodes(ctx, plugin.runner(), cluster.Spec.Kubeconfig, spec)
	if err != nil {
		return nil, err
	}
	manifest, err := networkManifest(application, nodes, spec)
	if err != nil {
		return nil, err
	}
	if err := log("info", "Applying node-pinned traffic shaping across application zones"); err != nil {
		return nil, err
	}
	if _, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, manifest, "apply", "-f", "-"); err != nil {
		return nil, err
	}
	if _, err := plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "daemonset", "-n", chaosNS, "-l", profileLabel+"="+application.Metadata.OwnershipMarker, "--timeout=3m"); err != nil {
		return nil, fmt.Errorf("network shapers did not become ready: %w", err)
	}
	activeNodes, links := profileDimensions(nodes, zones, spec)
	value := result{NetworkInjection: networkInjection{APIVersion: artifactAPI, Kind: "NetworkInjection", Metadata: networkInjectionMetadata{Name: application.Spec.Namespace, Version: "v1alpha1"}, Spec: networkInjectionSpec{ApplicationRef: application.Spec.ApplicationRef, ClusterServer: cluster.Spec.Server, Namespace: application.Spec.Namespace, Zones: zones, Nodes: len(nodes), Links: len(zones) * (len(zones) - 1), Configuration: spec}}}
	value.NetworkInjection.Spec.Nodes = activeNodes
	value.NetworkInjection.Spec.Links = links
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, cluster, application, _, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	count, err := profileCount(ctx, plugin.runner(), cluster.Spec.Kubeconfig, application)
	if step.Cleanup {
		if err != nil || count != 0 {
			return unhealthy("Network injection resources remain after cleanup", "networkInjection", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Network injection was removed", Checks: map[string]string{"networkInjection": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	nodes, zones, err := discoverNodes(ctx, plugin.runner(), cluster.Spec.Kubeconfig, spec)
	if err != nil {
		return unhealthy("Selected nodes cannot be rediscovered: "+err.Error(), "nodes", "unavailable"), nil
	}
	activeNodes, links := profileDimensions(nodes, zones, spec)
	if value.NetworkInjection.APIVersion != artifactAPI || value.NetworkInjection.Kind != "NetworkInjection" || value.NetworkInjection.Spec.ClusterServer != cluster.Spec.Server || value.NetworkInjection.Spec.Namespace != application.Spec.Namespace || value.NetworkInjection.Spec.Nodes != activeNodes || value.NetworkInjection.Spec.Links != links {
		return unhealthy("Network injection result is invalid", "artifact", "invalid"), nil
	}
	if err != nil || count != activeNodes {
		return unhealthy("Not every selected node has an active shaper", "networkInjection", "incomplete"), nil
	}
	if err := verifyAppliedProfile(ctx, plugin.runner(), cluster.Spec.Kubeconfig, application, nodes, zones, spec); err != nil {
		return unhealthy("Applied traffic shaping differs from the requested profile: "+err.Error(), "networkInjection", "mismatch"), nil
	}
	if err := log("info", fmt.Sprintf("Verified %d active node shapers and their kernel traffic-control parameters", count)); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Inter-zone network injection is active", Checks: map[string]string{"nodes": fmt.Sprint(count), "links": fmt.Sprint(links), "kernelParameters": "verified"}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	_, cluster, application, _, err := resolve(step)
	if err != nil {
		return err
	}
	if err := log("warning", "Removing all owned node traffic shapers"); err != nil {
		return err
	}
	_, err = plugin.runner().Run(ctx, cluster.Spec.Kubeconfig, nil, "delete", "daemonset", "-n", chaosNS, "-l", profileLabel+"="+application.Metadata.OwnershipMarker, "--ignore-not-found", "--wait=true", "--timeout=5m")
	return err
}

func resolve(step domain.PlanStep) (Spec, clusterConnection, applicationDeployment, managedPlatform, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, clusterConnection{}, applicationDeployment{}, managedPlatform{}, err
	}
	spec = withDefaults(spec)
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
	return errors.New("managed chaos runtime is not ready")
}

func withDefaults(spec Spec) Spec {
	if spec.NodeGroupLabel == "" {
		spec.NodeGroupLabel = "topology.kubernetes.io/zone"
	}
	if spec.NodeSelector == "" {
		spec.NodeSelector = "kubephos.dev/role=application"
	}
	if spec.NetworkInterface == "" {
		spec.NetworkInterface = "flannel.1"
	}
	if !spec.EnableLatency && !spec.EnableBandwidth && !spec.EnablePacketLoss && len(spec.LinkOverrides) == 0 && spec.LatencyMilliseconds == 0 && spec.BandwidthBytesPerSecond == 0 && spec.PacketLossPercent == 0 {
		spec.EnableLatency = true
	}
	if spec.LatencyMilliseconds == 0 {
		spec.LatencyMilliseconds = 50
	}
	if spec.BandwidthBytesPerSecond == 0 {
		spec.BandwidthBytesPerSecond = 12500000
	}
	return spec
}

func validateConfiguration(spec Spec) error {
	label := regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._/-]{0,251}[A-Za-z0-9])?$`)
	selector := regexp.MustCompile(`^[A-Za-z0-9._/-]+=[A-Za-z0-9._-]+$`)
	iface := regexp.MustCompile(`^[A-Za-z0-9_.:@-]{1,15}$`)
	if !label.MatchString(spec.NodeGroupLabel) {
		return errors.New("node group label is invalid")
	}
	if spec.NodeSelector != "" && !selector.MatchString(spec.NodeSelector) {
		return errors.New("node selector must use label=value format")
	}
	if !iface.MatchString(spec.NetworkInterface) {
		return errors.New("network interface is invalid")
	}
	if !spec.EnableLatency && !spec.EnableBandwidth && !spec.EnablePacketLoss && len(spec.LinkOverrides) == 0 {
		return errors.New("enable a shared network effect or configure at least one directed zone link")
	}
	if spec.EnableLatency && (spec.LatencyMilliseconds < 1 || spec.LatencyMilliseconds > 60000 || spec.JitterMilliseconds < 0 || spec.JitterMilliseconds > spec.LatencyMilliseconds || spec.CorrelationPercent < 0 || spec.CorrelationPercent > 100) {
		return errors.New("latency, jitter or correlation is outside the supported range")
	}
	if spec.EnableBandwidth && (spec.BandwidthBytesPerSecond < 1 || spec.BandwidthBytesPerSecond > 1000000000000) {
		return errors.New("bandwidth must be between one and one trillion bytes per second")
	}
	if spec.EnablePacketLoss && (spec.PacketLossPercent < 0 || spec.PacketLossPercent > 100) {
		return errors.New("packet loss must be between zero and 100 percent")
	}
	if len(spec.LinkOverrides) > 225 {
		return errors.New("at most 225 directed link overrides are supported")
	}
	return nil
}

func discoverNodes(ctx context.Context, runner commandRunner, kubeconfig string, spec Spec) ([]node, []string, error) {
	args := []string{"get", "nodes"}
	if spec.NodeSelector != "" {
		args = append(args, "-l", spec.NodeSelector)
	}
	args = append(args, "-o", "json")
	raw, err := runner.Run(ctx, kubeconfig, nil, args...)
	if err != nil {
		return nil, nil, err
	}
	var list nodeList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, nil, err
	}
	nodes := make([]node, 0, len(list.Items))
	zones := map[string]bool{}
	for _, item := range list.Items {
		value := node{Name: item.Metadata.Name, Zone: strings.TrimSpace(item.Metadata.Labels[spec.NodeGroupLabel]), PodCIDR: strings.TrimSpace(item.Spec.PodCIDR)}
		for _, address := range item.Status.Addresses {
			if address.Type == "InternalIP" {
				value.InternalIP = strings.TrimSpace(address.Address)
			}
		}
		if value.Name == "" || value.Zone == "" || value.InternalIP == "" || value.PodCIDR == "" {
			return nil, nil, fmt.Errorf("node %s requires a name, %s label, InternalIP and PodCIDR", item.Metadata.Name, spec.NodeGroupLabel)
		}
		nodes = append(nodes, value)
		zones[value.Zone] = true
	}
	if len(nodes) < 2 || len(zones) < 2 {
		return nil, nil, errors.New("at least two selected nodes in two different zones are required")
	}
	zoneNames := make([]string, 0, len(zones))
	for zone := range zones {
		zoneNames = append(zoneNames, zone)
	}
	sort.Strings(zoneNames)
	sort.Slice(nodes, func(left, right int) bool { return nodes[left].Name < nodes[right].Name })
	return nodes, zoneNames, nil
}

func validateOverrides(overrides []LinkOverride, zones []string, spec Spec) error {
	available := map[string]bool{}
	for _, zone := range zones {
		available[zone] = true
	}
	seen := map[string]bool{}
	for _, override := range overrides {
		key := override.SourceZone + ">" + override.TargetZone
		if !available[override.SourceZone] || !available[override.TargetZone] || override.SourceZone == override.TargetZone || seen[key] {
			return fmt.Errorf("directed override %s is invalid or duplicated", key)
		}
		seen[key] = true
		link := effective(spec, override.SourceZone, override.TargetZone)
		if !link.EnableLatency && !link.EnableBandwidth && !link.EnablePacketLoss {
			return fmt.Errorf("directed override %s does not define a network effect", key)
		}
		if link.EnableLatency && (link.Latency < 1 || link.Latency > 60000 || link.Jitter < 0 || link.Jitter > link.Latency || link.Correlation < 0 || link.Correlation > 100) || link.EnableBandwidth && (link.Bandwidth < 1 || link.Bandwidth > 1000000000000) || link.EnablePacketLoss && (link.PacketLoss < 0 || link.PacketLoss > 100 || link.Correlation < 0 || link.Correlation > 100) {
			return fmt.Errorf("directed override %s has invalid values", key)
		}
	}
	return nil
}

func effective(spec Spec, source, target string) effectiveLink {
	value := effectiveLink{EnableLatency: spec.EnableLatency, EnableBandwidth: spec.EnableBandwidth, EnablePacketLoss: spec.EnablePacketLoss, Latency: spec.LatencyMilliseconds, Jitter: spec.JitterMilliseconds, Correlation: spec.CorrelationPercent, Bandwidth: spec.BandwidthBytesPerSecond, PacketLoss: spec.PacketLossPercent}
	for _, override := range spec.LinkOverrides {
		if override.SourceZone != source || override.TargetZone != target {
			continue
		}
		if override.LatencyMilliseconds != nil {
			value.Latency = *override.LatencyMilliseconds
			value.EnableLatency = true
		}
		if override.JitterMilliseconds != nil {
			value.Jitter = *override.JitterMilliseconds
			value.EnableLatency = true
		}
		if override.CorrelationPercent != nil {
			value.Correlation = *override.CorrelationPercent
		}
		if override.BandwidthBytesPerSecond != nil {
			value.Bandwidth = *override.BandwidthBytesPerSecond
			value.EnableBandwidth = true
		}
		if override.PacketLossPercent != nil {
			value.PacketLoss = *override.PacketLossPercent
			value.EnablePacketLoss = true
		}
	}
	return value
}

func linkEnabled(link effectiveLink) bool {
	return link.EnableLatency || link.EnableBandwidth || link.EnablePacketLoss
}

func profileDimensions(nodes []node, zones []string, spec Spec) (int, int) {
	activeZones := map[string]bool{}
	links := 0
	for _, source := range zones {
		for _, target := range zones {
			if source != target && linkEnabled(effective(spec, source, target)) {
				activeZones[source] = true
				links++
			}
		}
	}
	activeNodes := 0
	for _, item := range nodes {
		if activeZones[item.Zone] {
			activeNodes++
		}
	}
	return activeNodes, links
}

func networkManifest(application applicationDeployment, nodes []node, spec Spec) ([]byte, error) {
	byZone := map[string][]string{}
	for _, item := range nodes {
		network := item.PodCIDR
		if spec.HostNetwork {
			network = item.InternalIP + "/32"
		}
		byZone[item.Zone] = append(byZone[item.Zone], network)
	}
	resources := make([]map[string]any, 0, len(nodes))
	for _, source := range nodes {
		targetZones := make([]string, 0, len(byZone)-1)
		for zone := range byZone {
			if zone != source.Zone && linkEnabled(effective(spec, source.Zone, zone)) {
				targetZones = append(targetZones, zone)
			}
		}
		sort.Strings(targetZones)
		if len(targetZones) == 0 {
			continue
		}
		if len(targetZones) > 15 {
			return nil, fmt.Errorf("node %s reaches more than 15 target zones", source.Name)
		}
		name := resourceName("kubephos-net-" + application.Metadata.OwnershipMarker + "-" + source.Name)
		labels := map[string]string{"app.kubernetes.io/name": name, "app.kubernetes.io/managed-by": "kubephos", profileLabel: application.Metadata.OwnershipMarker, "kubephos.dev/source-zone": source.Zone}
		resources = append(resources, map[string]any{"apiVersion": "apps/v1", "kind": "DaemonSet", "metadata": map[string]any{"name": name, "namespace": chaosNS, "labels": labels}, "spec": map[string]any{"selector": map[string]any{"matchLabels": map[string]string{"app.kubernetes.io/name": name}}, "template": map[string]any{"metadata": map[string]any{"labels": labels}, "spec": map[string]any{"hostPID": true, "nodeSelector": map[string]string{"kubernetes.io/hostname": source.Name}, "tolerations": []map[string]string{{"operator": "Exists"}}, "terminationGracePeriodSeconds": 15, "containers": []map[string]any{{"name": "node-shaper", "image": chaosImage, "imagePullPolicy": "IfNotPresent", "securityContext": map[string]bool{"privileged": true}, "command": []string{"/bin/sh", "-ec"}, "args": []string{shapingScript(spec, source.Zone, targetZones, byZone)}, "readinessProbe": map[string]any{"exec": map[string]any{"command": []string{"/bin/sh", "-ec", "nsenter -t 1 -n -- tc qdisc show dev " + spec.NetworkInterface + " | grep -q 'qdisc netem'"}}, "initialDelaySeconds": 1, "periodSeconds": 2}}}}}}})
	}
	return json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": resources})
}

func shapingScript(spec Spec, sourceZone string, targetZones []string, networks map[string][]string) string {
	var value strings.Builder
	value.WriteString("host() { nsenter -t 1 -n -- \"$@\"; }\nowns=false\ncleanup() { if [ \"$owns\" = true ]; then host tc qdisc del dev " + spec.NetworkInterface + " root 2>/dev/null || true; fi; }\ntrap cleanup EXIT\ntrap 'exit 0' INT TERM\nexisting=\"$(host tc qdisc show dev " + spec.NetworkInterface + ")\"\ncase \"$existing\" in *\"qdisc noqueue 0:\"*) ;; *\"qdisc prio 1:\"*) owns=true; cleanup; owns=false ;; *) echo \"refusing to replace existing qdisc: $existing\" >&2; exit 1 ;; esac\nhost tc qdisc add dev " + spec.NetworkInterface + " root handle 1: prio bands " + fmt.Sprint(len(targetZones)+1) + " priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0\nowns=true\n")
	for index, zone := range targetZones {
		link := effective(spec, sourceZone, zone)
		band, handle := index+2, (index+2)*10
		value.WriteString("host tc qdisc add dev " + spec.NetworkInterface + fmt.Sprintf(" parent 1:%d handle %d: netem", band, handle))
		if link.EnableLatency {
			value.WriteString(fmt.Sprintf(" delay %dms", link.Latency))
			if link.Jitter > 0 {
				value.WriteString(fmt.Sprintf(" %dms %.4g%%", link.Jitter, link.Correlation))
			}
		}
		if link.EnableBandwidth {
			value.WriteString(fmt.Sprintf(" rate %dbps", link.Bandwidth))
		}
		if link.EnablePacketLoss {
			value.WriteString(fmt.Sprintf(" loss %.4g%% %.4g%%", link.PacketLoss, link.Correlation))
		}
		value.WriteByte('\n')
		for _, destination := range networks[zone] {
			value.WriteString(fmt.Sprintf("host tc filter add dev %s protocol ip parent 1:0 prio 3 u32 match ip dst %s flowid 1:%d\n", spec.NetworkInterface, destination, band))
		}
	}
	value.WriteString("while :; do sleep 3600 & wait $!; done\n")
	return value.String()
}

type shaperDaemonSetList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			Template struct {
				Spec struct {
					NodeSelector map[string]string `json:"nodeSelector"`
					Containers   []struct {
						Args []string `json:"args"`
					} `json:"containers"`
				} `json:"spec"`
			} `json:"template"`
		} `json:"spec"`
		Status struct {
			Desired int `json:"desiredNumberScheduled"`
			Ready   int `json:"numberReady"`
		} `json:"status"`
	} `json:"items"`
}

type shaperPodList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Spec struct {
			NodeName string `json:"nodeName"`
		} `json:"spec"`
		Status struct {
			Phase      string `json:"phase"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type trafficQdisc struct {
	Kind    string `json:"kind"`
	Handle  string `json:"handle"`
	Parent  string `json:"parent"`
	Options struct {
		Bands int `json:"bands"`
		Delay *struct {
			Delay       float64 `json:"delay"`
			Jitter      float64 `json:"jitter"`
			Correlation float64 `json:"correlation"`
		} `json:"delay"`
		Loss *struct {
			Loss        float64 `json:"loss"`
			Correlation float64 `json:"correlation"`
		} `json:"loss-random"`
		Rate *struct {
			Rate int64 `json:"rate"`
		} `json:"rate"`
	} `json:"options"`
}

func verifyAppliedProfile(ctx context.Context, runner commandRunner, kubeconfig string, application applicationDeployment, nodes []node, zones []string, spec Spec) error {
	byZone := map[string][]string{}
	for _, item := range nodes {
		network := item.PodCIDR
		if spec.HostNetwork {
			network = item.InternalIP + "/32"
		}
		byZone[item.Zone] = append(byZone[item.Zone], network)
	}
	expectedScripts := map[string]string{}
	expectedTargets := map[string][]string{}
	for _, source := range nodes {
		targets := make([]string, 0, len(zones)-1)
		for _, target := range zones {
			if source.Zone != target && linkEnabled(effective(spec, source.Zone, target)) {
				targets = append(targets, target)
			}
		}
		if len(targets) > 0 {
			expectedTargets[source.Name] = targets
			expectedScripts[source.Name] = shapingScript(spec, source.Zone, targets, byZone)
		}
	}
	selector := profileLabel + "=" + application.Metadata.OwnershipMarker
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "daemonset", "-n", chaosNS, "-l", selector, "-o", "json")
	if err != nil {
		return err
	}
	var daemonSets shaperDaemonSetList
	if err := json.Unmarshal([]byte(raw), &daemonSets); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, daemonSet := range daemonSets.Items {
		nodeName := daemonSet.Spec.Template.Spec.NodeSelector["kubernetes.io/hostname"]
		expected, ok := expectedScripts[nodeName]
		if !ok || seen[nodeName] || daemonSet.Status.Desired != 1 || daemonSet.Status.Ready != 1 || len(daemonSet.Spec.Template.Spec.Containers) != 1 || len(daemonSet.Spec.Template.Spec.Containers[0].Args) != 1 || daemonSet.Spec.Template.Spec.Containers[0].Args[0] != expected {
			return fmt.Errorf("daemonset %s does not match its declared node profile", daemonSet.Metadata.Name)
		}
		seen[nodeName] = true
	}
	if len(seen) != len(expectedScripts) {
		return fmt.Errorf("found %d exact daemonset profiles, expected %d", len(seen), len(expectedScripts))
	}
	raw, err = runner.Run(ctx, kubeconfig, nil, "get", "pods", "-n", chaosNS, "-l", selector, "-o", "json")
	if err != nil {
		return err
	}
	var pods shaperPodList
	if err := json.Unmarshal([]byte(raw), &pods); err != nil {
		return err
	}
	verified := map[string]bool{}
	for _, pod := range pods.Items {
		targets, ok := expectedTargets[pod.Spec.NodeName]
		if !ok || verified[pod.Spec.NodeName] || pod.Status.Phase != "Running" || !podReady(pod.Status.Conditions) {
			return fmt.Errorf("pod %s is not a unique ready shaper", pod.Metadata.Name)
		}
		qdiscRaw, err := runner.Run(ctx, kubeconfig, nil, "exec", "-n", chaosNS, pod.Metadata.Name, "--", "nsenter", "-t", "1", "-n", "--", "tc", "-j", "-d", "qdisc", "show", "dev", spec.NetworkInterface)
		if err != nil {
			return fmt.Errorf("inspect pod %s traffic control: %w", pod.Metadata.Name, err)
		}
		if err := verifyQdiscs([]byte(qdiscRaw), pod.Spec.NodeName, nodeZone(nodes, pod.Spec.NodeName), targets, spec); err != nil {
			return err
		}
		verified[pod.Spec.NodeName] = true
	}
	if len(verified) != len(expectedTargets) {
		return fmt.Errorf("verified %d kernel profiles, expected %d", len(verified), len(expectedTargets))
	}
	return nil
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

func verifyQdiscs(raw []byte, nodeName, sourceZone string, targetZones []string, spec Spec) error {
	var qdiscs []trafficQdisc
	if err := json.Unmarshal(raw, &qdiscs); err != nil {
		return fmt.Errorf("decode kernel profile on %s: %w", nodeName, err)
	}
	rootBands := 0
	children := map[string]trafficQdisc{}
	for _, qdisc := range qdiscs {
		if qdisc.Kind == "prio" && qdisc.Handle == "1:" {
			rootBands = qdisc.Options.Bands
		}
		if qdisc.Kind == "netem" {
			children[qdisc.Parent] = qdisc
		}
	}
	if rootBands != len(targetZones)+1 || len(children) != len(targetZones) {
		return fmt.Errorf("kernel profile topology on %s is incomplete", nodeName)
	}
	for index, target := range targetZones {
		link := effective(spec, sourceZone, target)
		qdisc, ok := children[fmt.Sprintf("1:%d", index+2)]
		if !ok || qdisc.Handle != fmt.Sprintf("%d:", (index+2)*10) {
			return fmt.Errorf("kernel profile on %s is missing link to %s", nodeName, target)
		}
		if link.EnableLatency != (qdisc.Options.Delay != nil) || link.EnableBandwidth != (qdisc.Options.Rate != nil) || link.EnablePacketLoss != (qdisc.Options.Loss != nil) {
			return fmt.Errorf("kernel effects on %s to %s do not match", nodeName, target)
		}
		if qdisc.Options.Delay != nil {
			expectedCorrelation := 0.0
			if link.Jitter > 0 {
				expectedCorrelation = link.Correlation / 100
			}
			if !near(qdisc.Options.Delay.Delay, float64(link.Latency)/1000) || !near(qdisc.Options.Delay.Jitter, float64(link.Jitter)/1000) || !near(qdisc.Options.Delay.Correlation, expectedCorrelation) {
				return fmt.Errorf("kernel latency on %s to %s does not match", nodeName, target)
			}
		}
		if qdisc.Options.Rate != nil && qdisc.Options.Rate.Rate != link.Bandwidth {
			return fmt.Errorf("kernel bandwidth on %s to %s does not match", nodeName, target)
		}
		if qdisc.Options.Loss != nil && (!near(qdisc.Options.Loss.Loss, link.PacketLoss/100) || !near(qdisc.Options.Loss.Correlation, link.Correlation/100)) {
			return fmt.Errorf("kernel packet loss on %s to %s does not match", nodeName, target)
		}
	}
	return nil
}

func near(left, right float64) bool {
	return math.Abs(left-right) < 0.000001
}

func nodeZone(nodes []node, name string) string {
	for _, item := range nodes {
		if item.Name == name {
			return item.Zone
		}
	}
	return ""
}

func profileCount(ctx context.Context, runner commandRunner, kubeconfig string, application applicationDeployment) (int, error) {
	raw, err := runner.Run(ctx, kubeconfig, nil, "get", "daemonset", "-n", chaosNS, "-l", profileLabel+"="+application.Metadata.OwnershipMarker, "-o", "json")
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
	name := strings.Trim(strings.TrimSpace(result.String()), "-")
	if len(name) <= 63 {
		return name
	}
	digest := sha256.Sum256([]byte(name))
	return strings.Trim(name[:50], "-") + "-" + hex.EncodeToString(digest[:])[:12]
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
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		return stdout.String(), fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}
