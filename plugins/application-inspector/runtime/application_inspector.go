package applicationinspector

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const maxManifestBytes = 10 * 1024 * 1024

type Invocation struct {
	Input   json.RawMessage            `json:"input"`
	Catalog map[string]json.RawMessage `json:"catalog"`
}

type Plugin struct{}

type specification struct {
	ApplicationRef string `json:"applicationRef"`
}

type stepInput struct {
	ApplicationRef string             `json:"applicationRef"`
	Descriptor     catalog.Descriptor `json:"descriptor"`
}

type Result struct {
	ApplicationRef   string            `json:"applicationRef"`
	SourceRevision   string            `json:"sourceRevision"`
	ManifestDigest   string            `json:"manifestDigest"`
	WorkloadTargets  []WorkloadTarget  `json:"workloadTargets"`
	ServiceEndpoints []ServiceEndpoint `json:"serviceEndpoints"`
}

type WorkloadTarget struct {
	ID         string            `json:"id"`
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Selector   map[string]string `json:"selector"`
	Traits     []string          `json:"traits"`
}

type ServiceEndpoint struct {
	ID        string `json:"id"`
	Component string `json:"component"`
	Service   string `json:"service"`
	Port      int    `json:"port"`
	Protocol  string `json:"protocol"`
	Path      string `json:"path,omitempty"`
}

type manifestResource struct {
	APIVersion string `yaml:"apiVersion"`
	Kind       string `yaml:"kind"`
	Metadata   struct {
		Name string `yaml:"name"`
	} `yaml:"metadata"`
	Spec struct {
		Template struct {
			Metadata struct {
				Labels map[string]string `yaml:"labels"`
			} `yaml:"metadata"`
		} `yaml:"template"`
		Selector map[string]any `yaml:"selector"`
		Ports    []struct {
			Port int `yaml:"port"`
		} `yaml:"ports"`
	} `yaml:"spec"`
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID:          "io.kubephos.applications.inspect",
		Name:        "Application package check",
		Version:     "0.1.0",
		Description: "Resolves a catalog package and verifies its pinned source, workloads and endpoints.",
		Schema:      json.RawMessage(`{"type":"object","required":["applicationRef"],"additionalProperties":false,"properties":{"applicationRef":{"type":"string","title":"Application","description":"Immutable application version from the catalog.","format":"kubephos-application-ref"}}}`),
		ArtifactOutputs: []domain.ArtifactContract{
			{Type: "WorkloadTargets", Version: "v1alpha1"},
			{Type: "ServiceEndpoints", Version: "v1alpha1"},
		},
		Capabilities: []string{"applications.inspect"},
		Permissions:  []string{"catalog.read:applications"},
	}
}

func (Plugin) Validate(ctx context.Context, invocation Invocation) domain.ValidationReport {
	report := domain.ValidationReport{Valid: true, Issues: []domain.ValidationIssue{}, CheckedAt: time.Now().UTC()}
	if err := ctx.Err(); err != nil {
		return invalidReport(err.Error())
	}
	spec, descriptor, err := resolve(invocation)
	if err != nil {
		return invalidReport(err.Error())
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Path: "applicationRef", Message: fmt.Sprintf("Resolved %s with %d declared components.", spec.ApplicationRef, len(descriptor.Spec.Interface.Components))})
	return report
}

func (Plugin) Plan(ctx context.Context, invocation Invocation) (domain.Plan, error) {
	if err := ctx.Err(); err != nil {
		return domain.Plan{}, err
	}
	spec, descriptor, err := resolve(invocation)
	if err != nil {
		return domain.Plan{}, err
	}
	input, err := json.Marshal(stepInput{ApplicationRef: spec.ApplicationRef, Descriptor: descriptor})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: Plugin{}.Manifest().ID, Steps: []domain.PlanStep{{
		ID: "inspect-package", Name: "Verify application package", Input: input,
		Outputs: []domain.ArtifactOutput{
			{Name: "workload-targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json", Source: "/workloadTargets"},
			{Name: "service-endpoints", Type: "ServiceEndpoints", Version: "v1alpha1", MediaType: "application/json", Source: "/serviceEndpoints"},
		},
	}}}, nil
}

