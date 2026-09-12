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
	spec := json.RawMessage(`{"connectionRef":"conn_test","node":"pve","templateVMID":8000,"baseVMID":9000,"addressStart":"10.10.0.10","prefixLength":24,"gateway":"10.10.0.1","dnsServer":"1.1.1.1","namePrefix":"dev","machineCount":3,"cores":2,"memoryMiB":4096,"diskGiB":32,"machineProfiles":[{"cores":2,"memoryMiB":4096,"diskGiB":40},{"cores":4,"memoryMiB":8192,"diskGiB":80},{"cores":8,"memoryMiB":16384,"diskGiB":120}],"sshUser":"ubuntu","cleanupAfterTest":false}`)
	invocation := Invocation{Input: spec, Connections: map[string]json.RawMessage{"conn_test": json.RawMessage(`{"endpoint":"https://proxmox.test","credentialRef":"cred_test","verifyTLS":false}`)}}
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
	var input topologyStepInput
	if err := json.Unmarshal(plan.Steps[0].Input, &input); err != nil || input.DiskGiB != 32 {
		t.Fatalf("disk capacity is missing from the plan: %#v %v", input, err)
	}
	if input.Machines[0].Cores != 2 || input.Machines[1].MemoryMiB != 8192 || input.Machines[2].DiskGiB != 120 {
		t.Fatalf("per-machine capacity is missing from the plan: %#v", input.Machines)
	}
}

