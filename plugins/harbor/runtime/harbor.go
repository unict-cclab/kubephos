package harbor

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
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
	pluginID        = "io.kubephos.registry.harbor.managed"
	artifactAPI     = "artifacts.kubephos.dev/v1alpha1"
	harborVersion   = "v2.15.1"
	installerURL    = "https://github.com/goharbor/harbor/releases/download/v2.15.1/harbor-online-installer-v2.15.1.tgz"
	installerDigest = "35bfb53ee272b59134d118ff60dceb483fbf6ff6e43060c9ebec3f72a919ed1b"
	installPath     = "/opt/harbor"
	dataPath        = "/srv/kubephos-harbor"
	logPath         = "/var/log/harbor"
	tlsPath         = "/opt/harbor/tls"
	markerFile      = "/var/lib/kubephos/harbor.marker"
	development     = "kubephos-dev"
	releases        = "kubephos-releases"
	robotMarker     = "KUBEPHOS_ROBOT="
	caMarker        = "KUBEPHOS_CA="
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
	RegistryName     string `json:"registryName"`
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

type registryEndpoint struct {
	APIVersion string                   `json:"apiVersion"`
	Kind       string                   `json:"kind"`
	Metadata   registryEndpointMetadata `json:"metadata"`
	Spec       registryEndpointSpec     `json:"spec"`
}

type registryEndpointMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type registryEndpointSpec struct {
	Protocol string   `json:"protocol"`
	Host     string   `json:"host"`
	URL      string   `json:"url"`
	APIURL   string   `json:"apiURL"`
	CABundle string   `json:"caBundle"`
	Insecure bool     `json:"insecure"`
	Projects []string `json:"projects"`
}

type registryCredential struct {
	APIVersion string                     `json:"apiVersion"`
	Kind       string                     `json:"kind"`
	Metadata   registryCredentialMetadata `json:"metadata"`
	Spec       registryCredentialSpec     `json:"spec"`
}

type registryCredentialMetadata struct {
	Name string `json:"name"`
	Role string `json:"role"`
}

