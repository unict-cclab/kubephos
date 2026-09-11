package harborbrowser

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

func TestHarborBrowserListsArtifacts(t *testing.T) {
	client := &fakeClient{}
	plugin := Plugin{Client: client}
	spec := json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_management","action":"list-artifacts","project":"development","repository":"team/plugin","reference":""}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Mutating || plan.Steps[0].Outputs[0].Type != "RegistryView" {
		t.Fatalf("unexpected plan %#v", plan)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	health, err := plugin.Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, value, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}
	if !containsRequest(client.requests, "GET /projects/development/repositories/team%252Fplugin/artifacts?page=1&page_size=100&with_tag=true") {
		t.Fatalf("repository path was not safely encoded: %#v", client.requests)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RegistryView.Spec.Action != "list-artifacts" || !strings.Contains(string(decoded.RegistryView.Spec.Data), "sha256:test") {
		t.Fatalf("unexpected registry view %#v", decoded.RegistryView)
	}
}

func TestHarborBrowserVerifiesDeletion(t *testing.T) {
	client := &fakeClient{}
	plugin := Plugin{Client: client}
	spec := json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_management","action":"delete-artifact","project":"development","repository":"plugin","reference":"sha256:test"}`)
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Steps[0].Mutating {
		t.Fatal("expected delete action to be mutating")
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	value, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	health, err := plugin.Verify(context.Background(), step, value, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy || health.Checks["deletion"] != "verified" {
		t.Fatalf("delete verification failed %#v %v", health, err)
	}
}

func TestHarborBrowserRunsAndVerifiesGarbageCollection(t *testing.T) {
	client := &fakeClient{}
	plugin := Plugin{Client: client}
	spec := json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_management","action":"run-garbage-collection"}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid || len(report.Issues) != 1 || report.Issues[0].Level != "warning" {
		t.Fatalf("unexpected validation report %#v", report)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Steps[0].Mutating {
		t.Fatal("expected garbage collection to be mutating")
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	health, err := plugin.Precheck(context.Background(), step, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("garbage collection precheck failed %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, value, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy || health.Checks["garbageCollection"] != "verified" {
		t.Fatalf("garbage collection verification failed %#v %v", health, err)
	}
	if !containsRequest(client.requests, "POST /system/gc/schedule") || !containsRequest(client.requests, "GET /system/gc/42") {
		t.Fatalf("unexpected requests %#v", client.requests)
	}
	if !strings.Contains(string(client.bodies[0]), `"delete_untagged":false`) || !strings.Contains(string(client.bodies[0]), `"dry_run":false`) {
		t.Fatalf("unsafe garbage collection defaults %s", client.bodies[0])
	}
}

func TestHarborBrowserPreviewsGarbageCollectionWithoutDeletingData(t *testing.T) {
	client := &fakeClient{}
	plugin := Plugin{Client: client}
	spec := json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_management","action":"preview-garbage-collection"}`)
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts(t)
	value, err := plugin.Execute(context.Background(), step, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	health, err := plugin.Verify(context.Background(), step, value, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("garbage collection preview failed %#v %v", health, err)
	}
	if !strings.Contains(string(client.bodies[0]), `"dry_run":true`) {
		t.Fatalf("preview is not a dry run %s", client.bodies[0])
	}
}

func TestHarborBrowserRequiresActionCoordinates(t *testing.T) {
	values := []json.RawMessage{
		json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_management","action":"list-repositories","project":""}`),
		json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_management","action":"list-artifacts","project":"development","repository":""}`),
		json.RawMessage(`{"registryEndpointRef":"art_endpoint","registryCredentialRef":"art_management","action":"delete-artifact","project":"development","repository":"plugin","reference":""}`),
	}
	for _, value := range values {
		if report := (Plugin{}).Validate(context.Background(), Invocation{Input: value}); report.Valid {
			t.Fatalf("expected validation failure for %s", value)
		}
	}
}

func testArtifacts(t *testing.T) map[string]domain.ResolvedArtifact {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	ca := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
	endpoint, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "RegistryEndpoint", "spec": map[string]any{"protocol": "oci", "host": "10.10.0.20", "url": "https://10.10.0.20", "apiURL": "https://10.10.0.20/api/v2.0", "caBundle": ca, "insecure": false, "projects": []string{"development", "releases"}}})
	credential, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "RegistryCredential", "metadata": map[string]string{"role": "management"}, "spec": map[string]any{"server": "10.10.0.20", "username": "admin", "password": "secret", "scopes": []string{"manage"}}})
	return map[string]domain.ResolvedArtifact{"registry-endpoint": {Value: endpoint}, "registry-credential": {Value: credential}}
}

type fakeClient struct {
	requests []string
	bodies   [][]byte
}

func (client *fakeClient) Do(_ context.Context, _ registryEndpoint, _ registryCredential, method, path string, body []byte) (apiResponse, error) {
	client.requests = append(client.requests, method+" "+path)
	if len(body) > 0 {
		client.bodies = append(client.bodies, append([]byte(nil), body...))
	}
	if path == "/health" {
		return apiResponse{Body: []byte(`{"status":"healthy"}`), Status: http.StatusOK}, nil
	}
	if method == http.MethodPost && path == "/system/gc/schedule" {
		return apiResponse{Status: http.StatusCreated, Header: http.Header{"Location": []string{"/api/v2.0/system/gc/42"}}}, nil
	}
	if path == "/system/gc/42" {
		return apiResponse{Body: []byte(`{"id":42,"job_name":"GARBAGE_COLLECTION","job_status":"Success"}`), Status: http.StatusOK}, nil
	}
	if method == http.MethodDelete {
		return apiResponse{Status: http.StatusOK}, nil
	}
	if strings.Contains(path, "/artifacts/") && !strings.Contains(path, "?") {
		return apiResponse{Body: []byte(`{"errors":[{"code":"NOT_FOUND"}]}`), Status: http.StatusNotFound}, nil
	}
	if strings.Contains(path, "/artifacts?") {
		return apiResponse{Body: []byte(`[{"digest":"sha256:test","tags":[{"name":"git-test"}]}]`), Status: http.StatusOK}, nil
	}
	return apiResponse{Body: []byte(`[]`), Status: http.StatusOK}, nil
}

func containsRequest(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
