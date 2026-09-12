package proxmoxvm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const templatePluginID = "io.kubephos.infrastructure.proxmox.machine-template"

type TemplatePlugin struct {
	Guest templateGuest
}

type templateGuest interface {
	Prepare(context.Context, string, string, string, time.Duration, plugins.Logger) error
}

type networkTemplateGuest struct{}

type TemplateSpec struct {
	ConnectionRef string `json:"connectionRef"`
	Name          string `json:"name"`
	Node          string `json:"node"`
	VMID          int    `json:"vmid"`
	OS            string `json:"os"`
	CloudImageURL string `json:"cloudImageURL"`
	Storage       string `json:"storage"`
	ImageStorage  string `json:"imageStorage"`
	Bridge        string `json:"bridge"`
	DiskGiB       int    `json:"diskGiB"`
	Address       string `json:"address"`
	PrefixLength  int    `json:"prefixLength"`
	Gateway       string `json:"gateway"`
	DNSServer     string `json:"dnsServer"`
	SSHUser       string `json:"sshUser"`
}

type templateStepInput struct {
	Action        string `json:"action"`
	ConnectionRef string `json:"connectionRef"`
	Name          string `json:"name"`
	Node          string `json:"node"`
	VMID          int    `json:"vmid"`
	OS            string `json:"os"`
	CloudImageURL string `json:"cloudImageURL"`
	ImageFilename string `json:"imageFilename"`
	Storage       string `json:"storage"`
	ImageStorage  string `json:"imageStorage"`
	Bridge        string `json:"bridge"`
	DiskGiB       int    `json:"diskGiB"`
	Address       string `json:"address"`
	PrefixLength  int    `json:"prefixLength"`
	Gateway       string `json:"gateway"`
	DNSServer     string `json:"dnsServer"`
	SSHUser       string `json:"sshUser"`
	Marker        string `json:"marker"`
}

type proxmoxStorage struct {
	Storage string `json:"storage"`
	Active  int    `json:"active"`
	Enabled int    `json:"enabled"`
	Content string `json:"content"`
}

type storageContent struct {
	VolumeID string `json:"volid"`
}

type machineTemplateArtifact struct {
	APIVersion string                      `json:"apiVersion"`
	Kind       string                      `json:"kind"`
	Metadata   machineTemplateMetadata     `json:"metadata"`
	Spec       machineTemplateArtifactSpec `json:"spec"`
}

type machineTemplateMetadata struct {
	Name string `json:"name"`
}

type machineTemplateArtifactSpec struct {
	Provider      string `json:"provider"`
	ConnectionRef string `json:"connectionRef"`
	Node          string `json:"node"`
	VMID          int    `json:"vmid"`
	OS            string `json:"os"`
	Storage       string `json:"storage"`
	Bridge        string `json:"bridge"`
	DiskGiB       int    `json:"diskGiB"`
	SSHUser       string `json:"sshUser"`
}

type templateResult struct {
	Resources       []domain.DiscoveredResource `json:"resources"`
	MachineTemplate machineTemplateArtifact     `json:"machineTemplate"`
}