type registryCredentialSpec struct {
	Server   string   `json:"server"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	Project  string   `json:"project,omitempty"`
	Scopes   []string `json:"scopes"`
}

type result struct {
	RegistryEndpoint             registryEndpoint   `json:"registryEndpoint"`
	RegistryPushCredential       registryCredential `json:"registryPushCredential"`
	RegistryManagementCredential registryCredential `json:"registryManagementCredential"`
}

type robotCreated struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Secret string `json:"secret"`
}

type commandRunner interface {
	Run(context.Context, machine, machineAccess, string) (string, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Managed OCI registry", Version: "0.2.0",
		Description:     "Installs and validates a managed OCI registry on one dedicated machine.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["machineSetRef","machineAccessRef","registryName"],"properties":{"machineSetRef":{"type":"string","title":"Registry machine","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineSet","x-kubephos-artifact-version":"v1alpha1"},"machineAccessRef":{"type":"string","title":"Machine access","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"MachineAccess","x-kubephos-artifact-version":"v1alpha1"},"registryName":{"type":"string","title":"Registry name","pattern":"^[a-z0-9][a-z0-9-]{0,31}$","default":"managed-registry"}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}, {Type: "MachineAccess", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "RegistryEndpoint", Version: "v1alpha1"}, {Type: "RegistryCredential", Version: "v1alpha1"}},
		Capabilities:    []string{"registry.oci.provision", "registry.oci.preflight", "registry.oci.cleanup", "lifecycle.cleanup"},
		Permissions:     []string{"network.ssh", "registry.manage"},
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
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`).MatchString(spec.RegistryName) {
		return invalid(report, "registryName", "Registry name must be a lowercase label with at most 32 characters.")
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "KubePhos will install the managed registry profile after validating capacity, network access and machine ownership."})
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
		ID: "install-managed-registry", Name: "Install and verify managed OCI registry", Input: input, Mutating: true,
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "machines", Type: "MachineSet", Version: "v1alpha1", ArtifactID: spec.MachineSetRef},
			{Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", ArtifactID: spec.MachineAccessRef},
		},
		Outputs: []domain.ArtifactOutput{
			{Name: "registry-endpoint", Type: "RegistryEndpoint", Version: "v1alpha1", MediaType: "application/json", Source: "/registryEndpoint"},
			{Name: "registry-push-credential", Type: "RegistryCredential", Version: "v1alpha1", MediaType: "application/json", Source: "/registryPushCredential", Sensitive: true},
			{Name: "registry-management-credential", Type: "RegistryCredential", Version: "v1alpha1", MediaType: "application/json", Source: "/registryManagementCredential", Sensitive: true},
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
	command := installPrecheckCommand()
	if step.Cleanup {
		command = cleanupPrecheckCommand(input.Marker)
	}
	if err := log("info", "Validating the dedicated registry machine, capacity, ports, network and non-interactive sudo access"); err != nil {
		return domain.HealthReport{}, err
	}
	if _, err := plugin.runner().Run(ctx, target, access, command); err != nil {
		return unhealthy("Managed-registry precheck failed: "+err.Error(), "machine", "blocked"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "The dedicated machine is ready for the managed-registry action", Checks: map[string]string{"machines": "1", "ssh": "verified", "sudo": "verified", "cpu": "at-least-2", "memory": "at-least-3.5GiB", "disk": "at-least-8GiB", "network": machines.Spec.NetworkCIDR}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, machines, access, err := resolve(step)
	if err != nil {
		return nil, err
	}
	adminPassword, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	databasePassword, err := randomHex(24)
	if err != nil {
		return nil, err
	}
	robotSecret, err := randomHex(32)
	if err != nil {
		return nil, err
	}
	target := machines.Spec.Machines[0]
	if err := log("info", "Installing the digest-verified managed registry release"); err != nil {
		return nil, err
	}
	output, err := plugin.runner().Run(ctx, target, access, installCommand(input.Marker, target.Address, adminPassword, databasePassword, robotSecret))
	if err != nil {
		return nil, err
	}
	robotValue := extractMarkedLine(output, robotMarker)
	var robot robotCreated
	decodeErr := json.Unmarshal([]byte(robotValue), &robot)
	if robotValue == "" || decodeErr != nil || robot.ID <= 0 || robot.Name == "" {
		return nil, errors.New("managed registry did not return a valid robot identity")
	}
	robotPassword := robot.Secret
	if robotPassword == "" {
		robotPassword = robotSecret
	}
	caEncoded := extractMarkedLine(output, caMarker)
	caBytes, decodeErr := base64.StdEncoding.DecodeString(caEncoded)
	if caEncoded == "" || decodeErr != nil || validateCABundle(string(caBytes)) != nil {
		return nil, errors.New("managed registry did not return a valid certificate authority")
	}
	baseURL := "https://" + target.Address
	value := result{
		RegistryEndpoint: registryEndpoint{
			APIVersion: artifactAPI, Kind: "RegistryEndpoint",
			Metadata: registryEndpointMetadata{Name: input.RegistryName, Version: harborVersion},
			Spec:     registryEndpointSpec{Protocol: "oci", Host: target.Address, URL: baseURL, APIURL: baseURL + "/api/v2.0", CABundle: string(caBytes), Insecure: false, Projects: []string{development, releases}},
		},
		RegistryPushCredential: registryCredential{
			APIVersion: artifactAPI, Kind: "RegistryCredential",
			Metadata: registryCredentialMetadata{Name: input.RegistryName + "-push", Role: "push"},
			Spec:     registryCredentialSpec{Server: target.Address, Username: robot.Name, Password: robotPassword, Project: development, Scopes: []string{"pull", "push"}},
		},
		RegistryManagementCredential: registryCredential{
			APIVersion: artifactAPI, Kind: "RegistryCredential",
			Metadata: registryCredentialMetadata{Name: input.RegistryName + "-management", Role: "management"},
			Spec:     registryCredentialSpec{Server: target.Address, Username: "admin", Password: adminPassword, Scopes: []string{"manage"}},
		},
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
		if _, err := plugin.runner().Run(ctx, target, access, cleanupVerifyCommand()); err != nil {
			return unhealthy("Managed-registry cleanup verification failed: "+err.Error(), "managedResources", "present"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed registry containers, configuration and data were removed", Checks: map[string]string{"containers": "absent", "managedFiles": "absent"}}, nil
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateResult(input, target, value); err != nil {
		return unhealthy(err.Error(), "artifact", "invalid"), nil
	}
	if _, err := plugin.runner().Run(ctx, target, access, readinessCommand(input.Marker, target.Address, value.RegistryManagementCredential.Spec.Password, value.RegistryPushCredential.Spec.Username, value.RegistryPushCredential.Spec.Password)); err != nil {
		return unhealthy("Managed-registry health gate failed: "+err.Error(), "registry", "unhealthy"), nil
	}
	if err := log("info", "Registry health, installed version, managed projects, management access and robot token are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed OCI registry is ready", Checks: map[string]string{"health": "healthy", "tls": "verified", "version": harborVersion, "projects": "2", "robot": "verified", "server": target.Address}}, nil
}

func (plugin Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, log plugins.Logger) error {
	input, machines, access, err := resolve(step)
	if err != nil {
		return err
	}
	if err := log("warning", "Removing only the managed registry containers, configuration, logs, marker and data"); err != nil {
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
		return errors.New("managed registry requires one dedicated machine")
	}
	_, network, err := net.ParseCIDR(machines.Spec.NetworkCIDR)
	if err != nil || network.String() != machines.Spec.NetworkCIDR {
		return errors.New("machine topology does not contain a canonical managed network")
	}
	target := machines.Spec.Machines[0]
	address := net.ParseIP(target.Address)
	if target.Name == "" || target.SSHPort != 22 || target.SSHUser == "" || target.State != "running" || address == nil || address.To4() == nil || !network.Contains(address) {
		return errors.New("dedicated registry machine is invalid")
	}
	if target.Cores < 2 || target.MemoryMiB < 4096 || target.DiskGiB < 16 {
		return errors.New("dedicated registry machine requires at least 2 cores, 4096 MiB of memory and 16 GiB of disk")
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
	endpoint := value.RegistryEndpoint
	if endpoint.APIVersion != artifactAPI || endpoint.Kind != "RegistryEndpoint" || endpoint.Metadata.Name != input.RegistryName || endpoint.Metadata.Version != harborVersion || endpoint.Spec.Protocol != "oci" || endpoint.Spec.Host != target.Address || endpoint.Spec.URL != "https://"+target.Address || endpoint.Spec.APIURL != "https://"+target.Address+"/api/v2.0" || endpoint.Spec.Insecure || len(endpoint.Spec.Projects) != 2 || endpoint.Spec.Projects[0] != development || endpoint.Spec.Projects[1] != releases {
		return errors.New("registry endpoint does not match the validated plan")
	}
	if err := validateCABundle(endpoint.Spec.CABundle); err != nil {
		return errors.New("registry certificate authority is invalid")
	}
	push := value.RegistryPushCredential
	if push.APIVersion != artifactAPI || push.Kind != "RegistryCredential" || push.Metadata.Role != "push" || push.Spec.Server != target.Address || push.Spec.Username == "" || push.Spec.Password == "" || push.Spec.Project != development || len(push.Spec.Scopes) != 2 || push.Spec.Scopes[0] != "pull" || push.Spec.Scopes[1] != "push" {
		return errors.New("registry push credential is invalid")
	}
	management := value.RegistryManagementCredential
	if management.APIVersion != artifactAPI || management.Kind != "RegistryCredential" || management.Metadata.Role != "management" || management.Spec.Server != target.Address || management.Spec.Username != "admin" || management.Spec.Password == "" || len(management.Spec.Scopes) != 1 || management.Spec.Scopes[0] != "manage" {
		return errors.New("registry management credential is invalid")
	}
	return nil
}

func installPrecheckCommand() string {
	checks := []struct {
		name    string
		command string
	}{
		{"non-interactive sudo is unavailable", "sudo -n true"},
		{"apt-get is unavailable", "command -v apt-get >/dev/null"},
		{"curl is unavailable", "command -v curl >/dev/null"},
		{"ss is unavailable", "command -v ss >/dev/null"},
		{"the Harbor install path is not clean", "test ! -e " + installPath},
		{"the Harbor data path is not clean", "test ! -e " + dataPath},
		{"the Harbor log path is not clean", "test ! -e " + logPath},
		{"the Harbor ownership marker already exists", "test ! -e " + markerFile},
		{"at least 2 CPU cores are required", "test \"$(nproc)\" -ge 2"},
		{"at least 3.5 GiB of memory is required", "awk '/MemTotal:/ {exit !($2 >= 3500000)}' /proc/meminfo"},
		{"at least 8 GiB of free disk is required", "df -Pk / | awk 'NR == 2 {exit !($4 >= 8388608)}'"},
		{"TCP port 80 is already in use", "! sudo ss -ltn 'sport = :80' | grep -q LISTEN"},
		{"TCP port 443 is already in use", "! sudo ss -ltn 'sport = :443' | grep -q LISTEN"},
		{"an existing Harbor compose project was detected", "if command -v docker >/dev/null; then test -z \"$(sudo docker ps -aq --filter label=com.docker.compose.project=harbor)\"; fi"},
		{"the managed Harbor installer is unreachable", "curl -fsSIL --max-time 20 " + shellQuote(installerURL) + " >/dev/null"},
	}
	commands := make([]string, 0, len(checks))
	for _, check := range checks {
		commands = append(commands, "{ "+check.command+"; } || { printf '%s\\n' "+shellQuote(check.name)+"; exit 1; }")
	}
	return strings.Join(commands, "; ")
}

func installCommand(marker, address, adminPassword, databasePassword, robotSecret string) string {
	projectRequest := `{"project_name":"` + development + `","metadata":{"public":"false"}}`
	releasesRequest := `{"project_name":"` + releases + `","metadata":{"public":"false"}}`
	robotRequest, _ := json.Marshal(map[string]any{
		"name": "builder", "description": "KubePhos managed build identity", "secret": robotSecret, "level": "project", "disable": false, "duration": -1,
		"permissions": []any{map[string]any{"kind": "project", "namespace": development, "access": []any{map[string]string{"resource": "repository", "action": "pull"}, map[string]string{"resource": "repository", "action": "push"}}}},
	})
	configurationEdits := []string{
		"s|^hostname:.*|hostname: " + address + "|",
		"s|^#*[[:space:]]*https:|https:|",
		"s|^#*[[:space:]]*port: 443|  port: 443|",
		"s|^#*[[:space:]]*certificate:.*|  certificate: " + tlsPath + "/server.crt|",
		"s|^#*[[:space:]]*private_key:.*|  private_key: " + tlsPath + "/server.key|",
		"s|^harbor_admin_password:.*|harbor_admin_password: " + adminPassword + "|",
		"s|^  password: root123$|  password: " + databasePassword + "|",
		"s|^data_volume:.*|data_volume: " + dataPath + "|",
	}
	parts := []string{
		"sudo apt-get update -q",
		"sudo DEBIAN_FRONTEND=noninteractive apt-get install -y ca-certificates curl docker.io docker-compose-v2 openssl",
		"sudo systemctl enable --now docker",
		"sudo docker compose version >/dev/null",
		"sudo install -d -m 0755 /var/lib/kubephos",
		"sudo install -d -m 0750 " + dataPath,
		"printf '%s\\n' " + shellQuote(marker) + " | sudo tee " + markerFile + " >/dev/null",
		"curl -fsSL " + shellQuote(installerURL) + " -o /tmp/kubephos-harbor.tgz",
		"printf '%s  %s\\n' " + shellQuote(installerDigest) + " /tmp/kubephos-harbor.tgz | sha256sum -c -",
		"sudo tar -xzf /tmp/kubephos-harbor.tgz -C /opt",
		"sudo install -d -m 0755 " + tlsPath,
		"sudo openssl req -x509 -newkey rsa:3072 -sha256 -nodes -days 3650 -subj '/CN=KubePhos Harbor CA' -addext 'basicConstraints=critical,CA:TRUE' -addext 'keyUsage=critical,keyCertSign,cRLSign' -keyout " + tlsPath + "/ca.key -out " + tlsPath + "/ca.crt",
		"sudo openssl req -newkey rsa:3072 -sha256 -nodes -subj '/CN=" + address + "' -keyout " + tlsPath + "/server.key -out " + tlsPath + "/server.csr",
		"printf '%s\\n' 'subjectAltName=IP:" + address + "' 'extendedKeyUsage=serverAuth' | sudo tee " + tlsPath + "/server.ext >/dev/null",
		"sudo openssl x509 -req -sha256 -days 825 -in " + tlsPath + "/server.csr -CA " + tlsPath + "/ca.crt -CAkey " + tlsPath + "/ca.key -CAcreateserial -extfile " + tlsPath + "/server.ext -out " + tlsPath + "/server.crt",
		"sudo chmod 0600 " + tlsPath + "/ca.key " + tlsPath + "/server.key",
		"sudo chmod 0644 " + tlsPath + "/ca.crt " + tlsPath + "/server.crt",
		"test -r " + tlsPath + "/ca.crt",
		"openssl verify -CAfile " + tlsPath + "/ca.crt " + tlsPath + "/server.crt >/dev/null",
		"sudo cp " + installPath + "/harbor.yml.tmpl " + installPath + "/harbor.yml",
	}
	for _, edit := range configurationEdits {
		parts = append(parts, "sudo sed -i -e "+shellQuote(edit)+" "+installPath+"/harbor.yml")
	}
	parts = append(parts,
		"cd "+installPath+" && sudo ./install.sh",
		"rm -f /tmp/kubephos-harbor.tgz",
		"{ attempt=0; until curl --connect-timeout 3 --max-time 10 --cacert "+tlsPath+"/ca.crt -fsS https://"+address+"/api/v2.0/health | grep -q '\"status\":\"healthy\"'; do attempt=$((attempt + 1)); test \"$attempt\" -lt 61 || exit 1; sleep 5; done; }",
		"test \"$(curl --cacert "+tlsPath+"/ca.crt -sS -o /tmp/kubephos-project.json -w '%{http_code}' -u "+shellQuote("admin:"+adminPassword)+" -H 'Content-Type: application/json' -d "+shellQuote(projectRequest)+" https://"+address+"/api/v2.0/projects)\" = 201",
		"test \"$(curl --cacert "+tlsPath+"/ca.crt -sS -o /tmp/kubephos-releases.json -w '%{http_code}' -u "+shellQuote("admin:"+adminPassword)+" -H 'Content-Type: application/json' -d "+shellQuote(releasesRequest)+" https://"+address+"/api/v2.0/projects)\" = 201",
		"printf '\\n"+robotMarker+"'",
		"curl --cacert "+tlsPath+"/ca.crt -fsS -u "+shellQuote("admin:"+adminPassword)+" -H 'Content-Type: application/json' -d "+shellQuote(string(robotRequest))+" https://"+address+"/api/v2.0/robots",
		"printf '\n"+caMarker+"'",
		"sudo base64 -w0 "+tlsPath+"/ca.crt",
	)
	return strings.Join(parts, " && ")
}

func readinessCommand(marker, address, adminPassword, robotName, robotSecret string) string {
	baseURL := "https://" + address
	curl := "curl --cacert " + tlsPath + "/ca.crt -fsS"
	checks := []struct {
		name    string
		command string
	}{
		{"the registry ownership marker does not match", "sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker)},
		{"the registry compose definition is missing", "test -f " + installPath + "/docker-compose.yml"},
		{"one or more registry containers are not running", "test \"$(sudo docker ps -q --filter label=com.docker.compose.project=harbor | wc -l)\" -ge 8"},
		{"the registry certificate does not match its IP address", "sudo openssl x509 -checkip " + address + " -noout -in " + tlsPath + "/server.crt"},
		{"the registry health endpoint is not healthy", curl + " " + baseURL + "/api/v2.0/health | grep -q '\"status\":\"healthy\"'"},
		{"the installed registry version does not match the managed profile", curl + " -u " + shellQuote("admin:"+adminPassword) + " " + baseURL + "/api/v2.0/systeminfo | grep -Fq " + shellQuote(harborVersion)},
		{"the development project is unavailable to the management identity", curl + " -u " + shellQuote("admin:"+adminPassword) + " '" + baseURL + "/api/v2.0/projects?name=" + development + "' | grep -Fq '\"name\":\"" + development + "\"'"},
		{"the releases project is unavailable to the management identity", curl + " -u " + shellQuote("admin:"+adminPassword) + " '" + baseURL + "/api/v2.0/projects?name=" + releases + "' | grep -Fq '\"name\":\"" + releases + "\"'"},
		{"the registry robot cannot obtain a scoped token", curl + " -u " + shellQuote(robotName+":"+robotSecret) + " '" + baseURL + "/service/token?service=harbor-registry&scope=repository%3A" + development + "%2Fkubephos-probe%3Apull%2Cpush' | grep -Fq '\"token\"'"},
	}
	return guardedCommands(checks)
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

func cleanupPrecheckCommand(marker string) string {
	return "sudo -n true && if test -e " + markerFile + "; then sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker) + "; else test ! -e " + installPath + " && test ! -e " + dataPath + " && test ! -e " + logPath + " && if command -v docker >/dev/null; then test -z \"$(sudo docker ps -aq --filter label=com.docker.compose.project=harbor)\"; fi; fi"
}

func cleanupCommand(marker string) string {
	return "if test ! -e " + markerFile + " && test ! -e " + installPath + " && test ! -e " + dataPath + " && test ! -e " + logPath + "; then exit 0; fi && sudo test \"$(sudo cat " + markerFile + ")\" = " + shellQuote(marker) + " && if test -f " + installPath + "/docker-compose.yml; then cd " + installPath + " && sudo docker compose down -v --remove-orphans; fi && sudo rm -rf -- " + installPath + " " + dataPath + " " + logPath + " && sudo rm -f " + markerFile
}

func cleanupVerifyCommand() string {
	return "test ! -e " + markerFile + " && test ! -e " + installPath + " && test ! -e " + dataPath + " && test ! -e " + logPath + " && if command -v docker >/dev/null; then test -z \"$(sudo docker ps -aq --filter label=com.docker.compose.project=harbor)\"; fi"
}

func randomHex(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}

func extractMarkedLine(value, marker string) string {
	position := strings.LastIndex(value, marker)
	if position < 0 {
		return ""
	}
	line := value[position+len(marker):]
	if end := strings.IndexAny(line, "\r\n"); end >= 0 {
		line = line[:end]
	}
	return strings.TrimSpace(line)
}

func validateCABundle(value string) error {
	block, rest := pem.Decode([]byte(value))
	if block == nil || block.Type != "CERTIFICATE" || len(strings.TrimSpace(string(rest))) != 0 {
		return errors.New("certificate authority must contain exactly one PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || !certificate.IsCA || certificate.CheckSignatureFrom(certificate) != nil || time.Now().Before(certificate.NotBefore) || !time.Now().Before(certificate.NotAfter) {
		return errors.New("certificate authority is not a valid active self-signed CA")
	}
	return nil
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
