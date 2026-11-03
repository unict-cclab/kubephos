package api

import (
	"errors"
	"fmt"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
	"net/http"
)

func initializationStage(definition experimentConfigurationDefinitionInput) string {
	if !definition.TwoPhaseStart {
		return ""
	}
	for i, component := range definition.Components {
		if component.Capability == "load.session.control" {
			return fmt.Sprintf("capability-%d", i+1)
		}
	}
	return ""
}

func configureInitializationRun(run *domain.PipelineRun, definition experimentConfigurationDefinitionInput) {
	run.ApplicationNamespace = definition.Namespace
	run.PauseBeforeStage = initializationStage(definition)
}

func (s *Server) startExperimentTraffic(w http.ResponseWriter, r *http.Request) {
	if !s.authorizeExperiment(w, r, r.PathValue("id")) {
		return
	}
	var input struct {
		RunID string `json:"runId"`
	}
	if err := decodeJSON(r, &input); err != nil || input.RunID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "Select the initialized run to start traffic.")
		return
	}
	err := s.store.StartExperimentTraffic(r.Context(), r.PathValue("id"), input.RunID)
	if errors.Is(err, storage.ErrConflict) {
		writeError(w, http.StatusConflict, "invalid_transition", "Traffic can start only after the current run has finished initialization.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not start experiment traffic.")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}
