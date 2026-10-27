package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/keycloak"
)

func TestAdminCreatedUsersRequirePasswordChange(t *testing.T) {
	server, _ := transferServer(t, "")
	var temporary bool
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/kubephos/protocol/openid-connect/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token"})
		case "/admin/realms/kubephos/users":
			var payload struct {
				Credentials []struct {
					Temporary bool `json:"temporary"`
				} `json:"credentials"`
			}
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			temporary = len(payload.Credentials) == 1 && payload.Credentials[0].Temporary
			w.Header().Set("Location", "http://identity.test/admin/realms/kubephos/users/test-identity")
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer identity.Close()
	server.identity = keycloak.New(keycloak.Config{InternalURL: identity.URL, Realm: "kubephos", ManagementClientID: "management", ManagementClientSecret: "test-secret"})
	response := transferCall(t, server.createUser, "/api/v1/users", strings.NewReader(`{"username":"researcher","email":"researcher@example.test","password":"temporary-test-password","role":"user"}`), "application/json")
	if response.Code != http.StatusCreated {
		t.Fatalf("create user returned %d: %s", response.Code, response.Body.String())
	}
	if !temporary {
		t.Fatal("admin-assigned password must require replacement at first login")
	}
}
