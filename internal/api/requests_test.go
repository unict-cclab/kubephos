package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestAdminRequestTextValidation(t *testing.T) {
	if message, err := trimAdminRequestText("  Please add a cluster for my thesis.  ", true); err != nil || message != "Please add a cluster for my thesis." {
		t.Fatalf("valid message rejected or not trimmed: %q %v", message, err)
	}
	for name, message := range map[string]string{
		"empty":      "",
		"whitespace": " \n\t ",
		"too long":   strings.Repeat("é", maxAdminRequestLength+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := trimAdminRequestText(message, true); err == nil {
				t.Fatal("invalid message accepted")
			}
		})
	}
	if _, err := trimAdminRequestText(strings.Repeat("é", maxAdminRequestLength), true); err != nil {
		t.Fatal("limit is counted in characters, not bytes")
	}
	if reply, err := trimAdminRequestText("  ", false); err != nil || reply != "" {
		t.Fatalf("an optional reply cannot be left empty: %q %v", reply, err)
	}
}

func TestAdminRequestRoutesReachMembers(t *testing.T) {
	if !userMutationAllowed("/api/v1/admin-requests") {
		t.Fatal("members cannot send requests")
	}
	// Ownership and the administrator-only changes are enforced by the handlers.
	if !userMutationAllowed("/api/v1/admin-requests/req_1") {
		t.Fatal("members cannot edit or resolve their own requests")
	}
	if adminReadPath("/api/v1/admin-requests") {
		t.Fatal("members cannot read their own requests")
	}
}

func TestAdminRequestsAreAudited(t *testing.T) {
	for _, test := range []struct{ method, path, action, targetType, id string }{
		{http.MethodPost, "/api/v1/admin-requests", "admin-request.create", "admin-request", ""},
		{http.MethodPatch, "/api/v1/admin-requests/req_1", "admin-request.patch", "admin-requests", "req_1"},
		{http.MethodDelete, "/api/v1/admin-requests/req_1", "admin-request.delete", "admin-requests", "req_1"},
	} {
		action, targetType, targetID, ok := auditTarget(httptest.NewRequest(test.method, test.path, nil))
		if !ok || action != test.action || targetType != test.targetType || targetID != test.id {
			t.Fatalf("%s %s audited as %q %q %q %v", test.method, test.path, action, targetType, targetID, ok)
		}
	}
}

