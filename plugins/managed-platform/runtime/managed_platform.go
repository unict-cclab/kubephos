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
	pluginID               = "io.kubephos.platform.managed"
	artifactAPI            = "artifacts.kubephos.dev/v1alpha1"
	istioVersion           = "1.30.0"
	chaosVersion           = "2.8.3"
	cpaVersion             = "v1.4.2"
	mentatVersion          = "v0.3.3"
	clusterLensVersion     = "v1.2.2"
	lokiVersion            = "6.41.1"
	alloyVersion           = "1.4.0"
	jaegerVersion          = "1.53"
	kialiVersion           = "2.8.0"
	istioNamespace         = "istio-system"
	chaosNamespace         = "chaos-mesh"
	systemNamespace        = "kubephos-system"
	observabilityNamespace = "kubephos-observability"
	istioBaseRelease       = "kubephos-istio-base"
	istiodRelease          = "kubephos-istiod"
	chaosRelease           = "kubephos-chaos"
	cpaRelease             = "kubephos-cpa-operator"
	lokiRelease            = "kubephos-loki"
	alloyRelease           = "kubephos-alloy"
	kialiRelease           = "kubephos-kiali"
	chaosDashboardPort     = 32300
	clusterLensPort        = 32088
	lokiPort               = 32099
	jaegerPort             = 30002
	kialiPort              = 30001
	ownershipKey           = "kubephos.dev/ownership-marker"
)

