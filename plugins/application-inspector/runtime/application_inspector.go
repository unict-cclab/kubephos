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
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/schema"
)

const maxManifestBytes = 10 * 1024 * 1024

type Invocation struct {
	Input   json.RawMessage            `json:"input"`
	Catalog map[string]json.RawMessage `json:"catalog"`
}

type Plugin struct{}

type specification struct {
	ApplicationRef string         `json:"applicationRef"`
	Values         map[string]any `json:"values"`
}

type stepInput struct {
	ApplicationRef string             `json:"applicationRef"`
	Values         map[string]any     `json:"values"`
	Descriptor     catalog.Descriptor `json:"descriptor"`
}

type Result struct {
	ApplicationRef   string            `json:"applicationRef"`
	SourceRevision   string            `json:"sourceRevision"`
	ManifestDigest   string            `json:"manifestDigest"`
	ValuesDigest     string            `json:"valuesDigest"`
	ManifestSet      ManifestSet       `json:"manifestSet"`
	WorkloadTargets  []WorkloadTarget  `json:"workloadTargets"`
	ServiceEndpoints []ServiceEndpoint `json:"serviceEndpoints"`
	LoadScenarioSet  LoadScenarioSet   `json:"loadScenarioSet"`
}

type ManifestSet struct {
	APIVersion string              `json:"apiVersion"`
	Kind       string              `json:"kind"`
	Metadata   ManifestSetMetadata `json:"metadata"`
	Spec       ManifestSetSpec     `json:"spec"`
}

type ManifestSetMetadata struct {
	ApplicationRef string `json:"applicationRef"`
	Digest         string `json:"digest"`
	ValuesDigest   string `json:"valuesDigest"`
}

type ManifestSetSpec struct {
	Renderer string         `json:"renderer"`
	Content  string         `json:"content"`
	Source   ManifestSource `json:"source"`
}

type ManifestSource struct {
	Type       string `json:"type"`
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
	Path       string `json:"path"`
	Entrypoint string `json:"entrypoint"`
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

type LoadScenarioSet struct {
	APIVersion string                  `json:"apiVersion"`
	Kind       string                  `json:"kind"`
	Metadata   LoadScenarioSetMetadata `json:"metadata"`
	Spec       LoadScenarioSetSpec     `json:"spec"`
}

type LoadScenarioSetMetadata struct {
	ApplicationRef string `json:"applicationRef"`
	Digest         string `json:"digest"`
}

type LoadScenarioSetSpec struct {
	Scenarios []LoadScenario `json:"scenarios"`
}

type LoadScenario struct {
	ID             string `json:"id"`
	Engine         string `json:"engine"`
	TargetEndpoint string `json:"targetEndpoint"`
	RuntimeImage   string `json:"runtimeImage"`
	Script         string `json:"script"`
	ScriptDigest   string `json:"scriptDigest"`
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
		Name:        "Application package materializer",
		Version:     "0.3.0",
		Description: "Resolves, materializes and verifies a pinned application package and its declared interface.",
		Schema:      json.RawMessage(`{"type":"object","required":["applicationRef"],"additionalProperties":false,"properties":{"applicationRef":{"type":"string","title":"Application","description":"Immutable application version from the catalog.","format":"kubephos-application-ref"},"values":{"type":"object","title":"Application settings","description":"Validated settings declared by the selected application.","x-kubephos-schema-from-application":"applicationRef"}}}`),
		ArtifactOutputs: []domain.ArtifactContract{
			{Type: "ManifestSet", Version: "v1alpha1"},
			{Type: "WorkloadTargets", Version: "v1alpha1"},
			{Type: "ServiceEndpoints", Version: "v1alpha1"},
			{Type: "LoadScenarioSet", Version: "v1alpha1"},
		},
		Capabilities: []string{"applications.inspect", "applications.materialize"},
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
	input, err := json.Marshal(stepInput{ApplicationRef: spec.ApplicationRef, Values: spec.Values, Descriptor: descriptor})
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: Plugin{}.Manifest().ID, Steps: []domain.PlanStep{{
		ID: "inspect-package", Name: "Verify application package", Input: input,
		Outputs: []domain.ArtifactOutput{
			{Name: "manifest-set", Type: "ManifestSet", Version: "v1alpha1", MediaType: "application/json", Source: "/manifestSet"},
			{Name: "workload-targets", Type: "WorkloadTargets", Version: "v1alpha1", MediaType: "application/json", Source: "/workloadTargets"},
			{Name: "service-endpoints", Type: "ServiceEndpoints", Version: "v1alpha1", MediaType: "application/json", Source: "/serviceEndpoints"},
			{Name: "load-scenario-set", Type: "LoadScenarioSet", Version: "v1alpha1", MediaType: "application/json", Source: "/loadScenarioSet"},
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
	content, err := fetchPackage(ctx, input.Descriptor)
	if err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: err.Error(), Checks: map[string]string{"source": "unreachable"}}, nil
	}
	if len(content.Manifest) == 0 {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Manifest entrypoint is empty", Checks: map[string]string{"entrypoint": "empty"}}, nil
	}
	manifest, err := applyOverlays(content.Manifest, input.Descriptor.Spec.Overlays, input.Values)
	if err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: err.Error(), Checks: map[string]string{"overlays": "invalid"}}, nil
	}
	manifest, err = materializeManifest(manifest, input.Descriptor)
	if err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: err.Error(), Checks: map[string]string{"manifest": "invalid"}}, nil
	}
	if _, err := buildLoadScenarios(content.Scripts, input.Descriptor); err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: err.Error(), Checks: map[string]string{"loadScenarios": "invalid"}}, nil
	}
	if _, _, err := inspectManifest(manifest, input.Descriptor); err != nil {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: err.Error(), Checks: map[string]string{"interface": "invalid"}}, nil
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Pinned application resources and load scenarios are valid", Checks: map[string]string{"source": "reachable", "revision": "resolved", "entrypoint": "present", "values": "valid", "overlays": fmt.Sprintf("%d valid", len(input.Descriptor.Spec.Overlays)), "loadScenarios": fmt.Sprintf("%d valid", len(input.Descriptor.Spec.Interface.LoadScenarios))}}, nil
}

