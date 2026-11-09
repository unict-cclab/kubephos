package managedplatform

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

// Istio ambient mode is installed on a cluster the first time an experiment asks
// for it and then stays in place: later ambient runs find it ready, and sidecar
// experiments are unaffected because ztunnel only serves enrolled namespaces.
const (
	ambientPluginID  = "io.kubephos.platform.istio-ambient"
	istioCNIRelease  = "kubephos-istio-cni"
	ztunnelRelease   = "kubephos-ztunnel"
	gatewayAPICRD    = "gateways.gateway.networking.k8s.io"
	ambientIstiodEnv = `jsonpath={.spec.template.spec.containers[?(@.name=="discovery")].env[?(@.name=="PILOT_ENABLE_AMBIENT")].value}`
)

var ambientCharts = []chartSpec{
	{ID: "istio-cni", URL: "https://istio-release.storage.googleapis.com/charts/cni-1.30.0.tgz", Digest: "a758c61b07514aec6de9e91a04a54ab665e701eb1c0e4ec357f6ab62d51a3d5e"},
	{ID: "ztunnel", URL: "https://istio-release.storage.googleapis.com/charts/ztunnel-1.30.0.tgz", Digest: "41a591e342d4e4cffe29b9795884e4182fc55b301eb67466ad2b336c575457d2"},
	// Waypoints are Gateway API resources; the CRDs are applied only when the cluster has none.
	{ID: "gateway-api", URL: "https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.3.0/standard-install.yaml", Digest: "78796d5c51450fc55d8dc8092ba8137f8c807982d7508d7875d5c537a24082b9"},
}

// The k3s platform profile points istio-cni at the k3s CNI directories.
var istioCNIValues = []byte("profile: ambient\nglobal:\n  platform: k3s\n  variant: \"\"\n")

// AmbientPlugin enables Istio ambient mode on a cluster that runs the managed Istio control plane.
type AmbientPlugin struct {
	Runner  clusterRunner
	Fetcher chartFetcher
	// AgentTimeout bounds the wait for each node agent; zero means ten minutes.
	AgentTimeout time.Duration
}

func (plugin AmbientPlugin) platform() Plugin {
	return Plugin{Runner: plugin.Runner, Fetcher: plugin.Fetcher}
}

func (AmbientPlugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: ambientPluginID, Name: "Istio ambient mode", Version: "0.1.0",
		Description:    "Installs istio-cni and ztunnel and switches the managed Istio control plane to the ambient profile, once per cluster.",
		Schema:         json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"}}}`),
		ArtifactInputs: []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}},
		// Experiments run cleanup after every run; for ambient mode it is a deliberate no-op.
		Capabilities: []string{"istio.ambient.install", "lifecycle.cleanup"},
		Permissions:  []string{"cluster.admin", "network.http"},
	}
}

func (AmbientPlugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
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
	return report
}

func (AmbientPlugin) Plan(ctx context.Context, raw json.RawMessage) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec Spec
	if err := json.Unmarshal(raw, &spec); err != nil {
		return domain.Plan{}, err
	}
	input, err := json.Marshal(stepInput{Spec: spec})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: ambientPluginID, Steps: []domain.PlanStep{{
		ID: "enable-istio-ambient", Name: "Enable Istio ambient mode", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef}},
	}}}, nil
}

func (plugin AmbientPlugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	_, cluster, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateCluster(cluster); err != nil {
		return unhealthy(err.Error(), "cluster", "invalid"), nil
	}
	if cluster.Spec.Networking == "cilium" {
		return unhealthy("Istio ambient mode needs the managed Istio control plane, which Cilium clusters do not run", "istio", "unavailable"), nil
	}
	if step.Cleanup {
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Istio ambient mode stays installed for later runs", Checks: map[string]string{"ambient": "kept"}}, nil
	}
	runner := plugin.platform().runner()
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "deployment", "istiod", "-n", istioNamespace); err != nil {
		return unhealthy("The managed Istio control plane is not installed: "+err.Error(), "istio", "missing"), nil
	}
	if err := log("info", "The managed Istio control plane is available for ambient mode"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Cluster can run Istio ambient mode", Checks: map[string]string{"istio": "ready"}}, nil
}

