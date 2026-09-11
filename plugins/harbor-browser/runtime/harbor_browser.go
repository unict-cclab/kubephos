package harborbrowser

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const (
	pluginID          = "io.kubephos.registry.harbor.browser"
	artifactAPI       = "artifacts.kubephos.dev/v1alpha1"
	responseSizeLimit = 768 << 10
)

var nameExpression = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,254}$`)
var referenceExpression = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._:+-]{0,254}$`)

type Plugin struct {
	Client apiClient
}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	RegistryEndpointRef   string `json:"registryEndpointRef"`
	RegistryCredentialRef string `json:"registryCredentialRef"`
	Action                string `json:"action"`
	Project               string `json:"project"`
	Repository            string `json:"repository"`
	Reference             string `json:"reference"`
}

type registryEndpoint struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Spec       struct {
		Protocol string   `json:"protocol"`
		Host     string   `json:"host"`
		URL      string   `json:"url"`
		APIURL   string   `json:"apiURL"`
		CABundle string   `json:"caBundle"`
		Insecure bool     `json:"insecure"`
		Projects []string `json:"projects"`
	} `json:"spec"`
}

type registryCredential struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Role string `json:"role"`
	} `json:"metadata"`
	Spec struct {
		Server   string   `json:"server"`
		Username string   `json:"username"`
		Password string   `json:"password"`
		Scopes   []string `json:"scopes"`
	} `json:"spec"`
}

type registryView struct {
	APIVersion string               `json:"apiVersion"`
	Kind       string               `json:"kind"`
	Metadata   registryViewMetadata `json:"metadata"`
	Spec       registryViewSpec     `json:"spec"`
}

type registryViewMetadata struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	CreatedAt time.Time `json:"createdAt"`
}

type registryViewSpec struct {
	Action     string          `json:"action"`
	Registry   string          `json:"registry"`
	Project    string          `json:"project,omitempty"`
	Repository string          `json:"repository,omitempty"`
	Reference  string          `json:"reference,omitempty"`
	Data       json.RawMessage `json:"data"`
}

type result struct {
	RegistryView registryView `json:"registryView"`
}

type apiClient interface {
	Do(context.Context, registryEndpoint, registryCredential, string, string) ([]byte, int, error)
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Provider: "harbor", Name: "Harbor registry browser", Version: "0.1.0",
		Description:     "Browses and manages a verified Harbor registry through its managed API artifacts.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["registryEndpointRef","registryCredentialRef","action"],"properties":{"registryEndpointRef":{"type":"string","title":"Managed registry","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"RegistryEndpoint","x-kubephos-artifact-version":"v1alpha1"},"registryCredentialRef":{"type":"string","title":"Management access","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"RegistryCredential","x-kubephos-artifact-version":"v1alpha1"},"action":{"type":"string","title":"Action","x-kubephos-primary-action":true,"enum":["list-projects","list-repositories","list-artifacts","delete-artifact"],"default":"list-projects"},"project":{"type":"string","title":"Project","description":"Required when browsing repositories or artifacts.","maxLength":255},"repository":{"type":"string","title":"Repository","description":"Required when browsing or deleting artifacts.","maxLength":255},"reference":{"type":"string","title":"Tag or digest","description":"Required only when deleting one artifact.","maxLength":255}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "RegistryEndpoint", Version: "v1alpha1"}, {Type: "RegistryCredential", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "RegistryView", Version: "v1alpha1"}},
		Capabilities:    []string{"infrastructure.registry.control", "infrastructure.registry.preflight"},
		Permissions:     []string{"network.egress", "registry.read", "registry.manage"},
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
	if !strings.HasPrefix(spec.RegistryEndpointRef, "art_") {
		return invalid(report, "registryEndpointRef", "Select a verified managed registry endpoint.")
	}
	if !strings.HasPrefix(spec.RegistryCredentialRef, "art_") || spec.RegistryCredentialRef == spec.RegistryEndpointRef {
		return invalid(report, "registryCredentialRef", "Select the matching encrypted management credential.")
	}
	if err := validateAction(spec); err != nil {
		return invalid(report, err.path, err.message)
	}
	if spec.Action == "delete-artifact" {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "warning", Message: "The selected registry artifact will be deleted. Review the exact project, repository, reference and plan hash before starting."})
	} else {
		report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "The registry request is read-only and will be repeated only after TLS, health and management access pass."})
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
	normalize(&spec)
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "registry-" + spec.Action, Name: actionTitle(spec.Action), Input: input, Mutating: spec.Action == "delete-artifact",
		ArtifactInputs: []domain.ArtifactInput{
			{Name: "registry-endpoint", Type: "RegistryEndpoint", Version: "v1alpha1", ArtifactID: spec.RegistryEndpointRef},
			{Name: "registry-credential", Type: "RegistryCredential", Version: "v1alpha1", ArtifactID: spec.RegistryCredentialRef},
		},
		Outputs: []domain.ArtifactOutput{{Name: "registry-view", Type: "RegistryView", Version: "v1alpha1", MediaType: "application/json", Source: "/registryView"}},
	}}}, nil
}

