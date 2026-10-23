package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/config"
)

func TestDoctorUsesListenerInsteadOfPublicURL(t *testing.T) {
	seen := map[string]bool{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen[r.URL.Path] = true; w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	if err := doctor(config.Config{HTTPAddress: strings.TrimPrefix(server.URL, "http://"), PublicURL: "https://external.invalid:18080"}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/health/live", "/health/ready", "/api/v1/auth/status"} {
		if !seen[path] {
			t.Fatalf("missing health check %s", path)
		}
	}
}

func TestLocalAPIURL(t *testing.T) {
	for _, tc := range []struct{ address, want string }{{":8080", "http://127.0.0.1:8080"}, {"0.0.0.0:8080", "http://127.0.0.1:8080"}, {"[::]:8080", "http://[::1]:8080"}, {"127.0.0.1:9090", "http://127.0.0.1:9090"}} {
		actual, err := localAPIURL(tc.address)
		if err != nil || actual != tc.want {
			t.Fatalf("%s: %s %v", tc.address, actual, err)
		}
	}
}
