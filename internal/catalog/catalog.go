package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/schema"
)

const maxDescriptorBytes = 512 * 1024

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{2,127}$`)
	versionPattern  = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z.+_-]{0,63}$`)
	namePattern     = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{0,62}$`)
	groupPattern    = regexp.MustCompile(`^[A-Za-z0-9](?:[-_.A-Za-z0-9]{0,61}[A-Za-z0-9])?$`)
	commitPattern   = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)
	digestReference = regexp.MustCompile(`^.+@sha256:[0-9a-fA-F]{64}$`)
)

type Descriptor struct {
	APIVersion string   `json:"apiVersion" yaml:"apiVersion"`
	Kind       string   `json:"kind" yaml:"kind"`
	Metadata   Metadata `json:"metadata" yaml:"metadata"`
	Spec       Spec     `json:"spec" yaml:"spec"`
}

type Metadata struct {
	ID          string `json:"id" yaml:"id"`
	Name        string `json:"name" yaml:"name"`
	Version     string `json:"version" yaml:"version"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

type Spec struct {
	Package             Package              `json:"package" yaml:"package"`
	Materializer        string               `json:"materializer,omitempty" yaml:"materializer,omitempty"`
	MaterializerConfig  map[string]any       `json:"materializerConfig,omitempty" yaml:"materializerConfig,omitempty"`
	Interface           Interface            `json:"interface" yaml:"interface"`
	ValuesSchema        map[string]any       `json:"valuesSchema" yaml:"valuesSchema"`
	Defaults            map[string]any       `json:"defaults" yaml:"defaults"`
	AdditionalManifests []AdditionalManifest `json:"additionalManifests,omitempty" yaml:"additionalManifests,omitempty"`
	ExcludeResources    []Workload           `json:"excludeResources,omitempty" yaml:"excludeResources,omitempty"`
	Overlays            []Overlay            `json:"overlays,omitempty" yaml:"overlays,omitempty"`
}

type Package struct {
	Type       string `json:"type" yaml:"type"`
	Format     string `json:"format" yaml:"format"`
	Repository string `json:"repository,omitempty" yaml:"repository,omitempty"`
	Revision   string `json:"revision,omitempty" yaml:"revision,omitempty"`
	Path       string `json:"path,omitempty" yaml:"path,omitempty"`
	Reference  string `json:"reference,omitempty" yaml:"reference,omitempty"`
	Entrypoint string `json:"entrypoint,omitempty" yaml:"entrypoint,omitempty"`
}

type Interface struct {
	Group         string         `json:"group" yaml:"group"`
	Components    []Component    `json:"components" yaml:"components"`
	Endpoints     []Endpoint     `json:"endpoints,omitempty" yaml:"endpoints,omitempty"`
	LoadScenarios []LoadScenario `json:"loadScenarios,omitempty" yaml:"loadScenarios,omitempty"`
}

type Component struct {
	ID           string            `json:"id" yaml:"id"`
	Workload     Workload          `json:"workload" yaml:"workload"`
	Selector     map[string]string `json:"selector" yaml:"selector"`
	Traits       []string          `json:"traits" yaml:"traits"`
	Index        *int              `json:"index,omitempty" yaml:"index,omitempty"`
	Dependencies []string          `json:"dependencies,omitempty" yaml:"dependencies,omitempty"`
}

type Workload struct {
	APIVersion string `json:"apiVersion" yaml:"apiVersion"`
	Kind       string `json:"kind" yaml:"kind"`
	Name       string `json:"name" yaml:"name"`
}

type Endpoint struct {
	ID        string `json:"id" yaml:"id"`
	Component string `json:"component" yaml:"component"`
	Service   string `json:"service" yaml:"service"`
	Port      int    `json:"port" yaml:"port"`
	Protocol  string `json:"protocol" yaml:"protocol"`
	Path      string `json:"path,omitempty" yaml:"path,omitempty"`
}

type LoadScenario struct {
	ID             string `json:"id" yaml:"id"`
	Engine         string `json:"engine" yaml:"engine"`
	Script         string `json:"script" yaml:"script"`
	RuntimeImage   string `json:"runtimeImage" yaml:"runtimeImage"`
	TargetEndpoint string `json:"targetEndpoint" yaml:"targetEndpoint"`
}

type AdditionalManifest struct {
	ID      string `json:"id" yaml:"id"`
	Content string `json:"content" yaml:"content"`
}

type Overlay struct {
	Target     Workload           `json:"target" yaml:"target"`
	Operations []OverlayOperation `json:"operations" yaml:"operations"`
}

