package proxmoxvm

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const topologyPluginID = "io.kubephos.infrastructure.proxmox.topology"

type TopologyPlugin struct {
	SSH sshAccess
}

type sshAccess interface {
	Wait(context.Context, string, string, string, time.Duration) error
}

type networkSSHAccess struct{}

type TopologySpec struct {
	ConnectionRef    string `json:"connectionRef"`
	Node             string `json:"node"`
	TemplateVMID     int    `json:"templateVMID"`
	BaseVMID         int    `json:"baseVMID"`
	NamePrefix       string `json:"namePrefix"`
	MachineCount     int    `json:"machineCount"`
	Cores            int    `json:"cores"`
	MemoryMiB        int    `json:"memoryMiB"`
	DiskGiB          int    `json:"diskGiB"`
	SSHUser          string `json:"sshUser"`
	CleanupAfterTest bool   `json:"cleanupAfterTest"`
}

type topologyStepInput struct {
	Action        string           `json:"action"`
	ConnectionRef string           `json:"connectionRef"`
	Node          string           `json:"node"`
	TemplateVMID  int              `json:"templateVMID"`
	Cores         int              `json:"cores"`
	MemoryMiB     int              `json:"memoryMiB"`
	DiskGiB       int              `json:"diskGiB"`
	SSHUser       string           `json:"sshUser"`
	PrefixLength  int              `json:"prefixLength"`
	Gateway       string           `json:"gateway"`
	DNSServer     string           `json:"dnsServer"`
	Marker        string           `json:"marker"`
	Machines      []plannedMachine `json:"machines"`
}

type plannedMachine struct {
	VMID    int    `json:"vmid"`
	Name    string `json:"name"`
	Address string `json:"address"`
}

type topologyResult struct {
	Resources     []domain.DiscoveredResource `json:"resources"`
	MachineSet    machineSet                  `json:"machineSet"`
	MachineAccess machineAccess               `json:"machineAccess"`
}

type machineSet struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   machineSetMetadata `json:"metadata"`
	Spec       machineSetSpec     `json:"spec"`
}

type machineSetMetadata struct {
	Name string `json:"name"`
}

type machineSetSpec struct {
	Provider      string    `json:"provider"`
	ConnectionRef string    `json:"connectionRef"`
	NetworkCIDR   string    `json:"networkCIDR"`
	Machines      []machine `json:"machines"`
}

type machine struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Node      string `json:"node"`
	Address   string `json:"address"`
	SSHPort   int    `json:"sshPort"`
	SSHUser   string `json:"sshUser"`
	State     string `json:"state"`
	Cores     int    `json:"cores"`
	MemoryMiB int    `json:"memoryMiB"`
	DiskGiB   int    `json:"diskGiB"`
}

type machineAccess struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   machineAccessMetadata `json:"metadata"`
	Spec       machineAccessSpec     `json:"spec"`
}

type machineAccessMetadata struct {
	Name string `json:"name"`
}

type machineAccessSpec struct {
	Algorithm  string `json:"algorithm"`
	PublicKey  string `json:"publicKey"`
	PrivateKey string `json:"privateKey"`
}

type guestInterface struct {
	Name        string           `json:"name"`
	IPAddresses []guestIPAddress `json:"ip-addresses"`
}

type guestIPAddress struct {
	Address string `json:"ip-address"`
	Type    string `json:"ip-address-type"`
}

func (TopologyPlugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: topologyPluginID, Provider: "proxmox", Name: "Proxmox machine topology", Version: "0.2.0",
		Description:     "Creates a validated group of isolated machines from one Proxmox template.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["connectionRef","node","templateVMID","baseVMID","namePrefix","machineCount","cores","memoryMiB","diskGiB","sshUser","cleanupAfterTest"],"properties":{"connectionRef":{"type":"string","title":"Proxmox connection","format":"kubephos-connection-ref","x-kubephos-provider":"proxmox"},"node":{"type":"string","title":"Proxmox node","minLength":1,"maxLength":64},"templateVMID":{"type":"integer","title":"Template VMID","minimum":100,"maximum":999999999},"baseVMID":{"type":"integer","title":"First new VMID","minimum":100,"maximum":999999988},"namePrefix":{"type":"string","title":"Machine group name","pattern":"^[a-z0-9][a-z0-9-]{0,19}$","default":"cluster"},"machineCount":{"type":"integer","title":"Number of machines","minimum":1,"maximum":12,"default":3},"cores":{"type":"integer","title":"CPU cores per machine","minimum":1,"maximum":32,"default":2},"memoryMiB":{"type":"integer","title":"Memory MiB per machine","minimum":512,"maximum":131072,"default":4096},"diskGiB":{"type":"integer","title":"Disk GiB per machine","minimum":8,"maximum":2048,"default":32},"sshUser":{"type":"string","title":"SSH user","pattern":"^[a-z_][a-z0-9_-]{0,31}$","default":"ubuntu"},"cleanupAfterTest":{"type":"boolean","title":"Remove machines after validation","default":false}}}`),
		ArtifactOutputs: []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}, {Type: "MachineAccess", Version: "v1alpha1"}},
		Capabilities:    []string{"infrastructure.provision", "infrastructure.deprovision", "infrastructure.preflight", "lifecycle.cleanup"},
		Permissions:     []string{"network.proxmox.read", "network.proxmox.write", "secrets.read:proxmox-api-token"},
	}
}

