package proxmox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestDiscoveryUsesOnlyGetAndProtectsResources(t *testing.T) {
	var lock sync.Mutex
	methods := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		lock.Lock()
		methods = append(methods, request.Method)
		lock.Unlock()
		if request.Header.Get("Authorization") != "PVEAPIToken=test@pam!kubephos=top-secret" {
			http.Error(response, "unauthorized", http.StatusUnauthorized)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/api2/json/version":
			_, _ = response.Write([]byte(`{"data":{"version":"9.0","release":"1"}}`))
		case "/api2/json/nodes":
			_, _ = response.Write([]byte(`{"data":[{"node":"pve","status":"online"}]}`))
		case "/api2/json/cluster/resources":
			_, _ = response.Write([]byte(`{"data":[{"vmid":100,"name":"existing","node":"pve","status":"running","type":"qemu","template":0}]}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	secret := json.RawMessage(`{"tokenId":"test@pam!kubephos","tokenSecret":"top-secret"}`)
	spec := json.RawMessage(`{"endpoint":"` + server.URL + `","credentialRef":"cred_test","verifyTLS":false}`)
	invocation := Invocation{Input: spec, Secrets: map[string]json.RawMessage{"cred_test": secret}}
	report := (Plugin{}).Validate(context.Background(), invocation)
	if !report.Valid {
		t.Fatalf("unexpected validation: %#v", report.Issues)
	}
	plan, err := (Plugin{}).Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(plan.Steps))
	}
	result, err := (Plugin{}).Execute(context.Background(), plan.Steps[1], invocation.Secrets, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	var inventory struct {
		Resources []struct {
			ExternalID string `json:"externalId"`
			Kind       string `json:"kind"`
		} `json:"resources"`
	}
	if err := json.Unmarshal(result, &inventory); err != nil {
		t.Fatal(err)
	}
	if len(inventory.Resources) != 1 || inventory.Resources[0].ExternalID != "qemu/100" || inventory.Resources[0].Kind != "virtual-machine" {
		t.Fatalf("resource identity is invalid: %s", result)
	}
	lock.Lock()
	defer lock.Unlock()
	for _, method := range methods {
		if method != http.MethodGet {
			t.Fatalf("unexpected method %s", method)
		}
	}
}

func TestPrecheckRejectsMissingCredential(t *testing.T) {
	step := domain.PlanStep{Input: json.RawMessage(`{"action":"connection","endpoint":"https://example.test:8006","credentialRef":"cred_missing","verifyTLS":true}`)}
	_, err := (Plugin{}).Precheck(context.Background(), step, nil, func(string, string) error { return nil })
	if err == nil {
		t.Fatal("expected missing credential error")
	}
}