type OverlayOperation struct {
	Operation string `json:"op" yaml:"op"`
	Path      string `json:"path" yaml:"path"`
	ValueFrom string `json:"valueFrom,omitempty" yaml:"valueFrom,omitempty"`
}

func LoadDirectory(root string) ([]domain.CatalogApplication, error) {
	entries := []domain.CatalogApplication{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("catalog entry %s cannot be a symbolic link", path)
		}
		if entry.IsDir() || entry.Name() != "application.yaml" {
			return nil
		}
		raw, err := readLimited(path, maxDescriptorBytes)
		if err != nil {
			return err
		}
		application, err := Parse(raw, "built-in")
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if application.Descriptor == nil {
			return fmt.Errorf("%s: descriptor is empty", path)
		}
		entries = append(entries, application)
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("catalog directory %s does not exist", root)
	}
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(left, right int) bool {
		if entries[left].ID == entries[right].ID {
			return entries[left].Version < entries[right].Version
		}
		return entries[left].ID < entries[right].ID
	})
	for index := 1; index < len(entries); index++ {
		if entries[index-1].ID == entries[index].ID && entries[index-1].Version == entries[index].Version {
			return nil, fmt.Errorf("duplicate application %s version %s", entries[index].ID, entries[index].Version)
		}
	}
	return entries, nil
}

func Parse(raw []byte, origin string) (domain.CatalogApplication, error) {
	if len(raw) == 0 || len(raw) > maxDescriptorBytes {
		return domain.CatalogApplication{}, fmt.Errorf("descriptor must contain between 1 and %d bytes", maxDescriptorBytes)
	}
	if origin != "built-in" && origin != "imported" {
		return domain.CatalogApplication{}, errors.New("invalid catalog origin")
	}
	var descriptor Descriptor
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&descriptor); err != nil {
		return domain.CatalogApplication{}, fmt.Errorf("invalid descriptor: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return domain.CatalogApplication{}, errors.New("descriptor must contain exactly one document")
		}
		return domain.CatalogApplication{}, fmt.Errorf("invalid descriptor: %w", err)
	}
	if err := validate(descriptor, origin); err != nil {
		return domain.CatalogApplication{}, err
	}
	canonical, err := json.Marshal(descriptor)
	if err != nil {
		return domain.CatalogApplication{}, err
	}
	digest := sha256.Sum256(canonical)
	return domain.CatalogApplication{
		ID:          descriptor.Metadata.ID,
		Reference:   Reference(descriptor.Metadata.ID, descriptor.Metadata.Version),
		Name:        descriptor.Metadata.Name,
		Version:     descriptor.Metadata.Version,
		Description: descriptor.Metadata.Description,
		Origin:      origin,
		Descriptor:  canonical,
		Digest:      "sha256:" + hex.EncodeToString(digest[:]),
		Enabled:     true,
	}, nil
}

func Reference(applicationID, version string) string {
	return "app:" + applicationID + "@" + version
}

func ParseReference(value string) (string, string, error) {
	if !strings.HasPrefix(value, "app:") {
		return "", "", errors.New("application reference must start with app:")
	}
	identity := strings.TrimPrefix(value, "app:")
	separator := strings.LastIndex(identity, "@")
	if separator < 1 || separator == len(identity)-1 {
		return "", "", errors.New("application reference must include ID and version")
	}
	applicationID, version := identity[:separator], identity[separator+1:]
	if !idPattern.MatchString(applicationID) || !versionPattern.MatchString(version) {
		return "", "", errors.New("application reference is invalid")
	}
	return applicationID, version, nil
}

