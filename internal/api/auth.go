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
	"html/template"
	"net/http"
	"strings"
	"time"

	"kubephos.dev/kubephos/internal/auth"
	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/keycloak"
	"kubephos.dev/kubephos/internal/storage"
)

const sessionCookie = "kubephos_session"
const oidcStateCookie = "kubephos_oidc_state"
const oidcVerifierCookie = "kubephos_oidc_verifier"

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
		if mutating(request.Method) && request.URL.Path != "/api/v1/auth/logout" && session.User.Role != "admin" && !userMutationAllowed(request.URL.Path) {
			s.auditAuthenticationRejection(request, http.StatusForbidden)
			writeError(response, http.StatusForbidden, "permission_denied", "Administrator access is required.")
			return
		}
		if strings.HasPrefix(request.URL.Path, "/api/v1/users") && session.User.Role != "admin" {
			s.auditAuthenticationRejection(request, http.StatusForbidden)
			writeError(response, http.StatusForbidden, "permission_denied", "Administrator access is required.")
			return
		}
		if request.Method == http.MethodGet && session.User.Role != "admin" && adminReadPath(request.URL.Path) {
			s.auditAuthenticationRejection(request, http.StatusForbidden)
			writeError(response, http.StatusForbidden, "permission_denied", "Administrator access is required.")
			return
		}
		next.ServeHTTP(response, request)
	})
}

func adminReadPath(path string) bool {
	for _, prefix := range []string{
		"/api/v1/credentials", "/api/v1/connections", "/api/v1/machine-templates", "/api/v1/infrastructure-services",
		"/api/v1/infrastructure/resources", "/api/v1/audit", "/api/v1/plugin-imports", "/api/v1/plugin-packages",
		"/api/v1/administration", "/api/v1/plugin-runtime", "/api/v1/terminal-targets", "/api/v1/terminals", "/api/v1/pipelines",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return strings.HasSuffix(path, "/kubeconfig") || strings.HasSuffix(path, "/access") || strings.HasSuffix(path, "/ssh-key")
}

func userMutationAllowed(path string) bool {
	return strings.HasPrefix(path, "/api/v1/experiment-configurations") ||
		path == "/api/v1/experiments/start" ||
		path == "/api/v1/experiment-instances" ||
		path == "/api/v1/experiment-suites" ||
		strings.HasPrefix(path, "/api/v1/experiments") ||
		strings.HasPrefix(path, "/api/v1/catalog/applications/") && strings.HasSuffix(path, "/resolve")
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
	case "GET /api/v1/auth/status", "POST /api/v1/auth/setup", "GET /api/v1/auth/login", "GET /api/v1/auth/callback":
		return true
	default:
		return false
	}
}

func mutating(method string) bool {
	return method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete
}

func (s *Server) authStatus(response http.ResponseWriter, request *http.Request) {
	count, err := s.store.IdentityUserCount(request.Context())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "database_error", "Could not read authentication status.")
		return
	}
	result := map[string]any{"setupRequired": count == 0, "authenticated": false, "loginURL": "/api/v1/auth/login"}
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
	count, err := s.store.IdentityUserCount(request.Context())
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
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := decodeJSON(request, &input); err != nil {
		writeError(response, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	input.Username = strings.ToLower(strings.TrimSpace(input.Username))
	input.Email = strings.ToLower(strings.TrimSpace(input.Email))
	if auth.ValidateUsername(input.Username) != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_username", "Username must contain 3 to 40 lowercase letters, numbers, dots, dashes or underscores.")
		return
	}
	err = auth.ValidatePassword(input.Password)
	if err != nil {
		writeError(response, http.StatusUnprocessableEntity, "invalid_password", err.Error())
		return
	}
	if s.identity == nil {
		writeError(response, http.StatusServiceUnavailable, "identity_unavailable", "Keycloak is not configured.")
		return
	}
	externalID, err := s.identity.CreateUser(request.Context(), input.Username, input.Email, input.Password)
	if errors.Is(err, keycloak.ErrConflict) {
		writeError(response, http.StatusConflict, "username_taken", "This username already exists in Keycloak.")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "identity_unavailable", "Could not create the administrator in Keycloak.")
		return
	}
	user, err := s.store.CreateInitialIdentityUser(request.Context(), domain.User{ID: id.New("usr"), Username: input.Username, ExternalID: externalID, Email: input.Email, Role: "admin", Enabled: true})
	if errors.Is(err, storage.ErrConflict) {
		_ = s.identity.DeleteUser(request.Context(), externalID)
		writeError(response, http.StatusConflict, "setup_complete", "Initial setup has already been completed.")
		return
	}
	if err != nil {
		_ = s.identity.DeleteUser(request.Context(), externalID)
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
	if s.identity == nil {
		writeSignInError(response, request, http.StatusServiceUnavailable, "identity_unavailable", "Keycloak is not configured.")
		return
	}
	state, err := randomToken(32)
	if err != nil {
		writeSignInError(response, request, http.StatusInternalServerError, "login_error", "Could not start sign in.")
		return
	}
	verifier, err := randomToken(48)
	if err != nil {
		writeSignInError(response, request, http.StatusInternalServerError, "login_error", "Could not start sign in.")
		return
	}
	digest := sha256.Sum256([]byte(verifier))
	s.setOIDCCookie(response, request, oidcStateCookie, state)
	s.setOIDCCookie(response, request, oidcVerifierCookie, verifier)
	http.Redirect(response, request, s.identity.AuthorizationURL(state, base64.RawURLEncoding.EncodeToString(digest[:])), http.StatusFound)
}

