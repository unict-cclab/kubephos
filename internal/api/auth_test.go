package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestUserMutationAllowed(t *testing.T) {
	allowed := []string{
		"/api/v1/experiments/experiment-1/sharing",
		"/api/v1/experiment-configurations/configuration-1/sharing",
		"/api/v1/experiment-configurations",
		"/api/v1/experiment-configurations/configuration-1/clone",
		"/api/v1/experiments/start",
		"/api/v1/experiment-instances",
		"/api/v1/experiment-suites",
		"/api/v1/experiments/experiment-1/cancel",
		"/api/v1/catalog/applications/application-1/resolve",
	}
	for _, path := range allowed {
		if !userMutationAllowed(path) {
			t.Fatalf("expected %q to be allowed", path)
		}
	}
	denied := []string{
		"/api/v1/catalog/applications",
		"/api/v1/catalog/applications/application-1",
		"/api/v1/kubernetes-clusters/cluster-1/recreate",
		"/api/v1/users",
	}
	for _, path := range denied {
		if userMutationAllowed(path) {
			t.Fatalf("expected %q to be denied", path)
		}
	}
}

func TestExpiredSignInRecovery(t *testing.T) {
	for _, accept := range []string{"text/html,application/xhtml+xml", "application/json"} {
		t.Run(accept, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback?state=expired&code=private-code", nil)
			request.Header.Set("Accept", accept)
			response := httptest.NewRecorder()
			(&Server{}).authCallback(response, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", response.Code)
			}
			if response.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("authentication errors must not be cached")
			}
			body := response.Body.String()
			if strings.Contains(body, "private-code") {
				t.Fatal("authorization code exposed in response")
			}
			if strings.HasPrefix(accept, "text/html") {
				if strings.Contains(body, "<style>") || !strings.Contains(body, `href="/auth-error.css"`) {
					t.Fatal("recovery page must use an external stylesheet compatible with CSP")
				}
				if !strings.Contains(body, `href="/api/v1/auth/login"`) || !strings.Contains(body, "invalid or expired") {
					t.Fatal("missing recovery action or explanation")
				}
			} else if !strings.Contains(body, `"code":"invalid_login"`) {
				t.Fatalf("JSON compatibility lost: %s", body)
			}
			for _, cookie := range response.Result().Cookies() {
				if cookie.MaxAge != -1 {
					t.Fatalf("OIDC cookie was not cleared: %s", cookie.Name)
				}
			}
		})
	}
}

func TestSignInErrorEscapesMessage(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/callback", nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()
	writeSignInError(response, request, http.StatusBadRequest, "invalid_login", "<script>alert(1)</script>")
	if strings.Contains(response.Body.String(), "<script>") {
		t.Fatal("error message was not escaped")
	}
}
