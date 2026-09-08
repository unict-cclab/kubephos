package proxmox

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type Plugin struct{}

type Invocation struct {
	Input   json.RawMessage            `json:"input"`
	Secrets map[string]json.RawMessage `json:"secrets"`
}

type Spec struct {
	Endpoint      string `json:"endpoint"`
	CredentialRef string `json:"credentialRef"`
	VerifyTLS     bool   `json:"verifyTLS"`
}

type credential struct {
	TokenID     string `json:"tokenId"`
	TokenSecret string `json:"tokenSecret"`
}

type stepInput struct {
	Action        string `json:"action"`
	Endpoint      string `json:"endpoint"`
	CredentialRef string `json:"credentialRef"`
	VerifyTLS     bool   `json:"verifyTLS"`
}

type client struct {
	endpoint   string
	credential credential
	http       *http.Client
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{ID: "io.kubephos.infrastructure.proxmox.discovery", Provider: "proxmox", Name: "Proxmox discovery", Version: "0.1.0", Description: "Validates a Proxmox connection and inventories existing resources without modifying them."}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	spec, connection, err := parseSpec(invocation)
	if err != nil {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "$", Message: err.Error()})
		return report
	}
	version, err := connection.version(ctx)
	if err != nil {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "endpoint", Message: fmt.Sprintf("Proxmox API is not reachable: %v", err)})
		return report
	}
	nodes, err := connection.nodes(ctx)
	if err != nil {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "endpoint", Message: fmt.Sprintf("Proxmox nodes cannot be read: %v", err)})
		return report
	}
	online := 0
	for _, node := range nodes {
		if node.Status == "online" {
			online++
		}
	}
	if online == 0 {
		report.Valid = false
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: "endpoint", Message: "No Proxmox node is online."})
		return report
	}
	if !spec.VerifyTLS {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Path: "verifyTLS", Message: "TLS certificate verification is disabled for this connection."})
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: fmt.Sprintf("Proxmox %s is reachable and %d node(s) are online. Discovery is read-only.", version.Version, online)})
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
	steps := []domain.PlanStep{}
	for _, value := range []struct{ ID, Name, Action string }{
		{"connection", "Validate Proxmox connection", "connection"},
		{"inventory", "Discover protected resources", "inventory"},
	} {
		input, err := json.Marshal(stepInput{Action: value.Action, Endpoint: normalizeEndpoint(spec.Endpoint), CredentialRef: spec.CredentialRef, VerifyTLS: spec.VerifyTLS})
		if err != nil {
			return domain.Plan{}, err
		}
		steps = append(steps, domain.PlanStep{ID: value.ID, Name: value.Name, Input: input})
	}
	return domain.Plan{PluginID: Plugin{}.Manifest().ID, Steps: steps}, nil
}

func (Plugin) Precheck(ctx context.Context, step domain.PlanStep, secrets map[string]json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	input, connection, err := parseStep(step, secrets)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if err := log("info", fmt.Sprintf("Checking read-only access before %s", input.Action)); err != nil {
		return domain.HealthReport{}, err
	}
	version, err := connection.version(ctx)
	if err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Proxmox API precheck failed", Checks: map[string]string{"api": "unreachable"}}, nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Proxmox API is reachable", Checks: map[string]string{"api": "healthy", "version": version.Version, "access": "read-only"}}, nil
}

