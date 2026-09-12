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
	"time"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type fakeTemplateGuest struct {
	mu       sync.Mutex
	prepared bool
	address  string
}

func (guest *fakeTemplateGuest) Prepare(_ context.Context, address, user, privateKey string, _ time.Duration, _ plugins.Logger) error {
	if user != "ubuntu" || !strings.Contains(privateKey, "OPENSSH PRIVATE KEY") {
		return fmt.Errorf("invalid guest access")
	}
	guest.mu.Lock()
	defer guest.mu.Unlock()
	guest.prepared = true
	guest.address = address
	return nil
}

func TestTemplateLifecycleValidatesCreatesVerifiesAndDeletes(t *testing.T) {
	state := struct {
		sync.Mutex
		exists       bool
		template     bool
		status       string
		name         string
		tags         string
		description  string
		statusChecks int
	}{status: "stopped"}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		state.Lock()
		defer state.Unlock()
		write := func(value any) {
			_ = json.NewEncoder(response).Encode(map[string]any{"data": value})
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/resources":
			items := []map[string]any{}
			if state.exists {
				template := 0
				if state.template {
					template = 1
				}
				items = append(items, map[string]any{"vmid": 8100, "name": state.name, "node": "pve", "status": state.status, "type": "qemu", "template": template})
			}
			write(items)
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes":
			write([]map[string]any{{"node": "pve", "status": "online"}})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve/storage":
			write([]map[string]any{{"storage": "local", "active": 1, "enabled": 1, "content": "iso,vztmpl,import"}, {"storage": "local-lvm", "active": 1, "enabled": 1, "content": "images,rootdir"}})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/access/permissions":
			write(map[string]map[string]int{"/": {"Datastore.AllocateSpace": 1, "Datastore.AllocateTemplate": 1, "Datastore.Audit": 1, "VM.Allocate": 1, "VM.Config.Cloudinit": 1, "VM.Config.CPU": 1, "VM.Config.Disk": 1, "VM.Config.Memory": 1, "VM.Config.Network": 1, "VM.Config.Options": 1, "VM.PowerMgmt": 1}})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve/storage/local/content":
			write([]map[string]string{{"volid": "local:import/noble-server-cloudimg-amd64.qcow2"}})
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve/qemu":
			_ = request.ParseForm()
			state.exists = true
			state.name = request.Form.Get("name")
			state.tags = request.Form.Get("tags")
			state.description = request.Form.Get("description")
			write("UPID:pve:create")
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve/qemu/8100/config":
			write(map[string]string{"name": state.name, "tags": state.tags, "description": state.description, "boot": "order=scsi0;ide2", "scsi0": "local-lvm:vm-8100-disk-0,size=32G", "agent": "enabled=1", "net0": "virtio=AA:BB:CC:DD:EE:FF,bridge=vmbr0", "ciuser": "ubuntu", "nameserver": "1.1.1.1", "ipconfig0": "ip=dhcp"})
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve/qemu/8100/status/current":
			template := 0
			if state.template && state.statusChecks > 0 {
				template = 1
			}
			state.statusChecks++
			write(map[string]any{"vmid": 8100, "name": state.name, "status": state.status, "template": template})
		case request.Method == http.MethodPut && request.URL.Path == "/api2/json/nodes/pve/qemu/8100/resize":
			write("UPID:pve:resize")
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve/qemu/8100/status/start":
			state.status = "running"
			write("UPID:pve:start")
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve/qemu/8100/agent/network-get-interfaces":
			write(map[string]any{"result": []map[string]any{{"name": "eth0", "ip-addresses": []map[string]string{{"ip-address": "10.0.0.50", "ip-address-type": "ipv4"}}}}})
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve/qemu/8100/status/shutdown":
			state.status = "stopped"
			write("UPID:pve:shutdown")
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve/qemu/8100/template":
			state.template = true
			write("UPID:pve:template")
		case request.Method == http.MethodDelete && request.URL.Path == "/api2/json/nodes/pve/qemu/8100":
			state.exists = false
			write("UPID:pve:delete")
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/tasks/"):
			write(map[string]string{"status": "stopped", "exitstatus": "OK"})
		default:
			http.Error(response, request.Method+" "+request.URL.String(), http.StatusNotFound)
		}
	}))
	defer server.Close()
	configuration, _ := json.Marshal(map[string]any{"endpoint": server.URL, "credentialRef": "cred", "verifyTLS": false})
	secret := json.RawMessage(`{"tokenId":"root@pam!test","tokenSecret":"secret-value"}`)
	spec := json.RawMessage(`{"connectionRef":"conn","name":"ubuntu-template","node":"pve","vmid":8100,"os":"ubuntu-24.04","cloudImageURL":"","storage":"local-lvm","imageStorage":"local","bridge":"vmbr0","diskGiB":32,"address":"","prefixLength":24,"gateway":"","dnsServer":"1.1.1.1","sshUser":"ubuntu"}`)
	invocation := Invocation{Input: spec, Secrets: map[string]json.RawMessage{"cred": secret}, Connections: map[string]json.RawMessage{"conn": configuration}}
	guest := &fakeTemplateGuest{}
	plugin := TemplatePlugin{Guest: guest}
	validation := plugin.Validate(context.Background(), invocation)
	if !validation.Valid {
		t.Fatalf("validation failed: %+v", validation.Issues)
	}
	plan, err := plugin.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].Effects) != 1 || plan.Steps[0].Effects[0].Kind != "virtual-machine-template" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	precheck, err := plugin.Precheck(context.Background(), plan.Steps[0], invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil || precheck.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed: %+v %v", precheck, err)
	}
	result, err := plugin.Execute(context.Background(), plan.Steps[0], invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	guest.mu.Lock()
	prepared, address := guest.prepared, guest.address
	guest.mu.Unlock()
	if !prepared || address != "10.0.0.50" {
		t.Fatalf("guest preparation mismatch: %t %s", prepared, address)
	}
	health, err := plugin.Verify(context.Background(), plan.Steps[0], result, invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verification failed: %+v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), plan.Steps[0], result, invocation.Secrets, invocation.Connections, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	state.Lock()
	exists := state.exists
	state.Unlock()
	if exists {
		t.Fatal("template still exists after cleanup")
	}
}

func TestTemplateImageFilename(t *testing.T) {
	values := map[string]string{
		"https://example.test/image.img":            "image.qcow2",
		"https://example.test/debian.qcow2?x=1":     "debian.qcow2",
		"https://example.test/files/custom.raw":     "custom.raw",
		"https://example.test/files/custom.vmdk":    "custom.vmdk",
		"https://example.test/files/no-extension":   "no-extension.qcow2",
		"https://example.test/files/archive.tar.gz": "archive.tar.qcow2",
	}
	for input, expected := range values {
		if actual := templateImageFilename(input); actual != expected {
			t.Errorf("%s: expected %s, got %s", input, expected, actual)
		}
	}
}

func TestTemplateStaticNetworkValidation(t *testing.T) {
	spec := TemplateSpec{ConnectionRef: "conn", Name: "template", Node: "pve", VMID: 8100, OS: "ubuntu-24.04", Storage: "local-lvm", ImageStorage: "local", Bridge: "vmbr0", DiskGiB: 32, Address: "10.0.0.50", PrefixLength: 24, Gateway: "", DNSServer: "1.1.1.1", SSHUser: "ubuntu"}
	issue := validateTemplateValues(spec)
	if issue == nil || issue.Path != "address" {
		t.Fatalf("expected address issue, got %+v", issue)
	}
}