var managedCharts = []chartSpec{
	{ID: "istio-base", URL: "https://istio-release.storage.googleapis.com/charts/base-1.30.0.tgz", Digest: "aef833a87ed0022114c5759fe3ec6af7862c2a224d4c60e40ebaaef825e1e39d"},
	{ID: "istiod", URL: "https://istio-release.storage.googleapis.com/charts/istiod-1.30.0.tgz", Digest: "7a34c7688da34107841e9585b226b94357769749f82502fdc4fb43a6bc3db320"},
	{ID: "chaos-mesh", URL: "https://charts.chaos-mesh.org/chaos-mesh-2.8.3.tgz", Digest: "c96a2d6490c1fbb0693e18b04e2ab6f60ca45046c64e98ca774f72ac1eaf2a47"},
	{ID: "cpa-operator", URL: "https://github.com/jthomperoo/custom-pod-autoscaler-operator/releases/download/v1.4.2/custom-pod-autoscaler-operator-v1.4.2.tgz", Digest: "59f673d9a37f6667ba359bed3e1e8529b6f22470a2751d2279c7c1cd97e07b3c"},
	{ID: "loki", URL: "https://github.com/grafana/helm-charts/releases/download/helm-loki-6.41.1/loki-6.41.1.tgz", Digest: "27aa43d3a2792b79d94920feedf6a661ec7a17dedfb3bbd23d6e8e54b4c2d187"},
	{ID: "alloy", URL: "https://github.com/grafana/helm-charts/releases/download/alloy-1.4.0/alloy-1.4.0.tgz", Digest: "b1adbb6344301b8353c9d5d39bb5de196e3735441ca1742913d60922a96bffcb"},
	{ID: "kiali", URL: "https://kiali.org/helm-charts/kiali-server-2.8.0.tgz", Digest: "c4f1aa6530b75a3fed5950e1c8ab6a5c92733720a58dcdc123462401e468251b"},
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
		RegistryHost string `json:"registryHost,omitempty"`
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
		ID: pluginID, Name: "Managed Kubernetes platform", Version: "0.2.3",
		Description:     "Installs and verifies the managed service mesh, chaos, autoscaling and cluster support baseline.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ManagedPlatformCapability", Version: "v1alpha1"}},
		Capabilities:    []string{"platform.managed.install", "platform.managed.preflight", "platform.managed.cleanup", "lifecycle.cleanup", "lifecycle.cleanup.compatible"},
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
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("KubePhos will install managed Istio %s, Chaos Mesh %s and Custom Pod Autoscaler operator %s.", istioVersion, chaosVersion, cpaVersion)})
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
	if step.Cleanup {
		for _, namespace := range []string{istioNamespace, chaosNamespace, systemNamespace} {
			owned, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
			if err != nil || present && !owned {
				return unhealthy("Managed namespace ownership cannot be verified", namespace, "invalid"), nil
			}
		}
		if err := verifySupportOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Marker); err != nil {
			return unhealthy(err.Error(), "supportComponents", "invalid"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed platform resources are safe to remove", Checks: map[string]string{"api": "ready", "ownership": "verified"}}, nil
	}
	for _, namespace := range []string{istioNamespace, chaosNamespace, systemNamespace} {
		_, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
		if err != nil {
			return unhealthy(err.Error(), namespace, "unknown"), nil
		}
		if present {
			return unhealthy("Namespace "+namespace+" already exists", namespace, "conflict"), nil
		}
	}
	for _, resource := range []string{"clusterrole/kubephos-mentat", "clusterrolebinding/kubephos-mentat", "clusterrole/kubephos-cluster-lens", "clusterrolebinding/kubephos-cluster-lens", "daemonset/mentat", "deployment/cluster-lens", "service/cluster-lens"} {
		args := []string{"get", resource}
		if strings.HasPrefix(resource, "daemonset/") || strings.HasPrefix(resource, "deployment/") || strings.HasPrefix(resource, "service/") {
			args = append(args, "-n", "kubephos-observability")
		}
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, args...); err == nil {
			return unhealthy("Managed support resource "+resource+" already exists", resource, "conflict"), nil
		}
	}
	for _, release := range []struct{ name, namespace string }{{lokiRelease, "kubephos-observability"}, {alloyRelease, "kubephos-observability"}, {kialiRelease, istioNamespace}} {
		if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, nil, nil, "status", release.name, "--namespace", release.namespace); err == nil {
			return unhealthy("Managed release "+release.name+" already exists", release.name, "conflict"), nil
		}
	}
	for _, resource := range []string{"deployment/jaeger", "service/jaeger", "persistentvolumeclaim/jaeger-badger"} {
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", resource, "-n", "kubephos-observability"); err == nil {
			return unhealthy("Managed tracing resource "+resource+" already exists", resource, "conflict"), nil
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
	if err := log("info", "Downloading and verifying managed platform charts in parallel"); err != nil {
		return nil, err
	}
	charts, err := plugin.fetchCharts(ctx)
	if err != nil {
		return nil, err
	}
	runner := plugin.runner()
	for _, namespace := range []string{istioNamespace, chaosNamespace, systemNamespace} {
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
	istiodValues := []byte("replicaCount: 1\nnodeSelector:\n  kubephos.dev/role: management\nmeshConfig:\n  enableTracing: true\n  extensionProviders:\n  - name: jaeger\n    opentelemetry:\n      service: jaeger.kubephos-observability.svc.cluster.local\n      port: 4317\n")
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
	if err := log("info", "Installing the managed Custom Pod Autoscaler operator"); err != nil {
		return nil, err
	}
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, charts["cpa-operator"], []byte("mode: cluster\n"), "upgrade", "--install", cpaRelease, "@chart", "--namespace", systemNamespace, "--values", "@values", "--wait", "--timeout", "10m", "--atomic"); err != nil {
		return nil, fmt.Errorf("install Custom Pod Autoscaler operator: %w", err)
	}
	patch := []byte(`{"spec":{"template":{"spec":{"nodeSelector":{"kubephos.dev/role":"management"}}}}}`)
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, cpaPlacementArguments(patch)...); err != nil {
		return nil, fmt.Errorf("place Custom Pod Autoscaler operator: %w", err)
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/custom-pod-autoscaler-operator", "-n", systemNamespace, "--timeout=5m"); err != nil {
		return nil, fmt.Errorf("Custom Pod Autoscaler operator is not ready: %w", err)
	}
	support, err := supportManifest(input.Marker, cluster.Spec.RegistryHost)
	if err != nil {
		return nil, err
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, support, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return nil, fmt.Errorf("managed support components failed admission: %w", err)
	}
	if err := log("info", "Installing managed Mentat network measurements and Cluster Lens"); err != nil {
		return nil, err
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, support, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("install managed support components: %w", err)
	}
	for _, workload := range []string{"daemonset/mentat", "deployment/cluster-lens"} {
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", workload, "-n", "kubephos-observability", "--timeout=5m"); err != nil {
			return nil, fmt.Errorf("managed support component %s is not ready: %w", workload, err)
		}
	}
	if err := log("info", "Installing managed logs, Kubernetes event collection and distributed tracing"); err != nil {
		return nil, err
	}
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, charts["loki"], lokiValues(), "upgrade", "--install", lokiRelease, "@chart", "--namespace", "kubephos-observability", "--values", "@values", "--wait", "--timeout", "20m", "--atomic"); err != nil {
		return nil, fmt.Errorf("install Loki: %w", err)
	}
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, charts["alloy"], alloyValues(), "upgrade", "--install", alloyRelease, "@chart", "--namespace", "kubephos-observability", "--values", "@values", "--wait", "--timeout", "10m", "--atomic"); err != nil {
		return nil, fmt.Errorf("install Alloy: %w", err)
	}
	extended := extendedObservabilityManifest(input.Marker)
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, extended, "apply", "--dry-run=server", "-f", "-"); err != nil {
		return nil, fmt.Errorf("managed tracing failed admission: %w", err)
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, extended, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("install Jaeger tracing: %w", err)
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/jaeger", "-n", "kubephos-observability", "--timeout=5m"); err != nil {
		return nil, fmt.Errorf("Jaeger is not ready: %w", err)
	}
	prometheus, err := discoverPrometheusService(ctx, runner, cluster.Spec.Kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("discover managed Prometheus for Kiali: %w", err)
	}
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, charts["kiali"], kialiValues(prometheus), "upgrade", "--install", kialiRelease, "@chart", "--namespace", istioNamespace, "--values", "@values", "--wait", "--timeout", "10m", "--atomic"); err != nil {
		return nil, fmt.Errorf("install Kiali: %w", err)
	}
	value := result{ManagedPlatformCapability: managedPlatformCapability{
		APIVersion: artifactAPI, Kind: "ManagedPlatformCapability", Metadata: managedPlatformCapabilityMetadata{Name: "managed-platform", Version: istioVersion + "+chaos." + chaosVersion + "+cpa." + cpaVersion + "+mentat." + mentatVersion + "+lens." + clusterLensVersion},
		Spec: managedPlatformCapabilitySpec{ClusterServer: cluster.Spec.Server, Components: []managedComponent{{Name: "istio", Version: istioVersion, Namespace: istioNamespace, Status: "ready"}, {Name: "chaos-mesh", Version: chaosVersion, Namespace: chaosNamespace, Status: "ready"}, {Name: "custom-pod-autoscaler-operator", Version: cpaVersion, Namespace: systemNamespace, Status: "ready"}, {Name: "mentat", Version: mentatVersion, Namespace: "kubephos-observability", Status: "ready"}, {Name: "cluster-lens", Version: clusterLensVersion, Namespace: "kubephos-observability", Status: "ready"}, {Name: "loki", Version: lokiVersion, Namespace: "kubephos-observability", Status: "ready"}, {Name: "alloy", Version: alloyVersion, Namespace: "kubephos-observability", Status: "ready"}, {Name: "jaeger", Version: jaegerVersion, Namespace: "kubephos-observability", Status: "ready"}, {Name: "kiali", Version: kialiVersion, Namespace: istioNamespace, Status: "ready"}}, Endpoints: []managedEndpoint{{Name: "chaos-dashboard", Namespace: chaosNamespace, Service: "chaos-dashboard", Scheme: "http", NodePort: chaosDashboardPort}, {Name: "cluster-lens", Namespace: "kubephos-observability", Service: "cluster-lens", Scheme: "http", NodePort: clusterLensPort}, {Name: "loki", Namespace: "kubephos-observability", Service: "kubephos-loki-gateway", Scheme: "http", NodePort: lokiPort}, {Name: "jaeger", Namespace: "kubephos-observability", Service: "jaeger", Scheme: "http", NodePort: jaegerPort}, {Name: "kiali", Namespace: istioNamespace, Service: "kiali", Scheme: "http", NodePort: kialiPort}}},
	}}
	return json.Marshal(value)
}

