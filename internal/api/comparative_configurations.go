package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) validatedComparativeConfiguration(ctx context.Context, input experimentConfigurationInput) (domain.ExperimentConfiguration, *managedOperationFailure) {
	return validateComparativeConfiguration(input, func(configurationID string) (domain.ExperimentConfiguration, error) {
		configuration, err := s.store.GetExperimentConfiguration(ctx, configurationID)
		if err != nil {
			return configuration, err
		}
		if user, ok := ctx.Value(authenticatedUserKey{}).(domain.User); ok && user.ID != "" && !ownsResource(user, configuration.OwnerUserID) {
			return domain.ExperimentConfiguration{}, errUnavailableConfiguration
		}
		if configuration.Kind != "suite" {
			configuration = s.refreshChangedExperimentConfiguration(ctx, configuration)
		}
		return configuration, nil
	})
}

var errUnavailableConfiguration = errors.New("configuration unavailable")

func validateComparativeConfiguration(input experimentConfigurationInput, load func(string) (domain.ExperimentConfiguration, error)) (domain.ExperimentConfiguration, *managedOperationFailure) {
	fail := func(code, message string) (domain.ExperimentConfiguration, *managedOperationFailure) {
		return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusUnprocessableEntity, code: code, message: message}
	}
	input.Name = strings.TrimSpace(input.Name)
	input.Description = strings.TrimSpace(input.Description)
	if input.Name == "" || len([]rune(input.Name)) > 120 || len([]rune(input.Description)) > 1000 {
		return fail("invalid_name", "Name is required, with at most 120 characters; description can contain at most 1000 characters.")
	}
	definition := input.Definition
	definition.Kind = "suite"
	if definition.Runs == 0 {
		definition.Runs = 3
	}
	if len(definition.Variants) < 2 || len(definition.Variants) > 8 || definition.Runs < 1 || definition.Runs > 20 {
		return fail("invalid_suite", "A comparative configuration requires two to eight single configurations and one to twenty runs per configuration.")
	}
	seen, aliases, colors := map[string]bool{}, map[string]bool{}, map[string]bool{}
	var first domain.ExperimentConfiguration
	for i := range definition.Variants {
		member := &definition.Variants[i]
		member.ConfigurationID = strings.TrimSpace(member.ConfigurationID)
		if member.ConfigurationID == "" || seen[member.ConfigurationID] {
			return fail("invalid_suite", "Suite configurations must be unique.")
		}
		seen[member.ConfigurationID] = true
		configuration, err := load(member.ConfigurationID)
		if err != nil {
			if errors.Is(err, storage.ErrNotFound) || errors.Is(err, errUnavailableConfiguration) {
				return fail("invalid_configuration", "A selected single configuration is unavailable.")
			}
			return domain.ExperimentConfiguration{}, &managedOperationFailure{status: http.StatusInternalServerError, code: "database_error", message: "Could not read the selected single configurations."}
		}
		if configuration.Kind == "suite" {
			return fail("nested_suite", "Suites may contain only single configurations.")
		}
		if !configuration.Validation.Valid {
			return fail("invalid_configuration", "Every selected single configuration must be valid.")
		}
		if i == 0 {
			first = configuration
		} else if configuration.WorkspaceID != first.WorkspaceID || configuration.ClusterResourceID != first.ClusterResourceID || configuration.ApplicationRef != first.ApplicationRef {
			return fail("incompatible_suite", "Suite configurations must use the same environment, cluster and application or external destination.")
		}
		member.Alias = strings.TrimSpace(member.Alias)
		if member.Alias == "" {
			member.Alias = configuration.Name
		}
		member.Color = strings.ToLower(strings.TrimSpace(member.Color))
		if member.Color == "" {
			member.Color = suitePlotColors[i%len(suitePlotColors)]
		}
		alias := strings.ToLower(member.Alias)
		if len([]rune(member.Alias)) > 80 || aliases[alias] || !plotColorPattern.MatchString(member.Color) || colors[member.Color] {
			return fail("invalid_suite_presentation", "Every configuration requires a unique plot alias and six-digit color.")
		}
		aliases[alias], colors[member.Color] = true, true
	}
	// Only comparative settings belong to a suite; workload settings remain in
	// the selected single configurations and are frozen when the run starts.
	definition = experimentConfigurationDefinitionInput{Kind: "suite", Variants: definition.Variants, Runs: definition.Runs}
	value, err := json.Marshal(definition)
	if err != nil {
		return fail("invalid_suite", "Could not encode the suite configuration.")
	}
	return domain.ExperimentConfiguration{ID: id.New("expcfg"), Kind: "suite", WorkspaceID: first.WorkspaceID, ClusterResourceID: first.ClusterResourceID, Name: input.Name, Description: input.Description, ApplicationRef: first.ApplicationRef, ApplicationDigest: first.ApplicationDigest, Definition: value, Validation: domain.ValidationReport{Valid: true, CheckedAt: time.Now().UTC(), Issues: []domain.ValidationIssue{}}}, nil
}

func executionConfigurationSnapshot(configuration domain.ExperimentConfiguration, variants []validatedSuiteVariant, runs int) json.RawMessage {
	members := make([]domain.ExperimentConfiguration, 0, len(variants))
	for _, variant := range variants {
		members = append(members, variant.Configuration)
	}
	value, _ := json.Marshal(struct {
		Configuration domain.ExperimentConfiguration   `json:"configuration"`
		Members       []domain.ExperimentConfiguration `json:"members"`
		Runs          int                              `json:"runs"`
	}{configuration, members, runs})
	return value
}

func (s *Server) getExperimentConfigurationSnapshot(response http.ResponseWriter, request *http.Request) {
	if !s.authorizeExperiment(response, request, request.PathValue("id")) {
		return
	}
	value, err := s.store.GetExperimentConfigurationSnapshot(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the execution configuration snapshot.")
		return
	}
	writeJSON(response, http.StatusOK, value)
}
