package ociexecutor

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/pluginssh"
)

const (
	pluginID             = "io.kubephos.executor.oci.rootless"
	artifactAPI          = "artifacts.kubephos.dev/v1alpha1"
	dockerVersion        = "29.8.0"
	dockerPackageVersion = "5:29.8.0-1~ubuntu.24.04~noble"
	dockerKeyFingerprint = "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
	aptNetworkOptions    = "-o Acquire::Retries=4 -o Acquire::http::Timeout=20 -o Acquire::https::Timeout=20"
	markerFile           = "/var/lib/kubephos/oci-executor.marker"
	dockerKeyPath        = "/etc/apt/keyrings/docker.asc"
	dockerSourcePath     = "/etc/apt/sources.list.d/docker.sources"
	executorPort         = 2376
	caMarker             = "KUBEPHOS_EXECUTOR_CA="
	certMarker           = "KUBEPHOS_EXECUTOR_CERT="
	keyMarker            = "KUBEPHOS_EXECUTOR_KEY="
)

type Plugin struct {
	Runner           commandRunner
	endpointVerifier func(context.Context, machine, executorCredential) error
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	MachineSetRef    string `json:"machineSetRef"`
	MachineAccessRef string `json:"machineAccessRef"`
	ExecutorName     string `json:"executorName"`
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
	ID        string `json:"id"`
	Name      string `json:"name"`
	Address   string `json:"address"`
	SSHPort   int    `json:"sshPort"`
	SSHUser   string `json:"sshUser"`
	State     string `json:"state"`
	Cores     int    `json:"cores"`
	MemoryMiB int    `json:"memoryMiB"`
	DiskGiB   int    `json:"diskGiB"`
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

type executorEndpoint struct {
	APIVersion string                   `json:"apiVersion"`
	Kind       string                   `json:"kind"`
	Metadata   executorEndpointMetadata `json:"metadata"`
	Spec       executorEndpointSpec     `json:"spec"`
}

type executorEndpointMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type executorEndpointSpec struct {
	Host      string `json:"host"`
	Address   string `json:"address"`
	Port      int    `json:"port"`
	Transport string `json:"transport"`
	Security  string `json:"security"`
}

type executorCredential struct {
	APIVersion string                     `json:"apiVersion"`
	Kind       string                     `json:"kind"`
	Metadata   executorCredentialMetadata `json:"metadata"`
	Spec       executorCredentialSpec     `json:"spec"`
}

type executorCredentialMetadata struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type executorCredentialSpec struct {
	CA          string `json:"ca"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"privateKey"`
}

type result struct {
	ExecutorEndpoint   executorEndpoint   `json:"executorEndpoint"`
	ExecutorCredential executorCredential `json:"executorCredential"`
}

type commandRunner interface {
	Run(context.Context, machine, machineAccess, string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Managed OCI executor", Version: "0.1.0",
		Description:     "Installs and validates a dedicated rootless OCI executor with mutual TLS.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["machineSetRef","machineAccessRef","executorName"],"properties":{"machineSetRef":{"type":"string","title":"Executor machine","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineSet","x-kubephos-artifact-version":"v1alpha1"},"machineAccessRef":{"type":"string","title":"Machine access","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineAccess","x-kubephos-artifact-version":"v1alpha1"},"executorName":{"type":"string","title":"Executor name","pattern":"^[a-z0-9][a-z0-9-]{0,31}$","default":"managed-executor"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}, {Type: "MachineAccess", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "OCIExecutorEndpoint", Version: "v1alpha1"}, {Type: "OCIExecutorCredential", Version: "v1alpha1"}},
		Capabilities:    []string{"executor.oci.provision", "executor.oci.preflight", "executor.oci.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"network.ssh", "executor.manage"},
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
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`).MatchString(spec.ExecutorName) {
		return invalid(report, "executorName", "Executor name must be a lowercase label with at most 32 characters.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will install the managed rootless executor only after validating machine ownership, isolation, capacity, network and TLS prerequisites."})
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
		ID: "install-rootless-executor", Name: "Install and verify managed OCI executor", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "machines", Type: "MachineSet", Version: "v1alpha1", ArtifactID: spec.MachineSetRef},
			{Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", ArtifactID: spec.MachineAccessRef},
		},
		Outputs: []domain.ArtifactOutput{
			{Name: "executor-endpoint", Type: "OCIExecutorEndpoint", Version: "v1alpha1", MediaType: "application/json", Source: "/executorEndpoint"},
			{Name: "executor-credential", Type: "OCIExecutorCredential", Version: "v1alpha1", MediaType: "application/json", Source: "/executorCredential", Sensitive: true},
		},
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
	command := installPrecheckCommand(target.SSHUser)
	if step.Cleanup {
		command = cleanupPrecheckCommand(input.Marker, target.SSHUser)
	}
	if err := log("info", "Validating the dedicated executor machine, rootless prerequisites, port, capacity and network"); err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := plugin.runner().Run(ctx, target, access, command); err != nil {
		return unhealthy("OCI-executor precheck failed: "+err.Error(), "machine", "blocked"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The dedicated machine is ready for the managed rootless executor", Checks: map[string]string{"machines": "1", "ssh": "verified", "sudo": "verified", "os": "ubuntu-24.04-amd64", "userNamespaces": "enabled", "port": "2376-free"}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, machines, access, err := resolve(step)
	if err != nil {
		return nil, err
	}
	target := machines.Spec.Machines[0]
	if err := log("info", "Installing the versioned rootless executor and generating its mutual-TLS identity"); err != nil {
		return nil, err
	}
	output, err := plugin.runner().Run(ctx, target, access, installCommand(input.Marker, target))
	if err != nil {
		return nil, err
	}
	ca, err := decodeMarker(output, caMarker)
	if err != nil {
		return nil, err
	}
	certificate, err := decodeMarker(output, certMarker)
	if err != nil {
		return nil, err
	}
	privateKey, err := decodeMarker(output, keyMarker)
	if err != nil {
		return nil, err
	}
	credential := executorCredential{
		APIVersion: artifactAPI, Kind: "OCIExecutorCredential",
		Metadata: executorCredentialMetadata{Name: input.ExecutorName + "-client", Role: "client"},
		Spec:     executorCredentialSpec{CA: ca, Certificate: certificate, PrivateKey: privateKey},
	}
	if err := validateCredential(credential); err != nil {
		return nil, err
	}
	value := result{
		ExecutorEndpoint: executorEndpoint{
			APIVersion: artifactAPI, Kind: "OCIExecutorEndpoint",
			Metadata: executorEndpointMetadata{Name: input.ExecutorName, Version: dockerVersion},
			Spec:     executorEndpointSpec{Host: "tcp://" + target.Address + ":2376", Address: target.Address, Port: executorPort, Transport: "docker", Security: "rootless-mtls"},
		},
		ExecutorCredential: credential,
	}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, machines, access, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	target := machines.Spec.Machines[0]
	if step.Cleanup {
		if _, err := plugin.runner().Run(ctx, target, access, cleanupVerifyCommand(target.SSHUser)); err != nil {
			return unhealthy("OCI-executor cleanup verification failed: "+err.Error(), "managedResources", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed executor service, data and configuration were removed", Checks: map[string]string{"service": "absent", "port": "closed", "managedFiles": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateResult(input, target, value); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	if _, err := plugin.runner().Run(ctx, target, access, readinessCommand(input.Marker, target)); err != nil {
		return unhealthy("OCI-executor health gate failed: "+err.Error(), "executor", "unhealthy"), nil
	}
	if err := plugin.verifyEndpoint(ctx, target, value.ExecutorCredential); err != nil {
		return unhealthy("OCI-executor remote TLS gate failed: "+err.Error(), "executor", "unreachable"), nil
	}
	if err := log("info", "Executor version, rootless isolation, mutual TLS and remote API are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed OCI executor is ready", Checks: map[string]string{"version": dockerVersion, "isolation": "rootless", "tls": "mutual", "server": target.Address + ":2376"}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, machines, access, err := resolve(step)
	if err != nil {
		return err
	}
	if err := log("warning", "Removing only the managed rootless executor service, data, packages and repository configuration"); err != nil {
		return err
	}
	target := machines.Spec.Machines[0]
	_, err = plugin.runner().Run(ctx, target, access, cleanupCommand(input.Marker, target.SSHUser))
	return err
}

func (plugin Plugin) runner() commandRunner {
	if plugin.Runner != nil {
		return plugin.Runner
	}
	return &sshRunner{client: pluginssh.New()}
}

func (plugin Plugin) verifyEndpoint(ctx context.Context, target machine, credential executorCredential) error {
	if plugin.endpointVerifier != nil {
		return plugin.endpointVerifier(ctx, target, credential)
	}
	return verifyRemoteEndpoint(ctx, target, credential)
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
		return errors.New("managed executor requires one dedicated machine")
	}
	_, network, err := net.ParseCIDR(machines.Spec.NetworkCIDR)
	if err != nil || network.String() != machines.Spec.NetworkCIDR {
		return errors.New("machine topology does not contain a canonical managed network")
	}
	target := machines.Spec.Machines[0]
	address := net.ParseIP(target.Address)
	if target.Name == "" || target.SSHPort != 22 || !regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`).MatchString(target.SSHUser) || target.State != "running" || address == nil || address.To4() == nil || !network.Contains(address) {
		return errors.New("dedicated executor machine is invalid")
	}
	if target.Cores < 2 || target.MemoryMiB < 4096 || target.DiskGiB < 20 {
		return errors.New("dedicated executor machine requires at least 2 cores, 4096 MiB of memory and 20 GiB of disk")
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

func validateResult(input stepInput, target machine, value result) error {
	endpoint := value.ExecutorEndpoint
	if endpoint.APIVersion != artifactAPI || endpoint.Kind != "OCIExecutorEndpoint" || endpoint.Metadata.Name != input.ExecutorName || endpoint.Metadata.Version != dockerVersion || endpoint.Spec.Host != "tcp://"+target.Address+":2376" || endpoint.Spec.Address != target.Address || endpoint.Spec.Port != executorPort || endpoint.Spec.Transport != "docker" || endpoint.Spec.Security != "rootless-mtls" {
		return errors.New("executor endpoint does not match the validated plan")
	}
	if value.ExecutorCredential.Metadata.Name != input.ExecutorName+"-client" {
		return errors.New("executor credential does not match the validated plan")
	}
	return validateCredential(value.ExecutorCredential)
}

func validateCredential(value executorCredential) error {
	if value.APIVersion != artifactAPI || value.Kind != "OCIExecutorCredential" || value.Metadata.Name == "" || value.Metadata.Role != "client" {
		return errors.New("executor credential identity is invalid")
	}
	pair, err := tls.X509KeyPair([]byte(value.Spec.Certificate), []byte(value.Spec.PrivateKey))
	if err != nil || len(pair.Certificate) != 1 {
		return errors.New("executor client certificate and key are invalid")
	}
	block, rest := pem.Decode([]byte(value.Spec.CA))
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("executor CA is invalid")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !ca.IsCA || ca.CheckSignatureFrom(ca) != nil || time.Now().Before(ca.NotBefore) || !time.Now().Before(ca.NotAfter) {
		return errors.New("executor CA is not a valid active self-signed CA")
	}
	client, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return errors.New("executor client certificate is invalid")
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := client.Verify(x509.VerifyOptions{Roots: pool, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, CurrentTime: time.Now()}); err != nil {
		return errors.New("executor client certificate is not trusted by its CA")
	}
	return nil
}

func installPrecheckCommand(user string) string {
	checks := []struct {
		name    string
		command string
	}{
		{"non-interactive sudo is unavailable", "sudo -n true"},
		{"the executor user is invalid", "test \"$(id -u " + shellQuote(user) + ")\" -ge 1000"},
		{"Ubuntu 24.04 is required", ". /etc/os-release && test \"$ID\" = ubuntu && test \"$VERSION_ID\" = 24.04"},
		{"amd64 architecture is required", "test \"$(dpkg --print-architecture)\" = amd64"},
		{"systemd is unavailable", "test \"$(cat /proc/1/comm)\" = systemd && command -v systemctl >/dev/null && command -v loginctl >/dev/null"},
		{"apt-get is unavailable", "command -v apt-get >/dev/null"},
		{"curl is unavailable", "command -v curl >/dev/null"},
		{"ss is unavailable", "command -v ss >/dev/null"},
		{"an existing Docker installation was detected", "! command -v docker >/dev/null && test ! -e /var/run/docker.sock"},
		{"the executor ownership marker already exists", "test ! -e " + markerFile},
		{"the Docker repository configuration already exists", "test ! -e " + dockerKeyPath + " && test ! -e " + dockerSourcePath},
		{"the rootless service configuration already exists", "executor_home=$(getent passwd " + shellQuote(user) + " | cut -d: -f6) && test -n \"$executor_home\" && test ! -e \"$executor_home/.config/systemd/user/docker.service\" && test ! -e \"$executor_home/.config/systemd/user/kubephos-oci-proxy.service\" && test ! -e \"$executor_home/.local/share/docker\""},
		{"unprivileged user namespaces are disabled", "test ! -e /proc/sys/kernel/unprivileged_userns_clone || test \"$(cat /proc/sys/kernel/unprivileged_userns_clone)\" = 1"},
		{"no user namespaces are available", "test ! -e /proc/sys/user/max_user_namespaces || test \"$(cat /proc/sys/user/max_user_namespaces)\" -gt 0"},
		{"at least 2 CPU cores are required", "test \"$(nproc)\" -ge 2"},
		{"at least 3.5 GiB of memory is required", "awk '/MemTotal:/ {exit !($2 >= 3500000)}' /proc/meminfo"},
		{"at least 12 GiB of free disk is required", "df -Pk / | awk 'NR == 2 {exit !($4 >= 12582912)}'"},
		{"TCP port 2376 is already in use", "! sudo ss -ltn 'sport = :2376' | grep -q LISTEN"},
		{"the Docker package repository is unreachable", "curl -fsSIL --max-time 20 https://download.docker.com/linux/ubuntu/dists/noble/Release >/dev/null"},
	}
	return guardedCommands(checks)
}

func installCommand(marker string, target machine) string {
	user := shellQuote(target.SSHUser)
	address := target.Address
	serviceOverride := "[Service]\nExecStart=\nExecStart=/usr/bin/dockerd-rootless.sh -H unix://%t/docker.sock\n"
	proxyUnit := "[Unit]\nDescription=KubePhos OCI mTLS proxy\nAfter=docker.service\nRequires=docker.service\n\n[Service]\nExecStart=/usr/bin/socat -t 86400 OPENSSL-LISTEN:2376,reuseaddr,fork,cert=%h/.config/docker/tls/server-cert.pem,key=%h/.config/docker/tls/server-key.pem,cafile=%h/.config/docker/tls/ca.pem,verify=1 UNIX-CONNECT:%t/docker.sock\nRestart=always\nRestartSec=2\n\n[Install]\nWantedBy=default.target\n"
	serverExtensions := "subjectAltName=IP:" + address + "\nextendedKeyUsage=serverAuth\n"
	clientExtensions := "extendedKeyUsage=clientAuth\n"
	parts := []string{
		"sudo apt-get " + aptNetworkOptions + " update -q",
		"sudo DEBIAN_FRONTEND=noninteractive apt-get " + aptNetworkOptions + " install -y ca-certificates curl gnupg uidmap dbus-user-session slirp4netns fuse-overlayfs socat",
		"curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /tmp/kubephos-docker.asc",
		"test \"$(gpg --show-keys --with-colons /tmp/kubephos-docker.asc | awk -F: '$1 == \"fpr\" {print $10; exit}')\" = " + dockerKeyFingerprint,
		"sudo install -m 0644 /tmp/kubephos-docker.asc " + dockerKeyPath,
		"printf '%s\\n' 'Types: deb' 'URIs: https://download.docker.com/linux/ubuntu' 'Suites: noble' 'Components: stable' 'Architectures: amd64' 'Signed-By: " + dockerKeyPath + "' | sudo tee " + dockerSourcePath + " >/dev/null",
		"sudo apt-get " + aptNetworkOptions + " update -q",
		"apt-cache madison docker-ce | awk '{print $3}' | grep -Fxq " + shellQuote(dockerPackageVersion),
		"sudo DEBIAN_FRONTEND=noninteractive apt-get " + aptNetworkOptions + " install -y docker-ce=" + shellQuote(dockerPackageVersion) + " docker-ce-cli=" + shellQuote(dockerPackageVersion) + " containerd.io docker-buildx-plugin docker-ce-rootless-extras=" + shellQuote(dockerPackageVersion),
		"sudo systemctl disable --now docker.service docker.socket containerd.service",
		"sudo rm -f /var/run/docker.sock",
		"sudo install -d -m 0755 /var/lib/kubephos",
		"printf '%s\\n' " + shellQuote(marker) + " | sudo tee " + markerFile + " >/dev/null",
		"executor_uid=$(id -u " + user + ")",
		"executor_home=$(getent passwd " + user + " | cut -d: -f6)",
		"test -n \"$executor_home\"",
		"grep -Eq '^" + target.SSHUser + ":[0-9]+:[0-9]{5,}$' /etc/subuid && grep -Eq '^" + target.SSHUser + ":[0-9]+:[0-9]{5,}$' /etc/subgid",
		"sudo loginctl enable-linger " + user,
		"sudo systemctl start user@\"$executor_uid\".service",
		"executor_runtime=/run/user/$executor_uid",
		"sudo -u " + user + " env HOME=\"$executor_home\" XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" PATH=/usr/bin:/bin dockerd-rootless-setuptool.sh install --force",
		"sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" systemctl --user stop docker.service",
		"sudo install -d -m 0700 -o " + user + " -g " + user + " \"$executor_home/.config/docker/tls\" \"$executor_home/.config/systemd/user/docker.service.d\"",
		"sudo openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 3650 -subj '/CN=KubePhos OCI Executor CA' -addext 'basicConstraints=critical,CA:TRUE' -addext 'keyUsage=critical,keyCertSign,cRLSign' -keyout \"$executor_home/.config/docker/tls/ca-key.pem\" -out \"$executor_home/.config/docker/tls/ca.pem\"",
		"sudo openssl req -newkey rsa:3072 -sha256 -nodes -subj '/CN=" + address + "' -keyout \"$executor_home/.config/docker/tls/server-key.pem\" -out \"$executor_home/.config/docker/tls/server.csr\"",
		"printf '%s' " + shellQuote(serverExtensions) + " | sudo tee \"$executor_home/.config/docker/tls/server.ext\" >/dev/null",
		"sudo openssl x509 -req -sha256 -days 825 -in \"$executor_home/.config/docker/tls/server.csr\" -CA \"$executor_home/.config/docker/tls/ca.pem\" -CAkey \"$executor_home/.config/docker/tls/ca-key.pem\" -CAcreateserial -extfile \"$executor_home/.config/docker/tls/server.ext\" -out \"$executor_home/.config/docker/tls/server-cert.pem\"",
		"sudo openssl req -newkey rsa:3072 -sha256 -nodes -subj '/CN=KubePhos control plane' -keyout \"$executor_home/.config/docker/tls/client-key.pem\" -out \"$executor_home/.config/docker/tls/client.csr\"",
		"printf '%s' " + shellQuote(clientExtensions) + " | sudo tee \"$executor_home/.config/docker/tls/client.ext\" >/dev/null",
		"sudo openssl x509 -req -sha256 -days 825 -in \"$executor_home/.config/docker/tls/client.csr\" -CA \"$executor_home/.config/docker/tls/ca.pem\" -CAkey \"$executor_home/.config/docker/tls/ca-key.pem\" -CAcreateserial -extfile \"$executor_home/.config/docker/tls/client.ext\" -out \"$executor_home/.config/docker/tls/client-cert.pem\"",
		"printf '%s' " + shellQuote(serviceOverride) + " | sudo -u " + user + " tee \"$executor_home/.config/systemd/user/docker.service.d/kubephos.conf\" >/dev/null",
		"printf '%s' " + shellQuote(proxyUnit) + " | sudo -u " + user + " tee \"$executor_home/.config/systemd/user/kubephos-oci-proxy.service\" >/dev/null",
		"sudo chown -R " + user + ":" + user + " \"$executor_home/.config/docker\" \"$executor_home/.config/systemd/user\"",
		"sudo chmod 0600 \"$executor_home/.config/docker/tls/ca-key.pem\" \"$executor_home/.config/docker/tls/server-key.pem\" \"$executor_home/.config/docker/tls/client-key.pem\"",
		"sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" systemctl --user daemon-reload",
		"sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" systemctl --user restart docker.service || { sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" systemctl --user status docker.service --no-pager; sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" journalctl --user -u docker.service --no-pager -n 80; exit 1; }",
		"{ attempt=0; until sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" docker --host unix://\"$executor_runtime/docker.sock\" info --format '{{json .SecurityOptions}}' | grep -q rootless; do attempt=$((attempt + 1)); test \"$attempt\" -lt 61 || exit 1; sleep 2; done; }",
		"test \"$(sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" docker --host unix://\"$executor_runtime/docker.sock\" version --format '{{.Server.Version}}')\" = " + dockerVersion,
		"sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" systemctl --user enable --now kubephos-oci-proxy.service || { sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" systemctl --user status kubephos-oci-proxy.service --no-pager; sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" journalctl --user -u kubephos-oci-proxy.service --no-pager -n 80; exit 1; }",
		"{ attempt=0; until sudo ss -ltn 'sport = :2376' | grep -q LISTEN; do attempt=$((attempt + 1)); test \"$attempt\" -lt 31 || exit 1; sleep 1; done; }",
		"printf '\\n" + caMarker + "'",
		"sudo base64 -w0 \"$executor_home/.config/docker/tls/ca.pem\"",
		"printf '\\n" + certMarker + "'",
		"sudo base64 -w0 \"$executor_home/.config/docker/tls/client-cert.pem\"",
		"printf '\\n" + keyMarker + "'",
		"sudo base64 -w0 \"$executor_home/.config/docker/tls/client-key.pem\"",
		"sudo rm -f \"$executor_home/.config/docker/tls/ca-key.pem\" \"$executor_home/.config/docker/tls/ca.srl\" \"$executor_home/.config/docker/tls/server.csr\" \"$executor_home/.config/docker/tls/server.ext\" \"$executor_home/.config/docker/tls/client.csr\" \"$executor_home/.config/docker/tls/client.ext\" \"$executor_home/.config/docker/tls/client-key.pem\" \"$executor_home/.config/docker/tls/client-cert.pem\" /tmp/kubephos-docker.asc",
	}
	return strings.Join(parts, " && ")
}

func readinessCommand(marker string, target machine) string {
	user := shellQuote(target.SSHUser)
	docker := "sudo -u " + user + " env XDG_RUNTIME_DIR=\"$executor_runtime\" docker --host unix://\"$executor_runtime/docker.sock\""
	checks := []struct {
		name    string
		command string
	}{
		{"the executor ownership marker does not match", "sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker)},
		{"the executor TLS port is not listening", "sudo ss -ltn 'sport = :2376' | grep -q LISTEN"},
		{"the rootful Docker service is active", "! sudo systemctl is-active --quiet docker.service && ! sudo systemctl is-active --quiet docker.socket"},
		{"the executor version does not match the managed profile", "test \"$(" + docker + " version --format '{{.Server.Version}}')\" = " + dockerVersion},
		{"the executor is not rootless", docker + " info --format '{{json .SecurityOptions}}' | grep -q rootless"},
	}
	return "executor_uid=$(id -u " + user + ") && executor_runtime=/run/user/$executor_uid && " + guardedCommands(checks)
}

func verifyRemoteEndpoint(ctx context.Context, target machine, credential executorCredential) error {
	pair, err := tls.X509KeyPair([]byte(credential.Spec.Certificate), []byte(credential.Spec.PrivateKey))
	if err != nil {
		return errors.New("client identity is invalid")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(credential.Spec.CA)) {
		return errors.New("executor CA is invalid")
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: pool, ServerName: target.Address, MinVersion: tls.VersionTLS12},
		DialContext:     (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+net.JoinHostPort(target.Address, strconv.Itoa(executorPort))+"/_ping", nil)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("remote API handshake failed: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 16))
	if err != nil || response.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "OK" {
		return errors.New("remote API did not return the Docker ping response")
	}
	return nil
}

func cleanupPrecheckCommand(marker, user string) string {
	return "sudo -n true && if test -e " + markerFile + "; then sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker) + "; else executor_home=$(getent passwd " + shellQuote(user) + " | cut -d: -f6) && test ! -e \"$executor_home/.config/systemd/user/docker.service\" && test ! -e \"$executor_home/.local/share/docker\" && test ! -e " + dockerSourcePath + " && test ! -e " + dockerKeyPath + "; fi"
}

func cleanupCommand(marker, user string) string {
	return "if test ! -e " + markerFile + "; then exit 0; fi && sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker) + " && executor_uid=$(id -u " + shellQuote(user) + ") && executor_home=$(getent passwd " + shellQuote(user) + " | cut -d: -f6) && executor_runtime=/run/user/$executor_uid && { sudo -u " + shellQuote(user) + " env XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" systemctl --user disable --now kubephos-oci-proxy.service >/dev/null 2>&1 || true; } && { sudo -u " + shellQuote(user) + " env HOME=\"$executor_home\" XDG_RUNTIME_DIR=\"$executor_runtime\" DBUS_SESSION_BUS_ADDRESS=unix:path=\"$executor_runtime/bus\" PATH=/usr/bin:/bin dockerd-rootless-setuptool.sh uninstall --force >/dev/null 2>&1 || true; } && sudo rm -rf -- \"$executor_home/.local/share/docker\" \"$executor_home/.config/docker\" \"$executor_home/.config/systemd/user/docker.service\" \"$executor_home/.config/systemd/user/docker.service.d\" \"$executor_home/.config/systemd/user/kubephos-oci-proxy.service\" && sudo rm -f " + markerFile + " " + dockerSourcePath + " " + dockerKeyPath + " && sudo apt-get " + aptNetworkOptions + " purge -y docker-ce docker-ce-cli docker-ce-rootless-extras docker-buildx-plugin containerd.io >/dev/null && sudo apt-get " + aptNetworkOptions + " update -q"
}

func cleanupVerifyCommand(user string) string {
	return "executor_home=$(getent passwd " + shellQuote(user) + " | cut -d: -f6) && test ! -e " + markerFile + " && test ! -e " + dockerSourcePath + " && test ! -e " + dockerKeyPath + " && test ! -e \"$executor_home/.config/systemd/user/docker.service\" && test ! -e \"$executor_home/.config/systemd/user/kubephos-oci-proxy.service\" && test ! -e \"$executor_home/.local/share/docker\" && ! sudo ss -ltn 'sport = :2376' | grep -q LISTEN"
}

func guardedCommands(checks []struct {
	name    string
	command string
}) string {
	commands := make([]string, 0, len(checks))
	for _, check := range checks {
		commands = append(commands, "{ "+check.command+"; } || { printf '%s\\n' "+shellQuote(check.name)+"; exit 1; }")
	}
	return strings.Join(commands, "; ")
}

func decodeMarker(output, marker string) (string, error) {
	position := strings.LastIndex(output, marker)
	if position < 0 {
		return "", errors.New("executor did not return required TLS material")
	}
	line := output[position+len(marker):]
	if end := strings.IndexAny(line, "\r\n"); end >= 0 {
		line = line[:end]
	}
	value, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line))
	if err != nil || len(value) == 0 || len(value) > 32<<10 {
		return "", errors.New("executor returned invalid TLS material")
	}
	return string(value), nil
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
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
	return runner.client.Run(ctx, pluginssh.Target{Address: target.Address, Port: target.SSHPort, User: target.SSHUser}, access.Spec.PrivateKey, command)
}