func (TemplatePlugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: templatePluginID, Provider: "proxmox", Name: "Proxmox VM template", Version: "0.1.0",
		Description:     "Creates and validates a reusable Proxmox VM template.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["connectionRef","name","node","vmid","os","storage","imageStorage","bridge","diskGiB","prefixLength","dnsServer","sshUser"],"properties":{"connectionRef":{"type":"string","title":"Proxmox connection","format":"kubephos-connection-ref","x-kubephos-provider":"proxmox"},"name":{"type":"string","title":"Template name","pattern":"^[a-z0-9][a-z0-9-]{0,31}$"},"node":{"type":"string","title":"Proxmox node","minLength":1,"maxLength":64},"vmid":{"type":"integer","title":"Template VMID","minimum":100,"maximum":999999999},"os":{"type":"string","title":"Operating system","enum":["ubuntu-24.04","ubuntu-22.04","debian-12"],"default":"ubuntu-24.04"},"cloudImageURL":{"type":"string","title":"Custom cloud image URL","default":""},"storage":{"type":"string","title":"VM disk storage","minLength":1,"maxLength":64},"imageStorage":{"type":"string","title":"Image staging storage","minLength":1,"maxLength":64},"bridge":{"type":"string","title":"Network bridge","pattern":"^[A-Za-z0-9._-]{1,32}$","default":"vmbr0"},"diskGiB":{"type":"integer","title":"Disk GiB","minimum":8,"maximum":2048,"default":32},"address":{"type":"string","title":"Temporary address","description":"Leave empty to use DHCP.","default":""},"prefixLength":{"type":"integer","title":"Prefix length","minimum":1,"maximum":32,"default":24},"gateway":{"type":"string","title":"Gateway","default":""},"dnsServer":{"type":"string","title":"DNS server","default":"1.1.1.1"},"sshUser":{"type":"string","title":"SSH user","pattern":"^[a-z_][a-z0-9_-]{0,31}$","default":"ubuntu"}}}`),
		ArtifactOutputs: []domain.ArtifactContract{{Type: "MachineTemplate", Version: "v1alpha1"}},
		Capabilities:    []string{"infrastructure.provision", "infrastructure.deprovision", "infrastructure.machine-template.provision", "infrastructure.machine-template.deprovision", "infrastructure.preflight", "lifecycle.cleanup"},
		Permissions:     []string{"network.proxmox.read", "network.proxmox.write", "network.egress", "secrets.read:proxmox-api-token"},
	}
}

func (plugin TemplatePlugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	spec, configuration, connection, err := parseTemplateSpec(invocation)
	if err != nil {
		return invalidReport(report, "$", err.Error())
	}
	if issue := validateTemplateValues(spec); issue != nil {
		return invalidReport(report, issue.Path, issue.Message)
	}
	resources, err := connection.resources(ctx)
	if err != nil {
		return invalidReport(report, "connectionRef", fmt.Sprintf("Proxmox inventory cannot be read: %v", err))
	}
	if err := validateTemplateInventory(resources, spec); err != nil {
		return invalidReport(report, "vmid", err.Error())
	}
	nodes, err := connection.nodes(ctx)
	if err != nil || !nodeIsOnline(nodes, spec.Node) {
		return invalidReport(report, "node", fmt.Sprintf("Proxmox node %s is not online.", spec.Node))
	}
	storages, err := connection.storages(ctx, spec.Node)
	if err != nil {
		return invalidReport(report, "storage", fmt.Sprintf("Proxmox storage inventory cannot be read: %v", err))
	}
	if err := validateTemplateStorages(storages, spec.Storage, spec.ImageStorage); err != nil {
		return invalidReport(report, "storage", err.Error())
	}
	permissions, err := connection.permissions(ctx)
	if err != nil {
		return invalidReport(report, "connectionRef", fmt.Sprintf("Token permissions cannot be verified: %v", err))
	}
	for _, privilege := range []string{"Datastore.AllocateSpace", "Datastore.AllocateTemplate", "Datastore.Audit", "VM.Allocate", "VM.Config.Cloudinit", "VM.Config.CPU", "VM.Config.Disk", "VM.Config.Memory", "VM.Config.Network", "VM.Config.Options", "VM.PowerMgmt"} {
		if !hasPrivilege(permissions, privilege) {
			return invalidReport(report, "connectionRef", fmt.Sprintf("Token is missing required privilege %s.", privilege))
		}
	}
	if !configuration.VerifyTLS {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Path: "connectionRef", Message: "TLS certificate verification is disabled for this connection."})
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("VMID %d, node %s and both storages are ready for template creation.", spec.VMID, spec.Node)})
	return report
}

