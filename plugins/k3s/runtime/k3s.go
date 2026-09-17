package k3s

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	kubeconfigutil "kubephos.dev/kubephos/internal/kubeconfig"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/pluginssh"
)

const (
	pluginID          = "io.kubephos.cluster.k3s.bootstrap"
	k3sVersion        = "v1.36.0+k3s1"
	clusterAPIVersion = "artifacts.kubephos.dev/v1alpha1"
)

type Plugin struct {
	Runner commandRunner
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	MachineSetRef         string     `json:"machineSetRef"`
	MachineAccessRef      string     `json:"machineAccessRef"`
	ClusterName           string     `json:"clusterName"`
	ControlPlanes         int        `json:"controlPlanes"`
	ControlPlaneZones     []string   `json:"controlPlaneZones,omitempty"`
	NodePools             []NodePool `json:"nodePools,omitempty"`
	RegistryEndpointRef   string     `json:"registryEndpointRef,omitempty"`
	RegistryCredentialRef string     `json:"registryCredentialRef,omitempty"`
}

type NodePool struct {
	Name  string   `json:"name"`
	Role  string   `json:"role"`
	Count int      `json:"count"`
	Zones []string `json:"zones"`
}

type machineSet struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Provider string    `json:"provider"`
		Machines []machine `json:"machines"`
	} `json:"spec"`
}

type machine struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Address string `json:"address"`
	SSHPort int    `json:"sshPort"`
	SSHUser string `json:"sshUser"`
	State   string `json:"state"`
}

type machineAccess struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Algorithm  string `json:"algorithm"`
		PublicKey  string `json:"publicKey"`
		PrivateKey string `json:"privateKey"`
	} `json:"spec"`
}

type registryEndpoint struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Protocol string           `json:"protocol"`
		Host     string           `json:"host"`
		URL      string           `json:"url"`
		CABundle string           `json:"caBundle"`
		Insecure bool             `json:"insecure"`
		Mirrors  []registryMirror `json:"mirrors"`
	} `json:"spec"`
}

type registryMirror struct {
	Source        string `json:"source"`
	Endpoint      string `json:"endpoint"`
	RewritePrefix string `json:"rewritePrefix"`
	Probe         string `json:"probe"`
}

