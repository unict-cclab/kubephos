package workflows

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/artifacts"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
	"kubephos.dev/kubephos/internal/schema"
)

var stageIDPattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

type Result struct {
	Definition domain.PipelineDefinition
	Resolution domain.PipelineResolution
	Validation domain.ValidationReport
	Hash       string
}

func ResolveStage(pipeline domain.Pipeline, position int, produced map[string]map[string]string) (domain.ResolvedPipelineStage, error) {
	if position < 0 || position >= len(pipeline.Resolution.Stages) || position >= len(pipeline.Definition.Stages) {
		return domain.ResolvedPipelineStage{}, errors.New("pipeline stage position is invalid")
	}
	resolved := pipeline.Resolution.Stages[position]
	definition := pipeline.Definition.Stages[position]
	var spec any
	if err := json.Unmarshal(resolved.Spec, &spec); err != nil {
		return domain.ResolvedPipelineStage{}, fmt.Errorf("stage %q resolved spec is invalid: %w", resolved.ID, err)
	}
	resolved.Plan.Steps = append([]domain.PlanStep(nil), resolved.Plan.Steps...)
	for bindingIndex, binding := range definition.Bindings {
		stageOutputs, ok := produced[binding.FromStage]
		if !ok {
			return domain.ResolvedPipelineStage{}, fmt.Errorf("stage %q output is not available", binding.FromStage)
		}
		artifactID, ok := stageOutputs[binding.FromOutput]
		if !ok || artifactID == "" {
			return domain.ResolvedPipelineStage{}, fmt.Errorf("output %q from stage %q is not available", binding.FromOutput, binding.FromStage)
		}
		if err := setPointer(&spec, binding.Path, artifactID); err != nil {
			return domain.ResolvedPipelineStage{}, fmt.Errorf("stage %q binding %d: %w", resolved.ID, bindingIndex+1, err)
		}
		token := fmt.Sprintf("art_pipeline_%d_%d", position+1, bindingIndex+1)
		replaced := false
		for stepIndex := range resolved.Plan.Steps {
			resolved.Plan.Steps[stepIndex].ArtifactInputs = append([]domain.ArtifactInput(nil), resolved.Plan.Steps[stepIndex].ArtifactInputs...)
			for inputIndex := range resolved.Plan.Steps[stepIndex].ArtifactInputs {
				input := &resolved.Plan.Steps[stepIndex].ArtifactInputs[inputIndex]
				if input.ArtifactID == token {
					input.ArtifactID = artifactID
					replaced = true
				}
			}
			input, changed, err := replaceJSONToken(resolved.Plan.Steps[stepIndex].Input, token, artifactID)
			if err != nil {
				return domain.ResolvedPipelineStage{}, fmt.Errorf("stage %q plan input is invalid: %w", resolved.ID, err)
			}
			if changed {
				resolved.Plan.Steps[stepIndex].Input = input
				replaced = true
			}
		}
		if !replaced {
			return domain.ResolvedPipelineStage{}, fmt.Errorf("stage %q binding %d is absent from the resolved plan", resolved.ID, bindingIndex+1)
		}
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return domain.ResolvedPipelineStage{}, err
	}
	resolved.Spec = raw
	return resolved, nil
}

func replaceJSONToken(raw json.RawMessage, token, value string) (json.RawMessage, bool, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, false, err
	}
	changed := replaceValue(&decoded, token, value)
	if !changed {
		return raw, false, nil
	}
	replaced, err := json.Marshal(decoded)
	return replaced, true, err
}