func TestAdminRequestLifecycle(t *testing.T) {
	s, pool := transferServer(t, "http://127.0.0.1:1")
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `INSERT INTO users(id,username,role) VALUES('alice','alice','user'),('bob','bob','user'),('root','root','admin')`); err != nil {
		t.Fatal(err)
	}
	roles := map[string]string{"alice": "user", "bob": "user", "root": "admin"}
	call := func(handler http.HandlerFunc, method, id, user, body string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/admin-requests", strings.NewReader(body))
		r.SetPathValue("id", id)
		r = r.WithContext(context.WithValue(r.Context(), authenticatedUserKey{}, domain.User{ID: user, Username: user, Role: roles[user]}))
		w := httptest.NewRecorder()
		handler(w, r)
		return w
	}
	decode := func(w *httptest.ResponseRecorder, status int) domain.AdminRequest {
		t.Helper()
		var item domain.AdminRequest
		if w.Code != status || json.Unmarshal(w.Body.Bytes(), &item) != nil {
			t.Fatalf("expected %d, got %d %s", status, w.Code, w.Body.String())
		}
		return item
	}
	list := func(user string) []domain.AdminRequest {
		t.Helper()
		w := call(s.listAdminRequests, http.MethodGet, "", user, "")
		var result struct {
			Items []domain.AdminRequest `json:"items"`
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &result) != nil {
			t.Fatalf("list as %s: %d %s", user, w.Code, w.Body.String())
		}
		return result.Items
	}

	request := decode(call(s.createAdminRequest, http.MethodPost, "", "alice", `{"message":"  Please share the edge cluster with me.  "}`), http.StatusCreated)
	if request.Status != domain.AdminRequestOpen || request.Requester != "alice" || request.Message != "Please share the edge cluster with me." {
		t.Fatalf("unexpected request: %+v", request)
	}

	t.Run("visibility", func(t *testing.T) {
		if items := list("bob"); len(items) != 0 {
			t.Fatal("a member sees someone else's request")
		}
		if items := list("alice"); len(items) != 1 || items[0].ID != request.ID {
			t.Fatalf("the requester cannot see their request: %+v", items)
		}
		if items := list("root"); len(items) != 1 {
			t.Fatalf("an administrator does not see every request: %+v", items)
		}
	})

	t.Run("rejected changes", func(t *testing.T) {
		for name, test := range map[string]struct {
			handler      http.HandlerFunc
			method, id   string
			user, body   string
			expectedCode int
		}{
			"administrator sends":     {s.createAdminRequest, http.MethodPost, "", "root", `{"message":"hello"}`, http.StatusForbidden},
			"empty message":           {s.createAdminRequest, http.MethodPost, "", "alice", `{"message":"   "}`, http.StatusUnprocessableEntity},
			"member resolves another": {s.updateAdminRequest, http.MethodPatch, request.ID, "bob", `{"status":"resolved"}`, http.StatusNotFound},
			"member edits another":    {s.updateAdminRequest, http.MethodPatch, request.ID, "bob", `{"message":"Mine now."}`, http.StatusNotFound},
			"member reopens":          {s.updateAdminRequest, http.MethodPatch, request.ID, "alice", `{"status":"open"}`, http.StatusForbidden},
			"member replies":          {s.updateAdminRequest, http.MethodPatch, request.ID, "alice", `{"status":"resolved","reply":"Done."}`, http.StatusForbidden},
			"member clears message":   {s.updateAdminRequest, http.MethodPatch, request.ID, "alice", `{"message":"   "}`, http.StatusUnprocessableEntity},
			"member changes nothing":  {s.updateAdminRequest, http.MethodPatch, request.ID, "alice", `{}`, http.StatusUnprocessableEntity},
			"admin edits message":     {s.updateAdminRequest, http.MethodPatch, request.ID, "root", `{"message":"Rewritten."}`, http.StatusForbidden},
			"member deletes":          {s.deleteAdminRequest, http.MethodDelete, request.ID, "alice", ``, http.StatusForbidden},
			"delete missing request":  {s.deleteAdminRequest, http.MethodDelete, "req_missing", "root", ``, http.StatusNotFound},
			"unknown status":          {s.updateAdminRequest, http.MethodPatch, request.ID, "root", `{"status":"closed"}`, http.StatusUnprocessableEntity},
			"reply without resolving": {s.updateAdminRequest, http.MethodPatch, request.ID, "root", `{"status":"open","reply":"Done."}`, http.StatusUnprocessableEntity},
			"missing request":         {s.updateAdminRequest, http.MethodPatch, "req_missing", "root", `{"status":"resolved"}`, http.StatusNotFound},
		} {
			if w := call(test.handler, test.method, test.id, test.user, test.body); w.Code != test.expectedCode {
				t.Errorf("%s: expected %d, got %d %s", name, test.expectedCode, w.Code, w.Body.String())
			}
		}
	})

	t.Run("resolve with a reply, then reopen", func(t *testing.T) {
		resolved := decode(call(s.updateAdminRequest, http.MethodPatch, request.ID, "root", `{"status":"resolved","reply":"  Shared with you.  "}`), http.StatusOK)
		if resolved.Status != domain.AdminRequestResolved || resolved.Reply != "Shared with you." || resolved.ResolvedBy != "root" || resolved.ResolvedAt == nil {
			t.Fatalf("resolve: %+v", resolved)
		}
		if items := list("alice"); items[0].Reply != "Shared with you." || items[0].ResolvedBy != "root" {
			t.Fatalf("the requester does not see the answer: %+v", items[0])
		}
		reopened := decode(call(s.updateAdminRequest, http.MethodPatch, request.ID, "root", `{"status":"open"}`), http.StatusOK)
		if reopened.Status != domain.AdminRequestOpen || reopened.Reply != "" || reopened.ResolvedBy != "" || reopened.ResolvedAt != nil {
			t.Fatalf("reopen kept the resolution: %+v", reopened)
		}
	})

	t.Run("open requests are limited per member", func(t *testing.T) {
		for range maxOpenAdminRequests - 1 {
			decode(call(s.createAdminRequest, http.MethodPost, "", "alice", `{"message":"Another request."}`), http.StatusCreated)
		}
		if w := call(s.createAdminRequest, http.MethodPost, "", "alice", `{"message":"One too many."}`); w.Code != http.StatusConflict {
			t.Fatalf("limit not enforced: %d", w.Code)
		}
		decode(call(s.createAdminRequest, http.MethodPost, "", "bob", `{"message":"My own request."}`), http.StatusCreated)
		decode(call(s.updateAdminRequest, http.MethodPatch, request.ID, "root", `{"status":"resolved"}`), http.StatusOK)
		decode(call(s.createAdminRequest, http.MethodPost, "", "alice", `{"message":"Now there is room."}`), http.StatusCreated)
	})

	t.Run("members edit and resolve their own open requests", func(t *testing.T) {
		mine := decode(call(s.createAdminRequest, http.MethodPost, "", "bob", `{"message":"Please add GPUs."}`), http.StatusCreated)
		edited := decode(call(s.updateAdminRequest, http.MethodPatch, mine.ID, "bob", `{"message":"  Please add two GPUs.  "}`), http.StatusOK)
		if edited.Message != "Please add two GPUs." || edited.Status != domain.AdminRequestOpen {
			t.Fatalf("edit: %+v", edited)
		}
		resolved := decode(call(s.updateAdminRequest, http.MethodPatch, mine.ID, "bob", `{"status":"resolved"}`), http.StatusOK)
		if resolved.Status != domain.AdminRequestResolved || resolved.ResolvedBy != "bob" || resolved.Reply != "" || resolved.Message != "Please add two GPUs." {
			t.Fatalf("self-resolve: %+v", resolved)
		}
		if w := call(s.updateAdminRequest, http.MethodPatch, mine.ID, "bob", `{"message":"Changed my mind."}`); w.Code != http.StatusConflict {
			t.Fatalf("a resolved request was edited: %d", w.Code)
		}
	})

	t.Run("administrators delete requests", func(t *testing.T) {
		if w := call(s.deleteAdminRequest, http.MethodDelete, request.ID, "root", ""); w.Code != http.StatusNoContent {
			t.Fatalf("delete: %d %s", w.Code, w.Body.String())
		}
		for _, item := range list("root") {
			if item.ID == request.ID {
				t.Fatal("a deleted request is still listed")
			}
		}
	})
}
