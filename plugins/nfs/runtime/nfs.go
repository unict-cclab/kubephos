package nfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/pluginssh"
)

const (
	pluginID       = "io.kubephos.storage.nfs.managed"
	artifactAPI    = "artifacts.kubephos.dev/v1alpha1"
	exportPath     = "/srv/kubephos"
	exportFile     = "/etc/exports.d/kubephos.exports"
	markerFile     = "/var/lib/kubephos/nfs.marker"
	managedProfile = "ubuntu-nfs-kernel-server"
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
	StorageName      string `json:"storageName"`
}

type stepInput struct {
	Spec
	Marker string `json:"marker"`
}

type machineSet struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		NetworkCIDR string    `json:"networkCIDR"`
		Machines    []machine `json:"machines"`
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

type sharedStorageEndpoint struct {
	APIVersion string                        `json:"apiVersion"`
	Kind       string                        `json:"kind"`
	Metadata   sharedStorageEndpointMetadata `json:"metadata"`
	Spec       sharedStorageEndpointSpec     `json:"spec"`
}

type sharedStorageEndpointMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type sharedStorageEndpointSpec struct {
	Protocol     string   `json:"protocol"`
	Server       string   `json:"server"`
	ExportPath   string   `json:"exportPath"`
	ClientCIDR   string   `json:"clientCIDR"`
	MountOptions []string `json:"mountOptions"`
}

type result struct {
	SharedStorageEndpoint sharedStorageEndpoint `json:"sharedStorageEndpoint"`
}

