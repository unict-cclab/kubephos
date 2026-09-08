package managedobservability

import (
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
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID     = "io.kubephos.observability.managed"
	artifactAPI  = "artifacts.kubephos.dev/v1alpha1"
	chartVersion = "86.0.0"
	chartURL     = "https://github.com/prometheus-community/helm-charts/releases/download/kube-prometheus-stack-86.0.0/kube-prometheus-stack-86.0.0.tgz"
	chartDigest  = "aee1f7e4d82c484d9d9742c2f629a9e523bb9f23e6f5c4ac5f78128487fab41a"
	releaseName  = "kubephos-observability"
	namespace    = "kubephos-observability"
	ownershipKey = "kubephos.dev/ownership-marker"
	grafanaUser  = "admin"
)

var managedCRDs = []string{
	"alertmanagerconfigs.monitoring.coreos.com",
	"alertmanagers.monitoring.coreos.com",
	"podmonitors.monitoring.coreos.com",
	"probes.monitoring.coreos.com",
	"prometheusagents.monitoring.coreos.com",
	"prometheuses.monitoring.coreos.com",
	"prometheusrules.monitoring.coreos.com",
	"scrapeconfigs.monitoring.coreos.com",
	"servicemonitors.monitoring.coreos.com",
	"thanosrulers.monitoring.coreos.com",
}

type Plugin struct {
	Runner  clusterRunner
	Fetcher chartFetcher
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef      string `json:"clusterConnectionRef"`
	StorageClassCapabilityRef string `json:"storageClassCapabilityRef"`
	ScrapeInterval            string `json:"scrapeInterval"`
	Retention                 string `json:"retention"`
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

type storageClassCapability struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		ClusterServer        string `json:"clusterServer"`
		Provisioner          string `json:"provisioner"`
		DirectoryPermissions string `json:"directoryPermissions"`
		Expansion            bool   `json:"expansion"`
	} `json:"spec"`
}

type observabilityCapability struct {
	APIVersion string                          `json:"apiVersion"`
	Kind       string                          `json:"kind"`
	Metadata   observabilityCapabilityMetadata `json:"metadata"`
	Spec       observabilityCapabilitySpec     `json:"spec"`
}

type observabilityCapabilityMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type observabilityCapabilitySpec struct {
	ClusterServer  string            `json:"clusterServer"`
	Namespace      string            `json:"namespace"`
	ScrapeInterval string            `json:"scrapeInterval"`
	Retention      string            `json:"retention"`
	Endpoints      []serviceEndpoint `json:"endpoints"`
	MetricsAPI     string            `json:"metricsAPI"`
}

type serviceEndpoint struct {
	Name        string `json:"name"`
	ServiceName string `json:"serviceName"`
	Port        int    `json:"port"`
	Scheme      string `json:"scheme"`
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

type result struct {
	ObservabilityCapability observabilityCapability `json:"observabilityCapability"`
	GrafanaCredential       serviceCredential       `json:"grafanaCredential"`
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

type podList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Status struct {
			Phase      string `json:"phase"`
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
		} `json:"status"`
	} `json:"items"`
}

type clusterRunner interface {
	Kubectl(context.Context, string, []byte, ...string) (string, error)
	Helm(context.Context, string, []byte, []byte, ...string) (string, error)
}