type registryCredential struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Server   string `json:"server"`
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"spec"`
}

type registryProfile struct {
	Endpoint   registryEndpoint
	Credential registryCredential
	Enabled    bool
}

type clusterResult struct {
	ClusterConnection clusterConnection `json:"clusterConnection"`
	ClusterInventory  clusterInventory  `json:"clusterInventory"`
}

type clusterConnection struct {
	APIVersion string                    `json:"apiVersion"`
	Kind       string                    `json:"kind"`
	Metadata   clusterConnectionMetadata `json:"metadata"`
	Spec       clusterConnectionSpec     `json:"spec"`
}

type clusterConnectionMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type clusterConnectionSpec struct {
	Distribution string `json:"distribution"`
	Server       string `json:"server"`
	Kubeconfig   string `json:"kubeconfig"`
	RegistryHost string `json:"registryHost,omitempty"`
}

type clusterInventory struct {
	APIVersion string                   `json:"apiVersion"`
	Kind       string                   `json:"kind"`
	Metadata   clusterInventoryMetadata `json:"metadata"`
	Spec       clusterInventorySpec     `json:"spec"`
}

type clusterInventoryMetadata struct {
	Name string `json:"name"`
}

type clusterInventorySpec struct {
	Distribution string        `json:"distribution"`
	Version      string        `json:"version"`
	Nodes        []clusterNode `json:"nodes"`
}

type clusterNode struct {
	Name    string `json:"name"`
	Role    string `json:"role"`
	Pool    string `json:"pool,omitempty"`
	Zone    string `json:"zone,omitempty"`
	Ready   bool   `json:"ready"`
	Version string `json:"version"`
}

type kubernetesNodeList struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Status struct {
			Conditions []struct {
				Type   string `json:"type"`
				Status string `json:"status"`
			} `json:"conditions"`
			NodeInfo struct {
				KubeletVersion string `json:"kubeletVersion"`
			} `json:"nodeInfo"`
		} `json:"status"`
	} `json:"items"`
}

type commandRunner interface {
	Run(context.Context, machine, machineAccess, string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Kubernetes cluster bootstrap", Version: "0.3.1",
		Description:     "Builds a managed K3s cluster from a verified machine topology.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["machineSetRef","machineAccessRef","clusterName","controlPlanes"],"properties":{"machineSetRef":{"type":"string","title":"Machine topology","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineSet","x-kubephos-artifact-version":"v1alpha1"},"machineAccessRef":{"type":"string","title":"Machine access","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineAccess","x-kubephos-artifact-version":"v1alpha1"},"clusterName":{"type":"string","title":"Cluster name","pattern":"^[a-z0-9][a-z0-9-]{0,31}$","default":"development"},"controlPlanes":{"type":"integer","title":"Control plane nodes","enum":[1,3],"default":1},"controlPlaneZones":{"type":"array","title":"Control plane zones","minItems":1,"maxItems":12,"items":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,31}$"}},"nodePools":{"type":"array","title":"Node pools","maxItems":12,"items":{"type":"object","additionalProperties":false,"required":["name","role","count","zones"],"properties":{"name":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,31}$"},"role":{"type":"string","enum":["management","application"]},"count":{"type":"integer","minimum":1,"maximum":12},"zones":{"type":"array","minItems":1,"maxItems":12,"items":{"type":"string","pattern":"^[a-z0-9][a-z0-9-]{0,31}$"}}}}},"registryEndpointRef":{"type":"string","title":"Managed registry","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"RegistryEndpoint","x-kubephos-artifact-version":"v1alpha1"},"registryCredentialRef":{"type":"string","title":"Registry pull credential","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"RegistryCredential","x-kubephos-artifact-version":"v1alpha1"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}, {Type: "MachineAccess", Version: "v1alpha1"}, {Type: "RegistryEndpoint", Version: "v1alpha1"}, {Type: "RegistryCredential", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ClusterInventory", Version: "v1alpha1"}},
		Capabilities:    []string{"cluster.bootstrap", "cluster.preflight", "cluster.cleanup", "lifecycle.cleanup", "lifecycle.cleanup.compatible"},
		Permissions:     []string{"network.ssh", "cluster.admin"},
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
	if !strings.HasPrefix(spec.MachineSetRef, "art_") {
		return invalid(report, "machineSetRef", "Select a verified machine topology.")
	}
	if !strings.HasPrefix(spec.MachineAccessRef, "art_") || spec.MachineAccessRef == spec.MachineSetRef {
		return invalid(report, "machineAccessRef", "Select the matching encrypted machine access artifact.")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`).MatchString(spec.ClusterName) {
		return invalid(report, "clusterName", "Cluster name must be a lowercase label with at most 32 characters.")
	}
	if spec.ControlPlanes != 1 && spec.ControlPlanes != 3 {
		return invalid(report, "controlPlanes", "Use one or three control plane nodes.")
	}
	if err := validatePoolConfiguration(spec); err != nil {
		return invalid(report, "nodePools", err.Error())
	}
	if (spec.RegistryEndpointRef == "") != (spec.RegistryCredentialRef == "") || spec.RegistryEndpointRef != "" && (!strings.HasPrefix(spec.RegistryEndpointRef, "art_") || !strings.HasPrefix(spec.RegistryCredentialRef, "art_") || spec.RegistryEndpointRef == spec.RegistryCredentialRef) {
		return invalid(report, "registryEndpointRef", "Select a managed registry endpoint and its matching pull credential together.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("KubePhos will install managed K3s %s after validating every machine.", k3sVersion)})
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
	inputs := []domain.ArtifactInput{
		{Name: "machines", Type: "MachineSet", Version: "v1alpha1", ArtifactID: spec.MachineSetRef},
		{Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", ArtifactID: spec.MachineAccessRef},
	}
	if spec.RegistryEndpointRef != "" {
		inputs = append(inputs,
			domain.ArtifactInput{Name: "registry-endpoint", Type: "RegistryEndpoint", Version: "v1alpha1", ArtifactID: spec.RegistryEndpointRef},
			domain.ArtifactInput{Name: "registry-credential", Type: "RegistryCredential", Version: "v1alpha1", ArtifactID: spec.RegistryCredentialRef},
		)
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "bootstrap-cluster", Name: "Bootstrap and verify Kubernetes", Input: raw, Mutating: true,
		ArtifactInputs: inputs,
		Outputs: []domain.ArtifactOutput{
			{Name: "cluster-connection", Type: "ClusterConnection", Version: "v1alpha1", MediaType: "application/json", Source: "/clusterConnection", Sensitive: true},
			{Name: "cluster-inventory", Type: "ClusterInventory", Version: "v1alpha1", MediaType: "application/json", Source: "/clusterInventory"},
		},
	}}}, nil
}