func (plugin Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	spec, endpoint, credential, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "artifacts", "invalid"), nil
	}
	if err := validateArtifacts(endpoint, credential); err != nil {
		return unhealthy(err.Error(), "registry", "invalid"), nil
	}
	if err := validateAction(spec); err != nil {
		return unhealthy(err.message, "request", "invalid"), nil
	}
	if err := log("info", "Validating registry TLS, health and management access before the requested action"); err != nil {
		return domain.HealthReport{}, err
	}
	client := plugin.client()
	value, status, err := client.Do(ctx, endpoint, credential, http.MethodGet, "/health")
	if err != nil || status != http.StatusOK || !healthy(value) {
		return unhealthy("Managed registry health check failed", "health", "unhealthy"), nil
	}
	_, status, err = client.Do(ctx, endpoint, credential, http.MethodGet, "/projects?page=1&page_size=1")
	if err != nil || status != http.StatusOK {
		return unhealthy("Registry management access check failed", "management", "denied"), nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Registry is healthy and management access is verified", Checks: map[string]string{"tls": "verified", "health": "healthy", "management": "verified", "action": spec.Action}}, nil
}

func (plugin Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	spec, endpoint, credential, err := resolve(step)
	if err != nil {
		return nil, err
	}
	method, requestPath := actionRequest(spec)
	if err := log("info", actionTitle(spec.Action)); err != nil {
		return nil, err
	}
	data, status, err := plugin.client().Do(ctx, endpoint, credential, method, requestPath)
	if err != nil {
		return nil, err
	}
	expected := http.StatusOK
	if status != expected {
		return nil, fmt.Errorf("registry action returned HTTP %d", status)
	}
	if spec.Action == "delete-artifact" {
		data = json.RawMessage(`{"deleted":true}`)
	} else if !json.Valid(data) {
		return nil, errors.New("registry returned invalid JSON")
	}
	value := result{RegistryView: registryView{
		APIVersion: artifactAPI, Kind: "RegistryView",
		Metadata: registryViewMetadata{Name: spec.Action, Version: "v1alpha1", CreatedAt: time.Now().UTC()},
		Spec:     registryViewSpec{Action: spec.Action, Registry: endpoint.Spec.Host, Project: spec.Project, Repository: spec.Repository, Reference: spec.Reference, Data: data},
	}}
	return json.Marshal(value)
}