type chartFetcher interface {
	Fetch(context.Context) ([]byte, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Managed observability", Version: "0.1.0",
		Description:     "Installs the release-managed metrics and dashboard profile on a Kubernetes cluster.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","storageClassCapabilityRef","scrapeInterval","retention"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"storageClassCapabilityRef":{"type":"string","title":"Persistent storage","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"StorageClassCapability","x-kubephos-artifact-version":"v1alpha1"},"scrapeInterval":{"type":"string","title":"Metrics interval","enum":["5s","10s","15s","30s"],"default":"15s"},"retention":{"type":"string","title":"Metrics retention","enum":["1d","7d","15d"],"default":"7d"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "StorageClassCapability", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ObservabilityCapability", Version: "v1alpha1"}, {Type: "ServiceCredential", Version: "v1alpha1"}},
		Capabilities:    []string{"observability.metrics.install", "observability.dashboards.install", "observability.preflight", "observability.cleanup", "lifecycle.cleanup"},
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
	if !strings.HasPrefix(spec.StorageClassCapabilityRef, "art_") || spec.StorageClassCapabilityRef == spec.ClusterConnectionRef {
		return invalid(report, "storageClassCapabilityRef", "Select verified persistent storage for the same cluster.")
	}
	if !oneOf(spec.ScrapeInterval, "5s", "10s", "15s", "30s") {
		return invalid(report, "scrapeInterval", "Select a supported metrics interval.")
	}
	if !oneOf(spec.Retention, "1d", "7d", "15d") {
		return invalid(report, "retention", "Select a supported metrics retention.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("KubePhos will install managed observability chart %s with persistent storage.", chartVersion)})
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
		ID: "install-managed-observability", Name: "Install and verify managed observability", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "storage-class-capability", Type: "StorageClassCapability", Version: "v1alpha1", ArtifactID: spec.StorageClassCapabilityRef},
		},
		Outputs: []domain.ArtifactOutput{
			{Name: "observability-capability", Type: "ObservabilityCapability", Version: "v1alpha1", MediaType: "application/json", Source: "/observabilityCapability"},
			{Name: "grafana-credential", Type: "ServiceCredential", Version: "v1alpha1", MediaType: "application/json", Source: "/grafanaCredential", Sensitive: true},
		},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, storage, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateArtifacts(cluster, storage, !step.Cleanup); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := log("info", "Validating Kubernetes API, permissions, persistent storage and managed resource ownership"); err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	authorized, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "auth", "can-i", "*", "*", "--all-namespaces")
	if err != nil || strings.TrimSpace(authorized) != "yes" {
		return unhealthy("The cluster connection does not have the required administrative permissions", "authorization", "denied"), nil
	}
	provisioner, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "storageclass", storage.Metadata.Name, "-o", "jsonpath={.provisioner}")
	if err != nil || strings.TrimSpace(provisioner) != storage.Spec.Provisioner {
		return unhealthy("The persistent storage capability is not installed on the target cluster", "storage", "unavailable"), nil
	}
	if step.Cleanup {
		found, err := cleanupOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Marker)
		if err != nil {
			return unhealthy(err.Error(), "ownership", "blocked"), nil
		}
		state := "absent"
		if found {
			state = "verified"
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Every present managed observability resource has verified ownership", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "ownership": state}}, nil
	}
	if err := installConflicts(ctx, runner, cluster.Spec.Kubeconfig); err != nil {
		return unhealthy(err.Error(), "resources", "conflict"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The cluster is ready for managed observability", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "storageClass": storage.Metadata.Name, "resources": "available"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, cluster, storage, err := resolve(step)
	if err != nil {
		return nil, err
	}
	password, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	if err := log("info", "Downloading and verifying the release-managed observability chart"); err != nil {
		return nil, err
	}
	chart, err := plugin.fetcher().Fetch(ctx)
	if err != nil {
		return nil, err
	}
	values, err := managedValues(input, storage.Metadata.Name, password)
	if err != nil {
		return nil, err
	}
	runner := plugin.runner()
	namespaceManifest, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace, "annotations": map[string]string{ownershipKey: input.Marker}}})
	if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, namespaceManifest, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("create managed namespace: %w", err)
	}
	if err := log("info", "Installing Prometheus, Grafana, exporters and managed dashboards"); err != nil {
		return nil, err
	}
	if err := installWithDiagnostics(ctx, runner, cluster.Spec.Kubeconfig, chart, values, log); err != nil {
		return nil, fmt.Errorf("install managed observability: %w", err)
	}
	for _, crd := range managedCRDs {
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "annotate", "--overwrite", "crd/"+crd, ownershipKey+"="+input.Marker); err != nil {
			return nil, fmt.Errorf("mark managed CRD %s: %w", crd, err)
		}
	}
	endpoints, err := discoverEndpoints(ctx, runner, cluster.Spec.Kubeconfig)
	if err != nil {
		return nil, err
	}
	grafana := endpointByName(endpoints, "grafana")
	value := result{
		ObservabilityCapability: observabilityCapability{APIVersion: artifactAPI, Kind: "ObservabilityCapability", Metadata: observabilityCapabilityMetadata{Name: "managed-observability", Version: chartVersion}, Spec: observabilityCapabilitySpec{ClusterServer: cluster.Spec.Server, Namespace: namespace, ScrapeInterval: input.ScrapeInterval, Retention: input.Retention, Endpoints: endpoints, MetricsAPI: "prometheus-v1"}},
		GrafanaCredential:       serviceCredential{APIVersion: artifactAPI, Kind: "ServiceCredential", Metadata: serviceCredentialMetadata{Name: "grafana-admin", Role: "management"}, Spec: serviceCredentialSpec{Namespace: namespace, Service: grafana.ServiceName, Username: grafanaUser, Password: password}},
	}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, _, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if step.Cleanup {
		if err := verifyCleanup(ctx, runner, cluster.Spec.Kubeconfig); err != nil {
			return unhealthy(err.Error(), "resources", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed observability resources were removed", Checks: map[string]string{"release": "absent", "namespace": "absent", "crds": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateResult(value, input, cluster); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, nil, nil, "status", releaseName, "--namespace", namespace); err != nil {
		return unhealthy("Managed Helm release is unhealthy: "+err.Error(), "release", "unhealthy"), nil
	}
	if err := verifyPods(ctx, runner, cluster.Spec.Kubeconfig); err != nil {
		return unhealthy(err.Error(), "pods", "unhealthy"), nil
	}
	if err := verifyPVCs(ctx, runner, cluster.Spec.Kubeconfig); err != nil {
		return unhealthy(err.Error(), "persistence", "unhealthy"), nil
	}
	for _, endpoint := range value.ObservabilityCapability.Spec.Endpoints {
		path := healthProxyPath(endpoint)
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw="+path); err != nil {
			return unhealthy(endpoint.Name+" health endpoint failed: "+err.Error(), endpoint.Name, "unhealthy"), nil
		}
	}
	if err := verifyCRDOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Marker); err != nil {
		return unhealthy(err.Error(), "ownership", "invalid"), nil
	}
	if err := log("info", "Prometheus API, Grafana API, exporters, dashboards and persistent volumes are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed observability is ready", Checks: map[string]string{"chart": chartVersion, "prometheus": "ready", "grafana": "ready", "pods": "ready", "persistence": "bound", "scrapeInterval": input.ScrapeInterval, "retention": input.Retention}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, cluster, _, err := resolve(step)
	if err != nil {
		return err
	}
	runner := plugin.runner()
	found, err := cleanupOwnership(ctx, runner, cluster.Spec.Kubeconfig, input.Marker)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := log("warning", "Removing only the managed observability release, CRDs and namespace"); err != nil {
		return err
	}
	if _, err := runner.Helm(ctx, cluster.Spec.Kubeconfig, nil, nil, "uninstall", releaseName, "--namespace", namespace, "--wait", "--timeout", "10m", "--ignore-not-found"); err != nil {
		return err
	}
	for _, crd := range managedCRDs {
		if _, err := runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "delete", "crd", crd, "--ignore-not-found", "--wait=true", "--timeout=2m"); err != nil {
			return err
		}
	}
	_, err = runner.Kubectl(ctx, cluster.Spec.Kubeconfig, nil, "delete", "namespace", namespace, "--ignore-not-found", "--wait=true", "--timeout=5m")
	return err
}