func (p Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, machines, access, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if (spec.ControlPlanes != 1 && spec.ControlPlanes != 3) || len(machines.Spec.Machines) < spec.ControlPlanes {
		return unhealthy("The topology does not contain enough machines for the requested control plane", "capacity", "insufficient"), nil
	}
	if err := validatePoolCapacity(spec, len(machines.Spec.Machines)); err != nil {
		return unhealthy(err.Error(), "nodePools", "invalid"), nil
	}
	if err := validateMachineArtifacts(machines, access); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	_, err = resolveRegistry(step, spec)
	if err != nil {
		return unhealthy(err.Error(), "registry", "invalid"), nil
	}
	if err := log("info", fmt.Sprintf("Validating SSH and sudo access on %d machines", len(machines.Spec.Machines))); err != nil {
		return domain.HealthReport{}, err
	}
	command := "sudo -n true && command -v curl >/dev/null && test ! -x /usr/local/bin/k3s"
	if step.Cleanup {
		command = "sudo -n true"
	}
	if err := runAll(ctx, p.runner(), machines.Spec.Machines, access, func(machine, int) string { return command }); err != nil {
		if step.Cleanup {
			return unhealthy("At least one machine failed cleanup access or installation validation: "+err.Error(), "cleanup", "blocked"), nil
		}
		return unhealthy("At least one machine failed SSH, sudo or clean-host validation: "+err.Error(), "ssh", "unhealthy"), nil
	}
	if step.Cleanup {
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Every cleanup target is reachable with non-interactive sudo", Checks: map[string]string{"machines": strconv.Itoa(len(machines.Spec.Machines)), "ssh": "verified", "sudo": "verified"}}, nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Every machine is reachable and ready for a clean K3s installation", Checks: map[string]string{"machines": strconv.Itoa(len(machines.Spec.Machines)), "ssh": "verified", "sudo": "verified", "existingInstallation": "absent"}}, nil
}

func (p Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, machines, access, err := resolve(step)
	if err != nil {
		return nil, err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes)
	primary := machines.Spec.Machines[0]
	server := "https://" + net.JoinHostPort(primary.Address, "6443")
	runner := p.runner()
	registry, err := resolveRegistry(step, spec)
	if err != nil {
		return nil, err
	}
	if registry.Enabled {
		if err := log("info", "Installing the managed registry trust and pull identity on every machine"); err != nil {
			return nil, err
		}
		command, err := registryConfigurationCommand(registry)
		if err != nil {
			return nil, err
		}
		if err := runAll(ctx, runner, machines.Spec.Machines, access, func(machine, int) string { return command }); err != nil {
			return nil, fmt.Errorf("configure managed registry: %w", err)
		}
	}
	if err := log("info", fmt.Sprintf("Installing K3s %s on the first control plane", k3sVersion)); err != nil {
		return nil, err
	}
	if _, err := runner.Run(ctx, primary, access, installCommandWithLabels("server", primary.Name, server, token, true, machineLabels(spec, 0))); err != nil {
		return nil, fmt.Errorf("install first control plane: %w", err)
	}
	if err := waitForAPI(ctx, runner, primary, access, 5*time.Minute); err != nil {
		return nil, err
	}
	remaining := machines.Spec.Machines[1:]
	if len(remaining) > 0 {
		if err := log("info", fmt.Sprintf("Joining %d additional Kubernetes nodes", len(remaining))); err != nil {
			return nil, err
		}
		if err := runAll(ctx, runner, remaining, access, func(machine machine, index int) string {
			role := "agent"
			if index+1 < spec.ControlPlanes {
				role = "server"
			}
			return installCommandWithLabels(role, machine.Name, server, token, false, machineLabels(spec, index+1))
		}); err != nil {
			return nil, fmt.Errorf("join Kubernetes nodes: %w", err)
		}
	}
	inventory, err := waitForInventory(ctx, runner, primary, access, spec.ClusterName, len(machines.Spec.Machines), 5*time.Minute)
	if err != nil {
		return nil, err
	}
	if err := validateInventoryTopology(inventory, machines.Spec.Machines, spec.ControlPlanes); err != nil {
		return nil, err
	}
	if err := validateInventoryPools(inventory, spec); err != nil {
		return nil, err
	}
	if registry.Enabled {
		if err := verifyRegistryProfile(ctx, runner, machines, access, registry); err != nil {
			return nil, err
		}
	}
	kubeconfig, err := runner.Run(ctx, primary, access, "sudo cat /etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig: %w", err)
	}
	kubeconfig = strings.ReplaceAll(kubeconfig, "https://127.0.0.1:6443", server)
	kubeconfig = strings.ReplaceAll(kubeconfig, "https://localhost:6443", server)
	kubeconfig, err = kubeconfigutil.WithIdentity(kubeconfig, spec.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("name kubeconfig identity: %w", err)
	}
	if err := validateKubeconfig(kubeconfig, server); err != nil {
		return nil, err
	}
	registryHost := ""
	if registry.Enabled {
		registryHost = registry.Endpoint.Spec.Host
	}
	result := clusterResult{
		ClusterConnection: clusterConnection{APIVersion: clusterAPIVersion, Kind: "ClusterConnection", Metadata: clusterConnectionMetadata{Name: spec.ClusterName, Version: k3sVersion}, Spec: clusterConnectionSpec{Distribution: "k3s", Server: server, Kubeconfig: kubeconfig, RegistryHost: registryHost}},
		ClusterInventory:  inventory,
	}
	return json.Marshal(result)
}