func cpaPlacementArguments(patch []byte) []string {
	return []string{"patch", "deployment", "custom-pod-autoscaler-operator", "-n", systemNamespace, "--type=merge", "--patch", string(patch)}
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if step.Cleanup {
		for _, namespace := range []string{istioNamespace, chaosNamespace, systemNamespace} {
			_, present, err := namespaceOwnership(ctx, runner, cluster.Spec.Kubeconfig, namespace, input.Marker)
			if err != nil || present {
				return unhealthy("Managed namespace remains after cleanup", namespace, "present"), nil
			}
		}
		for _, resource := range []string{"clusterrole/kubephos-mentat", "clusterrolebinding/kubephos-mentat", "clusterrole/kubephos-cluster-lens", "clusterrolebinding/kubephos-cluster-lens"} {
			if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", resource); err == nil {
				return unhealthy("Managed support resource remains after cleanup", resource, "present"), nil
			}
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed platform resources were removed", Checks: map[string]string{"istio": "absent", "chaos": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	capability := value.ManagedPlatformCapability
	if capability.APIVersion != artifactAPI || capability.Kind != "ManagedPlatformCapability" || capability.Spec.ClusterServer != cluster.Spec.Server || len(capability.Spec.Components) != 9 || len(capability.Spec.Endpoints) != 5 {
		return unhealthy("Managed platform capability is invalid", "artifact", "invalid"), nil
	}
	for _, release := range []struct{ name, namespace string }{{istioBaseRelease, istioNamespace}, {istiodRelease, istioNamespace}, {chaosRelease, chaosNamespace}, {cpaRelease, systemNamespace}, {lokiRelease, "kubephos-observability"}, {alloyRelease, "kubephos-observability"}, {kialiRelease, istioNamespace}} {
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
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/custom-pod-autoscaler-operator", "-n", systemNamespace, "--timeout=5m"); err != nil {
		return unhealthy("Custom Pod Autoscaler operator is not ready: "+err.Error(), "customPodAutoscalerOperator", "unhealthy"), nil
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "customresourcedefinition", "custompodautoscalers.custompodautoscaler.com"); err != nil {
		return unhealthy("Custom Pod Autoscaler CRD is unavailable", "customPodAutoscalerCRD", "unhealthy"), nil
	}
	for _, workload := range []string{"daemonset/mentat", "deployment/cluster-lens"} {
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", workload, "-n", "kubephos-observability", "--timeout=5m"); err != nil {
			return unhealthy("Managed support component is not ready: "+err.Error(), workload, "unhealthy"), nil
		}
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/jaeger", "-n", "kubephos-observability", "--timeout=5m"); err != nil {
		return unhealthy("Jaeger is not ready: "+err.Error(), "jaeger", "unhealthy"), nil
	}
	if err := verifySupportOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Marker); err != nil {
		return unhealthy(err.Error(), "supportOwnership", "invalid"), nil
	}
	prometheus, err := discoverPrometheusService(ctx, runner, cluster.Spec.Kubeconfig)
	if err != nil {
		return unhealthy("Managed Prometheus discovery failed: "+err.Error(), "prometheusDiscovery", "unhealthy"), nil
	}
	prometheusReadyPath := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:%d/proxy/-/ready", prometheus.Namespace, prometheus.Name, prometheus.Port)
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw="+prometheusReadyPath); err != nil {
		return unhealthy("Managed Prometheus endpoint is not ready: "+err.Error(), "prometheusEndpoint", "unhealthy"), nil
	}
	kialiConfig, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "configmap", "kiali", "-n", istioNamespace, "-o", "jsonpath={.data.config\\.yaml}")
	if err != nil {
		return unhealthy("Kiali configuration is unavailable: "+err.Error(), "kialiPrometheus", "unhealthy"), nil
	}
	if err := validateKialiPrometheusConfig(kialiConfig, prometheus); err != nil {
		return unhealthy(err.Error(), "kialiPrometheus", "invalid"), nil
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "exec", "deployment/kiali", "-n", istioNamespace, "--", "getent", "hosts", prometheus.Hostname()); err != nil {
		return unhealthy("Kiali cannot resolve the managed Prometheus service: "+err.Error(), "kialiPrometheusDNS", "unhealthy"), nil
	}
	jaegerPath := "/api/v1/namespaces/kubephos-observability/services/http:jaeger:16686/proxy/api/services"
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw="+jaegerPath); err != nil {
		return unhealthy("Managed Jaeger query endpoint is unavailable: "+err.Error(), "jaegerEndpoint", "unhealthy"), nil
	}
	latencyNodes, bandwidthNodes, err := waitForNetworkAnnotations(ctx, runner, cluster.Spec.Kubeconfig, 90*time.Second)
	if err != nil {
		return unhealthy(err.Error(), "clusterLensNetwork", "unhealthy"), nil
	}
	for _, endpoint := range capability.Spec.Endpoints {
		ports, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "service", endpoint.Service, "-n", endpoint.Namespace, "-o", "jsonpath={.spec.ports[*].nodePort}")
		if err != nil || !containsString(strings.Fields(ports), fmt.Sprint(endpoint.NodePort)) {
			return unhealthy(endpoint.Name+" NodePort is unavailable", endpoint.Name, "unhealthy"), nil
		}
	}
	if err := log("info", "Istio control plane, chaos daemon coverage and chaos dashboard endpoint are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed platform baseline is ready", Checks: map[string]string{"istio": istioVersion, "chaos": chaosVersion, "customPodAutoscalerOperator": cpaVersion, "mentat": mentatVersion, "clusterLens": clusterLensVersion, "clusterLensLatencyNodes": fmt.Sprint(latencyNodes), "clusterLensBandwidthNodes": fmt.Sprint(bandwidthNodes), "loki": lokiVersion, "alloy": alloyVersion, "jaeger": jaegerVersion, "kiali": kialiVersion, "kialiPrometheus": prometheus.Name}}, nil
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

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
	if manifest, err := supportManifest(input.Marker, cluster.Spec.RegistryHost); err != nil {
		return err
	} else if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, manifest, "delete", "-f", "-", "--ignore-not-found", "--wait=true", "--timeout=5m"); err != nil {
		return err
	}
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, extendedObservabilityManifest(input.Marker), "delete", "-f", "-", "--ignore-not-found", "--wait=true", "--timeout=5m"); err != nil {
		return err
	}
	for _, release := range []struct{ name, namespace string }{{kialiRelease, istioNamespace}, {alloyRelease, "kubephos-observability"}, {lokiRelease, "kubephos-observability"}, {cpaRelease, systemNamespace}, {chaosRelease, chaosNamespace}, {istiodRelease, istioNamespace}, {istioBaseRelease, istioNamespace}} {
		if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, nil, nil, "uninstall", release.name, "--namespace", release.namespace, "--wait", "--timeout", "10m", "--ignore-not-found"); err != nil {
			return err
		}
	}
	for _, namespace := range []string{systemNamespace, chaosNamespace, istioNamespace} {
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "delete", "namespace", namespace, "--ignore-not-found", "--wait=true", "--timeout=5m"); err != nil {
			return err
		}
	}
	return nil
}

