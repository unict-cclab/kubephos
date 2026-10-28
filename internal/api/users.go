package api

import (
	"errors"
	"net/http"
	"net/mail"
	"strings"

	"kubephos.dev/kubephos/internal/auth"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/keycloak"
	"kubephos.dev/kubephos/internal/storage"
)

func (s *Server) listUsers(response http.ResponseWriter, request *http.Request) {
	users, err := s.store.ListUsers(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list users.")
		return
	}
	assignments, err := s.store.ListClusterAssignments(request.Context(), "")
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list cluster assignments.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": users, "assignments": assignments})
}

func (s *Server) createUser(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	input.Role = strings.ToLower(strings.TrimSpace(input.Role))
	if input.Role == "" {
		input.Role = "user"
	}
	if auth.ValidateUsername(input.Username) != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_username", "Username must contain 3 to 40 lowercase letters, numbers, dots, dashes or underscores.")
		return
	}
	if input.Email != "" {
		address, err := mail.ParseAddress(input.Email)
		if err != nil || !strings.EqualFold(address.Address, input.Email) {
			writeError(response, http.StatusUnprocessableEntity, "invalid_email", "Enter a valid email address.")
			return
		}
	}
	if input.Role != "admin" && input.Role != "user" {
		writeError(response, http.StatusUnprocessableEntity, "invalid_role", "Role must be admin or user.")
		return
	}
	if err := auth.ValidatePassword(input.Password); err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_password", err.Error())
		return
	}
	externalID, err := s.identity.CreateUserWithTemporaryPassword(request.Context(), input.Username, input.Email, input.Password)
	if errors.Is(err, keycloak.ErrConflict) {
		writeError(response, http.StatusConflict, "username_taken", "This username already exists.")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "identity_unavailable", "Could not create the user in Keycloak.")
		return
	}
	user, err := s.store.CreateIdentityUser(request.Context(), domain.User{ID: id.New("usr"), Username: input.Username, ExternalID: externalID, Email: input.Email, Role: input.Role, Enabled: true})
	if err != nil {
		_ = s.identity.DeleteUser(request.Context(), externalID)
		if errors.Is(err, storage.ErrConflict) {
			writeError(response, http.StatusConflict, "username_taken", "This username already exists.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save the user.")
		return
	}
	writeJSON(response, http.StatusCreated, user)
}

func (s *Server) deleteUser(response http.ResponseWriter, request *http.Request) {
	user, err := s.store.GetUserByID(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "User not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read the user.")
		return
	}
	if user.ID == requestUser(request).ID {
		writeError(response, http.StatusConflict, "self_deletion", "You cannot delete your own account.")
		return
	}
	identityFailed := false
	if err := s.store.DeleteIdentityUserAndAccount(request.Context(), user.ID, requestUser(request).ID, func() error {
		err := s.identity.DeleteUser(request.Context(), user.ExternalID)
		identityFailed = err != nil
		return err
	}); errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "user_in_use", "The user owns configurations or experiments and cannot be deleted.")
		return
	} else if err != nil {
		if identityFailed {
			writeError(response, http.StatusBadGateway, "identity_unavailable", "Could not delete the account. Please retry.")
			return
		}
		writeError(response, http.StatusInternalServerError, "database_error", "Could not delete the user.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) listUserClusters(response http.ResponseWriter, request *http.Request) {
	assignments, err := s.store.ListClusterAssignments(request.Context(), request.PathValue("id"))
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list cluster assignments.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": assignments})
}

func (s *Server) replaceUserClusters(response http.ResponseWriter, request *http.Request) {
	var input struct {
		ClusterIDs []string `json:"clusterIds"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	seen := map[string]bool{}
	clusterIDs := make([]string, 0, len(input.ClusterIDs))
	for _, clusterID := range input.ClusterIDs {
		clusterID = strings.TrimSpace(clusterID)
		if clusterID != "" && !seen[clusterID] {
			seen[clusterID] = true
			clusterIDs = append(clusterIDs, clusterID)
		}
	}
	actor, _ := request.Context().Value(authenticatedUserKey{}).(domain.User)
	if err := s.store.ReplaceClusterAssignments(request.Context(), request.PathValue("id"), actor.ID, clusterIDs); errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_cluster", "A selected Kubernetes cluster is unavailable.")
		return
	} else if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not save cluster assignments.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) updateUserEmail(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Email string `json:"email"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, 400, "invalid_request", "Enter an email address.")
		return
	}
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if input.Email != "" {
		address, err := mail.ParseAddress(input.Email)
		if err != nil || !strings.EqualFold(address.Address, input.Email) {
			writeError(w, 422, "invalid_email", "Enter a valid email address.")
			return
		}
	}
	user, err := s.store.GetUserByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, 404, "not_found", "User not found.")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not read the user.")
		return
	}
	identityFailed := false
	updated, err := s.store.UpdateIdentityEmail(r.Context(), user.ID, input.Email, func() error {
		err := s.identity.UpdateUserEmail(r.Context(), user.ExternalID, input.Email)
		identityFailed = err != nil
		return err
	})
	if errors.Is(err, keycloak.ErrConflict) {
		writeError(w, 409, "email_taken", "This email address is already in use.")
		return
	}
	if identityFailed {
		writeError(w, 502, "identity_unavailable", "Could not update the email address.")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not save the email address.")
		return
	}
	writeJSON(w, 200, updated)
}

func (s *Server) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, 400, "invalid_request", "Enter a temporary password.")
		return
	}
	if err := auth.ValidatePassword(input.Password); err != nil {
		writeError(w, 422, "invalid_password", err.Error())
		return
	}
	user, err := s.store.GetUserByID(r.Context(), r.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, 404, "not_found", "User not found.")
		return
	}
	if err != nil {
		writeError(w, 500, "database_error", "Could not read the user.")
		return
	}
	identityErr := s.identity.ResetTemporaryPassword(r.Context(), user.ExternalID, input.Password)
	sessionErr := s.store.RevokeUserSessions(r.Context(), user.ID)
	if identityErr != nil {
		writeError(w, 502, "identity_unavailable", "Could not reset the temporary password. Please retry.")
		return
	}
	if sessionErr != nil {
		writeError(w, 500, "database_error", "Password reset, but existing sessions could not be closed. Please retry.")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