func (TopologyPlugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	spec, configuration, connection, err := parseTopologySpec(invocation)
	if err != nil {
		return invalidReport(report, "$", err.Error())
	}
	if issue := validateTopologyValues(spec); issue != nil {
		return invalidReport(report, issue.Path, issue.Message)
	}
	if _, err := topologyAddresses(configuration, spec.BaseVMID, spec.MachineCount); err != nil {
		return invalidReport(report, "connectionRef", err.Error())
	}
	resources, err := connection.resources(ctx)
	if err != nil {
		return invalidReport(report, "connectionRef", fmt.Sprintf("Proxmox inventory cannot be read: %v", err))
	}
	if err := validateTopologyInventory(resources, spec.TemplateVMID, spec.BaseVMID, spec.MachineCount, spec.Node); err != nil {
		return invalidReport(report, "baseVMID", err.Error())
	}
	templateConfiguration, err := connection.config(ctx, spec.Node, spec.TemplateVMID)
	if err != nil {
		return invalidReport(report, "templateVMID", fmt.Sprintf("Template disk configuration cannot be read: %v", err))
	}
	if _, err := topologyRootDisk(templateConfiguration); err != nil {
		return invalidReport(report, "templateVMID", err.Error())
	}
	nodes, err := connection.nodes(ctx)
	if err != nil || !nodeIsOnline(nodes, spec.Node) {
		return invalidReport(report, "node", fmt.Sprintf("Proxmox node %s is not online.", spec.Node))
	}
	permissions, err := connection.permissions(ctx)
	if err != nil {
		return invalidReport(report, "connectionRef", fmt.Sprintf("Token permissions cannot be verified: %v", err))
	}
	for _, privilege := range []string{"Datastore.AllocateSpace", "VM.Allocate", "VM.Clone", "VM.Config.CPU", "VM.Config.Disk", "VM.Config.Memory", "VM.Config.Options", "VM.PowerMgmt"} {
		if !hasPrivilege(permissions, privilege) {
			return invalidReport(report, "connectionRef", fmt.Sprintf("Token is missing required privilege %s.", privilege))
		}
	}
	if !configuration.VerifyTLS {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Path: "connectionRef", Message: "TLS certificate verification is disabled for this connection."})
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("Template %d and %d consecutive VMIDs are available on %s.", spec.TemplateVMID, spec.MachineCount, spec.Node)})
	return report
}

func (TopologyPlugin) Plan(ctx context.Context, invocation Invocation) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec TopologySpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return domain.Plan{}, err
	}
	configuration, err := resolveConnection(spec.ConnectionRef, invocation.Connections)
	if err != nil {
		return domain.Plan{}, err
	}
	addresses, err := topologyAddresses(configuration, spec.BaseVMID, spec.MachineCount)
	if err != nil {
		return domain.Plan{}, err
	}
	markerBytes := make([]byte, 8)
	if _, err := rand.Read(markerBytes); err != nil {
		return domain.Plan{}, err
	}
	marker := hex.EncodeToString(markerBytes)
	machines := make([]plannedMachine, spec.MachineCount)
	createEffects := make([]domain.ResourceEffect, spec.MachineCount)
	for index := range machines {
		machines[index] = plannedMachine{VMID: spec.BaseVMID + index, Name: fmt.Sprintf("kubephos-%s-%02d-%s", spec.NamePrefix, index+1, marker[:6]), Address: addresses[index]}
		createEffects[index] = domain.ResourceEffect{Action: "create", ExternalID: externalID(machines[index].VMID), Kind: "virtual-machine", Name: machines[index].Name}
	}
	base := topologyStepInput{ConnectionRef: spec.ConnectionRef, Node: spec.Node, TemplateVMID: spec.TemplateVMID, Cores: spec.Cores, MemoryMiB: spec.MemoryMiB, DiskGiB: spec.DiskGiB, SSHUser: spec.SSHUser, PrefixLength: configuration.PrefixLength, Gateway: configuration.Gateway, DNSServer: configuration.DNSServer, Marker: marker, Machines: machines}
	create := base
	create.Action = "provision"
	createInput, err := json.Marshal(create)
	if err != nil {
		return domain.Plan{}, err
	}
	steps := []domain.PlanStep{{
		ID: "provision-topology", Name: "Provision isolated machine topology", Input: createInput, Effects: createEffects,
		Outputs: []domain.ArtifactOutput{
			{Name: "machine-set", Type: "MachineSet", Version: "v1alpha1", MediaType: "application/json", Source: "/machineSet"},
			{Name: "machine-access", Type: "MachineAccess", Version: "v1alpha1", MediaType: "application/json", Source: "/machineAccess", Sensitive: true},
		},
	}}
	if spec.CleanupAfterTest {
		remove := base
		remove.Action = "delete"
		removeInput, err := json.Marshal(remove)
		if err != nil {
			return domain.Plan{}, err
		}
		deleteEffects := make([]domain.ResourceEffect, len(createEffects))
		for index, effect := range createEffects {
			effect.Action = "delete"
			deleteEffects[index] = effect
		}
		steps = append(steps, domain.PlanStep{ID: "delete-topology", Name: "Remove verified machine topology", Input: removeInput, Effects: deleteEffects})
	}
	return domain.Plan{PluginID: topologyPluginID, Steps: steps}, nil
}