type commandRunner interface {
	Run(context.Context, machine, machineAccess, string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Managed shared storage", Version: "0.1.0",
		Description:     "Installs a validated shared-storage service on a dedicated machine.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["machineSetRef","machineAccessRef","storageName"],"properties":{"machineSetRef":{"type":"string","title":"Storage machine","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineSet","x-kubephos-artifact-version":"v1alpha1"},"machineAccessRef":{"type":"string","title":"Machine access","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineAccess","x-kubephos-artifact-version":"v1alpha1"},"storageName":{"type":"string","title":"Storage name","pattern":"^[a-z0-9][a-z0-9-]{0,31}$","default":"shared-storage"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}, {Type: "MachineAccess", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "SharedStorageEndpoint", Version: "v1alpha1"}},
		Capabilities:    []string{"storage.shared.provision", "storage.shared.preflight", "storage.shared.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"network.ssh", "storage.manage"},
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
		return invalid(report, "machineSetRef", "Select a verified dedicated machine topology.")
	}
	if !strings.HasPrefix(spec.MachineAccessRef, "art_") || spec.MachineAccessRef == spec.MachineSetRef {
		return invalid(report, "machineAccessRef", "Select the matching encrypted machine access artifact.")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`).MatchString(spec.StorageName) {
		return invalid(report, "storageName", "Storage name must be a lowercase label with at most 32 characters.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will install the managed NFS profile after validating the dedicated machine and network."})
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
		ID: "install-shared-storage", Name: "Install and verify managed shared storage", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "machines", Type: "MachineSet", Version: "v1alpha1", ArtifactID: spec.MachineSetRef},
			{Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", ArtifactID: spec.MachineAccessRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "shared-storage-endpoint", Type: "SharedStorageEndpoint", Version: "v1alpha1", MediaType: "application/json", Source: "/sharedStorageEndpoint"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, machines, access, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateArtifacts(machines, access); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	target := machines.Spec.Machines[0]
	command := "sudo -n true && command -v apt-get >/dev/null && test ! -e " + exportFile + " && test ! -e " + markerFile + " && test ! -e " + exportPath
	if step.Cleanup {
		command = cleanupPrecheckCommand(input.Marker)
	}
	if err := log("info", "Validating the dedicated storage machine, network and non-interactive sudo access"); err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := plugin.runner().Run(ctx, target, access, command); err != nil {
		return unhealthy("Shared-storage precheck failed: "+err.Error(), "machine", "blocked"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The dedicated machine is ready for the managed shared-storage action", Checks: map[string]string{"machines": "1", "ssh": "verified", "sudo": "verified", "network": machines.Spec.NetworkCIDR}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, machines, access, err := resolve(step)
	if err != nil {
		return nil, err
	}
	target := machines.Spec.Machines[0]
	if err := log("info", "Installing the managed shared-storage profile"); err != nil {
		return nil, err
	}
	version, err := plugin.runner().Run(ctx, target, access, installCommand(input.Marker, machines.Spec.NetworkCIDR))
	if err != nil {
		return nil, err
	}
	version = lastLine(version)
	if version == "" {
		return nil, errors.New("installed NFS package version is unavailable")
	}
	value := result{SharedStorageEndpoint: sharedStorageEndpoint{
		APIVersion: artifactAPI, Kind: "SharedStorageEndpoint",
		Metadata: sharedStorageEndpointMetadata{Name: input.StorageName, Version: version},
		Spec:     sharedStorageEndpointSpec{Protocol: "nfs", Server: target.Address, ExportPath: exportPath, ClientCIDR: machines.Spec.NetworkCIDR, MountOptions: []string{"nfsvers=4.1"}},
	}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, machines, access, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	target := machines.Spec.Machines[0]
	if step.Cleanup {
		if _, err := plugin.runner().Run(ctx, target, access, "test ! -e "+exportFile+" && test ! -e "+markerFile+" && test ! -e "+exportPath); err != nil {
			return unhealthy("Shared-storage cleanup verification failed: "+err.Error(), "managedFiles", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed shared-storage configuration and data were removed", Checks: map[string]string{"managedFiles": "absent", "export": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	endpoint := value.SharedStorageEndpoint
	if endpoint.APIVersion != artifactAPI || endpoint.Kind != "SharedStorageEndpoint" || endpoint.Metadata.Name != input.StorageName || endpoint.Metadata.Version == "" || endpoint.Spec.Protocol != "nfs" || endpoint.Spec.Server != target.Address || endpoint.Spec.ExportPath != exportPath || endpoint.Spec.ClientCIDR != machines.Spec.NetworkCIDR {
		return unhealthy("Shared-storage endpoint does not match the validated plan", "artifact", "invalid"), nil
	}
	if _, err := plugin.runner().Run(ctx, target, access, readinessCommand(input.Marker)); err != nil {
		return unhealthy("Shared-storage health gate failed: "+err.Error(), "service", "unhealthy"), nil
	}
	if err := log("info", "NFS service, managed export, ownership marker and package version are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed shared storage is ready", Checks: map[string]string{"service": "active", "export": exportPath, "server": target.Address, "version": endpoint.Metadata.Version}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, machines, access, err := resolve(step)
	if err != nil {
		return err
	}
	if err := log("warning", "Removing only the managed NFS export, marker and data directory"); err != nil {
		return err
	}
	_, err = plugin.runner().Run(ctx, machines.Spec.Machines[0], access, cleanupCommand(input.Marker))
	return err
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return &sshRunner{client: pluginssh.New()}
}

func resolve(step domain.PlanStep) (stepInput, machineSet, machineAccess, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, machineSet{}, machineAccess{}, err
	}
	machineValue, ok := step.ResolvedInputs["machines"]
	if !ok {
		return stepInput{}, machineSet{}, machineAccess{}, errors.New("verified MachineSet input is unavailable")
	}
	accessValue, ok := step.ResolvedInputs["machine-access"]
	if !ok {
		return stepInput{}, machineSet{}, machineAccess{}, errors.New("verified MachineAccess input is unavailable")
	}
	var machines machineSet
	if err := json.Unmarshal(machineValue.Value, &machines); err != nil {
		return stepInput{}, machineSet{}, machineAccess{}, err
	}
	var access machineAccess
	if err := json.Unmarshal(accessValue.Value, &access); err != nil {
		return stepInput{}, machineSet{}, machineAccess{}, err
	}
	return input, machines, access, nil
}

func validateArtifacts(machines machineSet, access machineAccess) error {
	if machines.APIVersion != artifactAPI || machines.Kind != "MachineSet" || len(machines.Spec.Machines) != 1 {
		return errors.New("shared storage requires one dedicated machine")
	}
	_, network, err := net.ParseCIDR(machines.Spec.NetworkCIDR)
	if err != nil || network.String() != machines.Spec.NetworkCIDR {
		return errors.New("machine topology does not contain a canonical managed network")
	}
	target := machines.Spec.Machines[0]
	address := net.ParseIP(target.Address)
	if target.Name == "" || target.SSHPort != 22 || target.SSHUser == "" || target.State != "running" || address == nil || address.To4() == nil || !network.Contains(address) {
		return errors.New("dedicated storage machine is invalid")
	}
	if access.APIVersion != artifactAPI || access.Kind != "MachineAccess" || access.Spec.Algorithm != "ssh-ed25519" {
		return errors.New("machine access identity is invalid")
	}
	signer, err := ssh.ParsePrivateKey([]byte(access.Spec.PrivateKey))
	if err != nil || strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey()))) != strings.TrimSpace(access.Spec.PublicKey) {
		return errors.New("machine access key pair is invalid")
	}
	return nil
}

func installCommand(marker, network string) string {
	export := exportPath + " " + network + "(rw,sync,no_subtree_check,root_squash)"
	return strings.Join([]string{
		"sudo apt-get update -q",
		"sudo DEBIAN_FRONTEND=noninteractive apt-get install -y nfs-kernel-server",
		"sudo install -d -m 0755 /var/lib/kubephos",
		"sudo install -d -m 0755 /etc/exports.d",
		"printf '%s\\n' " + shellQuote(marker) + " | sudo tee " + markerFile + " >/dev/null",
		"sudo install -d -m 0777 " + exportPath,
		"printf '%s\\n' " + shellQuote(export) + " | sudo tee " + exportFile + " >/dev/null",
		"sudo exportfs -rav",
		"sudo systemctl enable --now nfs-kernel-server",
		"dpkg-query -W -f='${Version}\\n' nfs-kernel-server",
	}, " && ")
}

func readinessCommand(marker string) string {
	return "sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker) + " && sudo systemctl is-active --quiet nfs-kernel-server && sudo exportfs -v | grep -F " + shellQuote(exportPath)
}

func cleanupPrecheckCommand(marker string) string {
	return "sudo -n true && if test -e " + markerFile + "; then sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker) + "; else test ! -e " + exportFile + " && test ! -e " + exportPath + "; fi"
}

func cleanupCommand(marker string) string {
	return "if test ! -e " + markerFile + " && test ! -e " + exportFile + " && test ! -e " + exportPath + "; then exit 0; fi && sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker) + " && sudo rm -f " + exportFile + " && sudo exportfs -ra && sudo rm -rf -- " + exportPath + " && sudo rm -f " + markerFile
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func lastLine(value string) string {
	lines := strings.Fields(strings.TrimSpace(value))
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}

type sshRunner struct {
	client *pluginssh.Client
}

func (runner *sshRunner) Run(ctx context.Context, target machine, access machineAccess, command string) (string, error) {
	return runner.client.Run(ctx, pluginssh.Target{Address: target.Address, Port: target.SSHPort, User: target.SSHUser}, access.Spec.PrivateKey, command)
}
