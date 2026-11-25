package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/plugins"
)

type vmTestPlugin struct{ manifestOnlyPlugin }

func (p vmTestPlugin) Plan(_ context.Context, spec json.RawMessage) (domain.Plan, error) {
	return domain.Plan{PluginID: p.manifest.ID, Steps: []domain.PlanStep{{ID: "clone", Name: "Clone VM", Input: spec, Outputs: []domain.ArtifactOutput{{Name: "machine-set", Type: "MachineSet", Version: "v1alpha1", MediaType: "application/json", Source: "/machineSet"}}}}}, nil
}
func (p vmTestPlugin) Precheck(context.Context, domain.PlanStep, plugins.Logger) (domain.HealthReport, error) {
	return domain.HealthReport{Status: domain.HealthHealthy}, nil
}

func TestCreateStandaloneVMQueuesOneMachineAndReservesAllocations(t *testing.T) {
	server, pool := transferServer(t, "")
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	proxmox := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api2/json/nodes" {
			fmt.Fprint(w, `{"data":[{"node":"pve-1","status":"online"},{"node":"pve-2","status":"online"}]}`)
			return
		}
		if r.URL.Path != "/api2/json/cluster/resources" {
			t.Errorf("unexpected Proxmox call %s", r.URL.Path)
		}
		fmt.Fprint(w, `{"data":[]}`)
	}))
	defer proxmox.Close()
	nonce, ciphertext, fingerprint, err := server.vault.Encrypt([]byte(`{"tokenId":"test@pve!test","tokenSecret":"test-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO credentials(id,name,kind,nonce,ciphertext,fingerprint) VALUES('cred','test','proxmox-api-token',$1,$2,$3)`, nonce, ciphertext, fingerprint)
	config, _ := json.Marshal(map[string]any{"endpoint": proxmox.URL, "credentialRef": "cred", "verifyTLS": false})
	exec(`INSERT INTO provider_connections(id,name,provider,plugin_id,configuration) VALUES('conn','Proxmox','proxmox','test',$1)`, config)
	exec(`INSERT INTO operations(id,workspace_id,plugin_id,title,status,spec,plan,validation,plan_hash) VALUES('template-op','ws_default','test','Template','succeeded','{}','{}','{}',repeat('a',64))`)
	exec(`INSERT INTO artifacts(id,operation_id,name,output_name,artifact_type,artifact_version,media_type,storage_key,digest,size_bytes,storage_digest,stored_size_bytes,verified_at) VALUES('template-art','template-op','template','machine-template','MachineTemplate','v1alpha1','application/json','template',repeat('a',64),1,repeat('a',64),1,now())`)
	exec(`INSERT INTO managed_resources(id,workspace_id,name,kind,provider,connection_id,plugin_id,plugin_version,spec,operation_id) VALUES('template','ws_default','ubuntu','machine-template','proxmox','conn','test','1','{"node":"pve-1","vmid":9000,"diskGiB":40,"sshUser":"ubuntu"}','template-op')`)
	server.registry = plugins.NewRegistry(vmTestPlugin{manifestOnlyPlugin{plugins.Manifest{ID: "topology", Version: "1", Provider: "proxmox", Capabilities: []string{"infrastructure.machine-topology.provision"}, Schema: json.RawMessage(`{"type":"object"}`), ArtifactOutputs: []domain.ArtifactContract{{Type: "MachineSet", Version: "v1alpha1"}}}}})
	body := `{"workspaceId":"ws_default","connectionId":"conn","templateId":"template","kind":"vm","name":"vm-01","node":"pve-2","vmid":201,"address":"192.0.2.20","prefixLength":24,"gateway":"192.0.2.1","dnsServer":"1.1.1.1","cores":2,"memoryMiB":2048,"diskGiB":64}`
	call := func(raw string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/infrastructure-services", strings.NewReader(raw))
		w := httptest.NewRecorder()
		server.createInfrastructureService(w, r)
		return w
	}
	w := call(body)
	if w.Code != http.StatusAccepted {
		t.Fatal(w.Code, w.Body.String())
	}
	var vm domain.ManagedResource
	if err := json.Unmarshal(w.Body.Bytes(), &vm); err != nil {
		t.Fatal(err)
	}
	if vm.Kind != "vm" || vm.Status != "provisioning" {
		t.Fatalf("unexpected VM %+v", vm)
	}
	pipeline, err := server.store.GetPipeline(ctx, vm.PipelineID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pipeline.Definition.Stages) != 1 || pipeline.Definition.Result.Output != "machine-set" {
		t.Fatalf("unexpected workflow %+v", pipeline.Definition)
	}
	var spec map[string]any
	_ = json.Unmarshal(pipeline.Definition.Stages[0].Spec, &spec)
	if spec["machineCount"] != float64(1) || spec["baseVMID"] != float64(201) || spec["diskGiB"] != float64(64) || spec["machineNodes"].([]any)[0] != "pve-2" {
		t.Fatalf("lost VM settings: %+v", spec)
	}
	if duplicate := call(strings.Replace(body, "vm-01", "vm-02", 1)); duplicate.Code < 400 {
		t.Fatal("duplicate VMID or address accepted")
	}
	if small := call(strings.Replace(body, `"diskGiB":64`, `"diskGiB":16`, 1)); small.Code != 422 {
		t.Fatal("disk smaller than template accepted", small.Code)
	}
}

func TestStandaloneVMHasOnlyTheManagedMachineLifecycle(t *testing.T) {
	spec := json.RawMessage(`{"machineCount":1,"machineNames":["vm-01"],"machineNodes":["pve-2"]}`)
	vm := infrastructureMachineDefinition("vm", "vm-01", "topology", "unused", spec)
	if len(vm.Stages) != 1 || vm.Stages[0].PluginID != "topology" || vm.Result.Stage != "machine" || vm.Result.Output != "machine-set" || vm.CleanupAfterRun {
		t.Fatalf("invalid VM lifecycle: %+v", vm)
	}
	if string(vm.Stages[0].Spec) != string(spec) {
		t.Fatal("VM placement or configuration changed")
	}
	for kind, output := range map[string]string{"harbor": "registry-endpoint", "nfs": "shared-storage-endpoint"} {
		service := infrastructureMachineDefinition(kind, "test", "topology", kind, spec)
		if len(service.Stages) != 2 || service.Result.Stage != "service" || service.Result.Output != output || len(service.Stages[1].Bindings) != 2 {
			t.Fatalf("service regression: %+v", service)
		}
	}
}

func TestStandaloneVMAccessIsAdministratorOnlyAndAudited(t *testing.T) {
	path := "/api/v1/infrastructure-services/vm-01/ssh-key"
	if !adminReadPath(path) || userMutationAllowed("/api/v1/infrastructure-services") {
		t.Fatal("VM access policy is not administrator-only")
	}
	action, kind, id, ok := auditTarget(httptest.NewRequest("GET", path, nil))
	if !ok || action != "vm.ssh-key.download" || kind != "infrastructure-services" || id != "vm-01" {
		t.Fatal("VM key access is not audited")
	}
	if !infrastructureServiceKind("vm") || infrastructureServiceKind("kubernetes-cluster") {
		t.Fatal("invalid VM resource kind routing")
	}
}