func supportManifest(marker, registryHost string) ([]byte, error) {
	return []byte(fmt.Sprintf(supportManifestTemplate, marker, marker, marker, marker, mentatVersion, marker, marker, marker, marker, marker, clusterLensImage(registryHost), marker, clusterLensPort)), nil
}

func clusterLensImage(registryHost string) string {
	if registryHost == "" {
		return "ghcr.io/unict-cclab/cluster-lens:v1.0.0"
	}
	return registryHost + "/kubephos-dev/cluster-lens:" + clusterLensVersion
}

func validManagedRegistryHost(host string) bool {
	parsed, err := url.Parse("https://" + host)
	return err == nil && parsed.Hostname() != "" && parsed.Path == "" && parsed.RawQuery == "" && parsed.Fragment == "" && parsed.User == nil
}

const supportManifestTemplate = `apiVersion: v1
kind: ServiceAccount
metadata:
  name: mentat
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubephos-mentat
  annotations: {kubephos.dev/ownership-marker: %q}
rules:
- apiGroups: [""]
  resources: [pods, nodes]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubephos-mentat
  annotations: {kubephos.dev/ownership-marker: %q}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: kubephos-mentat}
subjects: [{kind: ServiceAccount, name: mentat, namespace: kubephos-observability}]
---
apiVersion: apps/v1
kind: DaemonSet
metadata:
  name: mentat
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  selector: {matchLabels: {app.kubernetes.io/name: mentat}}
  template:
    metadata: {labels: {app: mentat, app.kubernetes.io/name: mentat, app.kubernetes.io/managed-by: kubephos}}
    spec:
      serviceAccountName: mentat
      tolerations: [{operator: Exists}]
      containers:
      - name: mentat
        image: ghcr.io/unict-cclab/mentat:%s
        imagePullPolicy: IfNotPresent
        ports: [{name: metrics, containerPort: 2112}, {name: bandwidth, containerPort: 2113, protocol: TCP}]
        env:
        - {name: SLEEP_SECONDS, value: "5"}
        - {name: NODE_NAME, valueFrom: {fieldRef: {fieldPath: spec.nodeName}}}
        - {name: POD_NAMESPACE, valueFrom: {fieldRef: {fieldPath: metadata.namespace}}}
        - {name: PING_ATTEMPTS, value: "5"}
        - {name: PING_TIMEOUT_SECONDS, value: "1"}
        - {name: BANDWIDTH_PORT, value: "2113"}
        - {name: BANDWIDTH_BYTES, value: "262144"}
        - {name: BANDWIDTH_INTERVAL_SECONDS, value: "90"}
        - {name: BANDWIDTH_JITTER_SECONDS, value: "30"}
        - {name: BANDWIDTH_TIMEOUT_SECONDS, value: "30"}
---
apiVersion: monitoring.coreos.com/v1
kind: PodMonitor
metadata:
  name: mentat
  namespace: kubephos-observability
  labels: {release: kubephos-observability}
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  selector: {matchLabels: {app.kubernetes.io/name: mentat}}
  podMetricsEndpoints: [{port: metrics}]
---
apiVersion: v1
kind: ServiceAccount
metadata:
  name: cluster-lens
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: kubephos-cluster-lens
  annotations: {kubephos.dev/ownership-marker: %q}
rules:
- apiGroups: [""]
  resources: [nodes, pods]
  verbs: [get, list, watch]
- apiGroups: [apps]
  resources: [deployments, replicasets, daemonsets, statefulsets]
  verbs: [get, list, watch]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: kubephos-cluster-lens
  annotations: {kubephos.dev/ownership-marker: %q}
roleRef: {apiGroup: rbac.authorization.k8s.io, kind: ClusterRole, name: kubephos-cluster-lens}
subjects: [{kind: ServiceAccount, name: cluster-lens, namespace: kubephos-observability}]
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: cluster-lens
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  replicas: 1
  selector: {matchLabels: {app.kubernetes.io/name: cluster-lens}}
  template:
    metadata: {labels: {app.kubernetes.io/name: cluster-lens, app.kubernetes.io/managed-by: kubephos}}
    spec:
      serviceAccountName: cluster-lens
      nodeSelector: {kubephos.dev/role: management}
      containers:
      - name: cluster-lens
        image: %s
        imagePullPolicy: IfNotPresent
        ports: [{name: http, containerPort: 8088}]
        env: [{name: CLUSTER_LENS_ADDR, value: ":8088"}, {name: CLUSTER_LENS_REFRESH, value: "2s"}, {name: CLUSTER_LENS_CONTEXT, value: in-cluster}]
---
apiVersion: v1
kind: Service
metadata:
  name: cluster-lens
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  type: NodePort
  selector: {app.kubernetes.io/name: cluster-lens}
  ports: [{name: http, port: 8088, targetPort: http, nodePort: %d}]
`

