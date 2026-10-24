package keycloak

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
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