func (Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	input, err := decodeStep(step)
	if err != nil {
		return nil, err
	}
	if err := log("info", "Fetching the immutable application package"); err != nil {
		return nil, err
	}
	content, err := fetchPackage(ctx, input.Descriptor)
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(content.Manifest) {
		return nil, errors.New("manifest entrypoint is not valid UTF-8")
	}
	manifest, err := applyOverlays(content.Manifest, input.Descriptor.Spec.Overlays, input.Values)
	if err != nil {
		return nil, err
	}
	applicationManifest, err := materializeManifest(manifest, input.Descriptor)
	if err != nil {
		return nil, err
	}
	loadScenarios, err := buildLoadScenarios(content.Scripts, input.Descriptor)
	if err != nil {
		return nil, err
	}
	workloads, endpoints, err := inspectManifest(applicationManifest, input.Descriptor)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(applicationManifest)
	digestValue := "sha256:" + hex.EncodeToString(digest[:])
	valuesDigest, err := digestJSON(input.Values)
	if err != nil {
		return nil, err
	}
	loadDigest, err := digestJSON(loadScenarios)
	if err != nil {
		return nil, err
	}
	result := Result{
		ApplicationRef: input.ApplicationRef,
		SourceRevision: input.Descriptor.Spec.Package.Revision,
		ManifestDigest: digestValue,
		ValuesDigest:   valuesDigest,
		ManifestSet: ManifestSet{
			APIVersion: "artifacts.kubephos.dev/v1alpha1",
			Kind:       "ManifestSet",
			Metadata:   ManifestSetMetadata{ApplicationRef: input.ApplicationRef, Digest: digestValue, ValuesDigest: valuesDigest},
			Spec: ManifestSetSpec{
				Renderer: input.Descriptor.Spec.Package.Format,
				Content:  string(applicationManifest),
				Source: ManifestSource{
					Type: input.Descriptor.Spec.Package.Type, Repository: input.Descriptor.Spec.Package.Repository,
					Revision: input.Descriptor.Spec.Package.Revision, Path: input.Descriptor.Spec.Package.Path, Entrypoint: input.Descriptor.Spec.Package.Entrypoint,
				},
			},
		},
		WorkloadTargets:  workloads,
		ServiceEndpoints: endpoints,
		LoadScenarioSet: LoadScenarioSet{
			APIVersion: "artifacts.kubephos.dev/v1alpha1",
			Kind:       "LoadScenarioSet",
			Metadata:   LoadScenarioSetMetadata{ApplicationRef: input.ApplicationRef, Digest: loadDigest},
			Spec:       LoadScenarioSetSpec{Scenarios: loadScenarios},
		},
	}
	if err := log("info", fmt.Sprintf("Verified %d application workloads, %d endpoints and %d immutable load scenarios", len(workloads), len(endpoints), len(loadScenarios))); err != nil {
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
	valuesDigest, err := digestJSON(input.Values)
	if err != nil {
		return domain.HealthReport{}, err
	}
	if result.ApplicationRef != input.ApplicationRef || result.SourceRevision != input.Descriptor.Spec.Package.Revision || result.ValuesDigest != valuesDigest || len(result.WorkloadTargets) != len(input.Descriptor.Spec.Interface.Components) || !strings.HasPrefix(result.ManifestDigest, "sha256:") {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Inspection result does not match the validated application", Checks: map[string]string{"identity": "mismatch"}}, nil
	}
	manifestDigest := sha256.Sum256([]byte(result.ManifestSet.Spec.Content))
	actualDigest := "sha256:" + hex.EncodeToString(manifestDigest[:])
	if result.ManifestSet.APIVersion != "artifacts.kubephos.dev/v1alpha1" || result.ManifestSet.Kind != "ManifestSet" ||
		result.ManifestSet.Metadata.ApplicationRef != input.ApplicationRef || result.ManifestSet.Metadata.Digest != actualDigest || result.ManifestSet.Metadata.ValuesDigest != valuesDigest || result.ManifestDigest != actualDigest ||
		len(result.ManifestSet.Spec.Content) > maxManifestBytes || !utf8.ValidString(result.ManifestSet.Spec.Content) ||
		result.ManifestSet.Spec.Renderer != input.Descriptor.Spec.Package.Format ||
		result.ManifestSet.Spec.Source.Type != input.Descriptor.Spec.Package.Type || result.ManifestSet.Spec.Source.Repository != input.Descriptor.Spec.Package.Repository ||
		result.ManifestSet.Spec.Source.Revision != input.Descriptor.Spec.Package.Revision || result.ManifestSet.Spec.Source.Path != input.Descriptor.Spec.Package.Path ||
		result.ManifestSet.Spec.Source.Entrypoint != input.Descriptor.Spec.Package.Entrypoint {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Materialized manifest identity or digest is invalid", Checks: map[string]string{"manifestSet": "invalid"}}, nil
	}
	loadDigest, err := digestJSON(result.LoadScenarioSet.Spec.Scenarios)
	if err != nil || result.LoadScenarioSet.APIVersion != "artifacts.kubephos.dev/v1alpha1" || result.LoadScenarioSet.Kind != "LoadScenarioSet" || result.LoadScenarioSet.Metadata.ApplicationRef != input.ApplicationRef || result.LoadScenarioSet.Metadata.Digest != loadDigest || len(result.LoadScenarioSet.Spec.Scenarios) != len(input.Descriptor.Spec.Interface.LoadScenarios) {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Load scenario set identity or digest is invalid", Checks: map[string]string{"loadScenarioSet": "invalid"}}, nil
	}
	if !validLoadScenarios(result.LoadScenarioSet.Spec.Scenarios, input.Descriptor) {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Materialized load scenarios do not match the application contract", Checks: map[string]string{"loadScenarioSet": "inconsistent"}}, nil
	}
	verifiedWorkloads, verifiedEndpoints, err := inspectManifest([]byte(result.ManifestSet.Spec.Content), input.Descriptor)
	if err != nil || len(verifiedWorkloads) != len(result.WorkloadTargets) || len(verifiedEndpoints) != len(result.ServiceEndpoints) {
		return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: "Materialized manifest no longer matches the declared interface", Checks: map[string]string{"manifestSet": "inconsistent"}}, nil
	}
	if err := log("info", "Application package identity and declared interface are consistent"); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Application resources and load scenarios are materialized independently", Checks: map[string]string{"identity": "verified", "manifest": "verified", "manifestSet": "verified", "interface": "verified", "loadScenarios": fmt.Sprintf("%d verified", len(result.LoadScenarioSet.Spec.Scenarios))}}, nil
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
	values, err := resolveValues(descriptor.Spec.ValuesSchema, descriptor.Spec.Defaults, spec.Values)
	if err != nil {
		return specification{}, catalog.Descriptor{}, err
	}
	spec.Values = values
	return spec, descriptor, nil
}