func (TemplatePlugin) Plan(ctx context.Context, invocation Invocation) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	var spec TemplateSpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return domain.Plan{}, err
	}
	imageURL, err := resolvedCloudImageURL(spec.OS, spec.CloudImageURL)
	if err != nil {
		return domain.Plan{}, err
	}
	marker, err := randomMarker()
	if err != nil {
		return domain.Plan{}, err
	}
	input := templateStepInput{Action: "provision", ConnectionRef: spec.ConnectionRef, Name: spec.Name, Node: spec.Node, VMID: spec.VMID, OS: spec.OS, CloudImageURL: imageURL, ImageFilename: templateImageFilename(imageURL), Storage: spec.Storage, ImageStorage: spec.ImageStorage, Bridge: spec.Bridge, DiskGiB: spec.DiskGiB, Address: spec.Address, PrefixLength: spec.PrefixLength, Gateway: spec.Gateway, DNSServer: spec.DNSServer, SSHUser: spec.SSHUser, Marker: marker}
	raw, err := json.Marshal(input)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: templatePluginID, Steps: []domain.PlanStep{{ID: "provision-template", Name: "Create and prepare Proxmox VM template", Input: raw, Mutating: true, Effects: []domain.ResourceEffect{{Action: "create", ExternalID: externalID(spec.VMID), Kind: "virtual-machine-template", Name: spec.Name}}, Outputs: []domain.ArtifactOutput{{Name: "machine-template", Type: "MachineTemplate", Version: "v1alpha1", MediaType: "application/json", Source: "/machineTemplate"}}}}}, nil
}

