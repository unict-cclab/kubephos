package proxmoxvm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

func TestTopologyPlanDeclaresAtomicMachineSet(t *testing.T) {
	spec := json.RawMessage(`{"connectionRef":"conn_test","node":"pve","templateVMID":8000,"baseVMID":9000,"namePrefix":"dev","machineCount":3,"cores":2,"memoryMiB":4096,"sshUser":"ubuntu","cleanupAfterTest":false}`)
	invocation := Invocation{Input: spec, Connections: map[string]json.RawMessage{"conn_test": json.RawMessage(`{"endpoint":"https://proxmox.test","credentialRef":"cred_test","verifyTLS":false,"vmidStart":9000,"addressStart":"10.10.0.10","prefixLength":24,"gateway":"10.10.0.1","dnsServer":"1.1.1.1"}`)}}
	plan, err := (TopologyPlugin{}).Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].Effects) != 3 || len(plan.Steps[0].Outputs) != 2 {
		t.Fatalf("unexpected topology plan %#v", plan)
	}
	if plan.Steps[0].Effects[0].ExternalID != "qemu/9000" || plan.Steps[0].Effects[2].ExternalID != "qemu/9002" {
		t.Fatalf("unexpected VMID range %#v", plan.Steps[0].Effects)
	}
	if !plan.Steps[0].Outputs[1].Sensitive || plan.Steps[0].Outputs[1].Type != "MachineAccess" {
		t.Fatalf("machine access must be a sensitive typed output %#v", plan.Steps[0].Outputs[1])
	}
}

func TestTopologyCreatesVerifiesAndRemovesOnlyPlannedMachines(t *testing.T) {
	server, state := newTopologyServer(t, 0)
	defer server.Close()
	invocation, _ := topologyInvocation(server.URL, true)
	plugin := TopologyPlugin{SSH: fakeSSHAccess{}}
	report := (TopologyPlugin{}).Validate(context.Background(), invocation)
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 2 {
		t.Fatalf("expected provision and cleanup steps, got %d", len(plan.Steps))
	}
	log := func(string, string) error { return nil }
	var provisionResult json.RawMessage
	for _, step := range plan.Steps {
		health, err := plugin.Precheck(context.Background(), step, invocation.Secrets, invocation.Connections, log)
		if err != nil || health.Status != domain.HealthHealthy {
			t.Fatalf("precheck failed %#v %v", health, err)
		}
		result, err := plugin.Execute(context.Background(), step, invocation.Secrets, invocation.Connections, log)
		if err != nil {
			t.Fatal(err)
		}
		if step.ID == "provision-topology" {
			provisionResult = result
		}
		health, err = plugin.Verify(context.Background(), step, result, invocation.Secrets, invocation.Connections, log)
		if err != nil || health.Status != domain.HealthHealthy {
			t.Fatalf("verify failed %#v %v", health, err)
		}
	}
	var result topologyResult
	if err := json.Unmarshal(provisionResult, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.MachineSet.Spec.Machines) != 3 || result.MachineSet.Spec.NetworkCIDR != "10.10.0.0/24" || !strings.Contains(result.MachineAccess.Spec.PrivateKey, "OPENSSH PRIVATE KEY") {
		t.Fatalf("unexpected topology result %#v", result)
	}
	state.lock.Lock()
	defer state.lock.Unlock()
	for id := 9000; id <= 9002; id++ {
		if state.machines[id].exists {
			t.Fatalf("VM %d was not removed", id)
		}
	}
	for _, write := range state.writes {
		if strings.Contains(write, "/qemu/100/") || strings.Contains(write, "/qemu/8000/config") {
			t.Fatalf("unexpected protected resource write %s", write)
		}
	}
}