func decodeStep(step domain.PlanStep) (stepInput, error) {
	var input stepInput
	if err := json.Unmarshal(step.Input, &input); err != nil {
		return stepInput{}, err
	}
	if input.ApplicationRef == "" || input.Descriptor.Metadata.ID == "" || input.Values == nil {
		return stepInput{}, errors.New("step input is incomplete")
	}
	return input, nil
}

func resolveValues(definition, defaults, supplied map[string]any) (map[string]any, error) {
	resolved := cloneObject(defaults)
	mergeObjects(resolved, supplied)
	rawDefinition, err := json.Marshal(definition)
	if err != nil {
		return nil, errors.New("application values schema is invalid")
	}
	rawValues, err := json.Marshal(resolved)
	if err != nil {
		return nil, errors.New("application settings are invalid")
	}
	issues, err := schema.Validate(rawDefinition, rawValues)
	if err != nil {
		return nil, errors.New("application values schema is invalid")
	}
	if len(issues) > 0 {
		return nil, fmt.Errorf("application settings do not satisfy valuesSchema at %s: %s", issues[0].Path, issues[0].Message)
	}
	return resolved, nil
}

func cloneObject(value map[string]any) map[string]any {
	result := map[string]any{}
	for key, item := range value {
		if nested, ok := item.(map[string]any); ok {
			result[key] = cloneObject(nested)
		} else {
			result[key] = item
		}
	}
	return result
}