func (TopologyPlugin) Precheck(ctx context.Context, step domain.PlanStep, secrets, connections map[string]json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, connection, err := parseTopologyStep(step, secrets, connections)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := log("info", fmt.Sprintf("Checking Proxmox before topology %s", input.Action)); err != nil {
		return domain.HealthReport{}, err
	}
	nodes, err := connection.nodes(ctx)
	if err != nil || !nodeIsOnline(nodes, input.Node) {
		return unhealthy("Proxmox node is not online", "node", "offline"), nil
	}
	resources, err := connection.resources(ctx)
	if err != nil {
		return unhealthy("Proxmox inventory is unavailable", "api", "unreachable"), nil
	}
	if step.Cleanup {
		present := 0
		for _, planned := range input.Machines {
			resource, found := findVM(resources, planned.VMID)
			if !found {
				continue
			}
			if resource.Node != input.Node {
				return unhealthy(fmt.Sprintf("Managed VM %d was found on another node", planned.VMID), "target", "mismatch"), nil
			}
			present++
			configuration, err := connection.config(ctx, input.Node, planned.VMID)
			if err != nil || !cleanupOwnedTopology(configuration, input, planned) {
				return unhealthy(fmt.Sprintf("VM %d ownership does not match the validated topology", planned.VMID), "ownership", "rejected"), nil
			}
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Every present cleanup target has verified KubePhos ownership", Checks: map[string]string{"api": "healthy", "ownership": "verified", "present": strconv.Itoa(present), "absent": strconv.Itoa(len(input.Machines) - present)}}, nil
	}
	switch input.Action {
	case "provision":
		if err := validatePlannedInventory(resources, input); err != nil {
			return unhealthy(err.Error(), "inventory", "blocked"), nil
		}
		templateConfiguration, err := connection.config(ctx, input.Node, input.TemplateVMID)
		if err != nil {
			return unhealthy("Template disk configuration is unavailable", "disk", "unknown"), nil
		}
		if _, err := topologyRootDisk(templateConfiguration); err != nil {
			return unhealthy("Template root disk is no longer supported", "disk", "changed"), nil
		}
	case "delete":
		for _, planned := range input.Machines {
			resource, found := findVM(resources, planned.VMID)
			if !found || resource.Node != input.Node {
				return unhealthy(fmt.Sprintf("Managed VM %d was not found on the expected node", planned.VMID), "target", "missing"), nil
			}
			configuration, err := connection.config(ctx, input.Node, planned.VMID)
			if err != nil || !ownedTopology(configuration, input, planned) {
				return unhealthy(fmt.Sprintf("VM %d ownership does not match the validated topology", planned.VMID), "ownership", "rejected"), nil
			}
		}
	default:
		return domain.HealthReport{}, fmt.Errorf("unsupported topology action %q", input.Action)
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "All topology preconditions are satisfied", Checks: map[string]string{"api": "healthy", "node": "online", "machines": strconv.Itoa(len(input.Machines))}}, nil
}

func (plugin TopologyPlugin) Execute(ctx context.Context, step domain.PlanStep, secrets, connections map[string]json.RawMessage, log plugins.Logger) (json.RawMessage, error) {
	input, connection, err := parseTopologyStep(step, secrets, connections)
	if err != nil {
		return nil, err
	}
	if input.Action == "delete" {
		if err := cleanupTopology(ctx, connection, input, log); err != nil {
			return nil, err
		}
		return json.Marshal(domain.DiscoveryResult{Resources: []domain.DiscoveredResource{}})
	}
	if input.Action != "provision" {
		return nil, fmt.Errorf("unsupported topology action %q", input.Action)
	}
	privateKey, publicKey, err := generateSSHKey()
	if err != nil {
		return nil, err
	}
	created := make([]plannedMachine, 0, len(input.Machines))
	for _, planned := range input.Machines {
		if err := createTopologyMachine(ctx, connection, input, planned, publicKey, log); err != nil {
			cleanupInput := input
			cleanupInput.Machines = created
			return nil, errors.Join(err, cleanupTopology(context.WithoutCancel(ctx), connection, cleanupInput, log))
		}
		created = append(created, planned)
	}
	result, err := topologyExecutionResult(ctx, connection, input, privateKey, publicKey, plugin.sshAccess())
	if err != nil {
		return nil, errors.Join(err, cleanupTopology(context.WithoutCancel(ctx), connection, input, log))
	}
	return json.Marshal(result)
}

func (plugin TopologyPlugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, secrets, connections map[string]json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, connection, err := parseTopologyStep(step, secrets, connections)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if step.Cleanup || input.Action == "delete" {
		for _, planned := range input.Machines {
			_, err := connection.vm(ctx, planned.VMID)
			if err == nil {
				return unhealthy(fmt.Sprintf("VM %d still exists after deletion", planned.VMID), "target", "present"), nil
			}
			var responseError apiError
			if !errors.As(err, &responseError) || responseError.Status != http.StatusNotFound {
				return domain.HealthReport{}, err
			}
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Machine topology was removed", Checks: map[string]string{"machines": "absent", "ownership": "ledger-authorized"}}, nil
	}
	var result topologyResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return domain.HealthReport{}, err
	}
	if err := validateTopologyResult(input, result); err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	for _, planned := range input.Machines {
		resource, err := connection.vm(ctx, planned.VMID)
		if err != nil || resource.Status != "running" {
			return unhealthy(fmt.Sprintf("VM %d is not running", planned.VMID), "power", "unhealthy"), nil
		}
		configuration, err := connection.config(ctx, input.Node, planned.VMID)
		if err != nil || !ownedTopology(configuration, input, planned) {
			return unhealthy(fmt.Sprintf("VM %d ownership could not be verified", planned.VMID), "ownership", "rejected"), nil
		}
		if !configuredTopologyAccess(configuration, input, planned, result.MachineAccess.Spec.PublicKey) {
			return unhealthy(fmt.Sprintf("VM %d SSH and network initialization is incomplete", planned.VMID), "access", "rejected"), nil
		}
	}
	if err := verifyTopologySSH(ctx, input, result.MachineAccess.Spec.PrivateKey, 30*time.Second, plugin.sshAccess()); err != nil {
		return unhealthy(err.Error(), "ssh", "unreachable"), nil
	}
	if err := log("info", "All machines, addresses, ownership markers and access artifacts are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Machine topology is ready for cluster bootstrap", Checks: map[string]string{"machines": strconv.Itoa(len(input.Machines)), "power": "running", "network": "addressed", "access": "verified"}}, nil
}

func (TopologyPlugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, secrets, connections map[string]json.RawMessage, log plugins.Logger) error {
	input, connection, err := parseTopologyStep(step, secrets, connections)
	if err != nil {
		return err
	}
	return cleanupTopology(ctx, connection, input, log)
}

func parseTopologySpec(invocation Invocation) (TopologySpec, connectionConfig, *client, error) {
	var spec TopologySpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return TopologySpec{}, connectionConfig{}, nil, errors.New("configuration must be valid JSON")
	}
	configuration, err := resolveConnection(spec.ConnectionRef, invocation.Connections)
	if err != nil {
		return TopologySpec{}, connectionConfig{}, nil, err
	}
	connection, err := newClient(configuration.Endpoint, configuration.VerifyTLS, configuration.CredentialRef, invocation.Secrets)
	return spec, configuration, connection, err
}

