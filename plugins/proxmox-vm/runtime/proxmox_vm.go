package proxmoxvm

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const managedTag = "kubephos-managed"

type Plugin struct{}

type Invocation struct {
	Input       json.RawMessage            `json:"input"`
	Secrets     map[string]json.RawMessage `json:"secrets"`
	Connections map[string]json.RawMessage `json:"connections"`
}

type Spec struct {
	ConnectionRef    string `json:"connectionRef"`
	Node             string `json:"node"`
	TemplateVMID     int    `json:"templateVMID"`
	TargetVMID       int    `json:"targetVMID"`
	Name             string `json:"name"`
	Cores            int    `json:"cores"`
	MemoryMiB        int    `json:"memoryMiB"`
	Start            bool   `json:"start"`
	CleanupAfterTest bool   `json:"cleanupAfterTest"`
}

type stepInput struct {
	Action        string `json:"action"`
	ConnectionRef string `json:"connectionRef"`
	Node          string `json:"node"`
	TemplateVMID  int    `json:"templateVMID"`
	TargetVMID    int    `json:"targetVMID"`
	Name          string `json:"name"`
	Cores         int    `json:"cores"`
	MemoryMiB     int    `json:"memoryMiB"`
	Start         bool   `json:"start"`
	Marker        string `json:"marker"`
}

type connectionConfig struct {
	Endpoint      string `json:"endpoint"`
	CredentialRef string `json:"credentialRef"`
	VerifyTLS     bool   `json:"verifyTLS"`
}

type credential struct {
	TokenID     string `json:"tokenId"`
	TokenSecret string `json:"tokenSecret"`
}

type apiError struct {
	Status  int
	Message string
}