func lokiValues() []byte {
	return []byte(fmt.Sprintf(`deploymentMode: SingleBinary
loki:
  auth_enabled: false
  commonConfig:
    replication_factor: 1
  storage:
    type: filesystem
  schemaConfig:
    configs:
    - from: "2024-04-01"
      store: tsdb
      object_store: filesystem
      schema: v13
      index: {prefix: loki_index_, period: 24h}
  limits_config:
    retention_period: 168h
  compactor:
    retention_enabled: true
    delete_request_store: filesystem
singleBinary:
  replicas: 1
  nodeSelector: {kubephos.dev/role: management}
  persistence: {enabled: true, storageClass: shared-storage, size: 10Gi}
read: {replicas: 0}
write: {replicas: 0}
backend: {replicas: 0}
gateway:
  enabled: true
  nodeSelector: {kubephos.dev/role: management}
  service: {type: NodePort, nodePort: %d}
chunksCache: {enabled: false}
resultsCache: {enabled: false}
lokiCanary: {enabled: false}
test: {enabled: false}
minio: {enabled: false}
`, lokiPort))
}

func alloyValues() []byte {
	return []byte(`controller:
  type: deployment
  replicas: 1
  nodeSelector: {kubephos.dev/role: management}
alloy:
  configMap:
    create: true
    content: |-
      discovery.kubernetes "pods" {
        role = "pod"
      }
      discovery.relabel "pod_logs" {
        targets = discovery.kubernetes.pods.targets
        rule {
          source_labels = ["__meta_kubernetes_namespace"]
          target_label = "namespace"
        }
        rule {
          source_labels = ["__meta_kubernetes_pod_name"]
          target_label = "pod"
        }
        rule {
          source_labels = ["__meta_kubernetes_pod_container_name"]
          target_label = "container"
        }
      }
      loki.source.kubernetes "pod_logs" {
        targets = discovery.relabel.pod_logs.output
        forward_to = [loki.write.local.receiver]
      }
      loki.source.kubernetes_events "events" {
        job_name = "kubernetes-events"
        log_format = "json"
        forward_to = [loki.write.local.receiver]
      }
      loki.write "local" {
        endpoint {
          url = "http://kubephos-loki-gateway.kubephos-observability.svc.cluster.local/loki/api/v1/push"
        }
      }
`)
}