// Execute is idempotent: every step either finds its target in place or upgrades it to the managed version.
func (plugin AmbientPlugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	_, cluster, err := resolve(step)
	if err != nil {
		return nil, err
	}
	platform := plugin.platform()
	if ready, err := ambientReady(ctx, platform.runner(), cluster.Spec.Kubeconfig); err != nil {
		return nil, err
	} else if ready {
		return json.RawMessage(`{}`), log("info", "Istio ambient mode is already enabled on this cluster")
	}
	if err := log("info", "Downloading and verifying the Istio ambient charts"); err != nil {
		return nil, err
	}
	charts, err := platform.fetchChartSet(ctx, ambientChartSet())
	if err != nil {
		return nil, err
	}
	runner := platform.runner()
	kubeconfig := cluster.Spec.Kubeconfig
	if _, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "customresourcedefinition", gatewayAPICRD); err != nil {
		if err := log("info", "Installing the Gateway API CRDs used by waypoints"); err != nil {
			return nil, err
		}
		if _, err := runner.Kubectl(ctx, kubeconfig, charts["gateway-api"], "apply", "--server-side", "-f", "-"); err != nil {
			return nil, fmt.Errorf("install Gateway API CRDs: %w", err)
		}
	}
	if err := log("info", "Switching the Istio control plane to the ambient profile"); err != nil {
		return nil, err
	}
	if _, err := runner.Helm(ctx, kubeconfig, charts["istiod"], istiodValues(true), "upgrade", "--install", istiodRelease, "@chart", "--namespace", istioNamespace, "--values", "@values", "--wait", "--timeout", "10m", "--atomic"); err != nil {
		return nil, fmt.Errorf("switch the Istio control plane to the ambient profile: %w", err)
	}
	agents := []nodeAgent{
		{"Installing the Istio CNI node agent", "istio-cni", istioCNIRelease, "istio-cni-node", istioCNIValues},
		{"Installing ztunnel on every node", "ztunnel", ztunnelRelease, "ztunnel", []byte("{}\n")},
	}
	for _, agent := range agents {
		if err := log("info", agent.message); err != nil {
			return nil, err
		}
		if err := plugin.installNodeAgent(ctx, runner, kubeconfig, charts[agent.chart], agent); err != nil {
			return nil, err
		}
	}
	return json.RawMessage(`{}`), nil
}

// nodeAgent is a DaemonSet that ambient mode runs on every node.
type nodeAgent struct {
	message, chart, release, daemonSet string
	values                             []byte
}

// Image pulls that fail this way never recover by waiting.
var imagePullFailures = []string{"ErrImagePull", "ImagePullBackOff", "InvalidImageName"}

// installNodeAgent installs a node agent and waits for it, stopping as soon as a
// pod cannot pull its image instead of waiting for the whole timeout. A failed
// install is removed, so the next run starts clean.
func (plugin AmbientPlugin) installNodeAgent(ctx context.Context, runner clusterRunner, kubeconfig string, chart []byte, agent nodeAgent) error {
	if _, err := runner.Helm(ctx, kubeconfig, chart, agent.values, "upgrade", "--install", agent.release, "@chart", "--namespace", istioNamespace, "--values", "@values"); err != nil {
		return fmt.Errorf("install %s: %w", agent.daemonSet, err)
	}
	remove := func() {
		_, _ = runner.Helm(ctx, kubeconfig, nil, nil, "uninstall", agent.release, "--namespace", istioNamespace, "--wait", "--timeout", "5m")
	}
	deadline := time.Now().Add(plugin.agentTimeout())
	for {
		reason, err := imagePullFailure(ctx, runner, kubeconfig, agent.daemonSet)
		if err != nil {
			return err
		}
		if reason != "" {
			remove()
			return fmt.Errorf("%s cannot pull its image (%s): the managed Harbor does not mirror registry.istio.io/release/install-cni and ztunnel yet. Run kubephos release harbor-profile-sync <harbor-resource-id> once, then retry", agent.daemonSet, reason)
		}
		if _, err := runner.Kubectl(ctx, kubeconfig, nil, "rollout", "status", "daemonset/"+agent.daemonSet, "-n", istioNamespace, "--timeout=15s"); err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			remove()
			return err
		}
		if time.Now().After(deadline) {
			remove()
			return fmt.Errorf("%s is not ready on every node after %s", agent.daemonSet, plugin.agentTimeout())
		}
	}
}