func resolve(step domain.PlanStep) (stepInput, clusterConnection, storageClassCapability, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, clusterConnection{}, storageClassCapability{}, err
	}
	clusterValue, ok := step.ResolvedInputs["cluster-connection"]
	if !ok {
		return stepInput{}, clusterConnection{}, storageClassCapability{}, errors.New("verified ClusterConnection input is unavailable")
	}
	storageValue, ok := step.ResolvedInputs["storage-class-capability"]
	if !ok {
		return stepInput{}, clusterConnection{}, storageClassCapability{}, errors.New("verified StorageClassCapability input is unavailable")
	}
	var cluster clusterConnection
	if err := json.Unmarshal(clusterValue.Value, &cluster); err != nil {
		return stepInput{}, clusterConnection{}, storageClassCapability{}, err
	}
	var storage storageClassCapability
	if err := json.Unmarshal(storageValue.Value, &storage); err != nil {
		return stepInput{}, clusterConnection{}, storageClassCapability{}, err
	}
	return input, cluster, storage, nil
}

func validateArtifacts(cluster clusterConnection, storage storageClassCapability, requireWritableDirectories bool) error {
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
	if storage.APIVersion != artifactAPI || storage.Kind != "StorageClassCapability" || storage.Metadata.Name == "" || storage.Metadata.Version == "" || storage.Spec.ClusterServer != cluster.Spec.Server || storage.Spec.Provisioner == "" || !storage.Spec.Expansion {
		return errors.New("persistent storage capability does not match the target cluster")
	}
	if requireWritableDirectories && storage.Spec.DirectoryPermissions != "0777" {
		return errors.New("persistent storage does not guarantee writable workload directories")
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
		if item.Name == clusterName && item.Cluster.Server == server {
			clusterOK = true
		}
	}
	userOK := false
	for _, item := range config.Users {
		if item.Name != userName {
			continue
		}
		cert, certErr := base64.StdEncoding.DecodeString(item.User.ClientCertificateData)
		key, keyErr := base64.StdEncoding.DecodeString(item.User.ClientKeyData)
		userOK = certErr == nil && keyErr == nil && len(cert) > 0 && len(key) > 0
	}
	if !clusterOK || !userOK {
		return errors.New("cluster kubeconfig target or client identity is invalid")
	}
	return nil
}

