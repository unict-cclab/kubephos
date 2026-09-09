package targetbinding

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

const pluginID = "io.kubephos.targets.bind"

var identityPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]{0,61}[a-z0-9])?$`)

type Plugin struct{}

type Invocation struct {
	Input json.RawMessage `json:"input"`
}

type Spec struct {
	WorkloadTargetsRef  string   `json:"workloadTargetsRef"`
	RequiredTrait       string   `json:"requiredTrait"`
	IncludeComponentIDs []string `json:"includeComponentIds,omitempty"`
	ExcludeComponentIDs []string `json:"excludeComponentIds,omitempty"`
}

type WorkloadTarget struct {
	ID         string            `json:"id"`
	APIVersion string            `json:"apiVersion"`
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Selector   map[string]string `json:"selector"`
	Traits     []string          `json:"traits"`
}

type TargetBinding struct {
	APIVersion string                `json:"apiVersion"`
	Kind       string                `json:"kind"`
	Metadata   TargetBindingMetadata `json:"metadata"`
	Spec       TargetBindingSpec     `json:"spec"`
}

type TargetBindingMetadata struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type TargetBindingSpec struct {
	SourceArtifactID    string           `json:"sourceArtifactId"`
	SourceDigest        string           `json:"sourceDigest"`
	RequiredTrait       string           `json:"requiredTrait"`
	IncludeComponentIDs []string         `json:"includeComponentIds"`
	ExcludeComponentIDs []string         `json:"excludeComponentIds"`
	Targets             []WorkloadTarget `json:"targets"`
}

type Result struct {
	TargetBinding TargetBinding `json:"targetBinding"`
}

func (Plugin) Manifest() plugins.Manifest {
	return plugins.Manifest{
		ID: pluginID, Name: "Workload target binding", Version: "0.1.0",
		Description:     "Selects compatible application workloads and produces an immutable target binding for component plugins.",
		Schema:          json.RawMessage(`{"type":"object","additionalProperties":false,"required":["workloadTargetsRef","requiredTrait"],"properties":{"workloadTargetsRef":{"type":"string","title":"Application workloads","format":"kubephos-artifact-ref","x-kubephos-artifact-type":"WorkloadTargets","x-kubephos-artifact-version":"v1alpha1"},"requiredTrait":{"type":"string","title":"Required capability","description":"Selects only workloads that declare this trait. Use * to accept every workload.","minLength":1,"maxLength":63,"default":"schedulable"},"includeComponentIds":{"type":"array","title":"Only these components","description":"Optional JSON array of component IDs. An empty array selects every compatible component.","maxItems":200,"default":[],"items":{"type":"string","minLength":1,"maxLength":63}},"excludeComponentIds":{"type":"array","title":"Exclude components","description":"Optional JSON array of component IDs removed from the compatible set.","maxItems":200,"default":[],"items":{"type":"string","minLength":1,"maxLength":63}}}}`),
		ArtifactInputs:  []domain.ArtifactContract{{Type: "WorkloadTargets", Version: "v1alpha1"}},
		ArtifactOutputs: []domain.ArtifactContract{{Type: "TargetBinding", Version: "v1alpha1"}},
		Capabilities:    []string{"targets.bind", "targets.validate"},
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
	if err := validateSpec(spec); err != nil {
		return invalid(report, "$", err.Error())
	}
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "info", Message: "The resolved workload contract and every requested component will be revalidated before the binding is produced."})
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
	if err := validateSpec(spec); err != nil {
		return domain.Plan{}, err
	}
	input, err := json.Marshal(spec)
	if err != nil {
		return domain.Plan{}, err
	}
	return domain.Plan{PluginID: pluginID, Steps: []domain.PlanStep{{
		ID: "bind-workload-targets", Name: "Resolve compatible workload targets", Input: input,
		ArtifactInputs: []domain.ArtifactInput{{Name: "workload-targets", Type: "WorkloadTargets", Version: "v1alpha1", ArtifactID: spec.WorkloadTargetsRef}},
		Outputs:        []domain.ArtifactOutput{{Name: "target-binding", Type: "TargetBinding", Version: "v1alpha1", MediaType: "application/json", Source: "/targetBinding"}},
	}}}, nil
}

func (Plugin) Precheck(ctx context.Context, step domain.PlanStep, log plugins.Logger) (domain.HealthReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.HealthReport{}, err
	}
	spec, source, targets, err := resolve(step)
	if err != nil {
		return unhealthy(err.Error(), "workloads", "invalid"), nil
	}
	selected, err := selectTargets(spec, targets)
	if err != nil {
		return unhealthy(err.Error(), "selection", "invalid"), nil
	}
	if err := log("info", fmt.Sprintf("Validated %d workload declarations and selected %d compatible targets", len(targets), len(selected))); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Workload target selection is valid", Checks: map[string]string{"source": source.Digest, "declared": fmt.Sprint(len(targets)), "selected": fmt.Sprint(len(selected))}}, nil
}

func (Plugin) Execute(ctx context.Context, step domain.PlanStep, log plugins.Logger) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	spec, source, targets, err := resolve(step)
	if err != nil {
		return nil, err
	}
	selected, err := selectTargets(spec, targets)
	if err != nil {
		return nil, err
	}
	binding := TargetBinding{
		APIVersion: "artifacts.kubephos.dev/v1alpha1", Kind: "TargetBinding",
		Metadata: TargetBindingMetadata{Name: "workload-targets-" + strings.ReplaceAll(spec.RequiredTrait, "*", "all"), Version: "v1alpha1"},
		Spec:     TargetBindingSpec{SourceArtifactID: source.ID, SourceDigest: source.Digest, RequiredTrait: spec.RequiredTrait, IncludeComponentIDs: normalized(spec.IncludeComponentIDs), ExcludeComponentIDs: normalized(spec.ExcludeComponentIDs), Targets: selected},
	}
	if err := log("info", fmt.Sprintf("Bound %d workloads for capability %s", len(selected), spec.RequiredTrait)); err != nil {
		return nil, err
	}
	return json.Marshal(Result{TargetBinding: binding})
}

func (Plugin) Verify(ctx context.Context, step domain.PlanStep, raw json.RawMessage, log plugins.Logger) (domain.HealthReport, error) {
	if err := ctx.Err(); err != nil {
		return domain.HealthReport{}, err
	}
	spec, source, targets, err := resolve(step)
	if err != nil {
		return domain.HealthReport{}, err
	}
	expected, err := selectTargets(spec, targets)
	if err != nil {
		return unhealthy(err.Error(), "selection", "invalid"), nil
	}
	var result Result
	if err := json.Unmarshal(raw, &result); err != nil {
		return unhealthy("Target binding result is invalid", "result", "invalid"), nil
	}
	binding := result.TargetBinding
	actualTargets, _ := json.Marshal(binding.Spec.Targets)
	expectedTargets, _ := json.Marshal(expected)
	if binding.APIVersion != "artifacts.kubephos.dev/v1alpha1" || binding.Kind != "TargetBinding" || binding.Metadata.Version != "v1alpha1" || binding.Spec.SourceArtifactID != source.ID || binding.Spec.SourceDigest != source.Digest || binding.Spec.RequiredTrait != spec.RequiredTrait || string(actualTargets) != string(expectedTargets) || !sameStrings(binding.Spec.IncludeComponentIDs, normalized(spec.IncludeComponentIDs)) || !sameStrings(binding.Spec.ExcludeComponentIDs, normalized(spec.ExcludeComponentIDs)) {
		return unhealthy("Target binding does not match the validated selection", "binding", "mismatch"), nil
	}
	if err := log("info", fmt.Sprintf("Verified immutable binding for %d workloads", len(expected))); err != nil {
		return domain.HealthReport{}, err
	}
	return domain.HealthReport{Status: domain.HealthHealthy, Summary: "Target binding is complete and verified", Checks: map[string]string{"source": "verified", "contract": "TargetBinding/v1alpha1", "targets": fmt.Sprint(len(expected))}}, nil
}

func (Plugin) Cleanup(context.Context, domain.PlanStep, json.RawMessage, plugins.Logger) error {
	return nil
}

func resolve(step domain.PlanStep) (Spec, domain.ResolvedArtifact, []WorkloadTarget, error) {
	var spec Spec
	if err := json.Unmarshal(step.Input, &spec); err != nil {
		return Spec{}, domain.ResolvedArtifact{}, nil, err
	}
	if err := validateSpec(spec); err != nil {
		return Spec{}, domain.ResolvedArtifact{}, nil, err
	}
	source, exists := step.ResolvedInputs["workload-targets"]
	if !exists || source.ID != spec.WorkloadTargetsRef || source.Type != "WorkloadTargets" || source.Version != "v1alpha1" || source.Digest == "" {
		return Spec{}, domain.ResolvedArtifact{}, nil, errors.New("verified workload target input is unavailable or incompatible")
	}
	var targets []WorkloadTarget
	if err := json.Unmarshal(source.Value, &targets); err != nil {
		return Spec{}, domain.ResolvedArtifact{}, nil, errors.New("workload target artifact is invalid")
	}
	if err := validateTargets(targets); err != nil {
		return Spec{}, domain.ResolvedArtifact{}, nil, err
	}
	return spec, source, targets, nil
}

func validateSpec(spec Spec) error {
	if !strings.HasPrefix(spec.WorkloadTargetsRef, "art_") {
		return errors.New("select a verified WorkloadTargets artifact")
	}
	if spec.RequiredTrait != "*" && !identityPattern.MatchString(spec.RequiredTrait) {
		return errors.New("required trait is invalid")
	}
	if len(spec.IncludeComponentIDs) > 200 || len(spec.ExcludeComponentIDs) > 200 {
		return errors.New("component selection exceeds 200 entries")
	}
	include, err := identitySet(spec.IncludeComponentIDs)
	if err != nil {
		return fmt.Errorf("include list: %w", err)
	}
	exclude, err := identitySet(spec.ExcludeComponentIDs)
	if err != nil {
		return fmt.Errorf("exclude list: %w", err)
	}
	for value := range include {
		if exclude[value] {
			return fmt.Errorf("component %q cannot be both included and excluded", value)
		}
	}
	return nil
}

func validateTargets(targets []WorkloadTarget) error {
	if len(targets) == 0 || len(targets) > 500 {
		return errors.New("workload target artifact must contain between 1 and 500 targets")
	}
	seen := map[string]bool{}
	for _, target := range targets {
		if !identityPattern.MatchString(target.ID) || target.APIVersion == "" || target.Kind == "" || target.Name == "" || len(target.Selector) == 0 || seen[target.ID] {
			return errors.New("workload target artifact contains an invalid or duplicate target")
		}
		seen[target.ID] = true
		traits := map[string]bool{}
		for _, trait := range target.Traits {
			if !identityPattern.MatchString(trait) || traits[trait] {
				return fmt.Errorf("workload %q contains an invalid or duplicate trait", target.ID)
			}
			traits[trait] = true
		}
	}
	return nil
}

func selectTargets(spec Spec, targets []WorkloadTarget) ([]WorkloadTarget, error) {
	include, _ := identitySet(spec.IncludeComponentIDs)
	exclude, _ := identitySet(spec.ExcludeComponentIDs)
	known := map[string]bool{}
	compatible := map[string]bool{}
	selected := []WorkloadTarget{}
	for _, target := range targets {
		known[target.ID] = true
		compatible[target.ID] = spec.RequiredTrait == "*" || contains(target.Traits, spec.RequiredTrait)
		if compatible[target.ID] && (len(include) == 0 || include[target.ID]) && !exclude[target.ID] {
			selected = append(selected, target)
		}
	}
	for value := range include {
		if !known[value] {
			return nil, fmt.Errorf("included component %q is not declared", value)
		}
		if !compatible[value] {
			return nil, fmt.Errorf("included component %q does not declare required trait %q", value, spec.RequiredTrait)
		}
	}
	for value := range exclude {
		if !known[value] {
			return nil, fmt.Errorf("excluded component %q is not declared", value)
		}
	}
	if len(selected) == 0 {
		return nil, errors.New("selection does not contain any compatible workload")
	}
	return selected, nil
}

func identitySet(values []string) (map[string]bool, error) {
	result := map[string]bool{}
	for _, value := range values {
		if !identityPattern.MatchString(value) || result[value] {
			return nil, errors.New("component IDs must be valid and unique")
		}
		result[value] = true
	}
	return result, nil
}

func normalized(values []string) []string {
	result := append([]string{}, values...)
	sort.Strings(result)
	return result
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func invalid(report domain.ValidationReport, path, message string) domain.ValidationReport {
	report.Valid = false
	report.Issues = append(report.Issues, domain.ValidationIssue{Level: "error", Path: path, Message: message})
	return report
}

func unhealthy(summary, key, value string) domain.HealthReport {
	return domain.HealthReport{Status: domain.HealthUnhealthy, Summary: summary, Checks: map[string]string{key: value}}
}
