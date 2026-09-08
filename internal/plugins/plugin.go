package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"kubephos.dev/kubephos/internal/domain"
)

type Manifest struct {
	ID                string             `json:"id"`
	Name              string             `json:"name"`
	Version           string             `json:"version"`
	Description       string             `json:"description"`
	Schema            json.RawMessage    `json:"schema"`
	CredentialSchemas []CredentialSchema `json:"credentialSchemas"`
	Permissions       []string           `json:"permissions"`
}

type CredentialSchema struct {
	Kind        string          `json:"kind"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
}

type SecretResolver func(context.Context, string) (string, json.RawMessage, error)

type Logger func(level, message string) error

type Plugin interface {
	Manifest() Manifest
	Validate(context.Context, json.RawMessage) domain.ValidationReport
	Plan(context.Context, json.RawMessage) (domain.Plan, error)
	Precheck(context.Context, domain.PlanStep, Logger) (domain.HealthReport, error)
	Execute(context.Context, domain.PlanStep, Logger) (json.RawMessage, error)
	Verify(context.Context, domain.PlanStep, json.RawMessage, Logger) (domain.HealthReport, error)
}

type Registry struct {
	plugins map[string]Plugin
}

func NewRegistry(values ...Plugin) *Registry {
	registry := &Registry{plugins: map[string]Plugin{}}
	for _, value := range values {
		registry.plugins[value.Manifest().ID] = value
	}
	return registry
}

func (r *Registry) Get(pluginID string) (Plugin, error) {
	plugin, ok := r.plugins[pluginID]
	if !ok {
		return nil, fmt.Errorf("plugin %q is not installed", pluginID)
	}
	return plugin, nil
}

func (r *Registry) Manifests() []Manifest {
	result := make([]Manifest, 0, len(r.plugins))
	for _, plugin := range r.plugins {
		result = append(result, plugin.Manifest())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