func managedValues(input stepInput, storageClass, password string) ([]byte, error) {
	values := map[string]any{
		"alertmanager":          map[string]any{"enabled": false},
		"kubeControllerManager": map[string]any{"enabled": false},
		"kubeScheduler":         map[string]any{"enabled": false},
		"kubeProxy":             map[string]any{"enabled": false},
		"grafana": map[string]any{
			"adminUser": grafanaUser, "adminPassword": password,
			"initChownData": map[string]any{"enabled": false},
			"persistence":   map[string]any{"enabled": true, "storageClassName": storageClass, "accessModes": []string{"ReadWriteOnce"}, "size": "2Gi"},
		},
		"prometheus": map[string]any{"prometheusSpec": map[string]any{
			"scrapeInterval": input.ScrapeInterval, "evaluationInterval": input.ScrapeInterval, "retention": input.Retention,
			"storageSpec": map[string]any{"volumeClaimTemplate": map[string]any{"spec": map[string]any{"storageClassName": storageClass, "accessModes": []string{"ReadWriteOnce"}, "resources": map[string]any{"requests": map[string]string{"storage": "10Gi"}}}}},
		}},
	}
	return yaml.Marshal(values)
}

type helmResult struct {
	value string
	err   error
}

func installWithDiagnostics(ctx context.Context, runner clusterRunner, kubeconfig string, chart, values []byte, log plugins.Logger) error {
	result := make(chan helmResult, 1)
	go func() {
		value, err := runner.Helm(ctx, kubeconfig, chart, values, "upgrade", "--install", releaseName, "@chart", "--namespace", namespace, "--values", "@values", "--wait", "--wait-for-jobs", "--timeout", "15m", "--atomic")
		result <- helmResult{value: value, err: err}
	}()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case outcome := <-result:
			return outcome.err
		case <-ticker.C:
			diagnosticContext, cancel := context.WithTimeout(ctx, 15*time.Second)
			status, err := runner.Kubectl(diagnosticContext, kubeconfig, nil, "get", "pods,pvc", "-n", namespace, "-o", "wide")
			cancel()
			if err == nil && strings.TrimSpace(status) != "" {
				_ = log("info", "Managed observability rollout diagnostic:\n"+strings.TrimSpace(status))
			} else if err != nil {
				_ = log("warning", "Managed observability rollout diagnostic failed: "+err.Error())
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func discoverEndpoints(ctx context.Context, runner clusterRunner, kubeconfig string) ([]serviceEndpoint, error) {
	raw, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "services", "-n", namespace, "-o", "json")
	if err != nil {
		return nil, err
	}
	var list serviceList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, err
	}
	values := make([]serviceEndpoint, 0, 2)
	for _, item := range list.Items {
		name := ""
		component := item.Metadata.Labels["app.kubernetes.io/name"]
		if component == "grafana" || strings.Contains(item.Metadata.Name, "grafana") {
			name = "grafana"
		}
		if component == "prometheus" || strings.HasSuffix(item.Metadata.Name, "-prometheus") {
			name = "prometheus"
		}
		if name == "" || endpointByName(values, name).Name != "" {
			continue
		}
		port := 0
		for _, candidate := range item.Spec.Ports {
			if candidate.Name == "http-web" || candidate.Name == "service" || candidate.Name == "http" || len(item.Spec.Ports) == 1 {
				port = candidate.Port
				break
			}
		}
		if port > 0 {
			values = append(values, serviceEndpoint{Name: name, ServiceName: item.Metadata.Name, Port: port, Scheme: "http"})
		}
	}
	if endpointByName(values, "prometheus").Name == "" || endpointByName(values, "grafana").Name == "" {
		return nil, errors.New("managed Prometheus or Grafana service endpoint is unavailable")
	}
	return values, nil
}

