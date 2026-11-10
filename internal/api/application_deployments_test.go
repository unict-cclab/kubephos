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
	"kubephos.dev/kubephos/internal/placement"
	"kubephos.dev/kubephos/internal/plugins"
	applicationdeployer "kubephos.dev/kubephos/plugins/application-deployer/runtime"
	managedplatform "kubephos.dev/kubephos/plugins/managed-platform/runtime"
)

func TestStandaloneDeploymentTargetsEveryMemberWithoutExperimentComponents(t *testing.T) {
	ctx := context.Background()
	server, pool := transferServer(t, "")
	exec := func(query string, args ...any) {
		if _, err := pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO catalog_applications(id,name,description,origin,descriptor,digest) VALUES('test.application','Test','','built-in','{"spec":{"materializer":"test-materializer","valuesSchema":{"type":"object"}}}',repeat('a',64))`)
	exec(`INSERT INTO pipelines(id,workspace_id,name,definition,resolution,validation,pipeline_hash) VALUES('pipeline','ws_default','Cluster','{}','{}','{}',repeat('a',64));
 INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('run','pipeline','ws_default','Cluster','succeeded',repeat('a',64),'','')`)
	for i := 1; i <= 3; i++ {
		op := fmt.Sprintf("op_%d", i)
		stage := fmt.Sprintf("c%d-kubernetes", i)
		exec(`INSERT INTO operations(id,workspace_id,plugin_id,plugin_version,title,status,spec,plan,validation,plan_hash) VALUES($1,'ws_default','test','1','Kubernetes','succeeded','{}','{}','{}',repeat('a',64))`, op)
		exec(`INSERT INTO pipeline_run_stages(id,run_id,position,stage_id,plugin_id,title,status,operation_id) VALUES($1,'run',$2,$3,'test','Kubernetes','succeeded',$4)`, stage, i, stage, op)
		exec(`INSERT INTO artifacts(id,operation_id,name,output_name,artifact_type,artifact_version,media_type,storage_key,digest,size_bytes,storage_digest,stored_size_bytes,verified_at) VALUES($1,$2,'cluster-connection','cluster-connection','ClusterConnection','v1alpha1','application/json',$1,repeat('a',64),1,repeat('a',64),1,now())`, fmt.Sprintf("art_%d", i), op)
	}
	outputs := []domain.ArtifactContract{}
	for _, kind := range []string{"ManifestSet", "WorkloadTargets", "ServiceEndpoints"} {
		outputs = append(outputs, domain.ArtifactContract{Type: kind, Version: "v1alpha1"})
	}
	server.registry = plugins.NewRegistry(manifestOnlyPlugin{plugins.Manifest{ID: "test-materializer", Version: "1", Capabilities: []string{"applications.materialize"}, ArtifactOutputs: outputs}}, manifestOnlyPlugin{(applicationdeployer.Plugin{}).Manifest()})
	injection := true
	input := applicationDeploymentInput{ApplicationRef: "app:test.application", Definition: experimentConfigurationDefinitionInput{Namespace: "my-app", ApplicationValues: json.RawMessage(`{"replicas":3}`), IstioInjection: &injection, ApplicationPlacement: &placement.ApplicationSettings{NodeSelector: map[string]string{"hardware": "gpu"}, Tolerations: []placement.Toleration{{Key: "gpu", Operator: "Exists"}}, ServiceOverrides: map[string]placement.ServiceOverride{"db": {NodeSelectorMode: "none"}}}}}
	cluster := domain.ManagedResource{Name: "Multi", PipelineRunID: "run", Spec: json.RawMessage(`{"mode":"multi","members":[{"name":"one"},{"name":"two"},{"name":"three"}]}`)}
	def, failure := server.applicationDeploymentPipeline(ctx, input, cluster)
	if failure != nil {
		t.Fatal(failure)
	}
	if len(def.Stages) != 4 || def.CleanupAfterRun {
		t.Fatalf("unexpected persistent pipeline: %+v", def)
	}
	for i, stage := range def.Stages[1:] {
		var spec map[string]any
		_ = json.Unmarshal(stage.Spec, &spec)
		if spec["applicationNamespace"] != "my-app" || spec["namespacePolicy"] != "create" || spec["istioInjection"] != false || spec["clusterConnectionRef"] != fmt.Sprintf("art_%d", i+1) {
			t.Fatalf("member target: %v", spec)
		}
		if spec["applicationPlacement"] == nil {
			t.Fatal("member placement missing")
		}
		if len(stage.Bindings) != 3 {
			t.Fatal("application artifact bindings missing", stage.Bindings)
		}
	}
	if def.Result.Stage != "deploy-3" || def.Result.Type != "ApplicationDeployment" {
		t.Fatal("result not persisted", def.Result)
	}
	cluster.Spec = json.RawMessage(`{}`)
	def, failure = server.applicationDeploymentPipeline(ctx, input, cluster)
	if failure != nil || len(def.Stages) != 2 {
		t.Fatal("single cluster path changed", failure, def)
	}
	var single map[string]any
	_ = json.Unmarshal(def.Stages[1].Spec, &single)
	if single["istioInjection"] != true {
		t.Fatal("single cluster injection setting lost")
	}

	// Ambient mode is installed on the cluster before the deployment, which enrolls the
	// namespace and sizes its waypoint; multi-cluster deployments run Cilium instead.
	server.registry = plugins.NewRegistry(manifestOnlyPlugin{plugins.Manifest{ID: "test-materializer", Version: "1", Capabilities: []string{"applications.materialize"}, ArtifactOutputs: outputs}}, manifestOnlyPlugin{(applicationdeployer.Plugin{}).Manifest()}, manifestOnlyPlugin{managedplatform.AmbientPlugin{}.Manifest()})
	ambient := input
	ambient.Definition.IstioInjection = nil
	ambient.Definition.IstioDataPlane = "ambient"
	ambient.Definition.IstioWaypoint = map[string]any{"replicas": 2.0, "cpuRequest": "250m"}
	def, failure = server.applicationDeploymentPipeline(ctx, ambient, cluster)
	if failure != nil || len(def.Stages) != 3 || def.Stages[1].ID != "istio-ambient" || def.Stages[2].ID != "deploy-1" {
		t.Fatal("ambient deployment does not install ambient mode before deploying", failure, def)
	}
	var deploy map[string]any
	_ = json.Unmarshal(def.Stages[2].Spec, &deploy)
	waypoint, _ := deploy["istioWaypoint"].(map[string]any)
	if deploy["istioDataPlane"] != "ambient" || waypoint["replicas"] != 2.0 || waypoint["cpuRequest"] != "250m" {
		t.Fatalf("ambient settings not passed to the deployer: %v", deploy)
	}
	cluster.Spec = json.RawMessage(`{"mode":"multi","members":[{"name":"one"},{"name":"two"},{"name":"three"}]}`)
	if _, failure = server.applicationDeploymentPipeline(ctx, ambient, cluster); failure == nil || failure.code != "invalid_istio_data_plane" {
		t.Fatal("ambient mode accepted on a multi-cluster deployment", failure)
	}
}

func TestStandaloneDeploymentInputAndOwnership(t *testing.T) {
	server, pool := transferServer(t, "")
	ctx := context.Background()
	for _, body := range []string{
		`{"name":"app","definition":{"namespace":"default"}}`,
		`{"name":"app","definition":{"namespace":""}}`,
		`{"name":"app","definition":{"namespace":"app","twoPhaseStart":true}}`,
		`{"name":"app","definition":{"namespace":"app","components":[{"capability":"load.session.control"}]}}`,
	} {
		w := httptest.NewRecorder()
		server.createApplicationDeployment(w, httptest.NewRequest(http.MethodPost, "/api/v1/application-deployments", strings.NewReader(body)))
		if w.Code != 422 {
			t.Fatalf("invalid settings status %d: %s", w.Code, w.Body.String())
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO pipelines(id,workspace_id,name,definition,resolution,validation,pipeline_hash) VALUES('p','ws_default','Deployment','{}','{}','{}',repeat('a',64));
 INSERT INTO pipeline_runs(id,pipeline_id,workspace_id,name,status,pipeline_hash,result_type,result_version) VALUES('r','p','ws_default','Deployment','succeeded',repeat('a',64),'','');
 INSERT INTO managed_resources(id,workspace_id,name,kind,provider,plugin_id,plugin_version,spec,pipeline_id,pipeline_run_id) VALUES('d','ws_default','Private','application-deployment','kubernetes','test','1','{"ownerUserId":"owner","clusterResourceId":"cluster","definition":{"namespace":"app"}}','p','r')`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		id, role string
		visible  bool
	}{{"owner", "user", true}, {"other", "user", false}, {"admin", "admin", true}} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/application-deployments", nil).WithContext(context.WithValue(ctx, authenticatedUserKey{}, domain.User{ID: tc.id, Role: tc.role}))
		w := httptest.NewRecorder()
		server.listApplicationDeployments(w, req)
		if w.Code != 200 || strings.Contains(w.Body.String(), "Private") != tc.visible {
			t.Fatalf("visibility %s: %s", tc.id, w.Body.String())
		}
	}
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/application-deployments/d", nil)
	req.SetPathValue("id", "d")
	req = req.WithContext(context.WithValue(ctx, authenticatedUserKey{}, domain.User{ID: "other", Role: "user"}))
	w := httptest.NewRecorder()
	server.deleteApplicationDeployment(w, req)
	if w.Code != 404 {
		t.Fatal("viewer could delete deployment", w.Code)
	}
	for _, endpoint := range []func(http.ResponseWriter, *http.Request){server.updateApplicationDeploymentPlacement, server.retryApplicationDeployment, server.getDeploymentPlacementDefaults} {
		response := httptest.NewRecorder()
		endpoint(response, req)
		if response.Code != 404 {
			t.Fatalf("another user accessed deployment settings: %d", response.Code)
		}
	}
}