func validate(descriptor Descriptor, origin string) error {
	if descriptor.APIVersion != "catalog.kubephos.dev/v1alpha1" || descriptor.Kind != "Application" {
		return errors.New("apiVersion must be catalog.kubephos.dev/v1alpha1 and kind must be Application")
	}
	metadata := descriptor.Metadata
	if !idPattern.MatchString(metadata.ID) {
		return errors.New("metadata.id is invalid")
	}
	if strings.TrimSpace(metadata.Name) == "" || len([]rune(metadata.Name)) > 80 {
		return errors.New("metadata.name must contain between 1 and 80 characters")
	}
	if !versionPattern.MatchString(metadata.Version) {
		return errors.New("metadata.version is invalid")
	}
	if len([]rune(metadata.Description)) > 280 {
		return errors.New("metadata.description cannot exceed 280 characters")
	}
	if err := validatePackage(descriptor.Spec.Package, origin); err != nil {
		return err
	}
	if descriptor.Spec.Materializer != "" && !idPattern.MatchString(descriptor.Spec.Materializer) {
		return errors.New("spec.materializer must be a valid plugin ID")
	}
	if descriptor.Spec.Materializer == "" && len(descriptor.Spec.MaterializerConfig) != 0 {
		return errors.New("spec.materializerConfig requires spec.materializer")
	}
	if len(descriptor.Spec.Interface.Components) == 0 {
		return errors.New("spec.interface.components must contain at least one component")
	}
	if !groupPattern.MatchString(descriptor.Spec.Interface.Group) {
		return errors.New("spec.interface.group must be a valid Kubernetes label value")
	}
	componentIDs := map[string]bool{}
	for position, component := range descriptor.Spec.Interface.Components {
		path := fmt.Sprintf("spec.interface.components[%d]", position)
		if !namePattern.MatchString(component.ID) || componentIDs[component.ID] {
			return fmt.Errorf("%s.id is invalid or duplicated", path)
		}
		componentIDs[component.ID] = true
		if strings.TrimSpace(component.Workload.APIVersion) == "" || strings.TrimSpace(component.Workload.Kind) == "" || !namePattern.MatchString(component.Workload.Name) {
			return fmt.Errorf("%s.workload is invalid", path)
		}
		if len(component.Selector) == 0 {
			return fmt.Errorf("%s.selector cannot be empty", path)
		}
		traits := map[string]bool{}
		for _, trait := range component.Traits {
			if !namePattern.MatchString(trait) || traits[trait] {
				return fmt.Errorf("%s.traits contains an invalid or duplicated value", path)
			}
			traits[trait] = true
		}
	}
	if err := validateComponentTopology(descriptor.Spec.Interface.Components, componentIDs); err != nil {
		return err
	}
	endpointIDs := map[string]bool{}
	for position, endpoint := range descriptor.Spec.Interface.Endpoints {
		path := fmt.Sprintf("spec.interface.endpoints[%d]", position)
		if !namePattern.MatchString(endpoint.ID) || endpointIDs[endpoint.ID] {
			return fmt.Errorf("%s.id is invalid or duplicated", path)
		}
		endpointIDs[endpoint.ID] = true
		if !componentIDs[endpoint.Component] || !namePattern.MatchString(endpoint.Service) || endpoint.Port < 1 || endpoint.Port > 65535 || !namePattern.MatchString(strings.ToLower(endpoint.Protocol)) {
			return fmt.Errorf("%s is invalid", path)
		}
		if endpoint.Path != "" && !strings.HasPrefix(endpoint.Path, "/") {
			return fmt.Errorf("%s.path must start with /", path)
		}
	}
	loadScenarioIDs := map[string]bool{}
	for position, scenario := range descriptor.Spec.Interface.LoadScenarios {
		path := fmt.Sprintf("spec.interface.loadScenarios[%d]", position)
		if !namePattern.MatchString(scenario.ID) || loadScenarioIDs[scenario.ID] {
			return fmt.Errorf("%s.id is invalid or duplicated", path)
		}
		loadScenarioIDs[scenario.ID] = true
		if scenario.Engine != "locust" {
			return fmt.Errorf("%s.engine must be locust", path)
		}
		if !safePackagePath(scenario.Script) || !strings.HasSuffix(strings.ToLower(scenario.Script), ".py") {
			return fmt.Errorf("%s.script must be a relative Python file", path)
		}
		if strings.TrimSpace(scenario.RuntimeImage) == "" || strings.ContainsAny(scenario.RuntimeImage, "\n\r ") {
			return fmt.Errorf("%s.runtimeImage is invalid", path)
		}
		if !endpointIDs[scenario.TargetEndpoint] {
			return fmt.Errorf("%s.targetEndpoint is invalid", path)
		}
	}
	manifestIDs := map[string]bool{}
	for position, manifest := range descriptor.Spec.AdditionalManifests {
		path := fmt.Sprintf("spec.additionalManifests[%d]", position)
		if !namePattern.MatchString(manifest.ID) || manifestIDs[manifest.ID] {
			return fmt.Errorf("%s.id is invalid or duplicated", path)
		}
		manifestIDs[manifest.ID] = true
		if strings.TrimSpace(manifest.Content) == "" || len(manifest.Content) > maxDescriptorBytes {
			return fmt.Errorf("%s.content is empty or too large", path)
		}
	}
	excluded := map[string]bool{}
	for position, resource := range descriptor.Spec.ExcludeResources {
		path := fmt.Sprintf("spec.excludeResources[%d]", position)
		if strings.TrimSpace(resource.APIVersion) == "" || strings.TrimSpace(resource.Kind) == "" || !namePattern.MatchString(resource.Name) || excluded[workloadKey(resource)] {
			return fmt.Errorf("%s is invalid or duplicated", path)
		}
		excluded[workloadKey(resource)] = true
	}
	if descriptor.Spec.ValuesSchema == nil || descriptor.Spec.ValuesSchema["type"] != "object" {
		return errors.New("spec.valuesSchema must be an object schema")
	}
	if descriptor.Spec.Defaults == nil {
		return errors.New("spec.defaults must be an object")
	}
	schemaValue, err := json.Marshal(descriptor.Spec.ValuesSchema)
	if err != nil {
		return fmt.Errorf("spec.valuesSchema is invalid: %w", err)
	}
	if err := schema.ValidateDefinition(schemaValue); err != nil {
		return fmt.Errorf("spec.valuesSchema is invalid: %w", err)
	}
	defaults, err := json.Marshal(descriptor.Spec.Defaults)
	if err != nil {
		return fmt.Errorf("spec.defaults is invalid: %w", err)
	}
	issues, err := schema.Validate(schemaValue, defaults)
	if err != nil {
		return err
	}
	if len(issues) > 0 {
		return fmt.Errorf("spec.defaults does not satisfy valuesSchema at %s: %s", issues[0].Path, issues[0].Message)
	}
	if err := validateOverlays(descriptor); err != nil {
		return err
	}
	return nil
}