func TestTopologyRejectsIncompleteMachineProfiles(t *testing.T) {
	spec := TopologySpec{ConnectionRef: "conn_test", Node: "pve", TemplateVMID: 8000, BaseVMID: 9000, AddressStart: "10.10.0.10", PrefixLength: 24, Gateway: "10.10.0.1", DNSServer: "1.1.1.1", NamePrefix: "dev", MachineCount: 3, Cores: 2, MemoryMiB: 4096, DiskGiB: 32, MachineProfiles: []machineCapacity{{Cores: 2, MemoryMiB: 4096, DiskGiB: 40}}, SSHUser: "ubuntu"}
	issue := validateTopologyValues(spec)
	if issue == nil || issue.Path != "machineProfiles" {
		t.Fatalf("expected machine profile cardinality failure, got %#v", issue)
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
		if state.machines[id].disk != "local-lvm:vm-disk,size=32G" {
			t.Fatalf("VM %d disk was not resized: %q", id, state.machines[id].disk)
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

func TestTopologyRejectsAnUnreachableGatewayBeforeSSH(t *testing.T) {
	server, state := newTopologyServer(t, 0)
	defer server.Close()
	state.guestExecExitCode = 3
	invocation, _ := topologyInvocation(server.URL, false)
	plugin := TopologyPlugin{SSH: fakeSSHAccess{}}
	plan, err := plugin.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	_, err = plugin.Execute(context.Background(), plan.Steps[0], invocation.Secrets, invocation.Connections, func(string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "gateway 10.10.0.1 is unreachable") {
		t.Fatalf("expected gateway rejection, got %v", err)
	}
	state.lock.Lock()
	defer state.lock.Unlock()
	for id := 9000; id <= 9002; id++ {
		if state.machines[id].exists {
			t.Fatalf("VM %d was not rolled back after gateway rejection", id)
		}
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
	addresses, err := topologyAddresses("10.10.0.253", 24, "10.10.0.1", "1.1.1.1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(addresses, ",") != "10.10.0.253,10.10.0.254" {
		t.Fatalf("unexpected addresses %#v", addresses)
	}
	if _, err := topologyAddresses("10.10.0.253", 24, "10.10.0.1", "1.1.1.1", 3); err == nil {
		t.Fatal("expected broadcast boundary rejection")
	}
	if _, err := topologyAddresses("10.10.0.10", 24, "10.11.0.1", "1.1.1.1", 1); err == nil {
		t.Fatal("expected gateway subnet rejection")
	}
}

func TestTopologyRootDiskAndCapacity(t *testing.T) {
	configuration := vmConfig{Boot: "order=virtio0;ide2", SCSI0: "store:unused,size=8G", VirtIO0: "store:root,size=32768M"}
	disk, err := topologyRootDisk(configuration)
	if err != nil || disk != "virtio0" {
		t.Fatalf("unexpected root disk %q: %v", disk, err)
	}
	size, err := topologyDiskSizeGiB(diskConfiguration(configuration, disk))
	if err != nil || size != 32 {
		t.Fatalf("unexpected disk size %v: %v", size, err)
	}
	if _, err := topologyRootDisk(vmConfig{}); err == nil {
		t.Fatal("expected missing root disk rejection")
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
	lock              sync.Mutex
	machines          map[int]*topologyServerMachine
	writes            []string
	failVMID          int
	guestExecExitCode int
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
	disk        string
}

type fakeSSHAccess struct{}

func (fakeSSHAccess) Wait(context.Context, string, string, string, time.Duration) error {
	return nil
}

type initialFailureSSHAccess struct {
	lock  sync.Mutex
	calls int
}

func (access *initialFailureSSHAccess) Wait(context.Context, string, string, string, time.Duration) error {
	access.lock.Lock()
	defer access.lock.Unlock()
	access.calls++
	if access.calls <= 3 {
		return fmt.Errorf("initial SSH probe failed")
	}
	return nil
}

func TestTopologyCollectsGuestDiagnosticsBeforeRetryingSSH(t *testing.T) {
	server, _ := newTopologyServer(t, 0)
	defer server.Close()
	invocation, _ := topologyInvocation(server.URL, false)
	access := &initialFailureSSHAccess{}
	plugin := TopologyPlugin{SSH: access}
	plan, err := plugin.Plan(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	var logs []string
	_, err = plugin.Execute(context.Background(), plan.Steps[0], invocation.Secrets, invocation.Connections, func(_ string, message string) error {
		logs = append(logs, message)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(logs, "\n")
	if !strings.Contains(joined, "collecting guest diagnostics") || !strings.Contains(joined, "Guest diagnostics for VM 9000") || !strings.Contains(joined, "ssh-services:") {
		t.Fatalf("diagnostics were not logged: %s", joined)
	}
	access.lock.Lock()
	defer access.lock.Unlock()
	if access.calls != 6 {
		t.Fatalf("expected initial and final SSH probes for three machines, got %d", access.calls)
	}
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
			_, _ = response.Write([]byte(`{"data":{"/":{"Datastore.AllocateSpace":1,"VM.Allocate":1,"VM.Clone":1,"VM.Config.CPU":1,"VM.Config.Disk":1,"VM.Config.Memory":1,"VM.Config.Options":1,"VM.GuestAgent.Unrestricted":1,"VM.PowerMgmt":1}}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/api2/json/nodes/pve/qemu/8000/config":
			_, _ = response.Write([]byte(`{"data":{"name":"template","boot":"order=scsi0;ide2","scsi0":"local-lvm:base-8000-disk-0,size=8G"}}`))
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
			machine.disk = "local-lvm:vm-disk,size=8G"
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
		case request.Method == http.MethodPut && strings.HasSuffix(request.URL.Path, "/resize"):
			id := topologyRequestVMID(request.URL.Path)
			_ = request.ParseForm()
			if request.Form.Get("disk") != "scsi0" {
				http.Error(response, "unexpected disk", http.StatusBadRequest)
				return
			}
			state.machines[id].disk = "local-lvm:vm-disk,size=" + request.Form.Get("size")
			_, _ = response.Write([]byte(`{"data":"UPID:pve:resize"}`))
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
			value, _ := json.Marshal(map[string]any{"data": map[string]string{"name": machine.name, "tags": machine.tags, "description": machine.description, "ciuser": machine.ciUser, "sshkeys": machine.sshKeys, "ipconfig0": machine.ipConfig0, "nameserver": machine.nameServer, "boot": "order=scsi0;ide2", "scsi0": machine.disk}})
			_, _ = response.Write(value)
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/agent/network-get-interfaces"):
			id := topologyRequestVMID(request.URL.Path)
			address := fmt.Sprintf("10.10.0.%d", id-8990)
			_, _ = response.Write([]byte(fmt.Sprintf(`{"data":{"result":[{"name":"eth0","ip-addresses":[{"ip-address":%q,"ip-address-type":"ipv4"}]}]}}`, address)))
		case request.Method == http.MethodPost && strings.HasSuffix(request.URL.Path, "/agent/exec"):
			_ = request.ParseForm()
			command := request.Form["command"]
			if len(command) < 3 || command[0] != "/bin/sh" || command[1] != "-c" {
				http.Error(response, "unexpected guest command", http.StatusBadRequest)
				return
			}
			_, _ = response.Write([]byte(`{"data":{"pid":42}}`))
		case request.Method == http.MethodGet && strings.HasSuffix(request.URL.Path, "/agent/exec-status"):
			if request.URL.Query().Get("pid") != "42" {
				http.Error(response, "unexpected process identifier", http.StatusBadRequest)
				return
			}
			output := "device=eth0 neighbor=10.10.0.1 dev eth0 lladdr 00:11:22:33:44:55 REACHABLE\nssh-services:\nactive"
			if state.guestExecExitCode == 0 {
				_, _ = response.Write([]byte(fmt.Sprintf(`{"data":{"exited":1,"exitcode":0,"out-data":%q}}`, output)))
			} else {
				_, _ = response.Write([]byte(fmt.Sprintf(`{"data":{"exited":1,"exitcode":%d,"out-data":%q}}`, state.guestExecExitCode, output)))
			}
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
	spec := json.RawMessage(fmt.Sprintf(`{"connectionRef":"conn_test","node":"pve","templateVMID":8000,"baseVMID":9000,"addressStart":"10.10.0.10","prefixLength":24,"gateway":"10.10.0.1","dnsServer":"1.1.1.1","namePrefix":"dev","machineCount":3,"cores":2,"memoryMiB":4096,"diskGiB":32,"sshUser":"ubuntu","cleanupAfterTest":%t}`, cleanup))
	return Invocation{
		Input:       spec,
		Secrets:     map[string]json.RawMessage{"cred_test": json.RawMessage(`{"tokenId":"test@pam!kubephos","tokenSecret":"top-secret"}`)},
		Connections: map[string]json.RawMessage{"conn_test": json.RawMessage(fmt.Sprintf(`{"endpoint":%q,"credentialRef":"cred_test","verifyTLS":false}`, endpoint))},
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