func mergeObjects(target, source map[string]any) {
	for key, item := range source {
		nested, nestedOK := item.(map[string]any)
		current, currentOK := target[key].(map[string]any)
		if nestedOK && currentOK {
			mergeObjects(current, nested)
			continue
		}
		target[key] = item
	}
}

type packageContent struct {
	Manifest []byte
	Scripts  map[string][]byte
}

func fetchPackage(ctx context.Context, descriptor catalog.Descriptor) (packageContent, error) {
	source := descriptor.Spec.Package
	directory, err := os.MkdirTemp("", "kubephos-application-")
	if err != nil {
		return packageContent{}, err
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
			return packageContent{}, fmt.Errorf("git %s failed: %s", arguments[0], boundedMessage(output, err))
		}
	}
	entrypoint := filepath.ToSlash(filepath.Join(source.Path, source.Entrypoint))
	manifest, err := readGitFile(ctx, directory, entrypoint)
	if err != nil {
		return packageContent{}, fmt.Errorf("read package entrypoint: %w", err)
	}
	if len(manifest) > maxManifestBytes {
		return packageContent{}, fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)
	}
	scripts := map[string][]byte{}
	for _, scenario := range descriptor.Spec.Interface.LoadScenarios {
		if _, exists := scripts[scenario.Script]; exists {
			continue
		}
		script, err := readGitFile(ctx, directory, scenario.Script)
		if err != nil {
			return packageContent{}, fmt.Errorf("read load scenario %s: %w", scenario.ID, err)
		}
		if len(script) == 0 || len(script) > 1024*1024 || !utf8.Valid(script) {
			return packageContent{}, fmt.Errorf("load scenario %s is empty, too large or not UTF-8", scenario.ID)
		}
		scripts[scenario.Script] = script
	}
	return packageContent{Manifest: manifest, Scripts: scripts}, nil
}

func readGitFile(ctx context.Context, directory, path string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", "-C", directory, "show", "FETCH_HEAD:"+filepath.ToSlash(path))
	value, err := command.Output()
	if err != nil {
		return nil, err
	}
	return value, nil
}