func (plugin TemplatePlugin) Precheck(ctx context.Context, step domain.PlanStep, secrets, connections map[string]json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, connection, err := parseTemplateStep(step, secrets, connections)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := log("info", "Checking Proxmox node, VMID, storage and image before template creation"); err != nil {
		return domain.HealthReport{}, err
	}
	resources, err := connection.resources(ctx)
	if err != nil {
		return unhealthy("Proxmox inventory is unavailable", "api", "unreachable"), nil
	}
	if step.Cleanup || input.Action == "delete" {
		resource, found := findVM(resources, input.VMID)
		if !found {
			return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Template is already absent", Checks: map[string]string{"target": "absent"}}, nil
		}
		configuration, err := connection.config(ctx, input.Node, input.VMID)
		if err != nil || resource.Node != input.Node || !ownedTemplate(configuration, input) {
			return unhealthy("Template ownership does not match the validated plan", "ownership", "rejected"), nil
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Template deletion target is owned by KubePhos", Checks: map[string]string{"target": "present", "ownership": "verified"}}, nil
	}
	if err := validateTemplateInventory(resources, input.spec()); err != nil {
		return unhealthy(err.Error(), "inventory", "blocked"), nil
	}
	nodes, err := connection.nodes(ctx)
	if err != nil || !nodeIsOnline(nodes, input.Node) {
		return unhealthy("Proxmox node is not online", "node", "offline"), nil
	}
	storages, err := connection.storages(ctx, input.Node)
	if err != nil {
		return unhealthy("Proxmox storage inventory is unavailable", "storage", "unreachable"), nil
	}
	if err := validateTemplateStorages(storages, input.Storage, input.ImageStorage); err != nil {
		return unhealthy(err.Error(), "storage", "blocked"), nil
	}
	permissions, err := connection.permissions(ctx)
	if err != nil {
		return unhealthy("Proxmox permissions are unavailable", "permissions", "unknown"), nil
	}
	for _, privilege := range []string{"Datastore.AllocateSpace", "Datastore.AllocateTemplate", "Datastore.Audit", "VM.Allocate", "VM.Config.Cloudinit", "VM.Config.CPU", "VM.Config.Disk", "VM.Config.Memory", "VM.Config.Network", "VM.Config.Options", "VM.PowerMgmt"} {
		if !hasPrivilege(permissions, privilege) {
			return unhealthy("Required Proxmox permissions changed after validation", "permissions", "insufficient"), nil
		}
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "All template provisioning preconditions are satisfied", Checks: map[string]string{"api": "healthy", "node": "online", "vmid": "available", "storage": "compatible"}}, nil
}

func (plugin TemplatePlugin) Execute(ctx context.Context, step domain.PlanStep, secrets, connections map[string]json.RawMessage, log plugins.Logger) (json.RawMessage, error) {
	input, connection, err := parseTemplateStep(step, secrets, connections)
	if err != nil {
		return nil, err
	}
	if step.Cleanup || input.Action == "delete" {
		if err := deleteOwnedTemplate(ctx, connection, input, log); err != nil {
			return nil, err
		}
		return json.Marshal(domain.DiscoveryResult{Resources: []domain.DiscoveredResource{}})
	}
	privateKey, publicKey, err := generateSSHKey()
	if err != nil {
		return nil, err
	}
	volumeID, err := ensureTemplateImage(ctx, connection, input, log)
	if err != nil {
		return nil, err
	}
	if err := createTemplateVM(ctx, connection, input, volumeID, publicKey, log); err != nil {
		return nil, err
	}
	address := input.Address
	if address == "" {
		if err := log("info", "Waiting for the temporary DHCP address"); err != nil {
			return nil, err
		}
		address, err = connection.waitIPv4(ctx, input.Node, input.VMID, 4*time.Minute)
		if err != nil {
			return nil, err
		}
	}
	if err := plugin.templateGuest().Prepare(ctx, address, input.SSHUser, privateKey, 20*time.Minute, log); err != nil {
		return nil, err
	}
	if err := log("info", "Shutting down the prepared guest"); err != nil {
		return nil, err
	}
	upid, err := connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/status/shutdown", url.PathEscape(input.Node), input.VMID), nil)
	if err != nil {
		return nil, err
	}
	if err := connection.waitTask(ctx, input.Node, upid); err != nil {
		return nil, err
	}
	if err := connection.waitVMStatus(ctx, input.VMID, "stopped", 2*time.Minute); err != nil {
		return nil, err
	}
	if err := log("info", "Converting the prepared VM into a Proxmox template"); err != nil {
		return nil, err
	}
	upid, err = connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/template", url.PathEscape(input.Node), input.VMID), nil)
	if err != nil {
		return nil, err
	}
	if err := connection.waitTask(ctx, input.Node, upid); err != nil {
		return nil, err
	}
	artifact := templateArtifact(input)
	metadata, _ := json.Marshal(map[string]any{"vmid": input.VMID, "node": input.Node, "type": "qemu", "storage": input.Storage, "os": input.spec().OS, "ownershipMarker": input.Marker})
	return json.Marshal(templateResult{Resources: []domain.DiscoveredResource{{ExternalID: externalID(input.VMID), Kind: "virtual-machine-template", Name: input.Name, State: "stopped", Metadata: metadata}}, MachineTemplate: artifact})
}

func (plugin TemplatePlugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, secrets, connections map[string]json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, connection, err := parseTemplateStep(step, secrets, connections)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if step.Cleanup || input.Action == "delete" {
		if _, err := connection.vm(ctx, input.VMID); err == nil {
			return unhealthy("Template still exists after deletion", "target", "present"), nil
		} else {
			var responseError apiError
			if !errors.As(err, &responseError) || responseError.Status != http.StatusNotFound {
				return domain.HealthReport{}, err
			}
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Template deletion is verified", Checks: map[string]string{"target": "absent"}}, nil
	}
	var result templateResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return domain.HealthReport{}, err
	}
	resource, err := connection.waitTemplateStatus(ctx, input.Node, input.VMID, 30*time.Second)
	if err != nil || resource.Template != 1 || resource.Status != "stopped" {
		return unhealthy("Created resource is not a stopped Proxmox template on the expected node", "template", "invalid"), nil
	}
	configuration, err := connection.config(ctx, input.Node, input.VMID)
	if err != nil || !ownedTemplate(configuration, input) {
		return unhealthy("Created template ownership could not be verified", "ownership", "rejected"), nil
	}
	if !configuredTemplate(configuration, input) {
		return unhealthy("Created template disk, network or guest agent configuration is incomplete", "configuration", "invalid"), nil
	}
	if result.MachineTemplate != templateArtifact(input) || len(result.Resources) != 1 || result.Resources[0].ExternalID != externalID(input.VMID) || result.Resources[0].Kind != "virtual-machine-template" {
		return unhealthy("MachineTemplate artifact does not match the validated plan", "artifact", "invalid"), nil
	}
	if err := log("info", "Template type, power state, identity, ownership and artifact are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Proxmox VM template is ready", Checks: map[string]string{"template": "ready", "power": "stopped", "ownership": "verified", "artifact": "verified"}}, nil
}

func (plugin TemplatePlugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, secrets, connections map[string]json.RawMessage, log plugins.Logger) error {
	input, connection, err := parseTemplateStep(step, secrets, connections)
	if err != nil {
		return err
	}
	return deleteOwnedTemplate(ctx, connection, input, log)
}

func (plugin TemplatePlugin) templateGuest() templateGuest {
	if plugin.Guest != nil {
		return plugin.Guest
	}
	return networkTemplateGuest{}
}

func parseTemplateSpec(invocation Invocation) (TemplateSpec, connectionConfig, *client, error) {
	var spec TemplateSpec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return TemplateSpec{}, connectionConfig{}, nil, errors.New("configuration must be valid JSON")
	}
	configuration, err := resolveConnection(spec.ConnectionRef, invocation.Connections)
	if err != nil {
		return TemplateSpec{}, connectionConfig{}, nil, err
	}
	connection, err := newClient(configuration.Endpoint, configuration.VerifyTLS, configuration.CredentialRef, invocation.Secrets)
	return spec, configuration, connection, err
}