func (Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	input, err := decodeStep(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if input.Descriptor.Spec.Package.Type != "git" || input.Descriptor.Spec.Package.Format != "plain-yaml" {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Package type is not supported by this plugin", Checks: map[string]string{"package": "unsupported"}}, nil
	}
	if err := log("info", "Checking the pinned source and manifest entrypoint"); err != nil {
		return domain.HealthReport{}, err
	}
	manifest, err := fetchManifest(ctx, input.Descriptor.Spec.Package)
	if err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: err.Error(), Checks: map[string]string{"source": "unreachable"}}, nil
	}
	if len(manifest) == 0 {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Manifest entrypoint is empty", Checks: map[string]string{"entrypoint": "empty"}}, nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Pinned source and entrypoint are reachable", Checks: map[string]string{"source": "reachable", "revision": "resolved", "entrypoint": "present"}}, nil
}

func (Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, err := decodeStep(step)
	if err != nil {
		return nil, err
	}
	if err := log("info", "Fetching the immutable application package"); err != nil {
		return nil, err
	}
	manifest, err := fetchManifest(ctx, input.Descriptor.Spec.Package)
	if err != nil {
		return nil, err
	}
	workloads, endpoints, err := inspectManifest(manifest, input.Descriptor)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(manifest)
	result := Result{
		ApplicationRef:   input.ApplicationRef,
		SourceRevision:   input.Descriptor.Spec.Package.Revision,
		ManifestDigest:   "sha256:" + hex.EncodeToString(digest[:]),
		WorkloadTargets:  workloads,
		ServiceEndpoints: endpoints,
	}
	if err := log("info", fmt.Sprintf("Verified %d workloads and %d endpoints", len(workloads), len(endpoints))); err != nil {
		return nil, err
	}
	return json.Marshal(result)
}

func (Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.HealthReport{}, err
	}
	input, err := decodeStep(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Inspection result is invalid", Checks: map[string]string{"result": "invalid"}}, nil
	}
	if result.ApplicationRef != input.ApplicationRef || result.SourceRevision != input.Descriptor.Spec.Package.Revision || len(result.WorkloadTargets) != len(input.Descriptor.Spec.Interface.Components) || !strings.HasPrefix(result.ManifestDigest, "sha256:") {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Inspection result does not match the validated application", Checks: map[string]string{"identity": "mismatch"}}, nil
	}
	if err := log("info", "Application package identity and declared interface are consistent"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Application package is ready for deployment", Checks: map[string]string{"identity": "verified", "manifest": "verified", "interface": "verified"}}, nil
}

func resolve(invocation Invocation) (specification, catalog.Descriptor, error) {
	var spec specification
	if err := json.Unmarshal(invocation.Input, &spec); err != nil {
		return specification{}, catalog.Descriptor{}, errors.New("configuration must be valid JSON")
	}
	if spec.ApplicationRef == "" {
		return specification{}, catalog.Descriptor{}, errors.New("applicationRef is required")
	}
	raw, exists := invocation.Catalog[spec.ApplicationRef]
	if !exists {
		return specification{}, catalog.Descriptor{}, errors.New("catalog application could not be resolved")
	}
	var descriptor catalog.Descriptor
	if err := json.Unmarshal(raw, &descriptor); err != nil {
		return specification{}, catalog.Descriptor{}, errors.New("catalog descriptor is invalid")
	}
	return spec, descriptor, nil
}

func decodeStep(step domain.PlanStep) (stepInput, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, err
	}
	if input.ApplicationRef == "" || input.Descriptor.Metadata.ID == "" {
		return stepInput{}, errors.New("step input is incomplete")
	}
	return input, nil
}