func (p Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, machines, access, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if step.Cleanup {
		command := "test ! -x /usr/local/bin/k3s && test ! -e /etc/systemd/system/k3s.service && test ! -e /etc/systemd/system/k3s-agent.service && test ! -e /etc/rancher/k3s/registries.yaml && test ! -e /etc/rancher/k3s/kubephos-registry-ca.crt && test ! -e /usr/local/share/ca-certificates/kubephos-registry-ca.crt"
		if err := runAll(ctx, p.runner(), machines.Spec.Machines, access, func(machine, int) string { return command }); err != nil {
			return unhealthy("K3s cleanup verification failed: "+err.Error(), "installation", "present"), nil
		}
		if err := log("info", "K3s binaries and services are absent from every machine"); err != nil {
			return domain.HealthReport{}, err
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "K3s was removed from every machine", Checks: map[string]string{"machines": strconv.Itoa(len(machines.Spec.Machines)), "installation": "absent"}}, nil
	}
	var result clusterResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return domain.HealthReport{}, err
	}
	primary := machines.Spec.Machines[0]
	server := "https://" + net.JoinHostPort(primary.Address, "6443")
	if result.ClusterConnection.APIVersion != clusterAPIVersion || result.ClusterConnection.Kind != "ClusterConnection" || result.ClusterConnection.Metadata.Name != spec.ClusterName || result.ClusterConnection.Metadata.Version != k3sVersion || result.ClusterConnection.Spec.Distribution != "k3s" || result.ClusterConnection.Spec.Server != server {
		return unhealthy("Cluster connection identity does not match the validated plan", "connection", "invalid"), nil
	}
	if err := validateKubeconfig(result.ClusterConnection.Spec.Kubeconfig, server); err != nil {
		return unhealthy(err.Error(), "kubeconfig", "invalid"), nil
	}
	inventory, err := readInventory(ctx, p.runner(), primary, access, spec.ClusterName)
	if err != nil {
		return unhealthy("Kubernetes API health check failed: "+err.Error(), "api", "unhealthy"), nil
	}
	if err := validateInventory(inventory, len(machines.Spec.Machines)); err != nil {
		return unhealthy(err.Error(), "nodes", "unhealthy"), nil
	}
	if err := validateInventoryTopology(inventory, machines.Spec.Machines, spec.ControlPlanes); err != nil {
		return unhealthy(err.Error(), "nodes", "mismatch"), nil
	}
	if err := validateInventoryPools(inventory, spec); err != nil {
		return unhealthy(err.Error(), "nodePools", "mismatch"), nil
	}
	registry, err := resolveRegistry(step, spec)
	if err != nil {
		return unhealthy(err.Error(), "registry", "invalid"), nil
	}
	if registry.Enabled {
		if err := verifyRegistryProfile(ctx, p.runner(), machines, access, registry); err != nil {
			return unhealthy(err.Error(), "registry", "unhealthy"), nil
		}
	}
	if len(result.ClusterInventory.Spec.Nodes) != len(inventory.Spec.Nodes) {
		return unhealthy("Persisted cluster inventory is incomplete", "inventory", "invalid"), nil
	}
	if err := log("info", "Kubernetes API, node readiness, version and kubeconfig are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Kubernetes cluster is ready for managed components", Checks: map[string]string{"api": "ready", "nodes": strconv.Itoa(len(inventory.Spec.Nodes)), "version": k3sVersion, "kubeconfig": "verified"}}, nil
}

func (p Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	_, machines, access, err := resolve(step)
	if err != nil {
		return err
	}
	if err := log("warning", "Removing the incomplete K3s installation from the reserved machines"); err != nil {
		return err
	}
	command := "if [ -x /usr/local/bin/k3s-agent-uninstall.sh ]; then sudo /usr/local/bin/k3s-agent-uninstall.sh; elif [ -x /usr/local/bin/k3s-uninstall.sh ]; then sudo /usr/local/bin/k3s-uninstall.sh; fi; sudo rm -f /etc/rancher/k3s/registries.yaml /etc/rancher/k3s/kubephos-registry-ca.crt /usr/local/share/ca-certificates/kubephos-registry-ca.crt; sudo update-ca-certificates >/dev/null"
	return runAll(ctx, p.runner(), machines.Spec.Machines, access, func(machine, int) string { return command })
}

func (p Plugin) runner() commandRunner {
	if p.Runner != nil {
		return p.Runner
	}
	return newSSHRunner()
}

func resolve(step domain.PlanStep) (Spec, machineSet, machineAccess, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, err
	}
	machineValue, ok := step.ResolvedInputs["machines"]
	if !ok {
		return Spec{}, machineSet{}, machineAccess{}, errors.New("verified MachineSet input is unavailable")
	}
	accessValue, ok := step.ResolvedInputs["machine-access"]
	if !ok {
		return Spec{}, machineSet{}, machineAccess{}, errors.New("verified MachineAccess input is unavailable")
	}
	var machines machineSet
	if err := json.Unmarshal(machineValue.Value, &machines); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, err
	}
	var access machineAccess
	if err := json.Unmarshal(accessValue.Value, &access); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, err
	}
	return spec, machines, access, nil
}