func validateComponentTopology(components []Component, componentIDs map[string]bool) error {
	declared := false
	for _, component := range components {
		declared = declared || component.Index != nil || len(component.Dependencies) != 0
	}
	if !declared {
		return nil
	}
	byID := make(map[string]Component, len(components))
	for position, component := range components {
		path := fmt.Sprintf("spec.interface.components[%d]", position)
		if component.Index == nil || *component.Index < 0 || *component.Index > 1000 {
			return fmt.Errorf("%s.index must be between 0 and 1000 when application topology is declared", path)
		}
		seen := map[string]bool{}
		for _, dependency := range component.Dependencies {
			if !componentIDs[dependency] || dependency == component.ID || seen[dependency] {
				return fmt.Errorf("%s.dependencies contains an invalid or duplicated component", path)
			}
			seen[dependency] = true
		}
		byID[component.ID] = component
	}
	for _, component := range components {
		for _, dependency := range component.Dependencies {
			if *byID[dependency].Index < *component.Index {
				return fmt.Errorf("component %s cannot depend on earlier topology index %d", component.ID, *byID[dependency].Index)
			}
		}
	}
	state := map[string]uint8{}
	var visit func(string) error
	visit = func(componentID string) error {
		if state[componentID] == 1 {
			return fmt.Errorf("application topology contains a cycle at component %s", componentID)
		}
		if state[componentID] == 2 {
			return nil
		}
		state[componentID] = 1
		for _, dependency := range byID[componentID].Dependencies {
			if err := visit(dependency); err != nil {
				return err
			}
		}
		state[componentID] = 2
		return nil
	}
	for _, component := range components {
		if err := visit(component.ID); err != nil {
			return err
		}
	}
	return nil
}

func validateOverlays(descriptor Descriptor) error {
	targets := map[string]bool{}
	for _, component := range descriptor.Spec.Interface.Components {
		targets[workloadKey(component.Workload)] = true
	}
	for _, endpoint := range descriptor.Spec.Interface.Endpoints {
		targets["v1|Service|"+endpoint.Service] = true
	}
	for position, overlay := range descriptor.Spec.Overlays {
		path := fmt.Sprintf("spec.overlays[%d]", position)
		if !targets[workloadKey(overlay.Target)] {
			return fmt.Errorf("%s.target must reference a declared workload or service", path)
		}
		if len(overlay.Operations) == 0 || len(overlay.Operations) > 64 {
			return fmt.Errorf("%s.operations must contain between 1 and 64 entries", path)
		}
		seen := map[string]bool{}
		for operationPosition, operation := range overlay.Operations {
			operationPath := fmt.Sprintf("%s.operations[%d]", path, operationPosition)
			if operation.Operation != "add" && operation.Operation != "replace" && operation.Operation != "remove" {
				return fmt.Errorf("%s.op must be add, replace or remove", operationPath)
			}
			if !safeOverlayPath(operation.Path) || seen[operation.Path] {
				return fmt.Errorf("%s.path is invalid or duplicated", operationPath)
			}
			seen[operation.Path] = true
			if operation.Operation == "remove" {
				if operation.ValueFrom != "" {
					return fmt.Errorf("%s.valueFrom is not allowed for remove", operationPath)
				}
				continue
			}
			if !validJSONPointer(operation.ValueFrom) {
				return fmt.Errorf("%s.valueFrom must be a valid JSON pointer", operationPath)
			}
			if _, ok := pointerValue(descriptor.Spec.Defaults, operation.ValueFrom); !ok {
				return fmt.Errorf("%s.valueFrom must resolve in spec.defaults", operationPath)
			}
		}
	}
	return nil
}