func applyOverlays(manifest []byte, overlays []catalog.Overlay, values map[string]any) ([]byte, error) {
	if len(overlays) == 0 {
		return manifest, nil
	}
	documents := []map[string]any{}
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode overlay target manifest: %w", err)
		}
		if len(document) > 0 {
			documents = append(documents, document)
		}
	}
	for position, overlay := range overlays {
		matched := -1
		for index, document := range documents {
			metadata, _ := document["metadata"].(map[string]any)
			if fmt.Sprint(document["apiVersion"]) == overlay.Target.APIVersion && fmt.Sprint(document["kind"]) == overlay.Target.Kind && fmt.Sprint(metadata["name"]) == overlay.Target.Name {
				if matched >= 0 {
					return nil, fmt.Errorf("overlay %d target %s is duplicated", position, overlay.Target.Name)
				}
				matched = index
			}
		}
		if matched < 0 {
			return nil, fmt.Errorf("overlay %d target %s is missing", position, overlay.Target.Name)
		}
		var current any = documents[matched]
		for _, operation := range overlay.Operations {
			var value any
			if operation.Operation != "remove" {
				var ok bool
				value, ok = overlayValue(values, operation.ValueFrom)
				if !ok {
					return nil, fmt.Errorf("overlay value %s is unavailable", operation.ValueFrom)
				}
			}
			updated, err := applyOverlayOperation(current, pointerTokens(operation.Path), operation.Operation, value)
			if err != nil {
				return nil, fmt.Errorf("apply overlay %d path %s: %w", position, operation.Path, err)
			}
			current = updated
		}
		document, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("overlay %d replaced the resource root", position)
		}
		documents[matched] = document
	}
	result := []byte{}
	for _, document := range documents {
		encoded, err := yaml.Marshal(document)
		if err != nil {
			return nil, fmt.Errorf("encode overlaid manifest: %w", err)
		}
		result = appendManifestDocument(result, encoded)
		if len(result) > maxManifestBytes {
			return nil, fmt.Errorf("overlaid manifest exceeds %d bytes", maxManifestBytes)
		}
	}
	return result, nil
}

func overlayValue(root map[string]any, pointer string) (any, bool) {
	var current any = root
	for _, token := range pointerTokens(pointer) {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		current, ok = object[token]
		if !ok {
			return nil, false
		}
	}
	return current, true
}

func pointerTokens(pointer string) []string {
	raw := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	result := make([]string, len(raw))
	for index, token := range raw {
		result[index] = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
	}
	return result
}

func applyOverlayOperation(current any, tokens []string, operation string, value any) (any, error) {
	if len(tokens) == 0 {
		return nil, errors.New("overlay path cannot replace the document root")
	}
	token := tokens[0]
	if len(tokens) == 1 {
		switch container := current.(type) {
		case map[string]any:
			_, exists := container[token]
			switch operation {
			case "add":
				container[token] = value
			case "replace":
				if !exists {
					return nil, errors.New("replace target does not exist")
				}
				container[token] = value
			case "remove":
				if !exists {
					return nil, errors.New("remove target does not exist")
				}
				delete(container, token)
			}
			return container, nil
		case []any:
			if token == "-" && operation == "add" {
				return append(container, value), nil
			}
			index, err := strconv.Atoi(token)
			if err != nil || index < 0 {
				return nil, errors.New("array index is invalid")
			}
			switch operation {
			case "add":
				if index > len(container) {
					return nil, errors.New("array add index is out of range")
				}
				container = append(container, nil)
				copy(container[index+1:], container[index:])
				container[index] = value
			case "replace":
				if index >= len(container) {
					return nil, errors.New("array replace index is out of range")
				}
				container[index] = value
			case "remove":
				if index >= len(container) {
					return nil, errors.New("array remove index is out of range")
				}
				container = append(container[:index], container[index+1:]...)
			}
			return container, nil
		default:
			return nil, errors.New("overlay parent is not an object or array")
		}
	}
	switch container := current.(type) {
	case map[string]any:
		child, exists := container[token]
		if !exists {
			return nil, errors.New("overlay parent path does not exist")
		}
		updated, err := applyOverlayOperation(child, tokens[1:], operation, value)
		if err != nil {
			return nil, err
		}
		container[token] = updated
		return container, nil
	case []any:
		index, err := strconv.Atoi(token)
		if err != nil || index < 0 || index >= len(container) {
			return nil, errors.New("overlay array path is out of range")
		}
		updated, err := applyOverlayOperation(container[index], tokens[1:], operation, value)
		if err != nil {
			return nil, err
		}
		container[index] = updated
		return container, nil
	default:
		return nil, errors.New("overlay path crosses a scalar value")
	}
}