func parseTopologyStep(step domain.PlanStep, secrets, connections map[string]json.RawMessage) (topologyStepInput, *client, error) {
	var input topologyStepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return topologyStepInput{}, nil, err
	}
	configuration, err := resolveConnection(input.ConnectionRef, connections)
	if err != nil {
		return topologyStepInput{}, nil, err
	}
	connection, err := newClient(configuration.Endpoint, configuration.VerifyTLS, configuration.CredentialRef, secrets)
	return input, connection, err
}

func validateTopologyValues(spec TopologySpec) *domain.ValidationIssue {
	switch {
	case spec.ConnectionRef == "":
		return &domain.ValidationIssue{Path: "connectionRef", Message: "Provider connection is required."}
	case !regexpNode.MatchString(spec.Node):
		return &domain.ValidationIssue{Path: "node", Message: "Proxmox node name is invalid."}
	case spec.TemplateVMID < 100 || spec.BaseVMID < 100 || spec.BaseVMID+spec.MachineCount-1 > 999999999:
		return &domain.ValidationIssue{Path: "baseVMID", Message: "The requested VMID range is invalid."}
	case spec.MachineCount < 1 || spec.MachineCount > 12:
		return &domain.ValidationIssue{Path: "machineCount", Message: "Machine count must be between 1 and 12."}
	case spec.TemplateVMID >= spec.BaseVMID && spec.TemplateVMID < spec.BaseVMID+spec.MachineCount:
		return &domain.ValidationIssue{Path: "baseVMID", Message: "The VMID range cannot contain the template VMID."}
	case !regexpNamePrefix.MatchString(spec.NamePrefix):
		return &domain.ValidationIssue{Path: "namePrefix", Message: "Machine group name must be a lowercase label with at most 20 characters."}
	case spec.Cores < 1 || spec.Cores > 32:
		return &domain.ValidationIssue{Path: "cores", Message: "CPU cores must be between 1 and 32."}
	case spec.MemoryMiB < 512 || spec.MemoryMiB > 131072:
		return &domain.ValidationIssue{Path: "memoryMiB", Message: "Memory must be between 512 and 131072 MiB."}
	case spec.DiskGiB < 8 || spec.DiskGiB > 2048:
		return &domain.ValidationIssue{Path: "diskGiB", Message: "Disk must be between 8 and 2048 GiB."}
	case !regexpSSHUser.MatchString(spec.SSHUser):
		return &domain.ValidationIssue{Path: "sshUser", Message: "SSH user is invalid."}
	}
	return nil
}