func safePackagePath(value string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(strings.TrimSpace(value)))
	return cleaned != "." && cleaned == strings.TrimSpace(value) && !strings.HasPrefix(cleaned, "/") && !strings.HasPrefix(cleaned, "../")
}

func workloadKey(value Workload) string {
	return value.APIVersion + "|" + value.Kind + "|" + value.Name
}

func safeOverlayPath(value string) bool {
	if !validJSONPointer(value) {
		return false
	}
	return strings.HasPrefix(value, "/spec/") || strings.HasPrefix(value, "/metadata/labels/") || strings.HasPrefix(value, "/metadata/annotations/")
}

func validJSONPointer(value string) bool {
	if !strings.HasPrefix(value, "/") || len(value) > 512 {
		return false
	}
	for position := 0; position < len(value); position++ {
		if value[position] == '~' && (position+1 >= len(value) || value[position+1] != '0' && value[position+1] != '1') {
			return false
		}
	}
	return true
}

func pointerValue(root map[string]any, pointer string) (any, bool) {
	var current any = root
	for _, token := range strings.Split(strings.TrimPrefix(pointer, "/"), "/") {
		token = strings.ReplaceAll(strings.ReplaceAll(token, "~1", "/"), "~0", "~")
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

func validatePackage(value Package, origin string) error {
	formats := map[string]bool{"plain-yaml": true, "kustomize": true, "helm": true}
	if !formats[value.Format] {
		return errors.New("spec.package.format must be plain-yaml, kustomize or helm")
	}
	switch value.Type {
	case "git":
		parsed, err := url.Parse(value.Repository)
		if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
			return errors.New("spec.package.repository must be an HTTPS URL")
		}
		if !commitPattern.MatchString(value.Revision) {
			return errors.New("spec.package.revision must be a complete 40-character Git commit SHA")
		}
		if !safeRelativePath(value.Path, true) {
			return errors.New("spec.package.path must be a safe relative path")
		}
		if !safeRelativePath(value.Entrypoint, false) {
			return errors.New("spec.package.entrypoint must be a safe relative path")
		}
		if value.Reference != "" {
			return errors.New("git packages cannot define reference")
		}
	case "oci":
		if !digestReference.MatchString(value.Reference) {
			return errors.New("spec.package.reference must be an OCI reference pinned by sha256 digest")
		}
		if !safeRelativePath(value.Entrypoint, false) {
			return errors.New("spec.package.entrypoint must be a safe relative path")
		}
		if value.Repository != "" || value.Revision != "" || value.Path != "" {
			return errors.New("oci packages cannot define repository, revision or path")
		}
	case "embedded":
		if origin != "built-in" {
			return errors.New("imported applications cannot use embedded packages")
		}
		if !safeRelativePath(value.Entrypoint, false) {
			return errors.New("spec.package.entrypoint must be a safe relative path")
		}
		if value.Repository != "" || value.Revision != "" || value.Path != "" || value.Reference != "" {
			return errors.New("embedded packages cannot define repository, revision, path or reference")
		}
	default:
		return errors.New("spec.package.type must be git, oci or embedded")
	}
	return nil
}

func safeRelativePath(value string, allowCurrent bool) bool {
	if value == "" || filepath.IsAbs(value) || strings.Contains(value, `\`) {
		return false
	}
	cleaned := filepath.ToSlash(filepath.Clean(value))
	return cleaned == value && (allowCurrent || cleaned != ".") && !strings.HasPrefix(cleaned, "../")
}

func readLimited(path string, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	value, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(value)) > maximum {
		return nil, fmt.Errorf("%s exceeds %d bytes", path, maximum)
	}
	return value, nil
}