func (s *Server) authCallback(response http.ResponseWriter, request *http.Request) {
	stateCookie, stateErr := request.Cookie(oidcStateCookie)
	verifierCookie, verifierErr := request.Cookie(oidcVerifierCookie)
	state := request.URL.Query().Get("state")
	code := request.URL.Query().Get("code")
	s.clearOIDCCookie(response, request, oidcStateCookie)
	s.clearOIDCCookie(response, request, oidcVerifierCookie)
	if stateErr != nil || verifierErr != nil || state == "" || code == "" || subtle.ConstantTimeCompare([]byte(state), []byte(stateCookie.Value)) != 1 {
		writeSignInError(response, request, http.StatusBadRequest, "invalid_login", "The sign-in response is invalid or expired.")
		return
	}
	if s.identity == nil {
		writeSignInError(response, request, http.StatusServiceUnavailable, "identity_unavailable", "Keycloak is not configured.")
		return
	}
	identity, err := s.identity.Exchange(request.Context(), code, verifierCookie.Value)
	if err != nil {
		writeSignInError(response, request, http.StatusBadGateway, "identity_unavailable", "Keycloak could not complete sign in.")
		return
	}
	user, err := s.store.GetUserByExternalID(request.Context(), identity.Subject)
	if err != nil {
		writeSignInError(response, request, http.StatusForbidden, "user_not_authorized", "This Keycloak account is not enabled in KubePhos.")
		return
	}
	session, token, err := s.issueSession(request.Context(), user)
	if err != nil {
		writeSignInError(response, request, http.StatusInternalServerError, "session_error", "Could not start the session.")
		return
	}
	s.setSessionCookie(response, request, token, session.ExpiresAt)
	http.Redirect(response, request, s.applicationURL, http.StatusFound)
}

func (s *Server) authLogout(response http.ResponseWriter, request *http.Request) {
	cookie, _ := request.Cookie(sessionCookie)
	if cookie != nil {
		_ = s.store.DeleteSession(request.Context(), tokenHash(cookie.Value))
	}
	http.SetCookie(response, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: s.usesHTTPS(request), SameSite: http.SameSiteStrictMode, MaxAge: -1, Expires: time.Unix(1, 0)})
	logoutURL := s.applicationURL
	if s.identity != nil {
		logoutURL = s.identity.LogoutURL()
	}
	writeJSON(response, http.StatusOK, map[string]any{"logoutURL": logoutURL})
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
	http.SetCookie(response, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: s.usesHTTPS(request), SameSite: http.SameSiteStrictMode, MaxAge: int(time.Until(expiresAt).Seconds()), Expires: expiresAt})
}

func (s *Server) setOIDCCookie(response http.ResponseWriter, request *http.Request, name, value string) {
	http.SetCookie(response, &http.Cookie{Name: name, Value: value, Path: "/api/v1/auth", HttpOnly: true, Secure: s.usesHTTPS(request), SameSite: http.SameSiteLaxMode, MaxAge: 600})
}

func (s *Server) clearOIDCCookie(response http.ResponseWriter, request *http.Request, name string) {
	http.SetCookie(response, &http.Cookie{Name: name, Value: "", Path: "/api/v1/auth", HttpOnly: true, Secure: s.usesHTTPS(request), SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0)})
}

// The configured public origin remains HTTPS when a reverse proxy terminates TLS.
// Do not trust arbitrary forwarded headers to determine cookie security.
func (s *Server) usesHTTPS(request *http.Request) bool {
	return request.TLS != nil || strings.HasPrefix(s.applicationURL, "https://")
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

var signInErrorPage = template.Must(template.New("sign-in-error").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><meta name="referrer" content="no-referrer"><title>Sign-in could not be completed · Kubephos</title><link rel="icon" href="/kubephos-icon.svg"><link rel="stylesheet" href="/auth-error.css"></head><body><main><span class="brand">Kubephos</span><h1>Sign-in could not be completed</h1><p>{{.}}</p><a href="/api/v1/auth/login">Try signing in again</a></main></body></html>`))

func writeSignInError(response http.ResponseWriter, request *http.Request, status int, code, message string) {
	response.Header().Set("Cache-Control", "no-store")
	response.Header().Set("Referrer-Policy", "no-referrer")
	response.Header().Add("Vary", "Accept")
	if !strings.Contains(request.Header.Get("Accept"), "text/html") {
		writeError(response, status, code, message)
		return
	}
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(status)
	_ = signInErrorPage.Execute(response, message)
}