func (Plugin) Execute(ctx context.Context, step domain.PlanStep, secrets map[string]json.RawMessage, log plugins.Logger) (json.RawMessage, error) {
	input, connection, err := parseStep(step, secrets)
	if err != nil {
		return nil, err
	}
	switch input.Action {
	case "connection":
		version, err := connection.version(ctx)
		if err != nil {
			return nil, err
		}
		nodes, err := connection.nodes(ctx)
		if err != nil {
			return nil, err
		}
		if err := log("info", fmt.Sprintf("Connection established; discovered %d node(s)", len(nodes))); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"version": version, "nodes": nodes, "mode": "read-only", "checkedAt": time.Now().UTC()})
	case "inventory":
		resources, err := connection.resources(ctx)
		if err != nil {
			return nil, err
		}
		protected := make([]map[string]any, 0, len(resources))
		for _, resource := range resources {
			protected = append(protected, map[string]any{
				"externalId": fmt.Sprintf("%s/%d", resource.Type, resource.VMID),
				"kind":       "virtual-machine",
				"name":       resource.Name,
				"state":      resource.Status,
				"metadata": map[string]any{
					"vmid": resource.VMID, "node": resource.Node, "type": resource.Type, "template": resource.Template == 1,
				},
			})
		}
		if err := log("info", fmt.Sprintf("Discovered %d existing resource(s); all marked imported and read-only", len(protected))); err != nil {
			return nil, err
		}
		return json.Marshal(map[string]any{"resources": protected, "count": len(protected), "mode": "read-only", "checkedAt": time.Now().UTC()})
	default:
		return nil, fmt.Errorf("unsupported action %q", input.Action)
	}
}

func (Plugin) Verify(ctx context.Context, step domain.PlanStep, result json.RawMessage, secrets map[string]json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	_, connection, err := parseStep(step, secrets)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if !json.Valid(result) {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Discovery result is invalid", Checks: map[string]string{"result": "invalid"}}, nil
	}
	version, err := connection.version(ctx)
	if err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Proxmox API became unavailable", Checks: map[string]string{"api": "unreachable"}}, nil
	}
	if err := log("info", "Post-condition verified; Proxmox remains reachable and no write was requested"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Read-only discovery verified", Checks: map[string]string{"api": "healthy", "version": version.Version, "writes": "none"}}, nil
}

func parseSpec(invocation Invocation) (Spec, *client, error) {
	var spec Spec
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return Spec{}, nil, errors.New("configuration must be valid JSON")
	}
	if spec.Endpoint == "" || spec.CredentialRef == "" {
		return Spec{}, nil, errors.New("endpoint and API credential are required")
	}
	connection, err := newClient(spec.Endpoint, spec.VerifyTLS, spec.CredentialRef, invocation.Secrets)
	return spec, connection, err
}

func parseStep(step domain.PlanStep, secrets map[string]json.RawMessage) (stepInput, *client, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, nil, err
	}
	connection, err := newClient(input.Endpoint, input.VerifyTLS, input.CredentialRef, secrets)
	return input, connection, err
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
	return &client{endpoint: strings.TrimRight(parsed.String(), "/"), credential: value, http: &http.Client{Timeout: 15 * time.Second, Transport: transport}}, nil
}

func (c *client) get(ctx context.Context, path string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Authorization", "PVEAPIToken="+c.credential.TokenID+"="+c.credential.TokenSecret)
	request.Header.Set("Accept", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return fmt.Errorf("API returned %s", response.Status)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 8<<20)).Decode(&envelope); err != nil {
		return err
	}
	return json.Unmarshal(envelope.Data, target)
}

func (c *client) version(ctx context.Context) (struct {
	Version string `json:"version"`
	Release string `json:"release"`
}, error) {
	var result struct {
		Version string `json:"version"`
		Release string `json:"release"`
	}
	err := c.get(ctx, "/api2/json/version", &result)
	return result, err
}

func (c *client) nodes(ctx context.Context) ([]struct {
	Node   string `json:"node"`
	Status string `json:"status"`
}, error) {
	var result []struct {
		Node   string `json:"node"`
		Status string `json:"status"`
	}
	err := c.get(ctx, "/api2/json/nodes", &result)
	return result, err
}

func (c *client) resources(ctx context.Context) ([]struct {
	VMID     int    `json:"vmid"`
	Name     string `json:"name"`
	Node     string `json:"node"`
	Status   string `json:"status"`
	Type     string `json:"type"`
	Template int    `json:"template"`
}, error) {
	var result []struct {
		VMID     int    `json:"vmid"`
		Name     string `json:"name"`
		Node     string `json:"node"`
		Status   string `json:"status"`
		Type     string `json:"type"`
		Template int    `json:"template"`
	}
	err := c.get(ctx, "/api2/json/cluster/resources?type=vm", &result)
	return result, err
}

func normalizeEndpoint(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
