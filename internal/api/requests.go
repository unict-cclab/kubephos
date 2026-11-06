package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
)

const (
	maxAdminRequestLength = 4000
	// Requests work like tickets, not a chat: a member waits for an answer before
	// opening more than a few at once.
	maxOpenAdminRequests = 3
)

// trimAdminRequestText trims a message or reply and checks its length in characters.
func trimAdminRequestText(text string, required bool) (string, error) {
	text = strings.TrimSpace(text)
	if (required && text == "") || utf8.RuneCountInString(text) > maxAdminRequestLength {
		return "", fmt.Errorf("Write a message of at most %d characters.", maxAdminRequestLength)
	}
	return text, nil
}

// Administrators see every request; other users see only their own.
func (s *Server) listAdminRequests(response http.ResponseWriter, request *http.Request) {
	user := requestUser(request)
	requester := user.ID
	if user.Role == "admin" {
		requester = ""
	}
	items, err := s.store.ListAdminRequests(request.Context(), requester)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not list requests.")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createAdminRequest(response http.ResponseWriter, request *http.Request) {
	user := requestUser(request)
	if user.Role == "admin" {
		writeError(response, http.StatusForbidden, "permission_denied", "Administrators answer requests; only other users send them.")
		return
	}
	var input struct {
		Message string `json:"message"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	message, err := trimAdminRequestText(input.Message, true)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_message", err.Error())
		return
	}
	saved, err := s.store.CreateAdminRequest(request.Context(), domain.AdminRequest{ID: id.New("req"), RequesterUserID: user.ID, Message: message}, maxOpenAdminRequests)
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "too_many_open_requests", fmt.Sprintf("You already have %d open requests. Send a new one when an administrator has resolved one of them.", maxOpenAdminRequests))
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not send the request. Try again.")
		return
	}
	writeJSON(response, http.StatusCreated, saved)
}

// Only administrators resolve or reopen requests; the middleware enforces the same rule.
// A reply is accepted only together with the resolution.
func (s *Server) updateAdminRequest(response http.ResponseWriter, request *http.Request) {
	user := requestUser(request)
	if user.Role != "admin" {
		writeError(response, http.StatusForbidden, "permission_denied", "Administrator access is required.")
		return
	}
	var input struct {
		Status string `json:"status"`
		Reply  string `json:"reply"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input.Status != domain.AdminRequestOpen && input.Status != domain.AdminRequestResolved {
		writeError(response, http.StatusUnprocessableEntity, "invalid_status", "Set the request status to open or resolved.")
		return
	}
	reply, err := trimAdminRequestText(input.Reply, false)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_reply", err.Error())
		return
	}
	if reply != "" && input.Status != domain.AdminRequestResolved {
		writeError(response, http.StatusUnprocessableEntity, "invalid_reply", "A reply can only be sent when resolving a request.")
		return
	}
	saved, err := s.store.SetAdminRequestStatus(request.Context(), request.PathValue("id"), input.Status, reply, user.ID)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Request not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not update the request. Try again.")
		return
	}
	writeJSON(response, http.StatusOK, saved)
}
