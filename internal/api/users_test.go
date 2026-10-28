package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/keycloak"
	"kubephos.dev/kubephos/internal/storage"
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

func TestUserEmailPasswordAndDeletion(t *testing.T) {
	server, pool := transferServer(t, "")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,role,external_id,email) VALUES('destination-admin','admin','admin','admin-id','admin@example.test'),('user-edit','researcher','user','researcher-id','old@example.test')`); err != nil {
		t.Fatal(err)
	}
	var temporary, logout, emailChanged, failDelete, failEmail bool
	identity := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/token"):
			json.NewEncoder(w).Encode(map[string]string{"access_token": "test-token"})
		case strings.HasSuffix(r.URL.Path, "/reset-password"):
			var value struct {
				Temporary bool   `json:"temporary"`
				Type      string `json:"type"`
			}
			json.NewDecoder(r.Body).Decode(&value)
			temporary = value.Temporary && value.Type == "password"
			w.WriteHeader(204)
		case strings.HasSuffix(r.URL.Path, "/logout"):
			logout = true
			w.WriteHeader(204)
		case r.Method == "PUT":
			if failEmail {
				w.WriteHeader(503)
				return
			}
			var value struct {
				Email    string `json:"email"`
				Verified bool   `json:"emailVerified"`
			}
			json.NewDecoder(r.Body).Decode(&value)
			emailChanged = value.Email == "new@example.test" && !value.Verified
			w.WriteHeader(204)
		case r.Method == "DELETE":
			if failDelete {
				w.WriteHeader(503)
			} else {
				w.WriteHeader(204)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer identity.Close()
	server.identity = keycloak.New(keycloak.Config{InternalURL: identity.URL, Realm: "kubephos", ManagementClientID: "management", ManagementClientSecret: "test-secret"})
	call := func(handler http.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/v1/users/"+id, strings.NewReader(body))
		r.SetPathValue("id", id)
		r = r.WithContext(context.WithValue(r.Context(), authenticatedUserKey{}, domain.User{ID: "destination-admin", Role: "admin"}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	if w := call(server.updateUserEmail, "PATCH", "user-edit", `{"email":"invalid"}`); w.Code != 422 {
		t.Fatal("invalid email accepted")
	}
	if w := call(server.updateUserEmail, "PATCH", "user-edit", `{"email":" NEW@example.test "}`); w.Code != 200 || !emailChanged {
		t.Fatalf("email update failed: %d", w.Code)
	}
	user, err := server.store.GetUserByID(ctx, "user-edit")
	if err != nil || user.Email != "new@example.test" {
		t.Fatal("local email not updated")
	}
	failEmail = true
	if w := call(server.updateUserEmail, "PATCH", "user-edit", `{"email":"failed@example.test"}`); w.Code != 502 {
		t.Fatal("identity email failure not reported")
	}
	user, err = server.store.GetUserByID(ctx, "user-edit")
	if err != nil || user.Email != "new@example.test" {
		t.Fatal("email failure was not rolled back")
	}
	failEmail = false
	if w := call(server.resetUserPassword, "POST", "user-edit", `{"password":"short"}`); w.Code != 422 {
		t.Fatal("weak temporary password accepted")
	}
	if _, err = pool.Exec(ctx, `INSERT INTO auth_sessions(token_hash,user_id,csrf_token,expires_at) VALUES('test-session','user-edit','csrf',now()+interval '1 hour')`); err != nil {
		t.Fatal(err)
	}
	if w := call(server.resetUserPassword, "POST", "user-edit", `{"password":"test-temporary-password"}`); w.Code != 204 || !temporary || !logout {
		t.Fatalf("temporary reset failed: %d", w.Code)
	}
	var sessions int
	pool.QueryRow(ctx, `SELECT count(*) FROM auth_sessions WHERE user_id='user-edit'`).Scan(&sessions)
	if sessions != 0 {
		t.Fatal("sessions were not revoked")
	}
	failDelete = true
	if w := call(server.deleteUser, "DELETE", "user-edit", ""); w.Code != 502 {
		t.Fatalf("expected identity failure: %d", w.Code)
	}
	if _, err = server.store.GetUserByID(ctx, "user-edit"); err != nil {
		t.Fatal("identity failure removed local account")
	}
	failDelete = false
	if w := call(server.deleteUser, "DELETE", "user-edit", ""); w.Code != 204 {
		t.Fatalf("delete failed: %d", w.Code)
	}
	if _, err = server.store.GetUserByID(ctx, "user-edit"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("user still exists")
	}
	if w := call(server.deleteUser, "DELETE", "destination-admin", ""); w.Code != 409 {
		t.Fatal("own account deletion accepted")
	}
}
