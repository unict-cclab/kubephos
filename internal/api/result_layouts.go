package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"kubephos.dev/kubephos/internal/storage"
)

// Each user arranges the charts of the results they can read (an experiment or a
// saved comparison). The layout is a JSON object owned by the frontend; only its
// shape and size are checked here.

const maxResultLayoutBytes = 512 << 10

// resultScope reads the results the request names and whether the user can read them.
func (s *Server) resultScope(w http.ResponseWriter, r *http.Request) (storage.ResultScope, bool) {
	scope := storage.ResultScope{Comparison: r.PathValue("kind") == "comparisons", ID: r.PathValue("id")}
	readable := false
	switch r.PathValue("kind") {
	case "experiments":
		readable = s.canReadResource(r, "experiments", scope.ID)
	case "comparisons":
		_, err := s.store.GetResultComparison(r.Context(), scope.ID, requestUser(r).ID)
		readable = err == nil
	}
	if !readable {
		writeError(w, http.StatusNotFound, "not_found", "Results not found.")
	}
	return scope, readable
}

func (s *Server) getResultLayout(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.resultScope(w, r)
	if !ok {
		return
	}
	layout, err := s.store.ResultLayout(r.Context(), requestUser(r).ID, scope)
	if errors.Is(err, storage.ErrNotFound) {
		writeJSON(w, http.StatusOK, map[string]any{"layout": nil})
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not read the chart layout.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"layout": layout})
}

func (s *Server) saveResultLayout(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.resultScope(w, r)
	if !ok {
		return
	}
	var input struct {
		Layout json.RawMessage `json:"layout"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	var object map[string]json.RawMessage
	if len(input.Layout) > maxResultLayoutBytes || json.Unmarshal(input.Layout, &object) != nil || object == nil {
		writeError(w, http.StatusBadRequest, "invalid_layout", "The chart layout must be a JSON object of at most 512 KiB.")
		return
	}
	err := s.store.SaveResultLayout(r.Context(), requestUser(r).ID, scope, input.Layout)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "Results not found.")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not save the chart layout.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"layout": input.Layout})
}

func (s *Server) deleteResultLayout(w http.ResponseWriter, r *http.Request) {
	scope, ok := s.resultScope(w, r)
	if !ok {
		return
	}
	if err := s.store.DeleteResultLayout(r.Context(), requestUser(r).ID, scope); err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not reset the chart layout.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