var (
	regexpNode       = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)
	regexpNamePrefix = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,19}$`)
	regexpSSHUser    = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
)

func validateTopologyInventory(resources []vmResource, templateVMID, baseVMID, count int, node string) error {
	templateFound := false
	for _, resource := range resources {
		if resource.VMID == templateVMID && resource.Type == "qemu" && resource.Template == 1 && resource.Node == node {
			templateFound = true
		}
		if resource.VMID >= baseVMID && resource.VMID < baseVMID+count {
			return fmt.Errorf("target VMID %d already exists and is protected", resource.VMID)
		}
	}
	if !templateFound {
		return fmt.Errorf("VMID %d is not a QEMU template on node %s", templateVMID, node)
	}
	return nil
}

func validatePlannedInventory(resources []vmResource, input topologyStepInput) error {
	if len(input.Machines) == 0 {
		return errors.New("topology does not contain machines")
	}
	if err := validateTopologyInventory(resources, input.TemplateVMID, input.Machines[0].VMID, len(input.Machines), input.Node); err != nil {
		return err
	}
	for index, planned := range input.Machines {
		if index > 0 && planned.VMID != input.Machines[index-1].VMID+1 {
			return errors.New("planned VMIDs are not consecutive")
		}
	}
	return nil
}

func createTopologyMachine(ctx context.Context, connection *client, input topologyStepInput, planned plannedMachine, publicKey string, log plugins.Logger) error {
	if err := log("info", fmt.Sprintf("Cloning template %d to VM %d", input.TemplateVMID, planned.VMID)); err != nil {
		return err
	}
	upid, err := connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/clone", url.PathEscape(input.Node), input.TemplateVMID), url.Values{"newid": {strconv.Itoa(planned.VMID)}, "name": {planned.Name}, "full": {"1"}})
	if err != nil {
		return err
	}
	if err := connection.waitTask(ctx, input.Node, upid); err != nil {
		return err
	}
	values := url.Values{
		"cores": {strconv.Itoa(input.Cores)}, "memory": {strconv.Itoa(input.MemoryMiB)}, "tags": {managedTag},
		"description": {topologyDescription(input.Marker)}, "onboot": {"1"}, "ciuser": {input.SSHUser},
		"sshkeys": {encodeProxmoxSSHKey(publicKey)}, "ipconfig0": {staticIPConfig(planned.Address, input.PrefixLength, input.Gateway)},
		"nameserver": {input.DNSServer}, "agent": {"enabled=1"}, "ciupgrade": {"0"},
	}
	upid, err = connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/config", url.PathEscape(input.Node), planned.VMID), values)
	if err != nil {
		return err
	}
	if err := connection.waitTask(ctx, input.Node, upid); err != nil {
		return err
	}
	configuration, err := connection.config(ctx, input.Node, planned.VMID)
	if err != nil {
		return err
	}
	disk, err := topologyRootDisk(configuration)
	if err != nil {
		return err
	}
	if err := log("info", fmt.Sprintf("Resizing VM %d disk to %d GiB", planned.VMID, input.DiskGiB)); err != nil {
		return err
	}
	upid, err = connection.task(ctx, http.MethodPut, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/resize", url.PathEscape(input.Node), planned.VMID), url.Values{"disk": {disk}, "size": {strconv.Itoa(input.DiskGiB) + "G"}})
	if err != nil {
		return err
	}
	if err := connection.waitTask(ctx, input.Node, upid); err != nil {
		return err
	}
	upid, err = connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/status/start", url.PathEscape(input.Node), planned.VMID), nil)
	if err != nil {
		return err
	}
	return connection.waitTask(ctx, input.Node, upid)
}

func topologyExecutionResult(ctx context.Context, connection *client, input topologyStepInput, privateKey, publicKey string, access sshAccess) (topologyResult, error) {
	if err := verifyTopologySSH(ctx, input, privateKey, 5*time.Minute, access); err != nil {
		return topologyResult{}, err
	}
	resources := make([]domain.DiscoveredResource, 0, len(input.Machines))
	machines := make([]machine, 0, len(input.Machines))
	for _, planned := range input.Machines {
		resource, err := connection.vm(ctx, planned.VMID)
		if err != nil {
			return topologyResult{}, err
		}
		metadata, _ := json.Marshal(map[string]any{"vmid": planned.VMID, "node": input.Node, "type": "qemu", "templateVMID": input.TemplateVMID, "address": planned.Address, "cores": input.Cores, "memoryMiB": input.MemoryMiB, "diskGiB": input.DiskGiB, "ownershipMarker": input.Marker})
		resources = append(resources, domain.DiscoveredResource{ExternalID: externalID(planned.VMID), Kind: "virtual-machine", Name: planned.Name, State: resource.Status, Metadata: metadata})
		machines = append(machines, machine{ID: externalID(planned.VMID), Name: planned.Name, Node: input.Node, Address: planned.Address, SSHPort: 22, SSHUser: input.SSHUser, State: resource.Status, Cores: input.Cores, MemoryMiB: input.MemoryMiB, DiskGiB: input.DiskGiB})
	}
	name := strings.TrimPrefix(input.Machines[0].Name, "kubephos-")
	return topologyResult{
		Resources:     resources,
		MachineSet:    machineSet{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "MachineSet", Metadata: machineSetMetadata{Name: name}, Spec: machineSetSpec{Provider: "proxmox", ConnectionRef: input.ConnectionRef, NetworkCIDR: networkCIDR(input.Machines[0].Address, input.PrefixLength), Machines: machines}},
		MachineAccess: machineAccess{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "MachineAccess", Metadata: machineAccessMetadata{Name: name}, Spec: machineAccessSpec{Algorithm: "ssh-ed25519", PublicKey: publicKey, PrivateKey: privateKey}},
	}, nil
}

func validateTopologyResult(input topologyStepInput, result topologyResult) error {
	if result.MachineSet.APIVersion != "artifacts.kubephos.dev/v1alpha1" || result.MachineSet.Kind != "MachineSet" || result.MachineSet.Spec.Provider != "proxmox" || result.MachineSet.Spec.ConnectionRef != input.ConnectionRef || result.MachineSet.Spec.NetworkCIDR != networkCIDR(input.Machines[0].Address, input.PrefixLength) {
		return errors.New("machine set identity is invalid")
	}
	if result.MachineAccess.APIVersion != "artifacts.kubephos.dev/v1alpha1" || result.MachineAccess.Kind != "MachineAccess" || result.MachineAccess.Spec.Algorithm != "ssh-ed25519" {
		return errors.New("machine access identity is invalid")
	}
	signer, err := ssh.ParsePrivateKey([]byte(result.MachineAccess.Spec.PrivateKey))
	if err != nil {
		return errors.New("machine access private key is invalid")
	}
	derivedPublic := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(signer.PublicKey())))
	if derivedPublic != strings.TrimSpace(result.MachineAccess.Spec.PublicKey) {
		return errors.New("machine access key pair does not match")
	}
	if len(result.MachineSet.Spec.Machines) != len(input.Machines) || len(result.Resources) != len(input.Machines) {
		return errors.New("machine set cardinality does not match the validated plan")
	}
	plannedByID := make(map[string]plannedMachine, len(input.Machines))
	for _, planned := range input.Machines {
		plannedByID[externalID(planned.VMID)] = planned
	}
	seen := make(map[string]bool, len(result.MachineSet.Spec.Machines))
	for _, value := range result.MachineSet.Spec.Machines {
		planned, ok := plannedByID[value.ID]
		address := net.ParseIP(value.Address)
		if !ok || seen[value.ID] || value.Name != planned.Name || value.Node != input.Node || value.Address != planned.Address || value.SSHUser != input.SSHUser || value.SSHPort != 22 || value.State != "running" || value.Cores != input.Cores || value.MemoryMiB != input.MemoryMiB || value.DiskGiB != input.DiskGiB || address == nil || address.To4() == nil || address.IsLoopback() || address.IsLinkLocalUnicast() {
			return fmt.Errorf("machine %q does not match the validated topology", value.ID)
		}
		seen[value.ID] = true
	}
	seenResources := make(map[string]bool, len(result.Resources))
	for _, resource := range result.Resources {
		planned, ok := plannedByID[resource.ExternalID]
		if !ok || seenResources[resource.ExternalID] || resource.Kind != "virtual-machine" || resource.Name != planned.Name || resource.State != "running" {
			return fmt.Errorf("resource %q does not match the validated topology", resource.ExternalID)
		}
		seenResources[resource.ExternalID] = true
	}
	return nil
}

func generateSSHKey() (string, string, error) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	block, err := ssh.MarshalPrivateKey(private, "kubephos")
	if err != nil {
		return "", "", err
	}
	sshPublic, err := ssh.NewPublicKey(public)
	if err != nil {
		return "", "", err
	}
	return string(pem.EncodeToMemory(block)), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPublic))), nil
}

func cleanupTopology(ctx context.Context, connection *client, input topologyStepInput, log plugins.Logger) error {
	values := append([]plannedMachine(nil), input.Machines...)
	sort.Slice(values, func(i, j int) bool { return values[i].VMID > values[j].VMID })
	var failures []error
	for _, planned := range values {
		if _, err := connection.vm(ctx, planned.VMID); err != nil {
			var responseError apiError
			if errors.As(err, &responseError) && responseError.Status == http.StatusNotFound {
				continue
			}
			failures = append(failures, fmt.Errorf("inspect VM %d before cleanup: %w", planned.VMID, err))
			continue
		}
		configuration, err := connection.config(ctx, input.Node, planned.VMID)
		if err != nil {
			var responseError apiError
			if errors.As(err, &responseError) && responseError.Status == http.StatusNotFound {
				continue
			}
			failures = append(failures, fmt.Errorf("inspect VM %d before cleanup: %w", planned.VMID, err))
			continue
		}
		if !cleanupOwnedTopology(configuration, input, planned) {
			failures = append(failures, fmt.Errorf("cleanup refused for VM %d because ownership does not match", planned.VMID))
			continue
		}
		if err := log("warning", fmt.Sprintf("Removing managed topology VM %d", planned.VMID)); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := removeVM(ctx, connection, stepInput{Node: input.Node, TargetVMID: planned.VMID, Name: planned.Name, Marker: input.Marker}, log); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func ownedTopology(configuration vmConfig, input topologyStepInput, planned plannedMachine) bool {
	tags := strings.FieldsFunc(configuration.Tags, func(value rune) bool { return value == ';' || value == ',' || value == ' ' })
	return configuration.Name == planned.Name && slices.Contains(tags, managedTag) && configuration.Description == topologyDescription(input.Marker) && input.Marker != ""
}

func cleanupOwnedTopology(configuration vmConfig, input topologyStepInput, planned plannedMachine) bool {
	if ownedTopology(configuration, input, planned) {
		return true
	}
	if input.Marker == "" || configuration.Name != planned.Name {
		return false
	}
	return configuration.Description == "" || configuration.Description == topologyDescription(input.Marker)
}

func configuredTopologyAccess(configuration vmConfig, input topologyStepInput, planned plannedMachine, publicKey string) bool {
	storedKey := strings.TrimSpace(configuration.SSHKeys)
	if decoded, err := url.QueryUnescape(storedKey); err == nil {
		storedKey = strings.TrimSpace(decoded)
	}
	disk, err := topologyRootDisk(configuration)
	if err != nil {
		return false
	}
	diskSize, err := topologyDiskSizeGiB(diskConfiguration(configuration, disk))
	return err == nil && diskSize >= float64(input.DiskGiB) && configuration.CIUser == input.SSHUser && storedKey == strings.TrimSpace(publicKey) && configuration.IPConfig0 == staticIPConfig(planned.Address, input.PrefixLength, input.Gateway) && configuration.NameServer == input.DNSServer
}

func topologyRootDisk(configuration vmConfig) (string, error) {
	disks := map[string]string{"scsi0": configuration.SCSI0, "virtio0": configuration.VirtIO0, "sata0": configuration.SATA0}
	boot := strings.TrimPrefix(configuration.Boot, "order=")
	for _, candidate := range strings.Split(boot, ";") {
		candidate = strings.TrimSpace(candidate)
		if disks[candidate] != "" {
			return candidate, nil
		}
	}
	for _, candidate := range []string{"scsi0", "virtio0", "sata0"} {
		if disks[candidate] != "" {
			return candidate, nil
		}
	}
	return "", errors.New("template does not expose a supported root disk")
}

func diskConfiguration(configuration vmConfig, disk string) string {
	switch disk {
	case "scsi0":
		return configuration.SCSI0
	case "virtio0":
		return configuration.VirtIO0
	case "sata0":
		return configuration.SATA0
	default:
		return ""
	}
}

func topologyDiskSizeGiB(configuration string) (float64, error) {
	for _, field := range strings.Split(configuration, ",") {
		field = strings.TrimSpace(field)
		if !strings.HasPrefix(field, "size=") || len(field) < 7 {
			continue
		}
		value := strings.TrimPrefix(field, "size=")
		unit := value[len(value)-1]
		number, err := strconv.ParseFloat(value[:len(value)-1], 64)
		if err != nil {
			return 0, err
		}
		switch unit {
		case 'K':
			return number / 1024 / 1024, nil
		case 'M':
			return number / 1024, nil
		case 'G':
			return number, nil
		case 'T':
			return number * 1024, nil
		}
	}
	return 0, errors.New("disk size is unavailable")
}

func (plugin TopologyPlugin) sshAccess() sshAccess {
	if plugin.SSH != nil {
		return plugin.SSH
	}
	return networkSSHAccess{}
}

func topologyAddresses(configuration connectionConfig, baseVMID, count int) ([]string, error) {
	start := net.ParseIP(configuration.AddressStart).To4()
	gateway := net.ParseIP(configuration.Gateway).To4()
	dns := net.ParseIP(configuration.DNSServer).To4()
	if start == nil || gateway == nil || dns == nil {
		return nil, errors.New("managed network profile requires valid IPv4 addresses")
	}
	if configuration.PrefixLength < 8 || configuration.PrefixLength > 30 {
		return nil, errors.New("managed network prefix must be between 8 and 30")
	}
	if count < 1 {
		return nil, errors.New("managed network allocation must contain at least one address")
	}
	if configuration.VMIDStart < 100 || baseVMID < configuration.VMIDStart {
		return nil, errors.New("managed VMID range starts before the network profile")
	}
	mask := binary.BigEndian.Uint32(net.CIDRMask(configuration.PrefixLength, 32))
	mappedStart := uint64(binary.BigEndian.Uint32(start)) + uint64(baseVMID-configuration.VMIDStart)
	if mappedStart > uint64(^uint32(0)) {
		return nil, errors.New("managed network address mapping exceeds IPv4")
	}
	startValue := uint32(mappedStart)
	gatewayValue := binary.BigEndian.Uint32(gateway)
	networkValue := startValue & mask
	broadcastValue := networkValue | ^mask
	if gatewayValue&mask != networkValue {
		return nil, errors.New("managed network gateway is outside the address pool subnet")
	}
	addresses := make([]string, count)
	for index := range count {
		value := uint64(startValue) + uint64(index)
		if value > uint64(^uint32(0)) || uint32(value) <= networkValue || uint32(value) >= broadcastValue || uint32(value) == gatewayValue {
			return nil, errors.New("managed network address pool does not contain enough usable addresses")
		}
		address := make(net.IP, net.IPv4len)
		binary.BigEndian.PutUint32(address, uint32(value))
		addresses[index] = address.String()
	}
	return addresses, nil
}

func staticIPConfig(address string, prefixLength int, gateway string) string {
	return fmt.Sprintf("ip=%s/%d,gw=%s", address, prefixLength, gateway)
}

func networkCIDR(address string, prefixLength int) string {
	value := net.ParseIP(address).To4()
	if value == nil || prefixLength < 0 || prefixLength > 32 {
		return ""
	}
	return (&net.IPNet{IP: value.Mask(net.CIDRMask(prefixLength, 32)), Mask: net.CIDRMask(prefixLength, 32)}).String()
}

func verifyTopologySSH(ctx context.Context, input topologyStepInput, privateKey string, timeout time.Duration, access sshAccess) error {
	type result struct {
		vmid int
		err  error
	}
	results := make(chan result, len(input.Machines))
	for _, planned := range input.Machines {
		planned := planned
		go func() {
			results <- result{vmid: planned.VMID, err: access.Wait(ctx, planned.Address, input.SSHUser, privateKey, timeout)}
		}()
	}
	var failures []error
	for range input.Machines {
		value := <-results
		if value.err != nil {
			failures = append(failures, fmt.Errorf("verify SSH access for VM %d: %w", value.vmid, value.err))
		}
	}
	return errors.Join(failures...)
}

func (networkSSHAccess) Wait(ctx context.Context, address, user, privateKey string, timeout time.Duration) error {
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return errors.New("machine access private key is invalid")
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		dialer := net.Dialer{Timeout: 10 * time.Second}
		connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, "22"))
		if err == nil {
			config := &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second}
			sshConnection, channels, requests, handshakeErr := ssh.NewClientConn(connection, net.JoinHostPort(address, "22"), config)
			if handshakeErr == nil {
				client := ssh.NewClient(sshConnection, channels, requests)
				session, sessionErr := client.NewSession()
				if sessionErr == nil {
					sessionErr = session.Run("true")
					_ = session.Close()
				}
				_ = client.Close()
				if sessionErr == nil {
					return nil
				}
				lastErr = sessionErr
			} else {
				_ = connection.Close()
				lastErr = handshakeErr
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("SSH did not become ready at %s: %w", address, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func encodeProxmoxSSHKey(value string) string {
	return strings.ReplaceAll(url.QueryEscape(strings.TrimSpace(value)), "+", "%20")
}

func topologyDescription(marker string) string {
	return "KubePhos managed topology " + marker
}

func (c *client) waitIPv4(ctx context.Context, node string, vmid int, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		var response struct {
			Result []guestInterface `json:"result"`
		}
		err := c.request(ctx, http.MethodGet, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/agent/network-get-interfaces", url.PathEscape(node), vmid), nil, &response)
		if err == nil {
			for _, networkInterface := range response.Result {
				if networkInterface.Name == "lo" {
					continue
				}
				for _, value := range networkInterface.IPAddresses {
					address := net.ParseIP(value.Address)
					if value.Type == "ipv4" && address != nil && !address.IsLoopback() && !address.IsLinkLocalUnicast() {
						return value.Address, nil
					}
				}
			}
		}
		if time.Now().After(deadline) {
			if err != nil {
				return "", err
			}
			return "", errors.New("guest agent did not report a usable IPv4 address")
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}
