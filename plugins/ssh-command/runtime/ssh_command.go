package sshcommand

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
	"unicode"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/pluginssh"
)

const (
	pluginID             = "io.kubephos.machine.ssh.command"
	artifactAPI          = "artifacts.kubephos.dev/v1alpha1"
	commandOutputLimit   = 256 << 10
	commandLogLineLimit  = 4096
	commandLogEventLimit = 100
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
	MachineIndex     int    `json:"machineIndex"`
	Command          string `json:"command"`
	TimeoutSeconds   int    `json:"timeoutSeconds"`
	ProtectOutput    bool   `json:"protectOutput"`
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

type commandResult struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   commandResultMetadata `json:"metadata"`
	Spec       commandResultSpec     `json:"spec"`
}

type commandResultMetadata struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
}

type commandResultSpec struct {
	MachineID string `json:"machineId"`
	Address   string `json:"address"`
	SSHUser   string `json:"sshUser"`
	Output    string `json:"output"`
}

type result struct {
	CommandResult commandResult `json:"commandResult"`
}

type commandRunner interface {
	Run(context.Context, machine, machineAccess, string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Provider: "machine", Name: "Audited SSH command", Version: "0.1.0",
		Description:     "Runs a bounded command on a selected managed machine through its verified SSH artifacts.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["machineSetRef","machineAccessRef","machineIndex","command","timeoutSeconds","protectOutput"],"properties":{"machineSetRef":{"type":"string","title":"Managed machines","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineSet","x-kubephos-artifact-version":"v1alpha1"},"machineAccessRef":{"type":"string","title":"Machine access","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineAccess","x-kubephos-artifact-version":"v1alpha1"},"machineIndex":{"type":"integer","title":"Machine position","description":"Zero-based position in the selected managed machine set.","minimum":0,"maximum":11,"default":0},"command":{"type":"string","title":"Command","description":"Non-interactive command executed by the declared SSH user.","minLength":1,"maxLength":4096,"x-kubephos-multiline":true},"timeoutSeconds":{"type":"integer","title":"Timeout seconds","minimum":1,"maximum":600,"default":60},"protectOutput":{"type":"boolean","title":"Protect command output","description":"Keep enabled when output may contain credentials or private data.","default":true}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}, {Type: "MachineAccess", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "RemoteCommandResult", Version: "v1alpha1"}},
		Capabilities:    []string{"infrastructure.ssh.control", "infrastructure.ssh.preflight"},
		Permissions:     []string{"network.ssh", "infrastructure.debug"},
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
		return invalid(report, "machineSetRef", "Select a verified managed machine set.")
	}
	if !strings.HasPrefix(spec.MachineAccessRef, "art_") || spec.MachineAccessRef == spec.MachineSetRef {
		return invalid(report, "machineAccessRef", "Select the matching encrypted machine access artifact.")
	}
	if spec.MachineIndex < 0 || spec.MachineIndex > 11 {
		return invalid(report, "machineIndex", "Machine position must be between 0 and 11.")
	}
	spec.Command = strings.TrimSpace(spec.Command)
	if spec.Command == "" || len(spec.Command) > 4096 || invalidCommandText(spec.Command) {
		return invalid(report, "command", "Command must contain 1 to 4096 printable characters.")
	}
	if spec.TimeoutSeconds < 1 || spec.TimeoutSeconds > 600 {
		return invalid(report, "timeoutSeconds", "Timeout must be between 1 and 600 seconds.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Message: "The command can change the selected machine. Review the target, command and plan hash before starting."})
	if !spec.ProtectOutput {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Message: "Command output will be downloadable and previewable. Do not print credentials or private data."})
	}
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
	spec.Command = strings.TrimSpace(spec.Command)
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "run-remote-command", Name: "Run bounded command on managed machine", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "machines", Type: "MachineSet", Version: "v1alpha1", ArtifactID: spec.MachineSetRef},
			{Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", ArtifactID: spec.MachineAccessRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "command-result", Type: "RemoteCommandResult", Version: "v1alpha1", MediaType: "application/json", Source: "/commandResult", Sensitive: spec.ProtectOutput}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, machines, access, target, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateArtifacts(machines, access, target); err != nil {
		return unhealthy(err.Error(), "machine", "invalid"), nil
	}
	if err := log("info", fmt.Sprintf("Validating SSH access to managed machine %s at position %d", target.Name, spec.MachineIndex)); err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := plugin.runner().Run(ctx, target, access, "command -v timeout >/dev/null && command -v mktemp >/dev/null && command -v tail >/dev/null"); err != nil {
		return unhealthy("SSH preflight failed: "+err.Error(), "ssh", "unreachable"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed machine is reachable and ready for the bounded command", Checks: map[string]string{"machine": target.ID, "ssh": "verified", "timeout": "available"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, _, access, target, err := resolve(step)
	if err != nil {
		return nil, err
	}
	if err := log("info", fmt.Sprintf("Running audited command on %s with a %d second timeout", target.Name, spec.TimeoutSeconds)); err != nil {
		return nil, err
	}
	command := fmt.Sprintf("output=$(mktemp) || exit 125; trap 'rm -f \"$output\"' EXIT HUP INT TERM; timeout --signal=TERM %ds /bin/sh -lc %s >\"$output\" 2>&1; status=$?; tail -c %d \"$output\"; exit \"$status\"", spec.TimeoutSeconds, shellQuote(spec.Command), commandOutputLimit)
	output, err := plugin.runner().Run(ctx, target, access, command)
	if err != nil {
		return nil, err
	}
	if !spec.ProtectOutput {
		for _, line := range outputLines(output, commandLogEventLimit) {
			if err := log("info", line); err != nil {
				return nil, err
			}
		}
	}
	value := result{CommandResult: commandResult{
		APIVersion: artifactAPI, Kind: "RemoteCommandResult",
		Metadata: commandResultMetadata{Name: target.Name, Version: "v1alpha1", CreatedAt: time.Now().UTC()},
		Spec:     commandResultSpec{MachineID: target.ID, Address: target.Address, SSHUser: target.SSHUser, Output: output},
	}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	_, _, access, target, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if value.CommandResult.APIVersion != artifactAPI || value.CommandResult.Kind != "RemoteCommandResult" || value.CommandResult.Metadata.Version != "v1alpha1" || value.CommandResult.Spec.MachineID != target.ID || value.CommandResult.Spec.Address != target.Address || value.CommandResult.Spec.SSHUser != target.SSHUser || len(value.CommandResult.Spec.Output) > commandOutputLimit {
		return unhealthy("Remote command result does not match the validated target", "artifact", "invalid"), nil
	}
	if err := log("info", "Verifying that the managed machine remains reachable after command completion"); err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := plugin.runner().Run(ctx, target, access, "true"); err != nil {
		return unhealthy("Post-command SSH health gate failed: "+err.Error(), "ssh", "unhealthy"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Command completed and the managed machine remains reachable", Checks: map[string]string{"machine": target.ID, "command": "completed", "ssh": "healthy"}}, nil
}

func (Plugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return &sshRunner{client: pluginssh.New()}
}

func resolve(step domain.PlanStep) (Spec, machineSet, machineAccess, machine, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, machine{}, err
	}
	machineValue, ok := step.ResolvedInputs["machines"]
	if !ok {
		return Spec{}, machineSet{}, machineAccess{}, machine{}, errors.New("verified MachineSet input is unavailable")
	}
	accessValue, ok := step.ResolvedInputs["machine-access"]
	if !ok {
		return Spec{}, machineSet{}, machineAccess{}, machine{}, errors.New("verified MachineAccess input is unavailable")
	}
	var machines machineSet
	if err := json.Unmarshal(machineValue.Value, &machines); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, machine{}, err
	}
	var access machineAccess
	if err := json.Unmarshal(accessValue.Value, &access); err != nil {
		return Spec{}, machineSet{}, machineAccess{}, machine{}, err
	}
	if spec.MachineIndex < 0 || spec.MachineIndex >= len(machines.Spec.Machines) {
		return Spec{}, machineSet{}, machineAccess{}, machine{}, errors.New("selected machine position is outside the managed machine set")
	}
	return spec, machines, access, machines.Spec.Machines[spec.MachineIndex], nil
}

func validateArtifacts(machines machineSet, access machineAccess, target machine) error {
	if machines.APIVersion != artifactAPI || machines.Kind != "MachineSet" || len(machines.Spec.Machines) < 1 || len(machines.Spec.Machines) > 12 {
		return errors.New("managed machine set is invalid")
	}
	_, network, err := net.ParseCIDR(machines.Spec.NetworkCIDR)
	if err != nil || network.String() != machines.Spec.NetworkCIDR {
		return errors.New("machine set does not contain a canonical managed network")
	}
	address := net.ParseIP(target.Address)
	if target.ID == "" || target.Name == "" || target.SSHPort != 22 || target.SSHUser == "" || target.State != "running" || address == nil || address.To4() == nil || !network.Contains(address) {
		return errors.New("selected managed machine is invalid")
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

func invalidCommandText(value string) bool {
	for _, current := range value {
		if current == 0 || unicode.IsControl(current) && current != '\n' && current != '\r' && current != '\t' {
			return true
		}
	}
	return false
}

func outputLines(value string, limit int) []string {
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return nil
	}
	if len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	for index, line := range lines {
		if len(line) > commandLogLineLimit {
			lines[index] = line[len(line)-commandLogLineLimit:]
		}
	}
	return lines
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
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
	output, err := runner.client.Run(ctx, pluginssh.Target{Address: target.Address, Port: target.SSHPort, User: target.SSHUser}, access.Spec.PrivateKey, command)
	if err != nil {
		return "", errors.New("remote command failed")
	}
	return output, nil
}