func (plugin Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	spec, endpoint, credential, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	var value result
	if err := json.Unmarshal(raw, &value); err != nil {
		return domain.HealthReport{}, err
	}
	if value.RegistryView.APIVersion != artifactAPI || value.RegistryView.Kind != "RegistryView" || value.RegistryView.Metadata.Version != "v1alpha1" || value.RegistryView.Spec.Action != spec.Action || value.RegistryView.Spec.Registry != endpoint.Spec.Host || !json.Valid(value.RegistryView.Spec.Data) {
		return unhealthy("Registry view artifact does not match the validated request", "artifact", "invalid"), nil
	}
	client := plugin.client()
	healthValue, status, err := client.Do(ctx, endpoint, credential, http.MethodGet, "/health")
	if err != nil || status != http.StatusOK || !healthy(healthValue) {
		return unhealthy("Registry became unhealthy after the requested action", "health", "unhealthy"), nil
	}
	checks := map[string]string{"health": "healthy", "tls": "verified", "action": "completed"}
	if spec.Action == "delete-artifact" {
		_, status, err = client.Do(ctx, endpoint, credential, http.MethodGet, artifactPath(spec))
		if err != nil || status != http.StatusNotFound {
			return unhealthy("Deleted registry artifact is still available", "deletion", "unverified"), nil
		}
		checks["deletion"] = "verified"
	}
	if err := log("info", "Registry action result and post-condition are verified"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Registry action completed and Harbor remains healthy", Checks: checks}, nil
}

func (Plugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

type validationError struct {
	path    string
	message string
}

func validateAction(spec Spec) *validationError {
	normalize(&spec)
	switch spec.Action {
	case "list-projects":
		return nil
	case "list-repositories":
		if !nameExpression.MatchString(spec.Project) || strings.Contains(spec.Project, "/") {
			return &validationError{"project", "Select a valid project."}
		}
	case "list-artifacts":
		if !nameExpression.MatchString(spec.Project) || strings.Contains(spec.Project, "/") {
			return &validationError{"project", "Select a valid project."}
		}
		if !nameExpression.MatchString(spec.Repository) {
			return &validationError{"repository", "Select a valid repository."}
		}
	case "delete-artifact":
		if !nameExpression.MatchString(spec.Project) || strings.Contains(spec.Project, "/") {
			return &validationError{"project", "Select a valid project."}
		}
		if !nameExpression.MatchString(spec.Repository) {
			return &validationError{"repository", "Select a valid repository."}
		}
		if !referenceExpression.MatchString(spec.Reference) {
			return &validationError{"reference", "Select a valid tag or digest."}
		}
	default:
		return &validationError{"action", "Select a supported registry action."}
	}
	return nil
}

func normalize(spec *Spec) {
	spec.Action = strings.TrimSpace(spec.Action)
	spec.Project = strings.TrimSpace(spec.Project)
	spec.Repository = strings.Trim(strings.TrimSpace(spec.Repository), "/")
	spec.Reference = strings.TrimSpace(spec.Reference)
}

func resolve(step domain.PlanStep) (Spec, registryEndpoint, registryCredential, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, registryEndpoint{}, registryCredential{}, err
	}
	normalize(&spec)
	endpointValue, ok := step.ResolvedInputs["registry-endpoint"]
	if !ok {
		return Spec{}, registryEndpoint{}, registryCredential{}, errors.New("verified RegistryEndpoint input is unavailable")
	}
	credentialValue, ok := step.ResolvedInputs["registry-credential"]
	if !ok {
		return Spec{}, registryEndpoint{}, registryCredential{}, errors.New("verified RegistryCredential input is unavailable")
	}
	var endpoint registryEndpoint
	if err := json.Unmarshal(endpointValue.Value, &endpoint); err != nil {
		return Spec{}, registryEndpoint{}, registryCredential{}, err
	}
	var credential registryCredential
	if err := json.Unmarshal(credentialValue.Value, &credential); err != nil {
		return Spec{}, registryEndpoint{}, registryCredential{}, err
	}
	return spec, endpoint, credential, nil
}

func validateArtifacts(endpoint registryEndpoint, credential registryCredential) error {
	if endpoint.APIVersion != artifactAPI || endpoint.Kind != "RegistryEndpoint" || endpoint.Spec.Protocol != "oci" || endpoint.Spec.Insecure || endpoint.Spec.Host == "" || endpoint.Spec.URL != "https://"+endpoint.Spec.Host || endpoint.Spec.APIURL != endpoint.Spec.URL+"/api/v2.0" {
		return errors.New("managed registry endpoint is invalid")
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(endpoint.Spec.CABundle)) {
		return errors.New("managed registry certificate authority is invalid")
	}
	if credential.APIVersion != artifactAPI || credential.Kind != "RegistryCredential" || credential.Metadata.Role != "management" || credential.Spec.Server != endpoint.Spec.Host || credential.Spec.Username == "" || credential.Spec.Password == "" || len(credential.Spec.Scopes) != 1 || credential.Spec.Scopes[0] != "manage" {
		return errors.New("registry management credential is invalid")
	}
	return nil
}

func actionRequest(spec Spec) (string, string) {
	switch spec.Action {
	case "list-projects":
		return http.MethodGet, "/projects?page=1&page_size=100"
	case "list-repositories":
		return http.MethodGet, "/projects/" + component(spec.Project) + "/repositories?page=1&page_size=100"
	case "list-artifacts":
		return http.MethodGet, artifactCollectionPath(spec) + "?page=1&page_size=100&with_tag=true"
	default:
		return http.MethodDelete, artifactPath(spec)
	}
}

func artifactCollectionPath(spec Spec) string {
	return "/projects/" + component(spec.Project) + "/repositories/" + repositoryComponent(spec.Repository) + "/artifacts"
}

func artifactPath(spec Spec) string {
	return artifactCollectionPath(spec) + "/" + component(spec.Reference)
}

func component(value string) string {
	return url.PathEscape(value)
}

func repositoryComponent(value string) string {
	return strings.ReplaceAll(url.PathEscape(value), "%2F", "%252F")
}

func actionTitle(action string) string {
	return map[string]string{"list-projects": "List Harbor projects", "list-repositories": "List Harbor repositories", "list-artifacts": "List Harbor artifacts", "delete-artifact": "Delete Harbor artifact"}[action]
}

func healthy(value []byte) bool {
	var response struct {
		Status string `json:"status"`
	}
	return json.Unmarshal(value, &response) == nil && response.Status == "healthy"
}

func (plugin Plugin) client() apiClient {
	if plugin.Client != nil {
		return plugin.Client
	}
	return httpAPIClient{}
}

type httpAPIClient struct{}

func (httpAPIClient) Do(ctx context.Context, endpoint registryEndpoint, credential registryCredential, method, requestPath string) ([]byte, int, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM([]byte(endpoint.Spec.CABundle)) {
		return nil, 0, errors.New("registry certificate authority is invalid")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, method, endpoint.Spec.APIURL+requestPath, nil)
	if err != nil {
		return nil, 0, err
	}
	request.SetBasicAuth(credential.Spec.Username, credential.Spec.Password)
	request.Header.Set("Accept", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer response.Body.Close()
	value, err := io.ReadAll(io.LimitReader(response.Body, responseSizeLimit+1))
	if err != nil {
		return nil, response.StatusCode, err
	}
	if len(value) > responseSizeLimit {
		return nil, response.StatusCode, errors.New("registry response exceeds 2 MiB")
	}
	return value, response.StatusCode, nil
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}
