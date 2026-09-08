package k3s

import (
	"context"
	"crypto/rand"
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
	"kubephos.dev/kubephos/internal/plugins"
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
	MachineSetRef    string `json:"machineSetRef"`
	MachineAccessRef string `json:"machineAccessRef"`
	ClusterName      string `json:"clusterName"`
	ControlPlanes    int    `json:"controlPlanes"`
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
		ID: pluginID, Name: "Kubernetes cluster bootstrap", Version: "0.1.0",
		Description:     "Builds a managed K3s cluster from a verified machine topology.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["machineSetRef","machineAccessRef","clusterName","controlPlanes"],"properties":{"machineSetRef":{"type":"string","title":"Machine topology","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineSet","x-kubephos-artifact-version":"v1alpha1"},"machineAccessRef":{"type":"string","title":"Machine access","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineAccess","x-kubephos-artifact-version":"v1alpha1"},"clusterName":{"type":"string","title":"Cluster name","pattern":"^[a-z0-9][a-z0-9-]{0,31}$","default":"development"},"controlPlanes":{"type":"integer","title":"Control plane nodes","enum":[1,3],"default":1}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}, {Type: "MachineAccess", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "ClusterConnection", Version: "v1alpha1"}, {Type: "ClusterInventory", Version: "v1alpha1"}},
		Capabilities:    []string{"cluster.bootstrap", "cluster.preflight", "cluster.cleanup"},
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
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "bootstrap-cluster", Name: "Bootstrap and verify Kubernetes", Input: raw, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "machines", Type: "MachineSet", Version: "v1alpha1", ArtifactID: spec.MachineSetRef},
			{Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", ArtifactID: spec.MachineAccessRef},
		},
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
	if err := validateMachineArtifacts(machines, access); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := log("info", fmt.Sprintf("Validating SSH and sudo access on %d machines", len(machines.Spec.Machines))); err != nil {
		return domain.HealthReport{}, err
	}
	command := "sudo -n true && command -v curl >/dev/null && test ! -x /usr/local/bin/k3s"
	if err := runAll(ctx, p.runner(), machines.Spec.Machines, access, func(machine, int) string { return command }); err != nil {
		return unhealthy("At least one machine failed SSH, sudo or clean-host validation: "+err.Error(), "ssh", "unhealthy"), nil
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
	if err := log("info", fmt.Sprintf("Installing K3s %s on the first control plane", k3sVersion)); err != nil {
		return nil, err
	}
	if _, err := runner.Run(ctx, primary, access, installCommand("server", primary.Name, server, token, true)); err != nil {
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
			return installCommand(role, machine.Name, server, token, false)
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
	kubeconfig, err := runner.Run(ctx, primary, access, "sudo cat /etc/rancher/k3s/k3s.yaml")
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig: %w", err)
	}
	kubeconfig = strings.ReplaceAll(kubeconfig, "https://127.0.0.1:6443", server)
	kubeconfig = strings.ReplaceAll(kubeconfig, "https://localhost:6443", server)
	if err := validateKubeconfig(kubeconfig, server); err != nil {
		return nil, err
	}
	result := clusterResult{
		ClusterConnection: clusterConnection{APIVersion: clusterAPIVersion, Kind: "ClusterConnection", Metadata: clusterConnectionMetadata{Name: spec.ClusterName, Version: k3sVersion}, Spec: clusterConnectionSpec{Distribution: "k3s", Server: server, Kubeconfig: kubeconfig}},
		ClusterInventory:  inventory,
	}
	return json.Marshal(result)
}

func (p Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, machines, access, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
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
	command := "if [ -x /usr/local/bin/k3s-agent-uninstall.sh ]; then sudo /usr/local/bin/k3s-agent-uninstall.sh; elif [ -x /usr/local/bin/k3s-uninstall.sh ]; then sudo /usr/local/bin/k3s-uninstall.sh; fi"
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

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}

func installCommand(role, nodeName, server, token string, first bool) string {
	arguments := role + " --node-name " + shellQuote(nodeName)
	if first {
		host, _, _ := net.SplitHostPort(strings.TrimPrefix(server, "https://"))
		arguments += " --cluster-init --tls-san " + shellQuote(host)
	} else {
		arguments += " --server " + shellQuote(server)
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
		nodes = append(nodes, clusterNode{Name: item.Metadata.Name, Role: role, Ready: ready, Version: item.Status.NodeInfo.KubeletVersion})
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
	lock         sync.Mutex
	fingerprints map[string]string
}

func newSSHRunner() *sshRunner {
	return &sshRunner{fingerprints: map[string]string{}}
}

func (r *sshRunner) Run(ctx context.Context, target machine, access machineAccess, command string) (string, error) {
	signer, err := ssh.ParsePrivateKey([]byte(access.Spec.PrivateKey))
	if err != nil {
		return "", errors.New("invalid SSH private key")
	}
	address := net.JoinHostPort(target.Address, strconv.Itoa(target.SSHPort))
	configuration := &ssh.ClientConfig{
		User: target.SSHUser, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, Timeout: 20 * time.Second,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			fingerprint := ssh.FingerprintSHA256(key)
			r.lock.Lock()
			defer r.lock.Unlock()
			if known, ok := r.fingerprints[address]; ok && known != fingerprint {
				return errors.New("SSH host key changed during operation")
			}
			r.fingerprints[address] = fingerprint
			return nil
		},
	}
	connection, err := (&net.Dialer{Timeout: 20 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(20 * time.Second))
	clientConnection, channels, requests, err := ssh.NewClientConn(connection, address, configuration)
	if err != nil {
		return "", err
	}
	_ = connection.SetDeadline(time.Time{})
	client := ssh.NewClient(clientConnection, channels, requests)
	defer client.Close()
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer session.Close()
	type commandResult struct {
		value []byte
		err   error
	}
	completed := make(chan commandResult, 1)
	go func() {
		value, err := session.CombinedOutput(command)
		completed <- commandResult{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		_ = client.Close()
		return "", ctx.Err()
	case result := <-completed:
		value := strings.TrimSpace(string(result.value))
		if result.err != nil {
			if len(value) > 1024 {
				value = value[len(value)-1024:]
			}
			return value, fmt.Errorf("remote command failed: %s", value)
		}
		return value, nil
	}
}