func TestTopologyRollsBackCreatedMachinesOnPartialFailure(t *testing.T) {
	server, state := newTopologyServer(t, 9001)
	defer server.Close()
	invocation, _ := topologyInvocation(server.URL, false)
	plugin := TopologyPlugin{SSH: fakeSSHAccess{}}
	plan, err := plugin.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plugin.Execute(context.Background(), plan.Steps[0], invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err == nil {
		t.Fatal("expected partial provisioning failure")
	}
	state.lock.Lock()
	defer state.lock.Unlock()
	if state.machines[9000].exists {
		t.Fatal("first machine was not rolled back")
	}
	if state.machines[9001].exists || state.machines[9002].exists {
		t.Fatal("unplanned machines were left behind")
	}
}

func TestTopologySupportsExplicitVerifiedCleanup(t *testing.T) {
	server, state := newTopologyServer(t, 0)
	defer server.Close()
	invocation, _ := topologyInvocation(server.URL, false)
	plugin := TopologyPlugin{SSH: fakeSSHAccess{}}
	plan, err := plugin.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	result, err := plugin.Execute(context.Background(), step, invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	step.Cleanup = true
	health, err := plugin.Precheck(context.Background(), step, invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup precheck failed %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), step, result, invocation.Secrets, invocation.Connections, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, nil, invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup verification failed %#v %v", health, err)
	}
	state.lock.Lock()
	defer state.lock.Unlock()
	for id := 9000; id <= 9002; id++ {
		if state.machines[id].exists {
			t.Fatalf("VM %d was not removed", id)
		}
	}
}

func TestTopologyCleanupAcceptsOnlyMatchingPartialClone(t *testing.T) {
	input := topologyStepInput{Marker: "marker", Machines: []plannedMachine{{VMID: 110, Name: "kubephos-acceptance-01-marker"}}}
	planned := input.Machines[0]
	if !cleanupOwnedTopology(vmConfig{Name: planned.Name}, input, planned) {
		t.Fatal("matching partial clone must remain recoverable")
	}
	if cleanupOwnedTopology(vmConfig{Name: "protected"}, input, planned) {
		t.Fatal("cleanup must reject another machine name")
	}
	if cleanupOwnedTopology(vmConfig{Name: planned.Name, Description: "another owner"}, input, planned) {
		t.Fatal("cleanup must reject a conflicting ownership description")
	}
}

func TestProxmoxSSHKeyEncodingUsesPercentEscapes(t *testing.T) {
	key := "ssh-ed25519 AAAA+/value"
	encoded := encodeProxmoxSSHKey(key)
	if strings.Contains(encoded, "+") || !strings.Contains(encoded, "%20") || !strings.Contains(encoded, "%2B") || !strings.Contains(encoded, "%2F") {
		t.Fatalf("unexpected encoded key %q", encoded)
	}
	decoded, err := url.QueryUnescape(encoded)
	if err != nil || decoded != key {
		t.Fatalf("unexpected decoded key %q: %v", decoded, err)
	}
}

func TestTopologyAddressesAreDeterministicAndBounded(t *testing.T) {
	configuration := connectionConfig{VMIDStart: 9000, AddressStart: "10.10.0.253", PrefixLength: 24, Gateway: "10.10.0.1", DNSServer: "1.1.1.1"}
	addresses, err := topologyAddresses(configuration, 9000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(addresses, ",") != "10.10.0.253,10.10.0.254" {
		t.Fatalf("unexpected addresses %#v", addresses)
	}
	if _, err := topologyAddresses(configuration, 9000, 3); err == nil {
		t.Fatal("expected broadcast boundary rejection")
	}
	if _, err := topologyAddresses(configuration, 8999, 1); err == nil {
		t.Fatal("expected VMID range boundary rejection")
	}
}

func TestTopologyCleanupIsIdempotent(t *testing.T) {
	server, _ := newTopologyServer(t, 0)
	defer server.Close()
	invocation, _ := topologyInvocation(server.URL, false)
	plugin := TopologyPlugin{SSH: fakeSSHAccess{}}
	plan, err := plugin.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	result, err := plugin.Execute(context.Background(), step, invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := plugin.Cleanup(context.Background(), step, result, invocation.Secrets, invocation.Connections, func(string, string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Cleanup(context.Background(), step, result, invocation.Secrets, invocation.Connections, func(string, string) error { return nil }); err != nil {
		t.Fatalf("repeated cleanup must succeed: %v", err)
	}
	step.Cleanup = true
	health, err := plugin.Precheck(context.Background(), step, invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("repeated cleanup precheck must succeed: %#v %v", health, err)
	}
}

type topologyServerState struct {
	lock     sync.Mutex
	machines map[int]*topologyServerMachine
	writes   []string
	failVMID int
}

type topologyServerMachine struct {
	exists      bool
	name        string
	status      string
	tags        string
	description string
	ciUser      string
	sshKeys     string
	ipConfig0   string
	nameServer  string
}

type fakeSSHAccess struct{}

func (fakeSSHAccess) Wait(context.Context, string, string, string, time.Duration) error {
	return nil
}

func newTopologyServer(t *testing.T, failVMID int) (*httptest.Server, *topologyServerState) {
	t.Helper()
	state := &topologyServerState{machines: map[int]*topologyServerMachine{}, failVMID: failVMID}
	for id := 9000; id <= 9002; id++ {
		state.machines[id] = &topologyServerMachine{}
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		state.lock.Lock()
		defer state.lock.Unlock()
		response.Header().Set("Content-Type", "application/json")
		if request.Method != http.MethodGet {
			state.writes = append(state.writes, request.Method+" "+request.URL.Path)
		}
		switch {
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/cluster/resources":
			items := []map[string]any{{"vmid": 8000, "name": "template", "node": "pve", "status": "stopped", "type": "qemu", "template": 1}}
			for id, machine := range state.machines {
				if machine.exists {
					items = append(items, map[string]any{"vmid": id, "name": machine.name, "node": "pve", "status": machine.status, "type": "qemu", "template": 0})
				}
			}
			value, _ := json.Marshal(map[string]any{"data": items})
			_, _ = response.Write(value)
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes":
			_, _ = response.Write([]byte(`{"data":[{"node":"pve","status":"online"}]}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/access/permissions":
			_, _ = response.Write([]byte(`{"data":{"/":{"Datastore.AllocateSpace":1,"VM.Allocate":1,"VM.Clone":1,"VM.Config.CPU":1,"VM.Config.Memory":1,"VM.Config.Options":1,"VM.PowerMgmt":1}}}`))
		case request.Method == http.MethodPost && request.URL.Path == "/api2/json/nodes/pve/qemu/8000/clone":
			_ = request.ParseForm()
			id, _ := strconv.Atoi(request.Form.Get("newid"))
			if id == state.failVMID {
				http.Error(response, "planned failure", http.StatusInternalServerError)
				return
			}
			machine := state.machines[id]
			machine.exists = true
			machine.name = request.Form.Get("name")
			machine.status = "stopped"
			_, _ = response.Write([]byte(`{"data":"UPID:pve:clone"}`))
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/config"):
			id := topologyRequestVMID(request.URL.Path)
			machine := state.machines[id]
			if machine == nil || !machine.exists {
				http.NotFound(response, request)
				return
			}
			_ = request.ParseForm()
			machine.tags = request.Form.Get("tags")
			machine.description = request.Form.Get("description")
			machine.ciUser = request.Form.Get("ciuser")
			machine.sshKeys = request.Form.Get("sshkeys")
			machine.ipConfig0 = request.Form.Get("ipconfig0")
			machine.nameServer = request.Form.Get("nameserver")
			_, _ = response.Write([]byte(`{"data":"UPID:pve:config"}`))
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/status/start"):
			id := topologyRequestVMID(request.URL.Path)
			state.machines[id].status = "running"
			_, _ = response.Write([]byte(`{"data":"UPID:pve:start"}`))
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/status/stop"):
			id := topologyRequestVMID(request.URL.Path)
			state.machines[id].status = "stopped"
			_, _ = response.Write([]byte(`{"data":"UPID:pve:stop"}`))
		case request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/tasks/"):
			_, _ = response.Write([]byte(`{"data":{"status":"stopped","exitstatus":"OK"}}`))
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/config"):
			id := topologyRequestVMID(request.URL.Path)
			machine := state.machines[id]
			if machine == nil || !machine.exists {
				http.NotFound(response, request)
				return
			}
			value, _ := json.Marshal(map[string]any{"data": map[string]string{"name": machine.name, "tags": machine.tags, "description": machine.description, "ciuser": machine.ciUser, "sshkeys": machine.sshKeys, "ipconfig0": machine.ipConfig0, "nameserver": machine.nameServer}})
			_, _ = response.Write(value)
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/agent/network-get-interfaces"):
			id := topologyRequestVMID(request.URL.Path)
			address := fmt.Sprintf("10.10.0.%d", id-8990)
			_, _ = response.Write([]byte(fmt.Sprintf(`{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address":%q,"ip-address-type":"ipv4"}]}]}}`, address)))
		case request.Method == http.MethodDelete && strings.Contains(request.URL.Path, "/qemu/"):
			id := topologyRequestVMID(request.URL.Path)
			machine := state.machines[id]
			if machine == nil || !machine.exists {
				http.NotFound(response, request)
				return
			}
			machine.exists = false
			_, _ = response.Write([]byte(`{"data":"UPID:pve:delete"}`))
		default:
			http.NotFound(response, request)
		}
	}))
	return server, state
}

func topologyInvocation(endpoint string, cleanup bool) (Invocation, json.RawMessage) {
	spec := json.RawMessage(fmt.Sprintf(`{"connectionRef":"conn_test","node":"pve","templateVMID":8000,"baseVMID":9000,"namePrefix":"dev","machineCount":3,"cores":2,"memoryMiB":4096,"sshUser":"ubuntu","cleanupAfterTest":%t}`, cleanup))
	return Invocation{
		Input:       spec,
		Secrets:     map[string]json.RawMessage{"cred_test": json.RawMessage(`{"tokenId":"test@pam!kubephos","tokenSecret":"top-secret"}`)},
		Connections: map[string]json.RawMessage{"conn_test": json.RawMessage(fmt.Sprintf(`{"endpoint":%q,"credentialRef":"cred_test","verifyTLS":false,"vmidStart":9000,"addressStart":"10.10.0.10","prefixLength":24,"gateway":"10.10.0.1","dnsServer":"1.1.1.1"}`, endpoint))},
	}, spec
}

func topologyRequestVMID(path string) int {
	parts := strings.Split(path, "/")
	for index, part := range parts {
		if part == "qemu" && index+1 < len(parts) {
			value, _ := strconv.Atoi(parts[index+1])
			return value
		}
	}
	return 0
}