func endpointByName(values []serviceEndpoint, name string) serviceEndpoint {
	for _, value := range values {
		if value.Name == name {
			return value
		}
	}
	return serviceEndpoint{}
}

func healthProxyPath(endpoint serviceEndpoint) string {
	path := "/-/ready"
	if endpoint.Name == "grafana" {
		path = "/api/health"
	}
	return fmt.Sprintf("/api/v1/namespaces/%s/services/%s:%s:%d/proxy%s", namespace, endpoint.Scheme, endpoint.ServiceName, endpoint.Port, path)
}

func validateResult(value result, input stepInput, cluster clusterConnection) error {
	capability := value.ObservabilityCapability
	credential := value.GrafanaCredential
	if capability.APIVersion != artifactAPI || capability.Kind != "ObservabilityCapability" || capability.Metadata.Name != "managed-observability" || capability.Metadata.Version != chartVersion || capability.Spec.ClusterServer != cluster.Spec.Server || capability.Spec.Namespace != namespace || capability.Spec.ScrapeInterval != input.ScrapeInterval || capability.Spec.Retention != input.Retention || capability.Spec.MetricsAPI != "prometheus-v1" || len(capability.Spec.Endpoints) != 2 {
		return errors.New("observability capability does not match the validated plan")
	}
	if credential.APIVersion != artifactAPI || credential.Kind != "ServiceCredential" || credential.Metadata.Name != "grafana-admin" || credential.Metadata.Role != "management" || credential.Spec.Namespace != namespace || credential.Spec.Service != endpointByName(capability.Spec.Endpoints, "grafana").ServiceName || credential.Spec.Username != grafanaUser || credential.Spec.Password == "" {
		return errors.New("Grafana credential does not match the managed endpoint")
	}
	return nil
}

func verifyPods(ctx context.Context, runner clusterRunner, kubeconfig string) error {
	raw, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "pods", "-n", namespace, "-o", "json")
	if err != nil {
		return err
	}
	var list podList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return err
	}
	running := 0
	for _, item := range list.Items {
		if item.Status.Phase == "Succeeded" {
			continue
		}
		ready := false
		for _, condition := range item.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				ready = true
			}
		}
		if item.Status.Phase != "Running" || !ready {
			return fmt.Errorf("managed pod %s is not ready", item.Metadata.Name)
		}
		running++
	}
	if running < 5 {
		return errors.New("managed observability pod inventory is incomplete")
	}
	return nil
}

func verifyPVCs(ctx context.Context, runner clusterRunner, kubeconfig string) error {
	raw, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "pvc", "-n", namespace, "-o", "jsonpath={range .items[*]}{.status.phase}{\"\\n\"}{end}")
	if err != nil {
		return err
	}
	values := strings.Fields(raw)
	if len(values) < 2 {
		return errors.New("managed observability persistent volumes are incomplete")
	}
	for _, value := range values {
		if value != "Bound" {
			return errors.New("at least one managed observability volume is not bound")
		}
	}
	return nil
}