func resolveRegistry(step domain.PlanStep, spec Spec) (registryProfile, error) {
	if spec.RegistryEndpointRef == "" && spec.RegistryCredentialRef == "" {
		return registryProfile{}, nil
	}
	endpointValue, endpointOK := step.ResolvedInputs["registry-endpoint"]
	credentialValue, credentialOK := step.ResolvedInputs["registry-credential"]
	if !endpointOK || !credentialOK || endpointValue.ID != spec.RegistryEndpointRef || credentialValue.ID != spec.RegistryCredentialRef || endpointValue.Type != "RegistryEndpoint" || endpointValue.Version != "v1alpha1" || credentialValue.Type != "RegistryCredential" || credentialValue.Version != "v1alpha1" || !credentialValue.Sensitive {
		return registryProfile{}, errors.New("verified managed registry artifacts are unavailable or incompatible")
	}
	var endpoint registryEndpoint
	var credential registryCredential
	if err := json.Unmarshal(endpointValue.Value, &endpoint); err != nil {
		return registryProfile{}, errors.New("managed registry endpoint is invalid")
	}
	if err := json.Unmarshal(credentialValue.Value, &credential); err != nil {
		return registryProfile{}, errors.New("managed registry credential is invalid")
	}
	if endpoint.APIVersion != clusterAPIVersion || endpoint.Kind != "RegistryEndpoint" || endpoint.Spec.Protocol != "oci" || endpoint.Spec.Insecure || endpoint.Spec.Host == "" || endpoint.Spec.URL != "https://"+endpoint.Spec.Host || !strings.Contains(endpoint.Spec.CABundle, "BEGIN CERTIFICATE") || credential.APIVersion != clusterAPIVersion || credential.Kind != "RegistryCredential" || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" {
		return registryProfile{}, errors.New("managed registry identity, TLS or credential does not satisfy the cluster contract")
	}
	if len(endpoint.Spec.Mirrors) == 0 {
		endpoint.Spec.Mirrors = legacyManagedMirrors(endpoint.Spec.URL)
	}
	for _, mirror := range endpoint.Spec.Mirrors {
		if !validRegistrySource(mirror.Source) || mirror.Endpoint != endpoint.Spec.URL || !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}/$`).MatchString(mirror.RewritePrefix) || !validRegistryProbe(mirror.Probe) {
			return registryProfile{}, errors.New("managed registry mirror profile is invalid")
		}
	}
	return registryProfile{Endpoint: endpoint, Credential: credential, Enabled: true}, nil
}

func registryConfiguration(profile registryProfile) ([]byte, error) {
	mirrors := map[string]any{}
	for _, mirror := range profile.Endpoint.Spec.Mirrors {
		mirrors[mirror.Source] = map[string]any{"endpoint": []string{mirror.Endpoint}, "rewrite": map[string]string{"^(.*)": mirror.RewritePrefix + "$1"}}
	}
	configuration := map[string]any{
		"mirrors": mirrors,
		"configs": map[string]any{profile.Endpoint.Spec.Host: map[string]any{
			"auth": map[string]string{"username": profile.Credential.Spec.Username, "password": profile.Credential.Spec.Password},
			"tls":  map[string]string{"ca_file": "/etc/rancher/k3s/kubephos-registry-ca.crt"},
		}},
	}
	return yaml.Marshal(configuration)
}

func registryConfigurationCommand(profile registryProfile) (string, error) {
	raw, err := registryConfiguration(profile)
	if err != nil {
		return "", err
	}
	ca := base64.StdEncoding.EncodeToString([]byte(profile.Endpoint.Spec.CABundle))
	config := base64.StdEncoding.EncodeToString(raw)
	return "sudo install -d -m 0755 /etc/rancher/k3s; printf %s " + shellQuote(ca) + " | base64 -d | sudo tee /etc/rancher/k3s/kubephos-registry-ca.crt /usr/local/share/ca-certificates/kubephos-registry-ca.crt >/dev/null; printf %s " + shellQuote(config) + " | base64 -d | sudo tee /etc/rancher/k3s/registries.yaml >/dev/null; sudo chmod 0600 /etc/rancher/k3s/registries.yaml; sudo update-ca-certificates >/dev/null", nil
}

func legacyManagedMirrors(endpoint string) []registryMirror {
	values := []struct{ source, project, probe string }{
		{"docker.io", "dockerhub-proxy", "library/alpine:3.23"},
		{"registry.k8s.io", "k8s-proxy", "pause:3.10.1"},
		{"ghcr.io", "ghcr-proxy", "unict-cclab/mon-agent:v0.0.7"},
		{"gcr.io", "gcr-proxy", "google-containers/pause:3.2"},
		{"quay.io", "quay-proxy", "prometheus/busybox:latest"},
	}
	mirrors := make([]registryMirror, 0, len(values))
	for _, value := range values {
		mirrors = append(mirrors, registryMirror{Source: value.source, Endpoint: endpoint, RewritePrefix: value.project + "/", Probe: value.probe})
	}
	return mirrors
}

func validRegistrySource(value string) bool {
	return regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[0-9]{1,5})?$`).MatchString(value)
}

