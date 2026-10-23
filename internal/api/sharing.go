package api

import (
	"errors"
	"net/http"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/storage"
)

func viewerID(user domain.User) string {
	if user.Role == "admin" {
		return ""
	}
	return user.ID
}

func (s *Server) canReadResource(r *http.Request, kind, id string) bool {
	user := requestUser(r)
	if user.Role == "admin" {
		return true
	}
	if user.ID == "" {
		return false
	}
	allowed, err := s.store.CanViewResource(r.Context(), kind, id, user.ID)
	return err == nil && allowed
}

func (s *Server) sharingUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.SharingUsers(r.Context())
	if err != nil {
		writeError(w, 500, "database_error", "Could not list sharing recipients.")
		return
	}
	writeJSON(w, 200, map[string]any{"items": users})
}

func (s *Server) resourceSharing(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("resourceKind")
	id := r.PathValue("id")
	user := requestUser(r)
	settings, owner, err := s.store.GetSharing(r.Context(), kind, id)
	if err != nil || !ownsResource(user, owner) {
		writeError(w, 404, "not_found", "Resource not found.")
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, 200, settings)
		return
	}
	var input storage.Sharing
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, 400, "invalid_request", err.Error())
		return
	}
	if len(input.UserIDs) > 1000 {
		writeError(w, 422, "invalid_sharing", "Select at most 1000 recipients.")
		return
	}
	if err := s.store.SetSharing(r.Context(), kind, id, user.ID, user.Role == "admin", input); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeError(w, 404, "not_found", "Resource not found.")
		} else if errors.Is(err, storage.ErrInvalidSharing) {
			writeError(w, 422, "invalid_sharing", err.Error())
		} else {
			writeError(w, 500, "database_error", "Could not save sharing settings.")
		}
		return
	}
	settings, _, err = s.store.GetSharing(r.Context(), kind, id)
	if err != nil {
		writeError(w, 500, "database_error", "Could not reload sharing settings.")
		return
	}
	writeJSON(w, 200, settings)
}
