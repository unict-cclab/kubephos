package nfscsi

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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID      = "io.kubephos.storage.kubernetes.nfs-csi"
	artifactAPI   = "artifacts.kubephos.dev/v1alpha1"
	driverVersion = "v4.13.4"
	driverCommit  = "f09798c0f1e7d1ae3b32dc8dfef67f6e848e8761"
	provisioner   = "nfs.csi.k8s.io"
)

var driverManifests = []manifestSource{
	{Name: "rbac", URL: "https://raw.githubusercontent.com/kubernetes-csi/csi-driver-nfs/" + driverCommit + "/deploy/v4.13.4/rbac-csi-nfs.yaml", Digest: "312de689d07627330d8280e50d7b51357e0b5f5ca0d8aa75794aec460ee4cff0"},
	{Name: "driver", URL: "https://raw.githubusercontent.com/kubernetes-csi/csi-driver-nfs/" + driverCommit + "/deploy/v4.13.4/csi-nfs-driverinfo.yaml", Digest: "fa2bba4674821b3317d1e016f3c9d54abe04fcdc6fed3fce9e58994c8311c4a0"},
	{Name: "controller", URL: "https://raw.githubusercontent.com/kubernetes-csi/csi-driver-nfs/" + driverCommit + "/deploy/v4.13.4/csi-nfs-controller.yaml", Digest: "dcc1aa5188c93bea3a3239175ce4e9ac577702818c4fb9fa611a0039aa5ab255"},
	{Name: "node", URL: "https://raw.githubusercontent.com/kubernetes-csi/csi-driver-nfs/" + driverCommit + "/deploy/v4.13.4/csi-nfs-node.yaml", Digest: "b66104b44d4569c926e11af5c6c3858fbc38fd16099b206fc4a0dc0f7fcd94d2"},
}