func replaceValue(current *any, token, value string) bool {
	switch typed := (*current).(type) {
	case string:
		if typed == token {
			*current = value
			return true
		}
	case map[string]any:
		changed := false
		for key, item := range typed {
			if replaceValue(&item, token, value) {
				typed[key] = item
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for index := range typed {
			if replaceValue(&typed[index], token, value) {
				changed = true
			}
		}
		return changed
	}
	return false
}

func Validate(ctx context.Context, registry *plugins.Registry, definition domain.PipelineDefinition) (Result, error) {
	normalized, err := normalize(definition)
	if err != nil {
		return Result{}, err
	}
	resolution := domain.PipelineResolution{Stages: make([]domain.ResolvedPipelineStage, 0, len(normalized.Stages))}
	issues := []domain.ValidationIssue{}
	outputs := map[string]map[string]domain.ArtifactContract{}
	for position, stage := range normalized.Stages {
		plugin, err := registry.Get(stage.PluginID)
		if err != nil {
			return Result{}, fmt.Errorf("stage %q: %w", stage.ID, err)
		}
		resolvedSpec, tokens, err := bindPlaceholders(stage, position, outputs)
		if err != nil {
			return Result{}, err
		}
		manifest := plugin.Manifest()
		schemaIssues, err := schema.Validate(manifest.Schema, resolvedSpec)
		if err != nil {
			return Result{}, fmt.Errorf("stage %q schema: %w", stage.ID, err)
		}
		for _, issue := range schemaIssues {
			issues = append(issues, domain.ValidationIssue{Level: "error", Path: "stages." + stage.ID + ".spec" + strings.TrimPrefix(issue.Path, "$"), Message: issue.Message})
		}
		if len(schemaIssues) > 0 {
			continue
		}
		pluginValidation := plugin.Validate(ctx, resolvedSpec)
		for _, issue := range pluginValidation.Issues {
			issue.Path = "stages." + stage.ID + "." + strings.TrimPrefix(issue.Path, ".")
			issues = append(issues, issue)
		}
		if !pluginValidation.Valid {
			continue
		}
		plan, err := plugin.Plan(ctx, resolvedSpec)
		if err != nil {
			return Result{}, fmt.Errorf("stage %q planning failed: %w", stage.ID, err)
		}
		if len(plan.Steps) == 0 {
			return Result{}, fmt.Errorf("stage %q produced an empty plan", stage.ID)
		}
		if normalized.CleanupAfterRun && mutatingPlan(plan) && !manifest.HasCapability("lifecycle.cleanup") {
			return Result{}, fmt.Errorf("stage %q has mutable steps but plugin %s does not declare lifecycle.cleanup", stage.ID, manifest.ID)
		}
		if err := artifacts.ValidatePlan(plan, manifest.ArtifactInputs, manifest.ArtifactOutputs); err != nil {
			return Result{}, fmt.Errorf("stage %q artifact graph: %w", stage.ID, err)
		}
		resolvedBindings, err := validateBoundInputs(stage, plan, tokens, outputs)
		if err != nil {
			return Result{}, err
		}
		normalized.Stages[position].Bindings = resolvedBindings
		stageOutputs, err := collectOutputs(stage.ID, plan)
		if err != nil {
			return Result{}, err
		}
		outputs[stage.ID] = stageOutputs
		resolution.Stages = append(resolution.Stages, domain.ResolvedPipelineStage{
			ID: stage.ID, PluginID: stage.PluginID, PluginVersion: manifest.Version, PluginDigest: manifest.Runtime.Digest,
			Spec: resolvedSpec, Bindings: resolvedBindings, Plan: plan,
		})
		issues = append(issues, domain.ValidationIssue{Level: "info", Path: "stages." + stage.ID, Message: fmt.Sprintf("Validated %s %s with %d planned step(s).", manifest.Name, manifest.Version, len(plan.Steps))})
	}
	validation := domain.ValidationReport{Valid: true, Issues: issues, CheckedAt: time.Now().UTC()}
	for _, issue := range issues {
		if issue.Level == "error" {
			validation.Valid = false
		}
	}
	if !validation.Valid {
		return Result{Definition: normalized, Resolution: resolution, Validation: validation}, nil
	}
	resultOutputs, ok := outputs[normalized.Result.Stage]
	if !ok {
		return Result{}, fmt.Errorf("result stage %q is unavailable", normalized.Result.Stage)
	}
	if normalized.Result.Output == "" {
		matches := []string{}
		for output, candidate := range resultOutputs {
			if (normalized.Result.Type == "" || candidate.Type == normalized.Result.Type) && (normalized.Result.Version == "" || candidate.Version == normalized.Result.Version) {
				matches = append(matches, output)
			}
		}
		if len(matches) != 1 {
			return Result{}, fmt.Errorf("result stage %q does not have one unambiguous matching output", normalized.Result.Stage)
		}
		normalized.Result.Output = matches[0]
	}
	contract, ok := resultOutputs[normalized.Result.Output]
	if !ok {
		return Result{}, fmt.Errorf("result output %q is not produced by stage %q", normalized.Result.Output, normalized.Result.Stage)
	}
	if (normalized.Result.Type != "" && normalized.Result.Type != contract.Type) || (normalized.Result.Version != "" && normalized.Result.Version != contract.Version) {
		return Result{}, fmt.Errorf("result output %q produces %s/%s instead of %s/%s", normalized.Result.Output, contract.Type, contract.Version, normalized.Result.Type, normalized.Result.Version)
	}
	normalized.Result.Type = contract.Type
	normalized.Result.Version = contract.Version
	resolution.Result = contract
	hash, err := fingerprint(normalized, resolution)
	if err != nil {
		return Result{}, err
	}
	return Result{Definition: normalized, Resolution: resolution, Validation: validation, Hash: hash}, nil
}

func mutatingPlan(plan domain.Plan) bool {
	for _, step := range plan.Steps {
		if step.Mutating {
			return true
		}
	}
	return false
}

func normalize(definition domain.PipelineDefinition) (domain.PipelineDefinition, error) {
	if len(definition.Stages) < 1 || len(definition.Stages) > 32 {
		return domain.PipelineDefinition{}, errors.New("a pipeline requires between 1 and 32 stages")
	}
	seen := map[string]bool{}
	for index := range definition.Stages {
		stage := &definition.Stages[index]
		stage.ID = strings.TrimSpace(stage.ID)
		stage.PluginID = strings.TrimSpace(stage.PluginID)
		stage.Title = strings.TrimSpace(stage.Title)
		if !stageIDPattern.MatchString(stage.ID) {
			return domain.PipelineDefinition{}, fmt.Errorf("stage %d id must start with a lowercase letter and contain only lowercase letters, digits or hyphens", index+1)
		}
		if seen[stage.ID] {
			return domain.PipelineDefinition{}, fmt.Errorf("stage id %q is duplicated", stage.ID)
		}
		seen[stage.ID] = true
		if stage.PluginID == "" {
			return domain.PipelineDefinition{}, fmt.Errorf("stage %q requires a plugin", stage.ID)
		}
		if stage.Title == "" || len(stage.Title) > 120 {
			return domain.PipelineDefinition{}, fmt.Errorf("stage %q title must contain between 1 and 120 characters", stage.ID)
		}
		if len(stage.Spec) == 0 {
			stage.Spec = json.RawMessage(`{}`)
		}
		var value map[string]any
		if err := json.Unmarshal(stage.Spec, &value); err != nil || value == nil {
			return domain.PipelineDefinition{}, fmt.Errorf("stage %q spec must be a JSON object", stage.ID)
		}
		paths := map[string]bool{}
		for bindingIndex := range stage.Bindings {
			binding := &stage.Bindings[bindingIndex]
			binding.Path = strings.TrimSpace(binding.Path)
			binding.FromStage = strings.TrimSpace(binding.FromStage)
			binding.FromOutput = strings.TrimSpace(binding.FromOutput)
			if binding.Path == "" || binding.FromStage == "" {
				return domain.PipelineDefinition{}, fmt.Errorf("stage %q binding %d is incomplete", stage.ID, bindingIndex+1)
			}
			if paths[binding.Path] {
				return domain.PipelineDefinition{}, fmt.Errorf("stage %q binds path %q more than once", stage.ID, binding.Path)
			}
			paths[binding.Path] = true
		}
	}
	definition.Result.Stage = strings.TrimSpace(definition.Result.Stage)
	definition.Result.Output = strings.TrimSpace(definition.Result.Output)
	definition.Result.Type = strings.TrimSpace(definition.Result.Type)
	definition.Result.Version = strings.TrimSpace(definition.Result.Version)
	if definition.Result.Stage == "" {
		return domain.PipelineDefinition{}, errors.New("pipeline result stage is required")
	}
	return definition, nil
}

func bindPlaceholders(stage domain.PipelineStage, position int, outputs map[string]map[string]domain.ArtifactContract) (json.RawMessage, map[string]domain.PipelineBinding, error) {
	var spec any
	if err := json.Unmarshal(stage.Spec, &spec); err != nil {
		return nil, nil, err
	}
	tokens := map[string]domain.PipelineBinding{}
	for bindingIndex, binding := range stage.Bindings {
		if _, ok := outputs[binding.FromStage]; !ok {
			return nil, nil, fmt.Errorf("stage %q binding %d must reference an earlier stage", stage.ID, bindingIndex+1)
		}
		if binding.FromOutput != "" {
			if _, ok := outputs[binding.FromStage][binding.FromOutput]; !ok {
				return nil, nil, fmt.Errorf("stage %q binding %d references unavailable output %q from stage %q", stage.ID, bindingIndex+1, binding.FromOutput, binding.FromStage)
			}
		}
		token := fmt.Sprintf("art_pipeline_%d_%d", position+1, bindingIndex+1)
		if err := setPointer(&spec, binding.Path, token); err != nil {
			return nil, nil, fmt.Errorf("stage %q binding %d: %w", stage.ID, bindingIndex+1, err)
		}
		tokens[token] = binding
	}
	raw, err := json.Marshal(spec)
	return raw, tokens, err
}

func validateBoundInputs(stage domain.PipelineStage, plan domain.Plan, tokens map[string]domain.PipelineBinding, outputs map[string]map[string]domain.ArtifactContract) ([]domain.PipelineBinding, error) {
	used := map[string]bool{}
	resolved := append([]domain.PipelineBinding(nil), stage.Bindings...)
	for _, step := range plan.Steps {
		for _, input := range step.ArtifactInputs {
			binding, ok := tokens[input.ArtifactID]
			if !ok {
				continue
			}
			outputName := binding.FromOutput
			if outputName == "" {
				matches := []string{}
				for name, contract := range outputs[binding.FromStage] {
					if input.Type == contract.Type && input.Version == contract.Version {
						matches = append(matches, name)
					}
				}
				if len(matches) != 1 {
					return nil, fmt.Errorf("stage %q path %q requires one unambiguous %s/%s output from stage %q", stage.ID, binding.Path, input.Type, input.Version, binding.FromStage)
				}
				outputName = matches[0]
				for index := range resolved {
					if resolved[index].Path == binding.Path {
						resolved[index].FromOutput = outputName
					}
				}
			}
			contract := outputs[binding.FromStage][outputName]
			if input.Type != contract.Type || input.Version != contract.Version {
				return nil, fmt.Errorf("stage %q path %q requires %s/%s but %s.%s produces %s/%s", stage.ID, binding.Path, input.Type, input.Version, binding.FromStage, outputName, contract.Type, contract.Version)
			}
			used[input.ArtifactID] = true
		}
	}
	for token, binding := range tokens {
		if !used[token] {
			return nil, fmt.Errorf("stage %q path %q is not used as a typed artifact input by its plugin plan", stage.ID, binding.Path)
		}
	}
	return resolved, nil
}

func collectOutputs(stageID string, plan domain.Plan) (map[string]domain.ArtifactContract, error) {
	result := map[string]domain.ArtifactContract{}
	for _, step := range plan.Steps {
		for _, output := range step.Outputs {
			if _, exists := result[output.Name]; exists {
				return nil, fmt.Errorf("stage %q produces output %q more than once", stageID, output.Name)
			}
			result[output.Name] = domain.ArtifactContract{Type: output.Type, Version: output.Version}
		}
	}
	return result, nil
}

func setPointer(root *any, pointer string, value string) error {
	if !strings.HasPrefix(pointer, "/") || pointer == "/" {
		return errors.New("path must be a non-root JSON pointer")
	}
	segments := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	for index := range segments {
		segments[index] = strings.ReplaceAll(strings.ReplaceAll(segments[index], "~1", "/"), "~0", "~")
		if segments[index] == "" {
			return errors.New("path contains an empty segment")
		}
	}
	current := *root
	for index, segment := range segments {
		last := index == len(segments)-1
		switch typed := current.(type) {
		case map[string]any:
			if last {
				typed[segment] = value
				return nil
			}
			next, ok := typed[segment]
			if !ok {
				return fmt.Errorf("parent path %q does not exist", "/"+strings.Join(segments[:index+1], "/"))
			}
			current = next
		case []any:
			position, err := strconv.Atoi(segment)
			if err != nil || position < 0 || position >= len(typed) {
				return fmt.Errorf("array index %q is invalid", segment)
			}
			if last {
				typed[position] = value
				return nil
			}
			current = typed[position]
		default:
			return fmt.Errorf("path segment %q does not address an object or array", segment)
		}
	}
	return errors.New("path could not be resolved")
}

func fingerprint(definition domain.PipelineDefinition, resolution domain.PipelineResolution) (string, error) {
	payload, err := json.Marshal(struct {
		Definition domain.PipelineDefinition `json:"definition"`
		Resolution domain.PipelineResolution `json:"resolution"`
	}{Definition: definition, Resolution: resolution})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func Fingerprint(definition domain.PipelineDefinition, resolution domain.PipelineResolution) (string, error) {
	return fingerprint(definition, resolution)
}