func fetchManifest(ctx context.Context, source catalog.Package) ([]byte, error) {
	directory, err := os.MkdirTemp("", "kubephos-application-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	commands := [][]string{
		{"init", "--quiet", directory},
		{"-C", directory, "remote", "add", "origin", source.Repository},
		{"-C", directory, "fetch", "--quiet", "--depth=1", "origin", source.Revision},
	}
	for _, arguments := range commands {
		command := exec.CommandContext(ctx, "git", arguments...)
		if output, err := command.CombinedOutput(); err != nil {
			return nil, fmt.Errorf("git %s failed: %s", arguments[0], boundedMessage(output, err))
		}
	}
	entrypoint := filepath.ToSlash(filepath.Join(source.Path, source.Entrypoint))
	command := exec.CommandContext(ctx, "git", "-C", directory, "show", "FETCH_HEAD:"+entrypoint)
	manifest, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("read package entrypoint: %w", err)
	}
	if len(manifest) > maxManifestBytes {
		return nil, fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)
	}
	return manifest, nil
}

func inspectManifest(manifest []byte, descriptor catalog.Descriptor) ([]WorkloadTarget, []ServiceEndpoint, error) {
	resources := map[string]manifestResource{}
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	for {
		var resource manifestResource
		err := decoder.Decode(&resource)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, nil, fmt.Errorf("decode Kubernetes manifest: %w", err)
		}
		if resource.APIVersion == "" || resource.Kind == "" || resource.Metadata.Name == "" {
			continue
		}
		key := resource.APIVersion + "|" + resource.Kind + "|" + resource.Metadata.Name
		if _, exists := resources[key]; exists {
			return nil, nil, fmt.Errorf("manifest contains duplicate resource %s", key)
		}
		resources[key] = resource
	}
	workloads := make([]WorkloadTarget, 0, len(descriptor.Spec.Interface.Components))
	components := map[string]catalog.Component{}
	for _, component := range descriptor.Spec.Interface.Components {
		key := component.Workload.APIVersion + "|" + component.Workload.Kind + "|" + component.Workload.Name
		resource, exists := resources[key]
		if !exists {
			return nil, nil, fmt.Errorf("declared workload %s is missing from the manifest", component.ID)
		}
		for label, expected := range component.Selector {
			if resource.Spec.Template.Metadata.Labels[label] != expected {
				return nil, nil, fmt.Errorf("declared selector for workload %s does not match its Pod template", component.ID)
			}
		}
		components[component.ID] = component
		workloads = append(workloads, WorkloadTarget{ID: component.ID, APIVersion: component.Workload.APIVersion, Kind: component.Workload.Kind, Name: component.Workload.Name, Selector: component.Selector, Traits: component.Traits})
	}
	endpoints := make([]ServiceEndpoint, 0, len(descriptor.Spec.Interface.Endpoints))
	for _, endpoint := range descriptor.Spec.Interface.Endpoints {
		resource, exists := resources["v1|Service|"+endpoint.Service]
		if !exists {
			return nil, nil, fmt.Errorf("declared endpoint %s references a missing Service", endpoint.ID)
		}
		portFound := false
		for _, port := range resource.Spec.Ports {
			portFound = portFound || port.Port == endpoint.Port
		}
		if !portFound {
			return nil, nil, fmt.Errorf("declared endpoint %s references a missing Service port", endpoint.ID)
		}
		component := components[endpoint.Component]
		for label, expected := range component.Selector {
			value, selected := resource.Spec.Selector[label]
			if !selected || fmt.Sprint(value) != expected {
				return nil, nil, fmt.Errorf("Service selector for endpoint %s conflicts with its component", endpoint.ID)
			}
		}
		endpoints = append(endpoints, ServiceEndpoint{ID: endpoint.ID, Component: endpoint.Component, Service: endpoint.Service, Port: endpoint.Port, Protocol: endpoint.Protocol, Path: endpoint.Path})
	}
	return workloads, endpoints, nil
}

func invalidReport(message string) domain.ValidationReport {
	return domain.ValidationReport{Valid: false, CheckedAt: time.Now().UTC(), Issues: []domain.ValidationIssue{{Level: "error", Path: "$", Message: message}}}
}

func boundedMessage(output []byte, fallback error) string {
	value := strings.TrimSpace(string(output))
	if value == "" {
		return fallback.Error()
	}
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}