func validRegistryProbe(value string) bool {
	return regexp.MustCompile(`^[a-z0-9][a-z0-9._/-]{0,254}:[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`).MatchString(value)
}

func verifyRegistryProfile(ctx context.Context, runner commandRunner, machines machineSet, access machineAccess, profile registryProfile) error {
	commands := []string{
		"sudo test -s /etc/rancher/k3s/registries.yaml",
		"sudo test -s /etc/rancher/k3s/kubephos-registry-ca.crt",
		"sudo systemctl cat k3s k3s-agent 2>/dev/null | grep -q -- '--disable-default-registry-endpoint'",
		"curl --connect-timeout 5 --max-time 15 --cacert /etc/rancher/k3s/kubephos-registry-ca.crt -fsS -u " + shellQuote(profile.Credential.Spec.Username+":"+profile.Credential.Spec.Password) + " " + shellQuote(profile.Endpoint.Spec.URL+"/v2/") + " >/dev/null",
	}
	for _, mirror := range profile.Endpoint.Spec.Mirrors {
		commands = append(commands, "sudo k3s crictl pull "+shellQuote(mirror.Source+"/"+mirror.Probe)+" >/dev/null")
	}
	command := strings.Join(commands, " && ")
	if err := runAll(ctx, runner, machines.Spec.Machines, access, func(machine, int) string { return command }); err != nil {
		return fmt.Errorf("managed registry verification failed: %w", err)
	}
	return nil
}

func validateMachineArtifacts(machines machineSet, access machineAccess) error {
	if machines.APIVersion != clusterAPIVersion || machines.Kind != "MachineSet" || len(machines.Spec.Machines) == 0 {
		return errors.New("machine topology identity is invalid")
	}
	if access.APIVersion != clusterAPIVersion || access.Kind != "MachineAccess" || access.Spec.Algorithm != "ssh-ed25519" {
		return errors.New("machine access identity is invalid")
	}
	signer, err := ssh.ParsePrivateKey([]byte(access.Spec.PrivateKey))
	if err != nil {
		return errors.New("machine access private key is invalid")
	}
	if strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) != strings.TrimSpace(access.Spec.PublicKey) {
		return errors.New("machine access key pair does not match")
	}
	seenNames := map[string]bool{}
	seenAddresses := map[string]bool{}
	for _, value := range machines.Spec.Machines {
		address := net.ParseIP(value.Address)
		if value.Name == "" || seenNames[value.Name] || seenAddresses[value.Address] || address == nil || address.To4() == nil || address.IsLoopback() || address.IsLinkLocalUnicast() || value.SSHPort != 22 || value.SSHUser == "" || value.State != "running" {
			return fmt.Errorf("machine %q is invalid or duplicated", value.Name)
		}
		seenNames[value.Name] = true
		seenAddresses[value.Address] = true
	}
	return nil
}

func validatePoolConfiguration(spec Spec) error {
	if len(spec.NodePools) == 0 && len(spec.ControlPlaneZones) == 0 {
		return nil
	}
	if len(spec.ControlPlaneZones) == 0 {
		return errors.New("at least one control plane zone is required")
	}
	label := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	seenPools := map[string]bool{}
	management := 0
	for _, pool := range spec.NodePools {
		if !label.MatchString(pool.Name) || seenPools[pool.Name] {
			return fmt.Errorf("node pool name %q is invalid or duplicated", pool.Name)
		}
		seenPools[pool.Name] = true
		if pool.Role != "management" && pool.Role != "application" {
			return fmt.Errorf("node pool %q has an unsupported role", pool.Name)
		}
		if pool.Count < 1 || pool.Count > 12 || len(pool.Zones) == 0 {
			return fmt.Errorf("node pool %q requires capacity and at least one zone", pool.Name)
		}
		if pool.Role == "management" {
			management++
		}
		seenZones := map[string]bool{}
		for _, zone := range pool.Zones {
			if !label.MatchString(zone) || seenZones[zone] {
				return fmt.Errorf("node pool %q has an invalid or duplicated zone", pool.Name)
			}
			seenZones[zone] = true
		}
	}
	if management != 1 {
		return errors.New("exactly one management node pool is required")
	}
	return nil
}

