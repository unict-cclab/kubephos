package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"

	"kubephos.dev/kubephos/internal/domain"
)

type Manifest struct {
	ID                string                    `json:"id"`
	Provider          string                    `json:"provider,omitempty"`
	Name              string                    `json:"name"`
	Version           string                    `json:"version"`
	Description       string                    `json:"description"`
	Schema            json.RawMessage           `json:"schema"`
	CredentialSchemas []CredentialSchema        `json:"credentialSchemas"`
	ArtifactInputs    []domain.ArtifactContract `json:"artifactInputs"`
	ArtifactOutputs   []domain.ArtifactContract `json:"artifactOutputs"`
	Capabilities      []string                  `json:"capabilities"`
	Permissions       []string                  `json:"permissions"`
	Runtime           Runtime                   `json:"runtime"`
}

type Runtime struct {
	Kind      string `json:"kind"`
	Reference string `json:"reference,omitempty"`
	Digest    string `json:"digest,omitempty"`
}

func (m Manifest) HasCapability(capability string) bool {
	for _, value := range m.Capabilities {
		if value == capability {
			return true
		}
	}
	return false
}

func (m Manifest) Matches(version, digest string) bool {
	return m.Version == version && m.Runtime.Digest == digest
}

type CredentialSchema struct {
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type SecretResolver func(context.Context, string) (string, json.RawMessage, error)

type ConnectionResolver func(context.Context, string) (string, json.RawMessage, error)

type CatalogResolver func(context.Context, string) (json.RawMessage, error)

type Logger func(level, message string) error

type ContainerRunner interface {
	Ready(context.Context) error
	Run(context.Context, string, string, []byte, bool, Logger) ([]byte, []string, error)
}

type Plugin interface {
	Manifest() Manifest
	Validate(context.Context, json.RawMessage) domain.ValidationReport
	Plan(context.Context, json.RawMessage) (domain.Plan, error)
	Precheck(context.Context, domain.PlanStep, Logger) (domain.HealthReport, error)
	Execute(context.Context, domain.PlanStep, Logger) (json.RawMessage, error)
	Verify(context.Context, domain.PlanStep, json.RawMessage, Logger) (domain.HealthReport, error)
	Cleanup(context.Context, domain.PlanStep, json.RawMessage, Logger) error
}

type Registry struct {
	mu      sync.RWMutex
	plugins map[string]Plugin
	bundled map[string]bool
}

func NewRegistry(values ...Plugin) *Registry {
	registry := &Registry{plugins: map[string]Plugin{}, bundled: map[string]bool{}}
	for _, value := range values {
		pluginID := value.Manifest().ID
		registry.plugins[pluginID] = value
		registry.bundled[pluginID] = true
	}
	return registry
}

func (r *Registry) Get(pluginID string) (Plugin, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	plugin, ok := r.plugins[pluginID]
	if !ok {
		return nil, fmt.Errorf("plugin %q is not installed", pluginID)
	}
	return plugin, nil
}

func (r *Registry) Manifests() []Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Manifest, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		result = append(result, plugin.Manifest())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (r *Registry) Install(plugin Plugin) error {
	if plugin == nil || plugin.Manifest().ID == "" {
		return errors.New("plugin is invalid")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	pluginID := plugin.Manifest().ID
	if r.bundled[pluginID] {
		return fmt.Errorf("bundled plugin %q cannot be replaced", pluginID)
	}
	r.plugins[pluginID] = plugin
	return nil
}

func (r *Registry) IsBundled(pluginID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.bundled[pluginID]
}

func (r *Registry) ExternalIDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := []string{}
	for pluginID := range r.plugins {
		if !r.bundled[pluginID] {
			result = append(result, pluginID)
		}
	}
	return result
}

func (r *Registry) Remove(pluginID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.bundled[pluginID] {
		return fmt.Errorf("bundled plugin %q cannot be removed", pluginID)
	}
	delete(r.plugins, pluginID)
	return nil
}
