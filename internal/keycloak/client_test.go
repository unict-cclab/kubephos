package keycloak

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestCreateUserCreatesCompleteKeycloakProfile(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		t.Run(map[bool]string{false: "regular", true: "temporary"}[temporary], func(t *testing.T) {
			testCreateUser(t, temporary)
		})
	}
}

func testCreateUser(t *testing.T, temporary bool) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/realms/kubephos/protocol/openid-connect/token":
			_ = json.NewEncoder(response).Encode(map[string]string{"access_token": "admin-token"})
		case "/admin/realms/kubephos/users":
			if request.Header.Get("Authorization") != "Bearer admin-token" {
				response.WriteHeader(http.StatusUnauthorized)
				return
			}
			if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
				t.Fatal(err)
			}
			response.Header().Set("Location", serverURL(request)+"/admin/realms/kubephos/users/external-id")
			response.WriteHeader(http.StatusCreated)
		default:
			response.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := New(Config{InternalURL: server.URL, Realm: "kubephos", ManagementClientID: "kubephos-management", ManagementClientSecret: "secret"})
	create := client.CreateUser
	if temporary {
		create = client.CreateUserWithTemporaryPassword
	}
	externalID, err := create(context.Background(), "researcher", "researcher@example.test", "password")
	if err != nil {
		t.Fatal(err)
	}
	if externalID != "external-id" {
		t.Fatalf("unexpected external ID %q", externalID)
	}
	for _, field := range []string{"username", "firstName", "lastName"} {
		if payload[field] != "researcher" {
			t.Fatalf("unexpected %s %v", field, payload[field])
		}
	}
	credentials, ok := payload["credentials"].([]any)
	if !ok || len(credentials) != 1 {
		t.Fatalf("expected one password credential, got %v", payload["credentials"])
	}
	credential := credentials[0].(map[string]any)
	if credential["type"] != "password" || credential["value"] != "password" || credential["temporary"] != temporary {
		t.Fatal("unexpected password credential configuration")
	}
	if payload["emailVerified"] != true {
		t.Fatalf("expected verified email, got %v", payload["emailVerified"])
	}
}

func TestAuthorizationURLUsesPublicPathAndPKCE(t *testing.T) {
	client := New(Config{PublicURL: "https://kubephos.example/auth", ApplicationURL: "https://kubephos.example", Realm: "kubephos", ClientID: "kubephos"})
	result, err := url.Parse(client.AuthorizationURL("state", "challenge"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != "/auth/realms/kubephos/protocol/openid-connect/auth" {
		t.Fatalf("unexpected path %q", result.Path)
	}
	if result.Query().Get("state") != "state" || result.Query().Get("code_challenge") != "challenge" || result.Query().Get("code_challenge_method") != "S256" {
		t.Fatalf("unexpected query %q", result.RawQuery)
	}
}

func serverURL(request *http.Request) string {
	return "http://" + request.Host
}