func parseTemplateStep(step domain.PlanStep, secrets, connections map[string]json.RawMessage) (templateStepInput, *client, error) {
	var input templateStepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return templateStepInput{}, nil, err
	}
	configuration, err := resolveConnection(input.ConnectionRef, connections)
	if err != nil {
		return templateStepInput{}, nil, err
	}
	connection, err := newClient(configuration.Endpoint, configuration.VerifyTLS, configuration.CredentialRef, secrets)
	return input, connection, err
}

func (input templateStepInput) spec() TemplateSpec {
	return TemplateSpec{ConnectionRef: input.ConnectionRef, Name: input.Name, Node: input.Node, VMID: input.VMID, OS: input.OS, CloudImageURL: input.CloudImageURL, Storage: input.Storage, ImageStorage: input.ImageStorage, Bridge: input.Bridge, DiskGiB: input.DiskGiB, Address: input.Address, PrefixLength: input.PrefixLength, Gateway: input.Gateway, DNSServer: input.DNSServer, SSHUser: input.SSHUser}
}

func validateTemplateValues(spec TemplateSpec) *domain.ValidationIssue {
	imageURL, err := resolvedCloudImageURL(spec.OS, spec.CloudImageURL)
	if err != nil {
		return &domain.ValidationIssue{Path: "cloudImageURL", Message: err.Error()}
	}
	parsed, _ := url.Parse(imageURL)
	switch {
	case spec.ConnectionRef == "":
		return &domain.ValidationIssue{Path: "connectionRef", Message: "Provider connection is required."}
	case !regexpNamePrefix.MatchString(spec.Name):
		return &domain.ValidationIssue{Path: "name", Message: "Template name must be a lowercase label with at most 32 characters."}
	case !regexpNode.MatchString(spec.Node):
		return &domain.ValidationIssue{Path: "node", Message: "Proxmox node name is invalid."}
	case spec.VMID < 100 || spec.VMID > 999999999:
		return &domain.ValidationIssue{Path: "vmid", Message: "Template VMID must be between 100 and 999999999."}
	case !regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`).MatchString(spec.Storage), !regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`).MatchString(spec.ImageStorage):
		return &domain.ValidationIssue{Path: "storage", Message: "Storage names are invalid."}
	case !regexp.MustCompile(`^[A-Za-z0-9._-]{1,32}$`).MatchString(spec.Bridge):
		return &domain.ValidationIssue{Path: "bridge", Message: "Network bridge is invalid."}
	case spec.DiskGiB < 8 || spec.DiskGiB > 2048:
		return &domain.ValidationIssue{Path: "diskGiB", Message: "Disk must be between 8 and 2048 GiB."}
	case parsed == nil || parsed.Scheme != "https" || parsed.Host == "":
		return &domain.ValidationIssue{Path: "cloudImageURL", Message: "Cloud image URL must use HTTPS."}
	case !regexpSSHUser.MatchString(spec.SSHUser):
		return &domain.ValidationIssue{Path: "sshUser", Message: "SSH user is invalid."}
	case net.ParseIP(spec.DNSServer) == nil:
		return &domain.ValidationIssue{Path: "dnsServer", Message: "DNS server must be an IP address."}
	case spec.Address != "" && (net.ParseIP(spec.Address) == nil || spec.PrefixLength < 1 || spec.PrefixLength > 32 || net.ParseIP(spec.Gateway) == nil):
		return &domain.ValidationIssue{Path: "address", Message: "Static temporary network configuration is invalid."}
	}
	return nil
}

