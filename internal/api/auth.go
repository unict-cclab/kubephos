package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/auth"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
)

const sessionCookie = "kubephos_session"

type authenticatedUserKey struct{}

func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !strings.HasPrefix(request.URL.Path, "/api/v1/") || publicAPI(request) {
			next.ServeHTTP(response, request)
			return
		}
		session, err := s.requestSession(request)
		if err != nil {
			s.auditAuthenticationRejection(request, http.StatusUnauthorized)
			writeError(response, http.StatusUnauthorized, "authentication_required", "Sign in to continue.")
			return
		}
		request = request.WithContext(context.WithValue(request.Context(), authenticatedUserKey{}, session.User))
		if mutating(request.Method) && subtle.ConstantTimeCompare([]byte(request.Header.Get("X-CSRF-Token")), []byte(session.CSRFToken)) != 1 {
			s.auditAuthenticationRejection(request, http.StatusForbidden)
			writeError(response, http.StatusForbidden, "csrf_rejected", "The request security token is missing or invalid.")
			return
		}
		if mutating(request.Method) && request.URL.Path != "/api/v1/auth/logout" && session.User.Role == "viewer" {
			s.auditAuthenticationRejection(request, http.StatusForbidden)
			writeError(response, http.StatusForbidden, "permission_denied", "Your role cannot modify this resource.")
			return
		}
		if mutating(request.Method) && (strings.HasPrefix(request.URL.Path, "/api/v1/credentials") || strings.HasPrefix(request.URL.Path, "/api/v1/connections") || strings.HasPrefix(request.URL.Path, "/api/v1/catalog") || strings.HasPrefix(request.URL.Path, "/api/v1/plugins") || strings.HasPrefix(request.URL.Path, "/api/v1/plugin-packages") || strings.HasPrefix(request.URL.Path, "/api/v1/plugin-runtime") || strings.HasSuffix(request.URL.Path, "/access")) && session.User.Role != "admin" {
			s.auditAuthenticationRejection(request, http.StatusForbidden)
			writeError(response, http.StatusForbidden, "permission_denied", "Administrator access is required.")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func (s *Server) auditAuthenticationRejection(request *http.Request, status int) {
	if !mutating(request.Method) {
		return
	}
	action, targetType, targetID, _ := auditTarget(request)
	details, _ := json.Marshal(map[string]any{"method": request.Method, "path": request.URL.Path, "status": status})
	_ = s.store.AppendAuditEvent(request.Context(), domain.AuditEvent{Actor: actorName(request), Action: action, TargetType: targetType, TargetID: targetID, Outcome: "rejected", Details: details})
}

func publicAPI(request *http.Request) bool {
	switch request.Method + " " + request.URL.Path {
	case "GET /api/v1/auth/status", "POST /api/v1/auth/setup", "POST /api/v1/auth/login":
		return true
	default:
		return false
	}
}

func mutating(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func (s *Server) authStatus(response http.ResponseWriter, request *http.Request) {
	count, err := s.store.UserCount(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read authentication status.")
		return
	}
	result := map[string]any{"setupRequired": count == 0, "authenticated": false}
	if count > 0 {
		if session, err := s.requestSession(request); err == nil {
			result["authenticated"] = true
			result["csrfToken"] = session.CSRFToken
			result["user"] = session.User
		}
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) authSetup(response http.ResponseWriter, request *http.Request) {
	count, err := s.store.UserCount(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read authentication status.")
		return
	}
	if count != 0 {
		writeError(response, http.StatusConflict, "setup_complete", "Initial setup has already been completed.")
		return
	}
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	if !regexp.MustCompile(`^[a-z][a-z0-9._-]{2,39}$`).MatchString(input.Username) {
		writeError(response, http.StatusUnprocessableEntity, "invalid_username", "Username must contain 3 to 40 lowercase letters, numbers, dots, dashes or underscores.")
		return
	}
	hash, err := auth.HashPassword(input.Password)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_password", err.Error())
		return
	}
	user, err := s.store.CreateInitialUser(request.Context(), domain.User{ID: id.New("usr"), Username: input.Username, PasswordHash: hash, Role: "admin"})
	if errors.Is(err, storage.ErrConflict) {
		writeError(response, http.StatusConflict, "setup_complete", "Initial setup has already been completed.")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not create the administrator.")
		return
	}
	session, token, err := s.issueSession(request.Context(), user)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session_error", "Administrator created, but the session could not be started.")
		return
	}
	s.setSessionCookie(response, request, token, session.ExpiresAt)
	writeJSON(response, http.StatusCreated, map[string]any{"authenticated": true, "csrfToken": session.CSRFToken, "user": user})
}

func (s *Server) authLogin(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	user, err := s.store.GetUserByUsername(request.Context(), strings.ToLower(strings.TrimSpace(input.Username)))
	if err != nil || !auth.VerifyPassword(user.PasswordHash, input.Password) {
		writeError(response, http.StatusUnauthorized, "invalid_credentials", "Username or password is invalid.")
		return
	}
	session, token, err := s.issueSession(request.Context(), user)
	if err != nil {
		writeError(response, http.StatusInternalServerError, "session_error", "Could not start the session.")
		return
	}
	s.setSessionCookie(response, request, token, session.ExpiresAt)
	writeJSON(response, http.StatusOK, map[string]any{"authenticated": true, "csrfToken": session.CSRFToken, "user": user})
}

func (s *Server) authLogout(response http.ResponseWriter, request *http.Request) {
	cookie, _ := request.Cookie(sessionCookie)
	if cookie != nil {
		_ = s.store.DeleteSession(request.Context(), tokenHash(cookie.Value))
	}
	http.SetCookie(response, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: request.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	response.WriteHeader(http.StatusNoContent)
}

func (s *Server) issueSession(ctx context.Context, user domain.User) (domain.Session, string, error) {
	token, err := randomToken(32)
	if err != nil {
		return domain.Session{}, "", err
	}
	csrfToken, err := randomToken(32)
	if err != nil {
		return domain.Session{}, "", err
	}
	session := domain.Session{TokenHash: tokenHash(token), CSRFToken: csrfToken, ExpiresAt: time.Now().UTC().Add(12 * time.Hour), User: user}
	if err := s.store.CreateSession(ctx, session); err != nil {
		return domain.Session{}, "", err
	}
	_ = s.store.DeleteExpiredSessions(ctx)
	return session, token, nil
}

func (s *Server) requestSession(request *http.Request) (domain.Session, error) {
	cookie, err := request.Cookie(sessionCookie)
	if err != nil || len(cookie.Value) < 32 || len(cookie.Value) > 128 {
		return domain.Session{}, storage.ErrNotFound
	}
	return s.store.GetSession(request.Context(), tokenHash(cookie.Value))
}

func (s *Server) setSessionCookie(response http.ResponseWriter, request *http.Request, token string, expiresAt time.Time) {
	http.SetCookie(response, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: request.TLS != nil, SameSite: http.SameSiteStrictMode, MaxAge: int(time.Until(expiresAt).Seconds()), Expires: expiresAt})
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func tokenHash(token string) string {
	digest := sha256.Sum256([]byte(token))
	return hex.EncodeToString(digest[:])
}

func actorName(request *http.Request) string {
	user, ok := request.Context().Value(authenticatedUserKey{}).(domain.User)
	if !ok {
		return "anonymous"
	}
	return user.Username
}