func kialiValues(prometheus prometheusService) []byte {
	return []byte(fmt.Sprintf(`auth:
  strategy: anonymous
deployment:
  image_version: v%s
  image_pull_policy: IfNotPresent
  node_selector: {kubephos.dev/role: management}
  service_type: NodePort
external_services:
  grafana:
    enabled: true
    internal_url: http://kubephos-observability-grafana.kubephos-observability.svc.cluster.local:80
  prometheus:
    url: %s
  tracing:
    enabled: true
    provider: jaeger
    internal_url: http://jaeger.kubephos-observability.svc.cluster.local:16686
    use_grpc: false
istio_namespace: istio-system
server:
  node_port: %d
  web_root: /kiali
`, kialiVersion, prometheus.URL(), kialiPort))
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

func validateKialiPrometheusConfig(raw string, prometheus prometheusService) error {
	var config struct {
		ExternalServices struct {
			Grafana struct {
				Enabled     bool   `yaml:"enabled"`
				InternalURL string `yaml:"internal_url"`
			} `yaml:"grafana"`
			Prometheus struct {
				URL string `yaml:"url"`
			} `yaml:"prometheus"`
			Tracing struct {
				Enabled     bool   `yaml:"enabled"`
				Provider    string `yaml:"provider"`
				InternalURL string `yaml:"internal_url"`
				UseGRPC     bool   `yaml:"use_grpc"`
			} `yaml:"tracing"`
		} `yaml:"external_services"`
	}
	if err := yaml.Unmarshal([]byte(raw), &config); err != nil {
		return err
	}
	if config.ExternalServices.Prometheus.URL != prometheus.URL() {
		return fmt.Errorf("Kiali Prometheus URL is %q, expected %q", config.ExternalServices.Prometheus.URL, prometheus.URL())
	}
	if !config.ExternalServices.Grafana.Enabled || config.ExternalServices.Grafana.InternalURL != "http://kubephos-observability-grafana.kubephos-observability.svc.cluster.local:80" {
		return errors.New("Kiali managed Grafana integration is invalid")
	}
	if !config.ExternalServices.Tracing.Enabled || config.ExternalServices.Tracing.Provider != "jaeger" || config.ExternalServices.Tracing.InternalURL != "http://jaeger.kubephos-observability.svc.cluster.local:16686" || config.ExternalServices.Tracing.UseGRPC {
		return errors.New("Kiali managed Jaeger integration is invalid")
	}
	return nil
}

func networkAnnotationCoverage(raw string) (int, int, error) {
	var nodes nodeList
	if err := json.Unmarshal([]byte(raw), &nodes); err != nil {
		return 0, 0, err
	}
	latency := 0
	bandwidth := 0
	for _, node := range nodes.Items {
		hasLatency := false
		hasBandwidth := false
		for name := range node.Metadata.Annotations {
			hasLatency = hasLatency || strings.HasPrefix(name, "network-latency.")
			hasBandwidth = hasBandwidth || strings.HasPrefix(name, "network-bandwidth.")
		}
		if hasLatency {
			latency++
		}
		if hasBandwidth {
			bandwidth++
		}
	}
	return latency, bandwidth, nil
}

func waitForNetworkAnnotations(ctx context.Context, runner clusterRunner, kubeconfig string, timeout time.Duration) (int, int, error) {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		raw, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "nodes", "-o", "json")
		if err == nil {
			latency, bandwidth, parseErr := networkAnnotationCoverage(raw)
			if parseErr == nil && latency > 0 && bandwidth > 0 {
				return latency, bandwidth, nil
			}
		}
		select {
		case <-ctx.Done():
			return 0, 0, ctx.Err()
		case <-deadline.C:
			return 0, 0, errors.New("Mentat metrics did not reach the node annotations consumed by Cluster Lens")
		case <-ticker.C:
		}
	}
}