func materializeManifest(manifest []byte, descriptor catalog.Descriptor) ([]byte, error) {
	excluded := map[string]bool{}
	for _, resource := range descriptor.Spec.ExcludeResources {
		excluded[resource.APIVersion+"|"+resource.Kind+"|"+resource.Name] = false
	}
	result := []byte{}
	resources := map[string]bool{}
	decoder := yaml.NewDecoder(bytes.NewReader(manifest))
	for {
		var document yaml.Node
		err := decoder.Decode(&document)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode Kubernetes manifest: %w", err)
		}
		if len(document.Content) == 0 {
			continue
		}
		var resource manifestResource
		if err := document.Decode(&resource); err != nil {
			return nil, fmt.Errorf("decode Kubernetes resource: %w", err)
		}
		encoded, err := yaml.Marshal(&document)
		if err != nil {
			return nil, fmt.Errorf("encode Kubernetes resource: %w", err)
		}
		key := resource.APIVersion + "|" + resource.Kind + "|" + resource.Metadata.Name
		if _, exists := excluded[key]; exists {
			excluded[key] = true
			continue
		}
		if resource.APIVersion == "" || resource.Kind == "" || resource.Metadata.Name == "" || resources[key] {
			return nil, fmt.Errorf("application package contains an invalid or duplicate resource %s", key)
		}
		resources[key] = true
		result = appendManifestDocument(result, encoded)
	}
	for key, found := range excluded {
		if !found {
			return nil, fmt.Errorf("excluded resource %s is missing from the package", key)
		}
	}
	for _, additional := range descriptor.Spec.AdditionalManifests {
		additionalDecoder := yaml.NewDecoder(strings.NewReader(additional.Content))
		for {
			var document yaml.Node
			err := additionalDecoder.Decode(&document)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("decode additional manifest %s: %w", additional.ID, err)
			}
			if len(document.Content) == 0 {
				continue
			}
			var resource manifestResource
			if err := document.Decode(&resource); err != nil {
				return nil, fmt.Errorf("decode additional resource %s: %w", additional.ID, err)
			}
			key := resource.APIVersion + "|" + resource.Kind + "|" + resource.Metadata.Name
			if resource.APIVersion == "" || resource.Kind == "" || resource.Metadata.Name == "" || resources[key] {
				return nil, fmt.Errorf("additional manifest %s contains an invalid or duplicate resource %s", additional.ID, key)
			}
			encoded, err := yaml.Marshal(&document)
			if err != nil {
				return nil, fmt.Errorf("encode additional resource %s: %w", additional.ID, err)
			}
			resources[key] = true
			result = appendManifestDocument(result, encoded)
			if len(result) > maxManifestBytes {
				return nil, fmt.Errorf("materialized manifest exceeds %d bytes", maxManifestBytes)
			}
		}
	}
	return result, nil
}

func appendManifestDocument(manifest, document []byte) []byte {
	value := bytes.TrimSpace(document)
	if len(value) == 0 {
		return manifest
	}
	if len(manifest) > 0 {
		manifest = append(manifest, []byte("---\n")...)
	}
	manifest = append(manifest, value...)
	return append(manifest, '\n')
}

func digestJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func buildLoadScenarios(scripts map[string][]byte, descriptor catalog.Descriptor) ([]LoadScenario, error) {
	result := make([]LoadScenario, 0, len(descriptor.Spec.Interface.LoadScenarios))
	for _, declared := range descriptor.Spec.Interface.LoadScenarios {
		script, exists := scripts[declared.Script]
		if !exists || len(script) == 0 || !utf8.Valid(script) {
			return nil, fmt.Errorf("load scenario %s script is unavailable", declared.ID)
		}
		digest := sha256.Sum256(script)
		result = append(result, LoadScenario{ID: declared.ID, Engine: declared.Engine, TargetEndpoint: declared.TargetEndpoint, RuntimeImage: declared.RuntimeImage, Script: string(script), ScriptDigest: "sha256:" + hex.EncodeToString(digest[:])})
	}
	return result, nil
}

func validLoadScenarios(actual []LoadScenario, descriptor catalog.Descriptor) bool {
	if len(actual) != len(descriptor.Spec.Interface.LoadScenarios) {
		return false
	}
	for index, declared := range descriptor.Spec.Interface.LoadScenarios {
		value := actual[index]
		digest := sha256.Sum256([]byte(value.Script))
		if value.ID != declared.ID || value.Engine != declared.Engine || value.TargetEndpoint != declared.TargetEndpoint || value.RuntimeImage != declared.RuntimeImage || value.ScriptDigest != "sha256:"+hex.EncodeToString(digest[:]) || value.Script == "" {
			return false
		}
	}
	return true
}

func selectorValue(selector map[string]string) string {
	keys := make([]string, 0, len(selector))
	for key := range selector {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys))
	for _, key := range keys {
		values = append(values, key+"="+selector[key])
	}
	return strings.Join(values, ",")
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
