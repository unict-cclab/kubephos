package proxmoxvm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestPlanDeclaresCreateAndDeleteEffects(t *testing.T) {
	spec := json.RawMessage(`{"connectionRef":"conn_test","node":"pve","templateVMID":8000,"targetVMID":9000,"name":"lifecycle","cores":2,"memoryMiB":2048,"start":false,"cleanupAfterTest":true}`)
	plan, err := (Plugin{}).Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 || plan.Steps[0].Effects[0].Action != "create" || plan.Steps[1].Effects[0].Action != "delete" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if plan.Steps[0].Effects[0].ExternalID != "qemu/9000" || !strings.HasPrefix(plan.Steps[0].Effects[0].Name, "kubephos-lifecycle-") {
		t.Fatalf("unexpected resource identity: %#v", plan.Steps[0].Effects[0])
	}
	if plan.Steps[0].Effects[0].Name != plan.Steps[1].Effects[0].Name {
		t.Fatal("create and delete must reference the same resolved identity")
	}
}

func TestLifecycleCreatesOnlyTargetAndRemovesIt(t *testing.T) {
	var lock sync.Mutex
	targetExists := false
	targetName := ""
	targetTags := ""
	targetDescription := ""
	writePaths := []string{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		response.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet {
			writePaths = append(writePaths, request.Method+" "+request.URL.Path)
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/resources":
			items := `[{"vmid":8000,"name":"template","node":"pve","status":"stopped","type":"qemu","template":1}`
			if targetExists {
				items += fmt.Sprintf(`,{"vmid":9000,"name":%q,"node":"pve","status":"stopped","type":"qemu","template":0}`, targetName)
			}
			_, _ = response.Write([]byte(`{"data":` + items + `]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes":
			_, _ = response.Write([]byte(`{"data":[{"node":"pve","status":"online"}]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/access/permissions":
			_, _ = response.Write([]byte(`{"data":{"/":{"Datastore.AllocateSpace":1,"VM.Allocate":1,"VM.Clone":1,"VM.Config.CPU":1,"VM.Config.Memory":1,"VM.Config.Options":1,"VM.PowerMgmt":1}}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve/qemu/8000/clone":
			_ = request.ParseForm()
			if request.Form.Get("newid") != "9000" || request.Form.Get("full") != "1" {
				http.Error(response, "invalid clone", http.StatusBadRequest)
				return
			}
			targetExists = true
			targetName = request.Form.Get("name")
			_, _ = response.Write([]byte(`{"data":"UPID:pve:clone"}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve/qemu/9000/config":
			_ = request.ParseForm()
			targetTags = request.Form.Get("tags")
			targetDescription = request.Form.Get("description")
			_, _ = response.Write([]byte(`{"data":"UPID:pve:config"}`))
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/tasks/"):
			_, _ = response.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve/qemu/9000/config" && targetExists:
			body, _ := json.Marshal(map[string]any{"data": map[string]string{"name": targetName, "tags": targetTags, "description": targetDescription}})
			_, _ = response.Write(body)
		case request.Method == http.MethodDelete && request.URL.Path == "/api2/json/nodes/pve/qemu/9000" && targetExists:
			targetExists = false
			_, _ = response.Write([]byte(`{"data":"UPID:pve:delete"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	secret := json.RawMessage(`{"tokenId":"test@pam!kubephos","tokenSecret":"top-secret"}`)
	spec := json.RawMessage(`{"connectionRef":"conn_test","node":"pve","templateVMID":8000,"targetVMID":9000,"name":"lifecycle","cores":2,"memoryMiB":2048,"start":false,"cleanupAfterTest":true}`)
	connection := json.RawMessage(fmt.Sprintf(`{"endpoint":%q,"credentialRef":"cred_test","verifyTLS":false}`, server.URL))
	invocation := Invocation{Input: spec, Secrets: map[string]json.RawMessage{"cred_test": secret}, Connections: map[string]json.RawMessage{"conn_test": connection}}
	report := (Plugin{}).Validate(context.Background(), invocation)
	if !report.Valid {
		t.Fatalf("unexpected validation failure: %#v", report.Issues)
	}
	plan, err := (Plugin{}).Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	log := func(string, string) error { return nil }
	for _, step := range plan.Steps {
		health, err := (Plugin{}).Precheck(context.Background(), step, invocation.Secrets, invocation.Connections, log)
		if err != nil || health.Status != domain.HealthHealthy {
			t.Fatalf("precheck failed: %#v %v", health, err)
		}
		result, err := (Plugin{}).Execute(context.Background(), step, invocation.Secrets, invocation.Connections, log)
		if err != nil {
			t.Fatal(err)
		}
		health, err = (Plugin{}).Verify(context.Background(), step, result, invocation.Secrets, invocation.Connections, log)
		if err != nil || health.Status != domain.HealthHealthy {
			t.Fatalf("verify failed: %#v %v", health, err)
		}
	}
	lock.Lock()
	defer lock.Unlock()
	if targetExists {
		t.Fatal("target VM was not deleted")
	}
	for _, write := range writePaths {
		if strings.Contains(write, "/qemu/100/") || strings.Contains(write, "/qemu/8001/") || strings.Contains(write, "/qemu/8002/") || strings.Contains(write, "/qemu/8000/config") {
			t.Fatalf("unexpected protected resource write: %s", write)
		}
	}
}

func TestCleanupRejectsUnownedVM(t *testing.T) {
	deleteCalled := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve/qemu/9000/config":
			_, _ = response.Write([]byte(`{"data":{"name":"someone-else","tags":"","description":""}}`))
		case request.Method == http.MethodDelete:
			deleteCalled = true
			_, _ = response.Write([]byte(`{"data":"UPID:pve:delete"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	defer server.Close()
	input, _ := json.Marshal(stepInput{Action: "create", ConnectionRef: "conn_test", Node: "pve", TemplateVMID: 8000, TargetVMID: 9000, Name: "kubephos-test-12345678", Marker: "1234567890abcdef"})
	step := domain.PlanStep{Input: input, Effects: []domain.ResourceEffect{{Action: "create", ExternalID: "qemu/9000", Kind: "virtual-machine", Name: "kubephos-test-12345678"}}}
	secrets := map[string]json.RawMessage{"cred_test": json.RawMessage(`{"tokenId":"test","tokenSecret":"secret"}`)}
	connections := map[string]json.RawMessage{"conn_test": json.RawMessage(fmt.Sprintf(`{"endpoint":%q,"credentialRef":"cred_test","verifyTLS":false}`, server.URL))}
	err := (Plugin{}).Cleanup(context.Background(), step, nil, secrets, connections, func(string, string) error { return nil })
	if err == nil || deleteCalled {
		t.Fatalf("cleanup should refuse unowned VM: %v", err)
	}
}