func (e apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("Proxmox API returned %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("Proxmox API returned %d", e.Status)
}

type client struct {
	endpoint   string
	credential credential
	http       *http.Client
}

type vmResource struct {
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Node     string `json:"node"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Template int    `json:"template"`
}

type nodeResource struct {
	Node   string `json:"node"`
	Status string `json:"status"`
}

type vmConfig struct {
	Name        string `json:"name"`
	Tags        string `json:"tags"`
	Description string `json:"description"`
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{ID: "io.kubephos.infrastructure.proxmox.vm", Provider: "proxmox", Name: "Proxmox VM lifecycle", Version: "0.2.0", Description: "Creates an isolated KubePhos VM from a template and optionally removes it after verification."}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	spec, configuration, connection, err := parseSpec(invocation)
	if err != nil {
		return invalidReport(report, "$", err.Error())
	}
	if issue := validateValues(spec); issue != nil {
		return invalidReport(report, issue.Path, issue.Message)
	}
	resources, err := connection.resources(ctx)
	if err != nil {
		return invalidReport(report, "endpoint", fmt.Sprintf("Proxmox inventory cannot be read: %v", err))
	}
	if err := validateInventory(resources, spec); err != nil {
		return invalidReport(report, "targetVMID", err.Error())
	}
	nodes, err := connection.nodes(ctx)
	if err != nil || !nodeIsOnline(nodes, spec.Node) {
		return invalidReport(report, "node", fmt.Sprintf("Proxmox node %s is not online.", spec.Node))
	}
	permissions, err := connection.permissions(ctx)
	if err != nil {
		return invalidReport(report, "credentialRef", fmt.Sprintf("Token permissions cannot be verified: %v", err))
	}
	required := []string{"Datastore.AllocateSpace", "VM.Allocate", "VM.Clone", "VM.Config.CPU", "VM.Config.Memory", "VM.Config.Options", "VM.PowerMgmt"}
	for _, privilege := range required {
		if !hasPrivilege(permissions, privilege) {
			return invalidReport(report, "credentialRef", fmt.Sprintf("Token is missing required privilege %s.", privilege))
		}
	}
	if !configuration.VerifyTLS {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Path: "connectionRef", Message: "TLS certificate verification is disabled for this connection."})
	}
	if spec.CleanupAfterTest {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "The new VM will be removed after its creation is verified."})
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("Template %d is available on %s and VMID %d is unused.", spec.TemplateVMID, spec.Node, spec.TargetVMID)})
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
	markerBytes := make([]byte, 8)
	if _, err := rand.Read(markerBytes); err != nil {
		return domain.Plan{}, err
	}
	marker := hex.EncodeToString(markerBytes)
	resolvedName := fmt.Sprintf("kubephos-%s-%s", spec.Name, marker[:8])
	base := stepInput{ConnectionRef: spec.ConnectionRef, Node: spec.Node, TemplateVMID: spec.TemplateVMID, TargetVMID: spec.TargetVMID, Name: resolvedName, Cores: spec.Cores, MemoryMiB: spec.MemoryMiB, Start: spec.Start, Marker: marker}
	create := base
	create.Action = "create"
	createInput, err := json.Marshal(create)
	if err != nil {
		return domain.Plan{}, err
	}
	effect := domain.ResourceEffect{Action: "create", ExternalID: externalID(spec.TargetVMID), Kind: "virtual-machine", Name: resolvedName}
	steps := []domain.PlanStep{{ID: "create", Name: "Create isolated Proxmox VM", Input: createInput, Effects: []domain.ResourceEffect{effect}}}
	if spec.CleanupAfterTest {
		remove := base
		remove.Action = "delete"
		removeInput, err := json.Marshal(remove)
		if err != nil {
			return domain.Plan{}, err
		}
		effect.Action = "delete"
		steps = append(steps, domain.PlanStep{ID: "delete", Name: "Remove verified Proxmox VM", Input: removeInput, Effects: []domain.ResourceEffect{effect}})
	}
	return domain.Plan{PluginID: Plugin{}.Manifest().ID, Steps: steps}, nil
}

func (Plugin) Precheck(ctx context.Context, step domain.PlanStep, secrets, connections map[string]json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, connection, err := parseStep(step, secrets, connections)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := log("info", fmt.Sprintf("Checking Proxmox before %s of %s", input.Action, input.Name)); err != nil {
		return domain.HealthReport{}, err
	}
	resources, err := connection.resources(ctx)
	if err != nil {
		return unhealthy("Proxmox inventory is unavailable", "api", "unreachable"), nil
	}
	nodes, err := connection.nodes(ctx)
	if err != nil || !nodeIsOnline(nodes, input.Node) {
		return unhealthy("Proxmox node is not online", "node", "offline"), nil
	}
	switch input.Action {
	case "create":
		if err := validateInventory(resources, input.spec()); err != nil {
			return unhealthy(err.Error(), "inventory", "blocked"), nil
		}
	case "delete":
		resource, found := findVM(resources, input.TargetVMID)
		if !found || resource.Node != input.Node {
			return unhealthy("Managed VM was not found on the expected node", "target", "missing"), nil
		}
		config, err := connection.config(ctx, input.Node, input.TargetVMID)
		if err != nil || !owned(config, input) {
			return unhealthy("VM ownership marker does not match the validated plan", "ownership", "rejected"), nil
		}
	default:
		return domain.HealthReport{}, fmt.Errorf("unsupported action %q", input.Action)
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "All Proxmox preconditions are satisfied", Checks: map[string]string{"api": "healthy", "ownership": "validated", "target": externalID(input.TargetVMID)}}, nil
}

func (Plugin) Execute(ctx context.Context, step domain.PlanStep, secrets, connections map[string]json.RawMessage, log plugins.Logger) (json.RawMessage, error) {
	input, connection, err := parseStep(step, secrets, connections)
	if err != nil {
		return nil, err
	}
	switch input.Action {
	case "create":
		if err := log("info", fmt.Sprintf("Cloning template %d to new VM %d", input.TemplateVMID, input.TargetVMID)); err != nil {
			return nil, err
		}
		upid, err := connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/clone", url.PathEscape(input.Node), input.TemplateVMID), url.Values{"newid": {strconv.Itoa(input.TargetVMID)}, "name": {input.Name}, "full": {"1"}})
		if err != nil {
			return nil, err
		}
		if err := connection.waitTask(ctx, input.Node, upid); err != nil {
			return nil, err
		}
		if err := log("info", "Applying the KubePhos ownership marker and requested capacity"); err != nil {
			return nil, err
		}
		upid, err = connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/config", url.PathEscape(input.Node), input.TargetVMID), url.Values{"cores": {strconv.Itoa(input.Cores)}, "memory": {strconv.Itoa(input.MemoryMiB)}, "tags": {managedTag}, "description": {description(input.Marker)}, "onboot": {"0"}})
		if err != nil {
			return nil, err
		}
		if upid != "" {
			if err := connection.waitTask(ctx, input.Node, upid); err != nil {
				return nil, err
			}
		}
		if input.Start {
			if err := log("info", "Starting the managed VM"); err != nil {
				return nil, err
			}
			upid, err = connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/status/start", url.PathEscape(input.Node), input.TargetVMID), nil)
			if err != nil {
				return nil, err
			}
			if err := connection.waitTask(ctx, input.Node, upid); err != nil {
				return nil, err
			}
		}
		resource, err := connection.vm(ctx, input.TargetVMID)
		if err != nil {
			return nil, err
		}
		metadata, _ := json.Marshal(map[string]any{"vmid": input.TargetVMID, "node": input.Node, "type": "qemu", "templateVMID": input.TemplateVMID, "ownershipMarker": input.Marker})
		return json.Marshal(domain.DiscoveryResult{Resources: []domain.DiscoveredResource{{ExternalID: externalID(input.TargetVMID), Kind: "virtual-machine", Name: input.Name, State: resource.Status, Metadata: metadata}}})
	case "delete":
		if err := removeOwnedVM(ctx, connection, input, log); err != nil {
			return nil, err
		}
		return json.Marshal(domain.DiscoveryResult{Resources: []domain.DiscoveredResource{}})
	default:
		return nil, fmt.Errorf("unsupported action %q", input.Action)
	}
}

func (Plugin) Verify(ctx context.Context, step domain.PlanStep, _ json.RawMessage, secrets, connections map[string]json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, connection, err := parseStep(step, secrets, connections)
	if err != nil {
		return domain.HealthReport{}, err
	}
	resource, err := connection.vm(ctx, input.TargetVMID)
	switch input.Action {
	case "create":
		if err != nil {
			return unhealthy("Created VM is not visible", "target", "missing"), nil
		}
		config, err := connection.config(ctx, input.Node, input.TargetVMID)
		if err != nil || !owned(config, input) {
			return unhealthy("Created VM ownership could not be verified", "ownership", "rejected"), nil
		}
		if input.Start && resource.Status != "running" {
			return unhealthy("Created VM is not running", "power", resource.Status), nil
		}
		if err := log("info", "VM existence, identity, ownership and requested power state are verified"); err != nil {
			return domain.HealthReport{}, err
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed VM is healthy", Checks: map[string]string{"target": "present", "ownership": "verified", "power": resource.Status}}, nil
	case "delete":
		if err == nil {
			return unhealthy("VM still exists after deletion", "target", "present"), nil
		}
		var responseError apiError
		if !errors.As(err, &responseError) || responseError.Status != http.StatusNotFound {
			return domain.HealthReport{}, err
		}
		if err := log("info", "Deletion verified; the temporary VM is absent"); err != nil {
			return domain.HealthReport{}, err
		}
		return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Managed VM was removed", Checks: map[string]string{"target": "absent", "ownership": "ledger-authorized"}}, nil
	default:
		return domain.HealthReport{}, fmt.Errorf("unsupported action %q", input.Action)
	}
}

func (Plugin) Cleanup(ctx context.Context, step domain.PlanStep, _ json.RawMessage, secrets, connections map[string]json.RawMessage, log plugins.Logger) error {
	input, connection, err := parseStep(step, secrets, connections)
	if err != nil {
		return err
	}
	config, err := connection.config(ctx, input.Node, input.TargetVMID)
	if err != nil {
		var responseError apiError
		if errors.As(err, &responseError) && responseError.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	if !owned(config, input) && !(config.Name == input.Name && strings.HasPrefix(config.Name, "kubephos-") && input.Marker != "") {
		return errors.New("cleanup refused because the VM does not match the validated KubePhos identity")
	}
	if err := log("warning", fmt.Sprintf("Cleaning up temporary VM %d after a failed step", input.TargetVMID)); err != nil {
		return err
	}
	return removeVM(ctx, connection, input, log)
}

func removeOwnedVM(ctx context.Context, connection *client, input stepInput, log plugins.Logger) error {
	config, err := connection.config(ctx, input.Node, input.TargetVMID)
	if err != nil {
		return err
	}
	if !owned(config, input) {
		return errors.New("deletion refused because the VM ownership marker does not match")
	}
	return removeVM(ctx, connection, input, log)
}

func removeVM(ctx context.Context, connection *client, input stepInput, log plugins.Logger) error {
	resource, err := connection.vm(ctx, input.TargetVMID)
	if err != nil {
		var responseError apiError
		if errors.As(err, &responseError) && responseError.Status == http.StatusNotFound {
			return nil
		}
		return err
	}
	if resource.Status == "running" {
		if err := log("info", "Stopping the managed VM before deletion"); err != nil {
			return err
		}
		upid, err := connection.task(ctx, http.MethodPost, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/status/stop", url.PathEscape(input.Node), input.TargetVMID), nil)
		if err != nil {
			return err
		}
		if err := connection.waitTask(ctx, input.Node, upid); err != nil {
			return err
		}
	}
	if err := log("info", fmt.Sprintf("Deleting managed VM %d", input.TargetVMID)); err != nil {
		return err
	}
	upid, err := connection.task(ctx, http.MethodDelete, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d", url.PathEscape(input.Node), input.TargetVMID), nil)
	if err != nil {
		return err
	}
	return connection.waitTask(ctx, input.Node, upid)
}

func parseSpec(invocation Invocation) (Spec, connectionConfig, *client, error) {
	var spec Spec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return Spec{}, connectionConfig{}, nil, errors.New("configuration must be valid JSON")
	}
	configuration, err := resolveConnection(spec.ConnectionRef, invocation.Connections)
	if err != nil {
		return Spec{}, connectionConfig{}, nil, err
	}
	connection, err := newClient(configuration.Endpoint, configuration.VerifyTLS, configuration.CredentialRef, invocation.Secrets)
	return spec, configuration, connection, err
}

func parseStep(step domain.PlanStep, secrets, connections map[string]json.RawMessage) (stepInput, *client, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, nil, err
	}
	configuration, err := resolveConnection(input.ConnectionRef, connections)
	if err != nil {
		return stepInput{}, nil, err
	}
	connection, err := newClient(configuration.Endpoint, configuration.VerifyTLS, configuration.CredentialRef, secrets)
	return input, connection, err
}

func (input stepInput) spec() Spec {
	return Spec{ConnectionRef: input.ConnectionRef, Node: input.Node, TemplateVMID: input.TemplateVMID, TargetVMID: input.TargetVMID, Name: input.Name, Cores: input.Cores, MemoryMiB: input.MemoryMiB, Start: input.Start}
}

func resolveConnection(connectionRef string, connections map[string]json.RawMessage) (connectionConfig, error) {
	if connectionRef == "" {
		return connectionConfig{}, errors.New("provider connection is required")
	}
	raw, ok := connections[connectionRef]
	if !ok {
		return connectionConfig{}, errors.New("provider connection is unavailable")
	}
	var configuration connectionConfig
	if err := json.Unmarshal(raw, &configuration); err != nil || configuration.Endpoint == "" || configuration.CredentialRef == "" {
		return connectionConfig{}, errors.New("provider connection is invalid")
	}
	return configuration, nil
}

func validateValues(spec Spec) *domain.ValidationIssue {
	switch {
	case spec.ConnectionRef == "":
		return &domain.ValidationIssue{Path: "connectionRef", Message: "Provider connection is required."}
	case !regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`).MatchString(spec.Node):
		return &domain.ValidationIssue{Path: "node", Message: "Proxmox node name is invalid."}
	case spec.TemplateVMID < 100 || spec.TargetVMID < 100 || spec.TemplateVMID == spec.TargetVMID:
		return &domain.ValidationIssue{Path: "targetVMID", Message: "Source and target VMIDs must be distinct values of at least 100."}
	case !regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`).MatchString(spec.Name):
		return &domain.ValidationIssue{Path: "name", Message: "VM name must be a lowercase label with at most 32 characters."}
	case spec.Cores < 1 || spec.Cores > 32:
		return &domain.ValidationIssue{Path: "cores", Message: "CPU cores must be between 1 and 32."}
	case spec.MemoryMiB < 512 || spec.MemoryMiB > 131072:
		return &domain.ValidationIssue{Path: "memoryMiB", Message: "Memory must be between 512 and 131072 MiB."}
	}
	return nil
}

func validateInventory(resources []vmResource, spec Spec) error {
	var templateFound bool
	for _, resource := range resources {
		if resource.VMID == spec.TargetVMID {
			return fmt.Errorf("target VMID %d already exists and is protected", spec.TargetVMID)
		}
		if resource.VMID == spec.TemplateVMID && resource.Type == "qemu" && resource.Template == 1 && resource.Node == spec.Node {
			templateFound = true
		}
	}
	if !templateFound {
		return fmt.Errorf("VMID %d is not a QEMU template on node %s", spec.TemplateVMID, spec.Node)
	}
	return nil
}

func nodeIsOnline(nodes []nodeResource, expected string) bool {
	for _, node := range nodes {
		if node.Node == expected && node.Status == "online" {
			return true
		}
	}
	return false
}

func invalidReport(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}

func owned(config vmConfig, input stepInput) bool {
	tags := strings.FieldsFunc(config.Tags, func(value rune) bool { return value == ';' || value == ',' || value == ' ' })
	return config.Name == input.Name && slices.Contains(tags, managedTag) && config.Description == description(input.Marker) && input.Marker != ""
}

func description(marker string) string {
	return "KubePhos managed resource " + marker
}

func externalID(vmid int) string {
	return "qemu/" + strconv.Itoa(vmid)
}

func findVM(resources []vmResource, vmid int) (vmResource, bool) {
	for _, resource := range resources {
		if resource.VMID == vmid {
			return resource, true
		}
	}
	return vmResource{}, false
}

func hasPrivilege(values map[string]map[string]int, privilege string) bool {
	for _, privileges := range values {
		if privileges[privilege] == 1 {
			return true
		}
	}
	return false
}

func newClient(endpoint string, verifyTLS bool, credentialRef string, secrets map[string]json.RawMessage) (*client, error) {
	parsed, err := url.Parse(normalizeEndpoint(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("Proxmox endpoint must be an absolute HTTPS URL")
	}
	raw, ok := secrets[credentialRef]
	if !ok {
		return nil, errors.New("API credential is unavailable")
	}
	var value credential
	if err := json.Unmarshal(raw, &value); err != nil || value.TokenID == "" || value.TokenSecret == "" {
		return nil, errors.New("API credential is invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: !verifyTLS}}
	return &client{endpoint: strings.TrimRight(parsed.String(), "/"), credential: value, http: &http.Client{Timeout: 30 * time.Second, Transport: transport}}, nil
}

func (c *client) request(ctx context.Context, method, requestPath string, values url.Values, target any) error {
	var body io.Reader
	if values != nil {
		body = strings.NewReader(values.Encode())
	}
	request, err := http.NewRequestWithContext(ctx, method, c.endpoint+requestPath, body)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "PVEAPIToken="+c.credential.TokenID+"="+c.credential.TokenSecret)
	request.Header.Set("Accept", "application/json")
	if values != nil {
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return apiError{Status: response.StatusCode, Message: strings.TrimSpace(string(message))}
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return err
	}
	if target == nil {
		return nil
	}
	return json.Unmarshal(envelope.Data, target)
}

func (c *client) resources(ctx context.Context) ([]vmResource, error) {
	var result []vmResource
	err := c.request(ctx, http.MethodGet, "/api2/json/cluster/resources?type=vm", nil, &result)
	return result, err
}

func (c *client) nodes(ctx context.Context) ([]nodeResource, error) {
	var result []nodeResource
	err := c.request(ctx, http.MethodGet, "/api2/json/nodes", nil, &result)
	return result, err
}

func (c *client) permissions(ctx context.Context) (map[string]map[string]int, error) {
	result := map[string]map[string]int{}
	err := c.request(ctx, http.MethodGet, "/api2/json/access/permissions", nil, &result)
	return result, err
}

func (c *client) vm(ctx context.Context, vmid int) (vmResource, error) {
	resources, err := c.resources(ctx)
	if err != nil {
		return vmResource{}, err
	}
	if result, found := findVM(resources, vmid); found {
		return result, nil
	}
	return vmResource{}, apiError{Status: http.StatusNotFound}
}

func (c *client) config(ctx context.Context, node string, vmid int) (vmConfig, error) {
	var result vmConfig
	err := c.request(ctx, http.MethodGet, fmt.Sprintf("/api2/json/nodes/%s/qemu/%d/config", url.PathEscape(node), vmid), nil, &result)
	return result, err
}

func (c *client) task(ctx context.Context, method, requestPath string, values url.Values) (string, error) {
	var upid string
	if err := c.request(ctx, method, requestPath, values, &upid); err != nil {
		return "", err
	}
	return upid, nil
}

func (c *client) waitTask(ctx context.Context, node, upid string) error {
	if upid == "" {
		return nil
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var status struct {
			Status     string `json:"status"`
			ExitStatus string `json:"exitstatus"`
		}
		if err := c.request(ctx, http.MethodGet, "/api2/json/nodes/"+url.PathEscape(node)+"/tasks/"+url.PathEscape(upid)+"/status", nil, &status); err != nil {
			return err
		}
		if status.Status == "stopped" {
			if status.ExitStatus != "OK" {
				return fmt.Errorf("Proxmox task failed with %s", status.ExitStatus)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func normalizeEndpoint(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
