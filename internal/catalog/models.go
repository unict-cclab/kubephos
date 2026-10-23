package catalog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Model struct {
	CreatedBy    string    `json:"createdBy,omitempty"`
	ModifiedBy   string    `json:"modifiedBy,omitempty"`
	ID           string    `json:"id"`
	URL          string    `json:"url"`
	SHA256       string    `json:"sha256"`
	Size         int64     `json:"size"`
	Backend      string    `json:"backend"`
	Quantization string    `json:"quantization"`
	Origin       string    `json:"origin,omitempty"`
	Enabled      bool      `json:"enabled"`
	CreatedAt    time.Time `json:"createdAt,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt,omitempty"`
}

var modelNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var modelChecksumPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var modelQuantizationPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,32}$`)

func ValidateModel(model Model) error {
	if !modelNamePattern.MatchString(model.ID) {
		return errors.New("model name must contain letters, numbers, dots, underscores or hyphens")
	}
	source, err := url.Parse(model.URL)
	if err != nil || source.Hostname() == "" || (source.Scheme != "https" && source.Scheme != "http") || source.User != nil || source.Fragment != "" {
		return errors.New("model URL must be an absolute HTTP or HTTPS download URL without credentials or fragments")
	}
	if model.SHA256 != "" && !modelChecksumPattern.MatchString(model.SHA256) {
		return errors.New("SHA-256 must contain 64 lowercase hexadecimal characters")
	}
	if model.Size < 0 {
		return errors.New("model file size cannot be negative")
	}
	if model.Backend != "llama-cpp" {
		return errors.New("GGUF models currently require the llama-cpp backend")
	}
	if !modelQuantizationPattern.MatchString(model.Quantization) {
		return errors.New("quantization must be specified, for example Q4_K_M or Q8_0")
	}
	return nil
}

func SeedModels(descriptor json.RawMessage) ([]Model, error) {
	var application Descriptor
	if err := json.Unmarshal(descriptor, &application); err != nil {
		return nil, err
	}
	if application.Spec.Materializer != "io.kubephos.applications.localai" {
		return nil, nil
	}
	raw, err := json.Marshal(application.Spec.MaterializerConfig["models"])
	if err != nil {
		return nil, err
	}
	var models []Model
	if err := json.Unmarshal(raw, &models); err != nil {
		return nil, err
	}
	for i := range models {
		models[i].Backend = "llama-cpp"
		models[i].Origin = "built-in"
		models[i].Enabled = true
		models[i].Quantization = "Q4_K_M"
		if strings.Contains(strings.ToLower(models[i].URL), "q8_0.gguf") {
			models[i].Quantization = "Q8_0"
		}
		if err := ValidateModel(models[i]); err != nil {
			return nil, err
		}
	}
	return models, nil
}

// WithModels snapshots the active model registry into the materializer contract.
func WithModels(raw json.RawMessage, models []Model) (json.RawMessage, string, error) {
	var application Descriptor
	if err := json.Unmarshal(raw, &application); err != nil {
		return nil, "", err
	}
	if len(models) == 0 {
		return nil, "", errors.New("at least one model must remain enabled")
	}
	registry := make([]map[string]any, 0, len(models))
	names := make([]any, 0, len(models))
	available := map[string]bool{}
	for _, model := range models {
		if err := ValidateModel(model); err != nil {
			return nil, "", fmt.Errorf("model %s: %w", model.ID, err)
		}
		registry = append(registry, map[string]any{"id": model.ID, "url": model.URL, "sha256": model.SHA256, "size": model.Size, "backend": model.Backend, "quantization": model.Quantization})
		names = append(names, model.ID)
		available[model.ID] = true
	}
	application.Spec.MaterializerConfig["models"] = registry
	properties := application.Spec.ValuesSchema["properties"].(map[string]any)
	properties["models"].(map[string]any)["items"].(map[string]any)["enum"] = names
	defaults := []any{}
	if selected, ok := application.Spec.Defaults["models"].([]any); ok {
		for _, name := range selected {
			if available[fmt.Sprint(name)] {
				defaults = append(defaults, name)
			}
		}
	}
	if len(defaults) == 0 {
		defaults = append(defaults, models[0].ID)
	}
	application.Spec.Defaults["models"] = defaults
	updated, err := json.Marshal(application)
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(updated)
	return updated, "sha256:" + hex.EncodeToString(sum[:]), nil
}

// NormalizeApplicationValues retains saved settings when LocalAI fields move to service overrides.
func NormalizeApplicationValues(descriptor Descriptor, values map[string]any) map[string]any {
	normalized := make(map[string]any, len(values))
	for key, value := range values {
		normalized[key] = value
	}
	if descriptor.Spec.Materializer != "io.kubephos.applications.localai" {
		return normalized
	}
	properties, _ := descriptor.Spec.ValuesSchema["properties"].(map[string]any)
	hasRouter := false
	for _, component := range descriptor.Spec.Interface.Components {
		if component.ID == "inference-router" {
			hasRouter = true
		}
	}
	if !hasRouter {
		for _, field := range []string{"replicaOverrides", "resourceOverrides"} {
			if saved, ok := values[field].(map[string]any); ok {
				copy := make(map[string]any, len(saved))
				for key, value := range saved {
					if key != "inference-router" {
						copy[key] = value
					}
				}
				normalized[field] = copy
			}
		}
	}
	if _, supported := properties["routerReplicas"]; !supported {
		if replicas, exists := values["routerReplicas"]; exists && hasRouter {
			overrides := map[string]any{}
			if saved, ok := values["replicaOverrides"].(map[string]any); ok {
				for key, value := range saved {
					overrides[key] = value
				}
			}
			if _, exists := overrides["inference-router"]; !exists {
				overrides["inference-router"] = replicas
			}
			normalized["replicaOverrides"] = overrides
		}
	}
	for _, field := range []string{"routerReplicas", "storageClass", "dnsServer", "clusterDomain"} {
		if _, supported := properties[field]; !supported {
			delete(normalized, field)
		}
	}
	return normalized
}