func validateTemplateInventory(resources []vmResource, spec TemplateSpec) error {
	for _, resource := range resources {
		if resource.VMID == spec.VMID {
			return fmt.Errorf("VMID %d already exists and is protected", spec.VMID)
		}
		if resource.Name == spec.Name {
			return fmt.Errorf("resource name %q already exists and is protected", spec.Name)
		}
	}
	return nil
}

func validateTemplateStorages(storages []proxmoxStorage, target, staging string) error {
	find := func(name, content string) bool {
		for _, storage := range storages {
			if storage.Storage == name && storage.Active == 1 && storage.Enabled == 1 {
				for _, value := range strings.Split(storage.Content, ",") {
					if strings.TrimSpace(value) == content {
						return true
					}
				}
			}
		}
		return false
	}
	if !find(staging, "import") {
		return fmt.Errorf("image storage %q must be active and support import content", staging)
	}
	if !find(target, "images") {
		return fmt.Errorf("VM storage %q must be active and support images content", target)
	}
	return nil
}

func ensureTemplateImage(ctx context.Context, connection *client, input templateStepInput, log plugins.Logger) (string, error) {
	contents, err := connection.storageContent(ctx, input.Node, input.ImageStorage)
	if err != nil {
		return "", err
	}
	for _, item := range contents {
		if path.Base(item.VolumeID) == input.ImageFilename || strings.HasSuffix(item.VolumeID, "/"+input.ImageFilename) {
			if err := log("info", "Reusing the verified cloud image from Proxmox staging storage"); err != nil {
				return "", err
			}
			return item.VolumeID, nil
		}
	}
	if err := log("info", "Downloading the cloud image into Proxmox staging storage"); err != nil {
		return "", err
	}
	upid, err := connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/storage/%s/download-url", url.PathEscape(input.Node), url.PathEscape(input.ImageStorage)), url.Values{"content": {"import"}, "filename": {input.ImageFilename}, "url": {input.CloudImageURL}})
	if err != nil {
		return "", err
	}
	if err := connection.waitTask(ctx, input.Node, upid); err != nil {
		return "", err
	}
	contents, err = connection.storageContent(ctx, input.Node, input.ImageStorage)
	if err != nil {
		return "", err
	}
	for _, item := range contents {
		if path.Base(item.VolumeID) == input.ImageFilename || strings.HasSuffix(item.VolumeID, "/"+input.ImageFilename) {
			return item.VolumeID, nil
		}
	}
	return "", errors.New("downloaded cloud image is not visible in staging storage")
}