func validatePoolCapacity(spec Spec, machineCount int) error {
	if len(spec.NodePools) == 0 && len(spec.ControlPlaneZones) == 0 {
		return nil
	}
	workers := 0
	for _, pool := range spec.NodePools {
		workers += pool.Count
	}
	if spec.ControlPlanes+workers != machineCount {
		return fmt.Errorf("the topology has %d machines but control plane and node pools require %d", machineCount, spec.ControlPlanes+workers)
	}
	return nil
}

func machineLabels(spec Spec, position int) map[string]string {
	if len(spec.NodePools) == 0 && len(spec.ControlPlaneZones) == 0 {
		return nil
	}
	if position < spec.ControlPlanes {
		return map[string]string{"kubephos.dev/pool": "control-plane", "kubephos.dev/role": "control-plane", "topology.kubernetes.io/zone": spec.ControlPlaneZones[position%len(spec.ControlPlaneZones)]}
	}
	worker := position - spec.ControlPlanes
	for _, pool := range spec.NodePools {
		if worker < pool.Count {
			return map[string]string{"kubephos.dev/pool": pool.Name, "kubephos.dev/role": pool.Role, "topology.kubernetes.io/zone": pool.Zones[worker%len(pool.Zones)]}
		}
		worker -= pool.Count
	}
	return nil
}