// imagePullFailure returns the first image pull failure among the DaemonSet's pods,
// which are named after it.
func imagePullFailure(ctx context.Context, runner clusterRunner, kubeconfig, daemonSet string) (string, error) {
	output, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "pods", "-n", istioNamespace, "-o", `jsonpath={range .items[*]}{.metadata.name}{" "}{.status.initContainerStatuses[*].state.waiting.reason}{" "}{.status.containerStatuses[*].state.waiting.reason}{"\n"}{end}`)
	if err != nil {
		return "", fmt.Errorf("read %s pods: %w", daemonSet, err)
	}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || !strings.HasPrefix(fields[0], daemonSet+"-") {
			continue
		}
		for _, reason := range fields[1:] {
			if slices.Contains(imagePullFailures, reason) {
				return reason, nil
			}
		}
	}
	return "", nil
}

func (plugin AmbientPlugin) agentTimeout() time.Duration {
	if plugin.AgentTimeout > 0 {
		return plugin.AgentTimeout
	}
	return 10 * time.Minute
}

func (plugin AmbientPlugin) Verify(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	_, cluster, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.platform().runner()
	for _, daemonSet := range []string{"ztunnel", "istio-cni-node"} {
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "daemonset/"+daemonSet, "-n", istioNamespace, "--timeout=5m"); err != nil {
			return unhealthy(daemonSet+" is not ready on every node: "+err.Error(), daemonSet, "unhealthy"), nil
		}
	}
	if ready, err := ambientReady(ctx, runner, cluster.Spec.Kubeconfig); err != nil || !ready {
		return unhealthy("The Istio control plane does not run the ambient profile", "istiod", "sidecar-only"), nil
	}
	if err := log("info", "ztunnel, istio-cni and the ambient control plane are ready"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Istio ambient mode is enabled", Checks: map[string]string{"ztunnel": "ready", "istioCNI": "ready", "istiod": "ambient"}}, nil
}

// Cleanup keeps ambient mode installed; the managed platform removes it with Istio.
func (AmbientPlugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

// ambientReady reports whether istiod runs the ambient profile and both node agents exist.
func ambientReady(ctx context.Context, runner clusterRunner, kubeconfig string) (bool, error) {
	value, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "deployment", "istiod", "-n", istioNamespace, "-o", ambientIstiodEnv)
	if err != nil {
		return false, fmt.Errorf("read the Istio control plane: %w", err)
	}
	if strings.TrimSpace(value) != "true" {
		return false, nil
	}
	for _, daemonSet := range []string{"ztunnel", "istio-cni-node"} {
		if _, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "daemonset", daemonSet, "-n", istioNamespace); err != nil {
			return false, nil
		}
	}
	return true, nil
}

// ambientChartSet adds the managed istiod chart, which ambient mode upgrades in place.
func ambientChartSet() []chartSpec {
	set := append([]chartSpec{}, ambientCharts...)
	for _, chart := range managedCharts {
		if chart.ID == "istiod" {
			set = append(set, chart)
		}
	}
	return set
}
