package api

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"reflect"
	"time"

	"kubephos.dev/kubephos/internal/catalog"
	"kubephos.dev/kubephos/internal/domain"
)

func (s *Server) refreshChangedExperimentConfigurations(ctx context.Context) {
	items, err := s.store.ListExperimentConfigurations(ctx)
	if err != nil {
		slog.Error("list configurations for catalog refresh", "error", err)
		return
	}
	for _, item := range items {
		if ctx.Err() != nil {
			return
		}
		s.refreshChangedExperimentConfiguration(ctx, item)
	}
}

func (s *Server) refreshChangedExperimentConfiguration(ctx context.Context, source domain.ExperimentConfiguration) domain.ExperimentConfiguration {
	if source.Kind == "suite" {
		var definition experimentConfigurationDefinitionInput
		if err := json.Unmarshal(source.Definition, &definition); err != nil {
			return source
		}
		validationCtx := context.WithValue(ctx, authenticatedUserKey{}, domain.User{})
		updated, failure := s.validatedComparativeConfiguration(validationCtx, experimentConfigurationInput{Kind: "suite", Name: source.Name, Description: source.Description, Definition: definition})
		if failure != nil {
			updated = source
			updated.Validation = domain.ValidationReport{Valid: false, CheckedAt: time.Now().UTC(), Issues: []domain.ValidationIssue{{Level: "error", Path: "definition.variants", Message: failure.message}}}
		}
		result, failure := s.persistRevalidatedExperimentConfiguration(ctx, source, updated)
		if failure != nil {
			return source
		}
		return result
	}
	var definition experimentConfigurationDefinitionInput
	if err := json.Unmarshal(source.Definition, &definition); err != nil {
		return source
	}
	applicationDigest := source.ApplicationDigest
	changed := !source.Validation.Valid || s.configurationPluginsChanged(definition)
	if applicationID, err := catalog.ParseReference(source.ApplicationRef); err == nil {
		application, err := s.store.GetCatalogApplication(ctx, applicationID)
		if err != nil {
			return source
		}
		applicationDigest = application.Digest
		changed = changed || applicationDigest != source.ApplicationDigest
	}
	for _, component := range definition.Components {
		if component.StrategyID != "" {
			strategy, err := s.store.GetCatalogStrategy(ctx, component.StrategyID)
			if err != nil || strategy.UpdatedAt.After(source.UpdatedAt) || strategy.Status != domain.OperationSucceeded {
				changed = true
			}
		}
	}
	if !changed {
		return source
	}
	updated, failure := s.validatedExperimentConfiguration(ctx, experimentConfigurationInput{
		WorkspaceID: source.WorkspaceID, ClusterResourceID: source.ClusterResourceID,
		Name: source.Name, Description: source.Description, ApplicationRef: source.ApplicationRef, Definition: definition,
	})
	if failure != nil {
		// Keep user inputs, and expose validation failures instead of claiming readiness.
		updated = source
		updated.ApplicationDigest = applicationDigest
		message := failure.message
		if message == "" {
			message = "An updated dependency is incompatible with this configuration. Edit the configuration to resolve its validation errors."
		}
		updated.Validation = domain.ValidationReport{Valid: false, CheckedAt: time.Now().UTC(), Issues: []domain.ValidationIssue{{Level: "error", Path: "definition", Message: message}}}
	}
	result, failure := s.persistRevalidatedExperimentConfiguration(ctx, source, updated)
	if failure != nil {
		slog.Warn("refresh experiment configuration", "configuration", source.ID, "error", failure.message)
		return source
	}
	return result
}

func retainConfigurationIdentity(source, updated domain.ExperimentConfiguration) domain.ExperimentConfiguration {
	updated.ID = source.ID
	updated.OwnerUserID = source.OwnerUserID
	updated.OwnerUsername = source.OwnerUsername
	updated.CreatedAt = source.CreatedAt
	return updated
}

func (s *Server) persistRevalidatedExperimentConfiguration(ctx context.Context, source, updated domain.ExperimentConfiguration) (domain.ExperimentConfiguration, *managedOperationFailure) {
	if source.WorkspaceID == updated.WorkspaceID && source.ClusterResourceID == updated.ClusterResourceID && source.ApplicationDigest == updated.ApplicationDigest && source.Validation.Valid == updated.Validation.Valid && sameConfigurationJSON(source.Definition, updated.Definition) {
		return source, nil
	}
	updated = retainConfigurationIdentity(source, updated)
	result, err := s.store.UpdateExperimentConfiguration(ctx, updated, source.UpdatedAt)
	if err != nil {
		return source, &managedOperationFailure{status: http.StatusConflict, code: "configuration_refresh_failed", message: "The configuration changed during automatic validation. Reload and try again."}
	}
	return result, nil
}

// PostgreSQL JSONB changes whitespace and object key order. Neither change is
// a new configuration revision and should invalidate an open editor.
func sameConfigurationJSON(left, right json.RawMessage) bool {
	if bytes.Equal(left, right) {
		return true
	}
	var a, b any
	decode := func(raw json.RawMessage, value *any) error {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		return decoder.Decode(value)
	}
	return decode(left, &a) == nil && decode(right, &b) == nil && reflect.DeepEqual(a, b)
}

func (s *Server) configurationPluginsChanged(definition experimentConfigurationDefinitionInput) bool {
	for _, component := range definition.Components {
		plugin, err := s.registry.Get(component.PluginID)
		if err != nil || !plugin.Manifest().Matches(component.PluginVersion, component.PluginDigest) {
			return true
		}
	}
	return false
}
