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

type adminRequestUpdate struct {
	Status  string  `json:"status"`
	Reply   string  `json:"reply"`
	Message *string `json:"message"`
}

// Administrators resolve, with an optional reply, or reopen any request. A member
// may edit the text of their own open request or resolve it themselves.
func (s *Server) updateAdminRequest(response http.ResponseWriter, request *http.Request) {
	user := requestUser(request)
	var input adminRequestUpdate
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if user.Role != "admin" {
		s.updateOwnAdminRequest(response, request, user, input)
		return
	}
	if input.Message != nil {
		writeError(response, http.StatusForbidden, "permission_denied", "Only the user who sent a request can edit its message.")
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

// A member changes only their own open request: its text, or its resolution when
// they solved the problem themselves. Reopening and replies stay with administrators.
func (s *Server) updateOwnAdminRequest(response http.ResponseWriter, request *http.Request, user domain.User, input adminRequestUpdate) {
	if input.Reply != "" || (input.Status != "" && input.Status != domain.AdminRequestResolved) {
		writeError(response, http.StatusForbidden, "permission_denied", "Only an administrator can reply to or reopen a request.")
		return
	}
	resolve := input.Status == domain.AdminRequestResolved
	message := ""
	if input.Message != nil {
		var err error
		if message, err = trimAdminRequestText(*input.Message, true); err != nil {
			writeError(response, http.StatusUnprocessableEntity, "invalid_message", err.Error())
			return
		}
	} else if !resolve {
		writeError(response, http.StatusUnprocessableEntity, "invalid_request", "Send a new message or resolve the request.")
		return
	}
	saved, err := s.store.UpdateOwnAdminRequest(request.Context(), request.PathValue("id"), user.ID, message, resolve)
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Request not found.")
		return
	}
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "request_resolved", "This request is already resolved and can no longer be changed.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not update the request. Try again.")
		return
	}
	writeJSON(response, http.StatusOK, saved)
}

// Only administrators delete requests.
func (s *Server) deleteAdminRequest(response http.ResponseWriter, request *http.Request) {
	if requestUser(request).Role != "admin" {
		writeError(response, http.StatusForbidden, "permission_denied", "Administrator access is required.")
		return
	}
	err := s.store.DeleteAdminRequest(request.Context(), request.PathValue("id"))
	if errors.Is(err, storage.ErrNotFound) {
		writeError(response, http.StatusNotFound, "not_found", "Request not found.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not delete the request. Try again.")
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