func validateInventoryPools(inventory clusterInventory, spec Spec) error {
	if len(spec.NodePools) == 0 && len(spec.ControlPlaneZones) == 0 {
		return nil
	}
	expected := map[string]int{"control-plane": spec.ControlPlanes}
	expectedZones := map[string]map[string]int{"control-plane": {}}
	for index := 0; index < spec.ControlPlanes; index++ {
		expectedZones["control-plane"][spec.ControlPlaneZones[index%len(spec.ControlPlaneZones)]]++
	}
	for _, pool := range spec.NodePools {
		expected[pool.Name] = pool.Count
		expectedZones[pool.Name] = map[string]int{}
		for index := 0; index < pool.Count; index++ {
			expectedZones[pool.Name][pool.Zones[index%len(pool.Zones)]]++
		}
	}
	actual := map[string]int{}
	actualZones := map[string]map[string]int{}
	for _, node := range inventory.Spec.Nodes {
		if node.Pool == "" || node.Zone == "" {
			return fmt.Errorf("node %q is missing its managed pool or zone label", node.Name)
		}
		if node.Role == "control-plane" && node.Pool != "control-plane" || node.Role != "control-plane" && node.Pool == "control-plane" {
			return fmt.Errorf("node %q role and managed pool do not match", node.Name)
		}
		actual[node.Pool]++
		if actualZones[node.Pool] == nil {
			actualZones[node.Pool] = map[string]int{}
		}
		actualZones[node.Pool][node.Zone]++
	}
	if len(actual) != len(expected) {
		return errors.New("the Kubernetes node pool set does not match the validated configuration")
	}
	for pool, count := range expected {
		if actual[pool] != count {
			return fmt.Errorf("node pool %q has %d nodes instead of %d", pool, actual[pool], count)
		}
		if len(actualZones[pool]) != len(expectedZones[pool]) {
			return fmt.Errorf("node pool %q zones do not match the validated layout", pool)
		}
		for zone, zoneCount := range expectedZones[pool] {
			if actualZones[pool][zone] != zoneCount {
				return fmt.Errorf("node pool %q zone %q has %d nodes instead of %d", pool, zone, actualZones[pool][zone], zoneCount)
			}
		}
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

func installCommand(role, nodeName, server, token string, first bool) string {
	return installCommandWithLabels(role, nodeName, server, token, first, nil)
}

func installCommandWithLabels(role, nodeName, server, token string, first bool, labels map[string]string) string {
	arguments := role + " --node-name " + shellQuote(nodeName) + " --disable-default-registry-endpoint"
	if first {
		host, _, _ := net.SplitHostPort(strings.TrimPrefix(server, "https://"))
		arguments += " --cluster-init --tls-san " + shellQuote(host)
	} else {
		arguments += " --server " + shellQuote(server)
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		arguments += " --node-label " + shellQuote(key+"="+labels[key])
	}
	return "curl -sfL https://get.k3s.io | INSTALL_K3S_VERSION=" + shellQuote(k3sVersion) + " K3S_TOKEN=" + shellQuote(token) + " sh -s - " + arguments
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func runAll(ctx context.Context, runner commandRunner, machines []machine, access machineAccess, command func(machine, int) string) error {
	var group sync.WaitGroup
	failures := make(chan error, len(machines))
	for index, value := range machines {
		group.Add(1)
		go func(index int, value machine) {
			defer group.Done()
			if _, err := runner.Run(ctx, value, access, command(value, index)); err != nil {
				failures <- fmt.Errorf("%s: %w", value.Name, err)
			}
		}(index, value)
	}
	group.Wait()
	close(failures)
	var values []error
	for err := range failures {
		values = append(values, err)
	}
	return errors.Join(values...)
}

func waitForAPI(ctx context.Context, runner commandRunner, primary machine, access machineAccess, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if _, err := runner.Run(ctx, primary, access, "sudo k3s kubectl get --raw=/readyz"); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("Kubernetes API did not become ready")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func waitForInventory(ctx context.Context, runner commandRunner, primary machine, access machineAccess, name string, count int, timeout time.Duration) (clusterInventory, error) {
	deadline := time.Now().Add(timeout)
	for {
		inventory, err := readInventory(ctx, runner, primary, access, name)
		if err == nil && validateInventory(inventory, count) == nil {
			return inventory, nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return clusterInventory{}, err
			}
			return clusterInventory{}, errors.New("Kubernetes nodes did not become ready")
		}
		select {
		case <-ctx.Done():
			return clusterInventory{}, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func readInventory(ctx context.Context, runner commandRunner, primary machine, access machineAccess, name string) (clusterInventory, error) {
	raw, err := runner.Run(ctx, primary, access, "sudo k3s kubectl get nodes -o json")
	if err != nil {
		return clusterInventory{}, err
	}
	var list kubernetesNodeList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return clusterInventory{}, err
	}
	nodes := make([]clusterNode, 0, len(list.Items))
	for _, item := range list.Items {
		role := "worker"
		if _, ok := item.Metadata.Labels["node-role.kubernetes.io/control-plane"]; ok {
			role = "control-plane"
		}
		ready := false
		for _, condition := range item.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" {
				ready = true
			}
		}
		nodes = append(nodes, clusterNode{Name: item.Metadata.Name, Role: role, Pool: item.Metadata.Labels["kubephos.dev/pool"], Zone: item.Metadata.Labels["topology.kubernetes.io/zone"], Ready: ready, Version: item.Status.NodeInfo.KubeletVersion})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Name < nodes[j].Name })
	return clusterInventory{APIVersion: clusterAPIVersion, Kind: "ClusterInventory", Metadata: clusterInventoryMetadata{Name: name}, Spec: clusterInventorySpec{Distribution: "k3s", Version: k3sVersion, Nodes: nodes}}, nil
}

func validateInventory(inventory clusterInventory, count int) error {
	if inventory.APIVersion != clusterAPIVersion || inventory.Kind != "ClusterInventory" || inventory.Spec.Distribution != "k3s" || inventory.Spec.Version != k3sVersion || len(inventory.Spec.Nodes) != count {
		return errors.New("cluster inventory does not match the managed topology")
	}
	for _, node := range inventory.Spec.Nodes {
		if !node.Ready || node.Version != k3sVersion {
			return fmt.Errorf("node %s is not ready on managed version %s", node.Name, k3sVersion)
		}
	}
	return nil
}

func validateInventoryTopology(inventory clusterInventory, machines []machine, controlPlanes int) error {
	expected := make(map[string]string, len(machines))
	for index, value := range machines {
		role := "worker"
		if index < controlPlanes {
			role = "control-plane"
		}
		expected[value.Name] = role
	}
	for _, node := range inventory.Spec.Nodes {
		role, ok := expected[node.Name]
		if !ok || role != node.Role {
			return fmt.Errorf("node %s has an unexpected identity or role", node.Name)
		}
		delete(expected, node.Name)
	}
	if len(expected) != 0 {
		return errors.New("cluster inventory is missing machines from the validated topology")
	}
	return nil
}

func validateKubeconfig(value, server string) error {
	var config struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
		Clusters   []struct {
			Cluster struct {
				Server string `yaml:"server"`
			} `yaml:"cluster"`
		} `yaml:"clusters"`
		Users          []map[string]any `yaml:"users"`
		Contexts       []map[string]any `yaml:"contexts"`
		CurrentContext string           `yaml:"current-context"`
	}
	if err := yaml.Unmarshal([]byte(value), &config); err != nil || config.APIVersion != "v1" || config.Kind != "Config" || len(config.Clusters) == 0 || len(config.Users) == 0 || len(config.Contexts) == 0 || config.CurrentContext == "" {
		return errors.New("generated kubeconfig is invalid")
	}
	if config.Clusters[0].Cluster.Server != server {
		return errors.New("generated kubeconfig does not target the verified control plane")
	}
	return nil
}

type sshRunner struct {
	client *pluginssh.Client
}

func newSSHRunner() *sshRunner {
	return &sshRunner{client: pluginssh.New()}
}

func (r *sshRunner) Run(ctx context.Context, target machine, access machineAccess, command string) (string, error) {
	return r.client.Run(ctx, pluginssh.Target{Address: target.Address, Port: target.SSHPort, User: target.SSHUser}, access.Spec.PrivateKey, command)
}