func extendedObservabilityManifest(marker string) []byte {
	return []byte(fmt.Sprintf(extendedObservabilityManifestTemplate, marker, jaegerPort, marker, marker, jaegerVersion, marker))
}

const extendedObservabilityManifestTemplate = `apiVersion: v1
kind: Service
metadata:
  name: jaeger
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  type: NodePort
  selector: {app.kubernetes.io/name: jaeger}
  ports:
  - {name: otlp-grpc, port: 4317, targetPort: 4317}
  - {name: otlp-http, port: 4318, targetPort: 4318}
  - {name: ui, port: 16686, targetPort: 16686, nodePort: %d}
---
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: jaeger-badger
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  accessModes: [ReadWriteOnce]
  storageClassName: shared-storage
  resources: {requests: {storage: 5Gi}}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: jaeger
  namespace: kubephos-observability
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  replicas: 1
  selector: {matchLabels: {app.kubernetes.io/name: jaeger}}
  template:
    metadata: {labels: {app.kubernetes.io/name: jaeger, app.kubernetes.io/managed-by: kubephos}}
    spec:
      nodeSelector: {kubephos.dev/role: management}
      containers:
      - name: jaeger
        image: jaegertracing/all-in-one:%s
        imagePullPolicy: IfNotPresent
        env:
        - {name: COLLECTOR_OTLP_ENABLED, value: "true"}
        - {name: SPAN_STORAGE_TYPE, value: badger}
        - {name: BADGER_EPHEMERAL, value: "false"}
        - {name: BADGER_DIRECTORY_VALUE, value: /badger/data}
        - {name: BADGER_DIRECTORY_KEY, value: /badger/key}
        ports: [{containerPort: 4317}, {containerPort: 4318}, {containerPort: 16686}]
        volumeMounts: [{name: data, mountPath: /badger}]
      volumes: [{name: data, persistentVolumeClaim: {claimName: jaeger-badger}}]
---
apiVersion: telemetry.istio.io/v1
kind: Telemetry
metadata:
  name: kubephos-tracing
  namespace: istio-system
  annotations: {kubephos.dev/ownership-marker: %q}
spec:
  tracing:
  - providers: [{name: jaeger}]
    randomSamplingPercentage: 100
`

func verifySupportOwnership(ctx context.Context, runner clusterRunner, kubeconfig, marker string) error {
	resources := []struct{ name, namespace string }{
		{"clusterrole/kubephos-mentat", ""}, {"clusterrolebinding/kubephos-mentat", ""}, {"clusterrole/kubephos-cluster-lens", ""}, {"clusterrolebinding/kubephos-cluster-lens", ""},
		{"serviceaccount/mentat", "kubephos-observability"}, {"daemonset/mentat", "kubephos-observability"}, {"podmonitor/mentat", "kubephos-observability"},
		{"serviceaccount/cluster-lens", "kubephos-observability"}, {"deployment/cluster-lens", "kubephos-observability"}, {"service/cluster-lens", "kubephos-observability"},
		{"deployment/jaeger", "kubephos-observability"}, {"service/jaeger", "kubephos-observability"}, {"persistentvolumeclaim/jaeger-badger", "kubephos-observability"}, {"telemetry/kubephos-tracing", istioNamespace},
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
		if strings.TrimSpace(value) != "" && strings.TrimSpace(value) != marker {
			return fmt.Errorf("managed support resource %s is not owned by this operation", resource.name)
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