func createTemplateVM(ctx context.Context, connection *client, input templateStepInput, volumeID, publicKey string, log plugins.Logger) error {
	if err := log("info", fmt.Sprintf("Creating Proxmox VM %d from the cloud image", input.VMID)); err != nil {
		return err
	}
	values := url.Values{
		"vmid": {strconv.Itoa(input.VMID)}, "name": {input.Name}, "memory": {"2048"}, "cores": {"2"}, "net0": {"virtio,bridge=" + input.Bridge},
		"agent": {"enabled=1"}, "ostype": {"l26"}, "cpu": {"host"}, "serial0": {"socket"}, "vga": {"serial0"}, "scsihw": {"virtio-scsi-pci"},
		"scsi0": {fmt.Sprintf("%s:0,import-from=%s", input.Storage, volumeID)}, "ide2": {input.Storage + ":cloudinit"}, "boot": {"order=scsi0;ide2"},
		"ciuser": {input.SSHUser}, "sshkeys": {encodeProxmoxSSHKey(publicKey)}, "ipconfig0": {templateIPConfig(input)}, "nameserver": {input.DNSServer}, "ciupgrade": {"0"},
		"tags": {managedTag}, "description": {templateDescription(input.Marker)}, "onboot": {"0"},
	}
	upid, err := connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu", url.PathEscape(input.Node)), values)
	if err != nil {
		return err
	}
	if err := connection.waitTask(ctx, input.Node, upid); err != nil {
		return err
	}
	if err := log("info", fmt.Sprintf("Resizing the template disk to %d GiB", input.DiskGiB)); err != nil {
		return err
	}
	upid, err = connection.task(ctx, http.MethodPut, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/resize", url.PathEscape(input.Node), input.VMID), url.Values{"disk": {"scsi0"}, "size": {strconv.Itoa(input.DiskGiB) + "G"}})
	if err != nil {
		return err
	}
	if err := connection.waitTask(ctx, input.Node, upid); err != nil {
		return err
	}
	if err := log("info", "Starting the template guest for preparation"); err != nil {
		return err
	}
	upid, err = connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/status/start", url.PathEscape(input.Node), input.VMID), nil)
	if err != nil {
		return err
	}
	return connection.waitTask(ctx, input.Node, upid)
}

func deleteOwnedTemplate(ctx context.Context, connection *client, input templateStepInput, log plugins.Logger) error {
	resource, err := connection.vm(ctx, input.VMID)
	if err != nil {
		var responseError apiError
		if errors.As(err, &responseError) && responseError.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	configuration, err := connection.config(ctx, input.Node, input.VMID)
	if err != nil {
		return err
	}
	if resource.Node != input.Node || !ownedTemplate(configuration, input) {
		return errors.New("template deletion refused because ownership does not match")
	}
	if err := log("warning", fmt.Sprintf("Deleting managed Proxmox template %d", input.VMID)); err != nil {
		return err
	}
	return removeVM(ctx, connection, stepInput{Node: input.Node, TargetVMID: input.VMID, Name: input.Name, Marker: input.Marker}, log)
}

func (networkTemplateGuest) Prepare(ctx context.Context, address, user, privateKey string, timeout time.Duration, log plugins.Logger) error {
	signer, err := ssh.ParsePrivateKey([]byte(privateKey))
	if err != nil {
		return errors.New("template access private key is invalid")
	}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		dialer := net.Dialer{Timeout: 10 * time.Second}
		connection, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(address, "22"))
		if err == nil {
			configuration := &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey(), Timeout: 10 * time.Second}
			sshConnection, channels, requests, handshakeErr := ssh.NewClientConn(connection, net.JoinHostPort(address, "22"), configuration)
			if handshakeErr == nil {
				client := ssh.NewClient(sshConnection, channels, requests)
				session, sessionErr := client.NewSession()
				if sessionErr == nil {
					if err := log("info", "Refreshing the guest and installing required base packages"); err != nil {
						_ = session.Close()
						_ = client.Close()
						return err
					}
					sessionErr = session.Run("sudo sh -c 'cloud-init status --wait; rc=$?; test $rc -eq 0 -o $rc -eq 2; export DEBIAN_FRONTEND=noninteractive; apt-get update; apt-get -y dist-upgrade; apt-get -y install qemu-guest-agent nfs-common; apt-get -y autoremove; apt-get clean; cloud-init clean --logs --machine-id'")
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
			return fmt.Errorf("template preparation did not complete at %s: %w", address, lastErr)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (c *client) storages(ctx context.Context, node string) ([]proxmoxStorage, error) {
	var result []proxmoxStorage
	err := c.request(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/storage", nil, &result)
	return result, err
}

func (c *client) storageContent(ctx context.Context, node, storage string) ([]storageContent, error) {
	var result []storageContent
	err := c.request(ctx, http.MethodGet, fmt.Sprintf("/api2/json/nodes/%s/storage/%s/content", url.PathEscape(node), url.PathEscape(storage)), nil, &result)
	return result, err
}

func (c *client) waitVMStatus(ctx context.Context, vmid int, expected string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		resource, err := c.vm(ctx, vmid)
		if err == nil && resource.Status == expected {
			return nil
		}
		if time.Now().After(deadline) {
			if err != nil {
				return err
			}
			return fmt.Errorf("VM %d did not reach state %s", vmid, expected)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func resolvedCloudImageURL(osName, custom string) (string, error) {
	if strings.TrimSpace(custom) != "" {
		return strings.TrimSpace(custom), nil
	}
	values := map[string]string{
		"ubuntu-24.04": "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img",
		"ubuntu-22.04": "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img",
		"debian-12":    "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2",
	}
	value, ok := values[osName]
	if !ok {
		return "", fmt.Errorf("operating system %q is not supported", osName)
	}
	return value, nil
}

func templateImageFilename(imageURL string) string {
	parsed, err := url.Parse(imageURL)
	if err != nil {
		return "cloud-image.qcow2"
	}
	name := path.Base(parsed.Path)
	for _, extension := range []string{".qcow2", ".raw", ".vmdk"} {
		if strings.HasSuffix(strings.ToLower(name), extension) {
			return name
		}
	}
	name = strings.TrimSuffix(name, path.Ext(name))
	if name == "" || name == "." || name == "/" {
		name = "cloud-image"
	}
	return name + ".qcow2"
}

func osForImage(imageURL string) string {
	switch {
	case strings.Contains(imageURL, "/noble/"):
		return "ubuntu-24.04"
	case strings.Contains(imageURL, "/jammy/"):
		return "ubuntu-22.04"
	case strings.Contains(imageURL, "/bookworm/"):
		return "debian-12"
	default:
		return "custom"
	}
}

func templateIPConfig(input templateStepInput) string {
	if input.Address == "" {
		return "ip=dhcp"
	}
	return staticIPConfig(input.Address, input.PrefixLength, input.Gateway)
}

func templateArtifact(input templateStepInput) machineTemplateArtifact {
	return machineTemplateArtifact{APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "MachineTemplate", Metadata: machineTemplateMetadata{Name: input.Name}, Spec: machineTemplateArtifactSpec{Provider: "proxmox", ConnectionRef: input.ConnectionRef, Node: input.Node, VMID: input.VMID, OS: input.OS, Storage: input.Storage, Bridge: input.Bridge, DiskGiB: input.DiskGiB, SSHUser: input.SSHUser}}
}

func ownedTemplate(configuration vmConfig, input templateStepInput) bool {
	tags := strings.FieldsFunc(configuration.Tags, func(value rune) bool { return value == ';' || value == ',' || value == ' ' })
	return configuration.Name == input.Name && slices.Contains(tags, managedTag) && configuration.Description == templateDescription(input.Marker) && input.Marker != ""
}

func configuredTemplate(configuration vmConfig, input templateStepInput) bool {
	diskSize, err := topologyDiskSizeGiB(configuration.SCSI0)
	if err != nil || diskSize < float64(input.DiskGiB) {
		return false
	}
	agent := strings.Split(configuration.Agent, ",")[0]
	return strings.HasPrefix(configuration.SCSI0, input.Storage+":") && strings.Contains(configuration.Net0, "bridge="+input.Bridge) && (agent == "1" || agent == "enabled=1") && configuration.CIUser == input.SSHUser && configuration.NameServer == input.DNSServer && configuration.IPConfig0 == templateIPConfig(input)
}

func templateDescription(marker string) string {
	return "KubePhos managed template " + marker
}

func randomMarker() (string, error) {
	value := make([]byte, 8)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