func installConflicts(ctx context.Context, runner clusterRunner, kubeconfig string) error {
	if _, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "namespace", namespace); err == nil {
		return errors.New("managed observability namespace already exists")
	}
	for _, crd := range managedCRDs {
		if _, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "crd", crd); err == nil {
			return fmt.Errorf("required CRD %s already exists", crd)
		}
	}
	return nil
}

func cleanupOwnership(ctx context.Context, runner clusterRunner, kubeconfig, marker string) (bool, error) {
	found := false
	namespaceOwned := false
	value, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "namespace", namespace, "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}")
	if err == nil {
		found = true
		if strings.TrimSpace(value) != marker {
			return false, errors.New("managed observability namespace is not owned by this operation")
		}
		namespaceOwned = true
	}
	for _, crd := range managedCRDs {
		value, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "crd", crd, "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}")
		if err != nil {
			continue
		}
		found = true
		actual := strings.TrimSpace(value)
		if actual != marker && !(namespaceOwned && actual == "") {
			return false, fmt.Errorf("CRD %s is not owned by this operation", crd)
		}
	}
	if found && !namespaceOwned {
		return false, errors.New("managed observability CRDs exist without the owned namespace")
	}
	return found, nil
}

func verifyCRDOwnership(ctx context.Context, runner clusterRunner, kubeconfig, marker string) error {
	for _, crd := range managedCRDs {
		value, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "crd", crd, "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}")
		if err != nil || strings.TrimSpace(value) != marker {
			return fmt.Errorf("managed CRD %s failed ownership verification", crd)
		}
	}
	return nil
}

func verifyCleanup(ctx context.Context, runner clusterRunner, kubeconfig string) error {
	if _, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "namespace", namespace); err == nil {
		return errors.New("managed observability namespace remains after cleanup")
	}
	for _, crd := range managedCRDs {
		if _, err := runner.Kubectl(ctx, kubeconfig, nil, "get", "crd", crd); err == nil {
			return fmt.Errorf("managed CRD %s remains after cleanup", crd)
		}
	}
	return nil
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
		command.Stdin = strings.NewReader(string(stdin))
	}
	return commandOutput(command, "kubectl "+strings.Join(args, " "))
}

func (localRunner) Helm(ctx context.Context, kubeconfig string, chart, values []byte, args ...string) (string, error) {
	directory, err := os.MkdirTemp("", "kubephos-helm-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(directory)
	kubeconfigPath := directory + "/kubeconfig"
	if err := os.WriteFile(kubeconfigPath, []byte(kubeconfig), 0600); err != nil {
		return "", err
	}
	chartPath := directory + "/chart.tgz"
	valuesPath := directory + "/values.yaml"
	if chart != nil {
		if err := os.WriteFile(chartPath, chart, 0600); err != nil {
			return "", err
		}
	}
	if values != nil {
		if err := os.WriteFile(valuesPath, values, 0600); err != nil {
			return "", err
		}
	}
	resolved := make([]string, len(args))
	for index, arg := range args {
		resolved[index] = arg
		if arg == "@chart" {
			resolved[index] = chartPath
		}
		if arg == "@values" {
			resolved[index] = valuesPath
		}
	}
	resolved = append(resolved, "--kubeconfig", kubeconfigPath)
	return commandOutput(exec.CommandContext(ctx, "helm", resolved...), "helm "+strings.Join(args, " "))
}

func commandOutput(command *exec.Cmd, display string) (string, error) {
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %w: %s", display, err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

type httpChartFetcher struct {
	client *http.Client
}

func (fetcher httpChartFetcher) Fetch(ctx context.Context) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, chartURL, nil)
	if err != nil {
		return nil, err
	}
	response, err := fetcher.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("observability chart returned HTTP status %d", response.StatusCode)
	}
	value, err := io.ReadAll(io.LimitReader(response.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(value)
	if hex.EncodeToString(digest[:]) != chartDigest {
		return nil, errors.New("observability chart digest does not match the managed release")
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

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}