type Plugin struct {
	Runner  commandRunner
	Fetcher manifestFetcher
	Probe   endpointProbe
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	ClusterConnectionRef     string `json:"clusterConnectionRef"`
	SharedStorageEndpointRef string `json:"sharedStorageEndpointRef"`
	StorageClassName         string `json:"storageClassName"`
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

type sharedStorageEndpoint struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"metadata"`
	Spec struct {
		Protocol     string   `json:"protocol"`
		Server       string   `json:"server"`
		ExportPath   string   `json:"exportPath"`
		ClientCIDR   string   `json:"clientCIDR"`
		MountOptions []string `json:"mountOptions"`
	} `json:"spec"`
}

type storageClassCapability struct {
	APIVersion string                         `json:"apiVersion"`
	Kind       string                         `json:"kind"`
	Metadata   storageClassCapabilityMetadata `json:"metadata"`
	Spec       storageClassCapabilitySpec     `json:"spec"`
}

type storageClassCapabilityMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type storageClassCapabilitySpec struct {
	ClusterServer        string   `json:"clusterServer"`
	Provisioner          string   `json:"provisioner"`
	Protocol             string   `json:"protocol"`
	Server               string   `json:"server"`
	ExportPath           string   `json:"exportPath"`
	DirectoryPermissions string   `json:"directoryPermissions"`
	ReclaimPolicy        string   `json:"reclaimPolicy"`
	BindingMode          string   `json:"bindingMode"`
	Expansion            bool     `json:"expansion"`
	AccessModes          []string `json:"accessModes"`
	MountOptions         []string `json:"mountOptions"`
}

type result struct {
	StorageClassCapability storageClassCapability `json:"storageClassCapability"`
}

type manifestSource struct {
	Name   string
	URL    string
	Digest string
}

type commandRunner interface {
	Run(context.Context, string, []byte, ...string) (string, error)
}

type manifestFetcher interface {
	Fetch(context.Context, manifestSource) ([]byte, error)
}

type endpointProbe interface {
	Check(context.Context, string, string) error
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Kubernetes shared storage", Version: "0.2.0",
		Description:     "Attaches validated shared storage to a Kubernetes cluster.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["clusterConnectionRef","sharedStorageEndpointRef","storageClassName"],"properties":{"clusterConnectionRef":{"type":"string","title":"Kubernetes cluster","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"ClusterConnection","x-kubephos-artifact-version":"v1alpha1"},"sharedStorageEndpointRef":{"type":"string","title":"Shared storage","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"SharedStorageEndpoint","x-kubephos-artifact-version":"v1alpha1"},"storageClassName":{"type":"string","title":"Storage class name","pattern":"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$","maxLength":63,"default":"shared-storage"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "SharedStorageEndpoint", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "StorageClassCapability", Version: "v1alpha1"}},
		Capabilities:    []string{"storage.kubernetes.attach", "storage.kubernetes.preflight", "storage.kubernetes.cleanup", "lifecycle.cleanup", "lifecycle.cleanup.compatible"},
		Permissions:     []string{"cluster.admin", "network.http", "network.tcp"},
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
	if !strings.HasPrefix(spec.SharedStorageEndpointRef, "art_") || spec.SharedStorageEndpointRef == spec.ClusterConnectionRef {
		return invalid(report, "sharedStorageEndpointRef", "Select a verified shared-storage endpoint.")
	}
	if !validName(spec.StorageClassName) {
		return invalid(report, "storageClassName", "Storage class name must be a valid lowercase Kubernetes name with at most 63 characters.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will validate the cluster, storage endpoint, permissions, CSI rollout and dynamic provisioning."})
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
	input, err := json.Marshal(stepInput{Spec: spec, Marker: hex.EncodeToString(markerBytes)})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "attach-shared-storage", Name: "Attach and verify Kubernetes shared storage", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", ArtifactID: spec.ClusterConnectionRef},
			{Name: "shared-storage-endpoint", Type: "SharedStorageEndpoint", Version: "v1alpha1", ArtifactID: spec.SharedStorageEndpointRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "storage-class-capability", Type: "StorageClassCapability", Version: "v1alpha1", MediaType: "application/json", Source: "/storageClassCapability"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, storage, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateArtifacts(cluster, storage); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := plugin.probe().Check(ctx, storage.Spec.Server, "2049"); err != nil {
		return unhealthy("Shared-storage endpoint is unreachable on TCP port 2049: "+err.Error(), "storage", "unreachable"), nil
	}
	if err := log("info", "Validating Kubernetes API readiness, administrative permissions and resource ownership"); err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "get", "--raw=/readyz"); err != nil {
		return unhealthy("Kubernetes API readiness check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	authorized, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "auth", "can-i", "*", "*", "--all-namespaces")
	if err != nil || strings.TrimSpace(authorized) != "yes" {
		return unhealthy("The cluster connection does not have the required administrative permissions", "authorization", "denied"), nil
	}
	if step.Cleanup {
		if _, err := checkCleanupOwnership(ctx, runner, cluster.Spec.Kubeconfig, input); err != nil {
			return unhealthy(err.Error(), "ownership", "blocked"), nil
		}
	} else if err := checkResourcesAbsent(ctx, runner, cluster.Spec.Kubeconfig, input.StorageClassName); err != nil {
		return unhealthy(err.Error(), "resources", "conflict"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The cluster and shared-storage endpoint are ready for CSI installation", Checks: map[string]string{"api": "ready", "authorization": "cluster-admin", "storage": net.JoinHostPort(storage.Spec.Server, "2049"), "ownership": "verified"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, cluster, storage, err := resolve(step)
	if err != nil {
		return nil, err
	}
	if err := log("info", "Downloading digest-verified Kubernetes CSI manifests"); err != nil {
		return nil, err
	}
	bundle, err := plugin.manifestBundle(ctx)
	if err != nil {
		return nil, err
	}
	runner := plugin.runner()
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, bundle, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("apply CSI driver: %w", err)
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "patch", "csidriver", provisioner, "--type=merge", "--patch", `{"spec":{"fsGroupPolicy":"None"}}`); err != nil {
		return nil, fmt.Errorf("configure NFS ownership policy: %w", err)
	}
	for _, target := range []string{"csidriver/" + provisioner, "deployment/csi-nfs-controller", "daemonset/csi-nfs-node"} {
		args := []string{"annotate", "--overwrite", target, "kubephos.dev/ownership-marker=" + input.Marker}
		if !strings.HasPrefix(target, "csidriver/") {
			args = append(args, "-n", "kube-system")
		}
		if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, args...); err != nil {
			return nil, fmt.Errorf("mark managed CSI resource %s: %w", target, err)
		}
	}
	storageClass, err := storageClassManifest(input, storage)
	if err != nil {
		return nil, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, storageClass, "apply", "-f", "-"); err != nil {
		return nil, fmt.Errorf("apply storage class: %w", err)
	}
	value := result{StorageClassCapability: storageClassCapability{
		APIVersion: artifactAPI, Kind: "StorageClassCapability",
		Metadata: storageClassCapabilityMetadata{Name: input.StorageClassName, Version: driverVersion},
		Spec:     storageClassCapabilitySpec{ClusterServer: cluster.Spec.Server, Provisioner: provisioner, Protocol: storage.Spec.Protocol, Server: storage.Spec.Server, ExportPath: storage.Spec.ExportPath, DirectoryPermissions: "0777", ReclaimPolicy: "Delete", BindingMode: "Immediate", Expansion: true, AccessModes: []string{"ReadWriteOnce", "ReadWriteMany"}, MountOptions: append([]string(nil), storage.Spec.MountOptions...)},
	}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, cluster, storage, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	runner := plugin.runner()
	if step.Cleanup {
		if err := checkResourcesAbsentAfterCleanup(ctx, runner, cluster.Spec.Kubeconfig, input.StorageClassName); err != nil {
			return unhealthy(err.Error(), "resources", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed CSI resources were removed", Checks: map[string]string{"driver": "absent", "storageClass": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateResult(value, input, cluster, storage); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	if err := log("info", "Waiting for CSI controller and node components to become ready"); err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "deployment/csi-nfs-controller", "-n", "kube-system", "--timeout=5m"); err != nil {
		return unhealthy("CSI controller rollout failed: "+err.Error(), "controller", "unhealthy"), nil
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "rollout", "status", "daemonset/csi-nfs-node", "-n", "kube-system", "--timeout=5m"); err != nil {
		return unhealthy("CSI node rollout failed: "+err.Error(), "nodes", "unhealthy"), nil
	}
	if err := verifyManagedResources(ctx, runner, cluster.Spec.Kubeconfig, input, storage); err != nil {
		return unhealthy(err.Error(), "configuration", "invalid"), nil
	}
	if err := log("info", "Running an isolated dynamic provisioning, mount, write and read probe"); err != nil {
		return domain.HealthReport{}, err
	}
	if err := runProvisioningProbe(ctx, runner, cluster.Spec.Kubeconfig, input); err != nil {
		return unhealthy("Dynamic provisioning probe failed: "+err.Error(), "provisioning", "failed"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Kubernetes shared storage is ready", Checks: map[string]string{"driver": driverVersion, "controller": "ready", "nodes": "ready", "storageClass": input.StorageClassName, "provisioning": "read-write-verified"}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, cluster, _, err := resolve(step)
	if err != nil {
		return err
	}
	if err := log("warning", "Removing only CSI resources owned by this operation"); err != nil {
		return err
	}
	runner := plugin.runner()
	found, err := checkCleanupOwnership(ctx, runner, cluster.Spec.Kubeconfig, input)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	_, _ = runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "delete", "namespace", probeNamespace(input.Marker), "--ignore-not-found", "--wait=true", "--timeout=2m")
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, nil, "delete", "storageclass", input.StorageClassName, "--ignore-not-found"); err != nil {
		return err
	}
	bundle, err := plugin.manifestBundle(ctx)
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, cluster.Spec.Kubeconfig, bundle, "delete", "--ignore-not-found", "-f", "-"); err != nil {
		return fmt.Errorf("delete CSI driver: %w", err)
	}
	return nil
}

func resolve(step domain.PlanStep) (stepInput, clusterConnection, sharedStorageEndpoint, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, clusterConnection{}, sharedStorageEndpoint{}, err
	}
	clusterValue, ok := step.ResolvedInputs["cluster-connection"]
	if !ok {
		return stepInput{}, clusterConnection{}, sharedStorageEndpoint{}, errors.New("verified ClusterConnection input is unavailable")
	}
	storageValue, ok := step.ResolvedInputs["shared-storage-endpoint"]
	if !ok {
		return stepInput{}, clusterConnection{}, sharedStorageEndpoint{}, errors.New("verified SharedStorageEndpoint input is unavailable")
	}
	var cluster clusterConnection
	if err := json.Unmarshal(clusterValue.Value, &cluster); err != nil {
		return stepInput{}, clusterConnection{}, sharedStorageEndpoint{}, err
	}
	var storage sharedStorageEndpoint
	if err := json.Unmarshal(storageValue.Value, &storage); err != nil {
		return stepInput{}, clusterConnection{}, sharedStorageEndpoint{}, err
	}
	return input, cluster, storage, nil
}

func validateArtifacts(cluster clusterConnection, storage sharedStorageEndpoint) error {
	if cluster.APIVersion != artifactAPI || cluster.Kind != "ClusterConnection" || cluster.Metadata.Name == "" || cluster.Metadata.Version == "" || cluster.Spec.Distribution == "" {
		return errors.New("cluster connection identity is invalid")
	}
	parsedServer, err := url.Parse(cluster.Spec.Server)
	if err != nil || parsedServer.Scheme != "https" || parsedServer.Host == "" || parsedServer.Path != "" {
		return errors.New("cluster server must be a valid HTTPS endpoint")
	}
	if err := validateKubeconfig(cluster.Spec.Kubeconfig, cluster.Spec.Server); err != nil {
		return err
	}
	if storage.APIVersion != artifactAPI || storage.Kind != "SharedStorageEndpoint" || storage.Metadata.Name == "" || storage.Metadata.Version == "" || storage.Spec.Protocol != "nfs" {
		return errors.New("shared-storage endpoint identity is invalid")
	}
	server := net.ParseIP(storage.Spec.Server)
	if server == nil || server.To4() == nil || server.IsLoopback() || server.IsLinkLocalUnicast() {
		return errors.New("shared-storage server must be a routable IPv4 address")
	}
	_, network, err := net.ParseCIDR(storage.Spec.ClientCIDR)
	if err != nil || network.String() != storage.Spec.ClientCIDR || !network.Contains(server) {
		return errors.New("shared-storage endpoint does not contain a canonical client network")
	}
	if storage.Spec.ExportPath == "/" || path.Clean(storage.Spec.ExportPath) != storage.Spec.ExportPath || !strings.HasPrefix(storage.Spec.ExportPath, "/") {
		return errors.New("shared-storage export path is invalid")
	}
	if len(storage.Spec.MountOptions) == 0 {
		return errors.New("shared-storage mount options are unavailable")
	}
	for _, option := range storage.Spec.MountOptions {
		if option == "" || strings.ContainsAny(option, "\n\r, ") {
			return errors.New("shared-storage mount options are invalid")
		}
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
	contexts := map[string][2]string{}
	for _, item := range config.Contexts {
		contexts[item.Name] = [2]string{item.Context.Cluster, item.Context.User}
	}
	current, ok := contexts[config.CurrentContext]
	if !ok || current[0] == "" || current[1] == "" {
		return errors.New("cluster kubeconfig current context is invalid")
	}
	clusterOK := false
	for _, item := range config.Clusters {
		if item.Name == current[0] && item.Cluster.Server == server {
			clusterOK = true
		}
	}
	if !clusterOK {
		return errors.New("cluster kubeconfig does not target the verified server")
	}
	userOK := false
	for _, item := range config.Users {
		if item.Name != current[1] {
			continue
		}
		cert, certErr := base64.StdEncoding.DecodeString(item.User.ClientCertificateData)
		key, keyErr := base64.StdEncoding.DecodeString(item.User.ClientKeyData)
		userOK = certErr == nil && keyErr == nil && len(cert) > 0 && len(key) > 0
	}
	if !userOK {
		return errors.New("cluster kubeconfig client identity is invalid")
	}
	return nil
}

func storageClassManifest(input stepInput, storage sharedStorageEndpoint) ([]byte, error) {
	value := map[string]any{
		"apiVersion": "storage.k8s.io/v1", "kind": "StorageClass",
		"metadata":      map[string]any{"name": input.StorageClassName, "annotations": map[string]string{"kubephos.dev/ownership-marker": input.Marker}},
		"provisioner":   provisioner,
		"parameters":    map[string]string{"server": storage.Spec.Server, "share": storage.Spec.ExportPath, "mountPermissions": "0777", "subDir": "${pvc.metadata.namespace}/${pvc.metadata.name}"},
		"reclaimPolicy": "Delete", "volumeBindingMode": "Immediate", "allowVolumeExpansion": true,
		"mountOptions": storage.Spec.MountOptions,
	}
	return json.Marshal(value)
}

func validateResult(value result, input stepInput, cluster clusterConnection, storage sharedStorageEndpoint) error {
	capability := value.StorageClassCapability
	if capability.APIVersion != artifactAPI || capability.Kind != "StorageClassCapability" || capability.Metadata.Name != input.StorageClassName || capability.Metadata.Version != driverVersion || capability.Spec.ClusterServer != cluster.Spec.Server || capability.Spec.Provisioner != provisioner || capability.Spec.Protocol != "nfs" || capability.Spec.Server != storage.Spec.Server || capability.Spec.ExportPath != storage.Spec.ExportPath || capability.Spec.DirectoryPermissions != "0777" || capability.Spec.ReclaimPolicy != "Delete" || capability.Spec.BindingMode != "Immediate" || !capability.Spec.Expansion || len(capability.Spec.AccessModes) != 2 {
		return errors.New("storage class capability does not match the validated plan")
	}
	return nil
}

func checkResourcesAbsent(ctx context.Context, runner commandRunner, kubeconfig, storageClass string) error {
	for _, args := range [][]string{{"get", "csidriver", provisioner}, {"get", "deployment", "csi-nfs-controller", "-n", "kube-system"}, {"get", "daemonset", "csi-nfs-node", "-n", "kube-system"}, {"get", "storageclass", storageClass}} {
		if _, err := runner.Run(ctx, kubeconfig, nil, args...); err == nil {
			return fmt.Errorf("Kubernetes resource %s already exists", strings.Join(args[1:], "/"))
		}
	}
	return nil
}

func checkCleanupOwnership(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput) (bool, error) {
	found := false
	for _, args := range [][]string{{"get", "csidriver", provisioner}, {"get", "deployment", "csi-nfs-controller", "-n", "kube-system"}, {"get", "daemonset", "csi-nfs-node", "-n", "kube-system"}, {"get", "storageclass", input.StorageClassName}} {
		query := append(append([]string(nil), args...), "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}")
		value, err := runner.Run(ctx, kubeconfig, nil, query...)
		if err != nil {
			continue
		}
		found = true
		if strings.TrimSpace(value) != input.Marker {
			return false, fmt.Errorf("Kubernetes resource %s is not owned by this operation", strings.Join(args[1:], "/"))
		}
	}
	return found, nil
}

func verifyManagedResources(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput, storage sharedStorageEndpoint) error {
	checks := []struct {
		args     []string
		expected string
	}{
		{[]string{"get", "csidriver", provisioner, "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}"}, input.Marker},
		{[]string{"get", "csidriver", provisioner, "-o", "jsonpath={.spec.fsGroupPolicy}"}, "None"},
		{[]string{"get", "deployment", "csi-nfs-controller", "-n", "kube-system", "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}"}, input.Marker},
		{[]string{"get", "daemonset", "csi-nfs-node", "-n", "kube-system", "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}"}, input.Marker},
		{[]string{"get", "storageclass", input.StorageClassName, "-o", "jsonpath={.metadata.annotations.kubephos\\.dev/ownership-marker}"}, input.Marker},
		{[]string{"get", "storageclass", input.StorageClassName, "-o", "jsonpath={.provisioner}"}, provisioner},
		{[]string{"get", "storageclass", input.StorageClassName, "-o", "jsonpath={.parameters.server}"}, storage.Spec.Server},
		{[]string{"get", "storageclass", input.StorageClassName, "-o", "jsonpath={.parameters.share}"}, storage.Spec.ExportPath},
		{[]string{"get", "storageclass", input.StorageClassName, "-o", "jsonpath={.parameters.mountPermissions}"}, "0777"},
	}
	for _, check := range checks {
		value, err := runner.Run(ctx, kubeconfig, nil, check.args...)
		if err != nil || strings.TrimSpace(value) != check.expected {
			return fmt.Errorf("managed Kubernetes resource validation failed for %s", strings.Join(check.args[1:3], "/"))
		}
	}
	return nil
}

func runProvisioningProbe(ctx context.Context, runner commandRunner, kubeconfig string, input stepInput) error {
	namespace := probeNamespace(input.Marker)
	manifest, err := probeManifest(namespace, input.StorageClassName)
	if err != nil {
		return err
	}
	if _, err := runner.Run(ctx, kubeconfig, manifest, "apply", "-f", "-"); err != nil {
		return err
	}
	defer runner.Run(context.Background(), kubeconfig, nil, "delete", "namespace", namespace, "--ignore-not-found", "--wait=true", "--timeout=2m")
	if _, err := runner.Run(ctx, kubeconfig, nil, "wait", "--for=jsonpath={.status.phase}=Bound", "pvc/storage-probe", "-n", namespace, "--timeout=5m"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, kubeconfig, nil, "wait", "--for=condition=Ready", "pod/storage-probe", "-n", namespace, "--timeout=5m"); err != nil {
		return err
	}
	value, err := runner.Run(ctx, kubeconfig, nil, "exec", "pod/storage-probe", "-n", namespace, "--", "sh", "-c", "mkdir -m 0700 /data/private && printf kubephos-storage-ready > /data/private/probe && cat /data/private/probe")
	if err != nil {
		return err
	}
	if strings.TrimSpace(value) != "kubephos-storage-ready" {
		return errors.New("storage probe returned unexpected data")
	}
	if _, err := runner.Run(ctx, kubeconfig, nil, "delete", "pod/storage-probe", "-n", namespace, "--wait=true", "--timeout=2m"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, kubeconfig, manifest, "apply", "-f", "-"); err != nil {
		return err
	}
	if _, err := runner.Run(ctx, kubeconfig, nil, "wait", "--for=condition=Ready", "pod/storage-probe", "-n", namespace, "--timeout=5m"); err != nil {
		return err
	}
	value, err = runner.Run(ctx, kubeconfig, nil, "exec", "pod/storage-probe", "-n", namespace, "--", "cat", "/data/private/probe")
	if err != nil || strings.TrimSpace(value) != "kubephos-storage-ready" {
		return errors.New("storage remount probe could not read non-root data")
	}
	return nil
}

func probeManifest(namespace, storageClass string) ([]byte, error) {
	objects := []map[string]any{
		{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": namespace}},
		{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "metadata": map[string]any{"name": "storage-probe", "namespace": namespace}, "spec": map[string]any{"accessModes": []string{"ReadWriteMany"}, "storageClassName": storageClass, "resources": map[string]any{"requests": map[string]string{"storage": "16Mi"}}}},
		{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": "storage-probe", "namespace": namespace}, "spec": map[string]any{"restartPolicy": "Never", "securityContext": map[string]any{"runAsUser": 472, "runAsGroup": 472}, "containers": []map[string]any{{"name": "probe", "image": "busybox:1.37.0", "command": []string{"sh", "-c", "sleep 600"}, "volumeMounts": []map[string]any{{"name": "data", "mountPath": "/data"}}}}, "volumes": []map[string]any{{"name": "data", "persistentVolumeClaim": map[string]string{"claimName": "storage-probe"}}}}},
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

func probeNamespace(marker string) string {
	value := marker
	if len(value) > 12 {
		value = value[:12]
	}
	return "kubephos-storage-probe-" + value
}

func checkResourcesAbsentAfterCleanup(ctx context.Context, runner commandRunner, kubeconfig, storageClass string) error {
	for _, args := range [][]string{{"get", "csidriver", provisioner}, {"get", "deployment", "csi-nfs-controller", "-n", "kube-system"}, {"get", "daemonset", "csi-nfs-node", "-n", "kube-system"}, {"get", "storageclass", storageClass}} {
		if _, err := runner.Run(ctx, kubeconfig, nil, args...); err == nil {
			return fmt.Errorf("Kubernetes resource %s remains after cleanup", strings.Join(args[1:], "/"))
		}
	}
	return nil
}

func (plugin Plugin) manifestBundle(ctx context.Context) ([]byte, error) {
	parts := make([]string, 0, len(driverManifests))
	for _, source := range driverManifests {
		value, err := plugin.fetcher().Fetch(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("fetch %s CSI manifest: %w", source.Name, err)
		}
		parts = append(parts, string(value))
	}
	return []byte(strings.Join(parts, "\n---\n")), nil
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return kubectlRunner{}
}
func (plugin Plugin) fetcher() manifestFetcher {
	if plugin.Fetcher != nil {
		return plugin.Fetcher
	}
	return httpManifestFetcher{client: &http.Client{Timeout: 30 * time.Second}}
}
func (plugin Plugin) probe() endpointProbe {
	if plugin.Probe != nil {
		return plugin.Probe
	}
	return tcpProbe{dialer: &net.Dialer{Timeout: 5 * time.Second}}
}

type kubectlRunner struct{}

func (kubectlRunner) Run(ctx context.Context, kubeconfig string, stdin []byte, args ...string) (string, error) {
	file, err := os.CreateTemp("", "kubephos-kubeconfig-*")
	if err != nil {
		return "", err
	}
	name := file.Name()
	defer os.Remove(name)
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
	commandArgs := append([]string{"--kubeconfig", name}, args...)
	command := exec.CommandContext(ctx, "kubectl", commandArgs...)
	if stdin != nil {
		command.Stdin = strings.NewReader(string(stdin))
	}
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("kubectl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

type httpManifestFetcher struct{ client *http.Client }

func (fetcher httpManifestFetcher) Fetch(ctx context.Context, source manifestSource) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.URL, nil)
	if err != nil {
		return nil, err
	}
	response, err := fetcher.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected HTTP status %d", response.StatusCode)
	}
	value, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(value)
	if hex.EncodeToString(digest[:]) != source.Digest {
		return nil, errors.New("manifest digest does not match the pinned release")
	}
	return value, nil
}

type tcpProbe struct{ dialer *net.Dialer }

func (probe tcpProbe) Check(ctx context.Context, host, port string) error {
	connection, err := probe.dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	return connection.Close()
}

func validName(value string) bool {
	return len(value) <= 63 && regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`).MatchString(value)
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}
