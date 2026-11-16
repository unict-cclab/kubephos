package keycloak

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestSynchronizeChangesURLsAndSecretsWithoutReplacingIdentitySettings(t *testing.T) {
	clients := map[string]map[string]any{
		"application": {"id": "application", "clientId": "kubephos", "enabled": true, "redirectUris": []any{"http://old/callback"}, "attributes": map[string]any{"pkce.code.challenge.method": "S256", "custom": "preserved"}},
		"management":  {"id": "management", "clientId": "kubephos-management", "serviceAccountsEnabled": true, "attributes": map[string]any{"custom": "management"}},
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/master/protocol/openid-connect/token" {
			_ = r.ParseForm()
			if r.Form.Get("username") != "bootstrap" || r.Form.Get("password") != "admin-password" || r.Form.Get("client_id") != "admin-cli" {
				t.Error("wrong administrator token request")
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "admin-token"})
			return
		}
		if r.Header.Get("Authorization") != "Bearer admin-token" {
			w.WriteHeader(401)
			return
		}
		key := "application"
		if strings.Contains(r.URL.Path, "management") || r.URL.Query().Get("clientId") == "kubephos-management" {
			key = "management"
		}
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/admin/realms/kubephos/clients":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"id": key}})
		case r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(clients[key])
		case r.Method == http.MethodPut:
			var value map[string]any
			if err := json.NewDecoder(r.Body).Decode(&value); err != nil {
				t.Fatal(err)
			}
			clients[key] = value
			w.WriteHeader(204)
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	for _, origin := range []string{"http://first.example:8080", "https://second.example"} {
		c := New(Config{InternalURL: server.URL, Realm: "kubephos", ApplicationURL: origin, ClientID: "kubephos", ClientSecret: "new-secret", ManagementClientID: "kubephos-management", ManagementClientSecret: "management-secret"})
		if err := c.SynchronizeConfiguration(context.Background(), "bootstrap", "admin-password"); err != nil {
			t.Fatal(err)
		}
		app := clients["application"]
		if !reflect.DeepEqual(app["redirectUris"], []any{origin + "/api/v1/auth/callback"}) || !reflect.DeepEqual(app["webOrigins"], []any{origin}) {
			t.Fatalf("incorrect application URLs: %v", app)
		}
		attributes := app["attributes"].(map[string]any)
		if attributes["post.logout.redirect.uris"] != origin || attributes["pkce.code.challenge.method"] != "S256" || attributes["custom"] != "preserved" {
			t.Fatalf("attributes were lost: %v", attributes)
		}
		if app["enabled"] != true || app["id"] != "application" || app["secret"] != "new-secret" || clients["management"]["serviceAccountsEnabled"] != true || clients["management"]["secret"] != "management-secret" {
			t.Fatal("identity settings were changed or secrets were not synchronized")
		}
	}
}

func TestSynchronizeDoesNotLeakAdministratorCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte("secret-administrator-password"))
	}))
	defer server.Close()
	c := New(Config{InternalURL: server.URL, ApplicationURL: "http://example.test"})
	err := c.SynchronizeConfiguration(context.Background(), "admin", "secret-administrator-password")
	if err == nil || strings.Contains(err.Error(), "secret-administrator-password") {
		t.Fatalf("unsafe error: %v", err)
	}
}

func TestSynchronizeRejectsInvalidOriginsBeforeContactingKeycloak(t *testing.T) {
	for _, origin := range []string{"http://user:pass@example.test", "http://example.test/path", "javascript:alert(1)", "https://example.test?x=1"} {
		c := New(Config{ApplicationURL: origin})
		if err := c.SynchronizeConfiguration(context.Background(), "admin", "password"); err == nil {
			t.Fatalf("accepted %s", origin)
		}
	}
}

func TestConfigurationAdminTokenRetriesTemporaryFailuresOnly(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		status, failures, wantCalls int
		success                     bool
	}{
		{"database reconnect", 500, 2, 3, true},
		{"rate limit", 429, 1, 2, true},
		{"unavailable", 503, 20, 10, false},
		{"wrong password", 401, 20, 1, false},
		{"invalid grant", 400, 20, 1, false},
		{"forbidden", 403, 20, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls <= tc.failures {
					w.WriteHeader(tc.status)
					w.Write([]byte("private-password-and-server-detail"))
					return
				}
				w.Write([]byte(`{"access_token":"test-token"}`))
			}))
			defer server.Close()
			c := New(Config{InternalURL: server.URL})
			token, err := c.configurationAdminToken(context.Background(), "admin", "private-password", time.Millisecond)
			if (err == nil) != tc.success || calls != tc.wantCalls {
				t.Fatalf("calls=%d token=%v err=%v", calls, token.AccessToken != "", err)
			}
			if err != nil && (strings.Contains(err.Error(), "private-password") || strings.Contains(err.Error(), "server-detail")) {
				t.Fatal("credential or response leaked", err)
			}
			if !tc.success && tc.status >= 500 && strings.Contains(err.Error(), "password") {
				t.Fatal("server failure described as bad credentials", err)
			}
		})
	}
}

func TestConfigurationAdminTokenStopsRetryOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(503); cancel() }))
	defer server.Close()
	c := New(Config{InternalURL: server.URL})
	started := time.Now()
	if _, err := c.configurationAdminToken(ctx, "admin", "password", time.Hour); err == nil {
		t.Fatal("expected cancellation")
	}
	if calls != 1 || time.Since(started) > time.Second {
		t.Fatal("cancellation did not stop retries")
	}
}
