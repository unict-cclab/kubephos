package secondaryscheduler

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestSecondarySchedulerLifecycle(t *testing.T) {
	runner := &fakeRunner{}
	plugin := Plugin{Runner: runner}
	spec := json.RawMessage(`{"clusterConnectionRef":"art_cluster","applicationDeploymentRef":"art_application","targetBindingRef":"art_binding"}`)
	report := plugin.Validate(context.Background(), Invocation{Input: spec})
	if !report.Valid {
		t.Fatalf("unexpected validation failure %#v", report.Issues)
	}
	plan, err := plugin.Plan(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Steps) != 1 || len(plan.Steps[0].ArtifactInputs) != 3 || len(plan.Steps[0].Outputs) != 1 || plan.Steps[0].Outputs[0].Type != "SchedulerDeployment" {
		t.Fatalf("unexpected plan %#v", plan)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	health, err = plugin.Verify(context.Background(), step, value, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("verify failed %#v %v", health, err)
	}
	if runner.schedulerName == "" || runner.schedulerName != runner.workloadScheduler || runner.workloadOwner == "" {
		t.Fatalf("scheduler binding was not applied %#v", runner)
	}
	var decoded result
	if err := json.Unmarshal(value, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.SchedulerDeployment.Spec.Image != schedulerImage || decoded.SchedulerDeployment.Spec.Targets[0].ID != "frontend" {
		t.Fatalf("unexpected result %#v", decoded)
	}
	step.Cleanup = true
	health, err = plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup precheck failed %#v %v", health, err)
	}
	if err := plugin.Cleanup(context.Background(), step, value, discardLog); err != nil {
		t.Fatal(err)
	}
	if err := plugin.Cleanup(context.Background(), step, value, discardLog); err != nil {
		t.Fatalf("cleanup must be idempotent: %v", err)
	}
	health, err = plugin.Verify(context.Background(), step, nil, discardLog)
	if err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("cleanup verify failed %#v %v", health, err)
	}
	if runner.workloadScheduler != "" || runner.workloadOwner != "" || runner.installed {
		t.Fatalf("scheduler cleanup did not restore the workload %#v", runner)
	}
}

func TestPrecheckRejectsTargetFromAnotherApplication(t *testing.T) {
	plugin := Plugin{Runner: &fakeRunner{}}
	step := plannedStep(t)
	var binding targetBinding
	if err := json.Unmarshal(step.ResolvedInputs["target-binding"].Value, &binding); err != nil {
		t.Fatal(err)
	}
	binding.Spec.Targets[0].Name = "other"
	value, _ := json.Marshal(binding)
	artifact := step.ResolvedInputs["target-binding"]
	artifact.Value = value
	step.ResolvedInputs["target-binding"] = artifact
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy || health.Checks["artifacts"] != "invalid" {
		t.Fatalf("expected mismatched binding rejection %#v %v", health, err)
	}
}

func TestPrecheckRejectsExistingSchedulerBinding(t *testing.T) {
	plugin := Plugin{Runner: &fakeRunner{workloadScheduler: "external-scheduler"}}
	step := plannedStep(t)
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy || health.Checks["target"] != "frontend" {
		t.Fatalf("expected existing scheduler rejection %#v %v", health, err)
	}
}

func TestCleanupRejectsForeignOwnership(t *testing.T) {
	runner := &fakeRunner{installed: true, marker: "foreign", schedulerName: "kubephos-foreign"}
	plugin := Plugin{Runner: runner}
	step := plannedStep(t)
	step.Cleanup = true
	health, err := plugin.Precheck(context.Background(), step, discardLog)
	if err != nil || health.Status != domain.HealthUnhealthy || health.Checks["ownership"] != "blocked" {
		t.Fatalf("expected cleanup ownership rejection %#v %v", health, err)
	}
}

func TestValidationRejectsInvalidReferences(t *testing.T) {
	values := []json.RawMessage{
		json.RawMessage(`{}`),
		json.RawMessage(`{"clusterConnectionRef":"art_same","applicationDeploymentRef":"art_same","targetBindingRef":"art_binding"}`),
	}
	for _, value := range values {
		if (Plugin{}).Validate(context.Background(), Invocation{Input: value}).Valid {
			t.Fatalf("expected validation rejection for %s", value)
		}
	}
}

func TestSchedulerManifestUsesManagedTaggedImage(t *testing.T) {
	value, err := schedulerManifest(stepInput{Marker: "marker", Namespace: "kubephos-test", SchedulerName: "kubephos-test"})
	if err != nil {
		t.Fatal(err)
	}
	text := string(value)
	if !strings.Contains(text, schedulerImage) || strings.Contains(text, ":latest") || !strings.Contains(text, "system:kube-scheduler") || !strings.Contains(text, "system:volume-scheduler") || !strings.Contains(text, "/usr/local/bin/kube-scheduler") {
		t.Fatalf("unexpected scheduler manifest %s", text)
	}
	foundation, components, err := partitionSchedulerManifest(value)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(foundation), `"kind":"Deployment"`) || !strings.Contains(string(components), `"kind":"Deployment"`) || strings.Contains(string(components), `"kind":"Namespace"`) {
		t.Fatalf("unexpected scheduler manifest partition")
	}
}

func plannedStep(t *testing.T) domain.PlanStep {
	t.Helper()
	plugin := Plugin{}
	plan, err := plugin.Plan(context.Background(), json.RawMessage(`{"clusterConnectionRef":"art_cluster","applicationDeploymentRef":"art_application","targetBindingRef":"art_binding"}`))
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = testArtifacts()
	return step
}

func testArtifacts() map[string]domain.ResolvedArtifact {
	certificate := base64.StdEncoding.EncodeToString([]byte("certificate"))
	key := base64.StdEncoding.EncodeToString([]byte("private-key"))
	kubeconfig := "apiVersion: v1\nkind: Config\ncurrent-context: managed\nclusters:\n- name: managed\n  cluster:\n    server: https://10.10.0.10:6443\ncontexts:\n- name: managed\n  context:\n    cluster: managed\n    user: admin\nusers:\n- name: admin\n  user:\n    client-certificate-data: " + certificate + "\n    client-key-data: " + key + "\n"
	cluster, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ClusterConnection", "metadata": map[string]string{"name": "development", "version": "v1.36.0+k3s1"}, "spec": map[string]string{"distribution": "k3s", "server": "https://10.10.0.10:6443", "kubeconfig": kubeconfig}})
	target := workloadTarget{ID: "frontend", APIVersion: "apps/v1", Kind: "Deployment", Name: "frontend", Selector: map[string]string{"app": "frontend"}, Traits: []string{"scalable", "schedulable"}}
	application, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "ApplicationDeployment", "metadata": map[string]string{"name": "kubephos-app-one", "version": "v1alpha1", "ownershipMarker": "application-marker"}, "spec": map[string]any{"applicationRef": "app:dev.example.app@1.0.0", "clusterServer": "https://10.10.0.10:6443", "namespace": "kubephos-app-one", "manifestDigest": "sha256:" + strings.Repeat("a", 64), "workloads": []workloadTarget{target}}})
	targets, _ := json.Marshal([]workloadTarget{target})
	digest := sha256.Sum256(targets)
	binding, _ := json.Marshal(map[string]any{"apiVersion": artifactAPI, "kind": "TargetBinding", "metadata": map[string]string{"name": "workload-targets-schedulable", "version": "v1alpha1"}, "spec": map[string]any{"sourceArtifactId": "art_workloads", "sourceDigest": "sha256:" + hex.EncodeToString(digest[:]), "requiredTrait": "schedulable", "includeComponentIds": []string{"frontend"}, "excludeComponentIds": []string{}, "targets": []workloadTarget{target}}})
	return map[string]domain.ResolvedArtifact{
		"cluster-connection":     {ID: "art_cluster", Type: "ClusterConnection", Version: "v1alpha1", Value: cluster},
		"application-deployment": {ID: "art_application", Type: "ApplicationDeployment", Version: "v1alpha1", Value: application},
		"target-binding":         {ID: "art_binding", Type: "TargetBinding", Version: "v1alpha1", Value: binding},
	}
}

func discardLog(string, string) error { return nil }

type fakeRunner struct {
	installed         bool
	marker            string
	schedulerName     string
	workloadScheduler string
	workloadOwner     string
	previous          string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, stdin []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if command == "get --raw=/readyz" {
		return "ok", nil
	}
	if strings.HasPrefix(command, "auth can-i") {
		return "yes\n", nil
	}
	if strings.HasPrefix(command, "get namespace kubephos-app-one") && strings.Contains(command, "jsonpath=") {
		return "application-marker", nil
	}
	if strings.HasPrefix(command, "get deployment/frontend ") && strings.HasSuffix(command, "-o json") {
		return runner.workloadJSON(), nil
	}
	if strings.HasPrefix(command, "apply --dry-run=server") || strings.HasPrefix(command, "apply --dry-run=client") || strings.Contains(command, "patch deployment/frontend") && strings.Contains(command, "--dry-run=server") {
		return "accepted", nil
	}
	if strings.HasPrefix(command, "get namespace kubephos-") || strings.HasPrefix(command, "get clusterrolebinding kubephos-") || strings.HasPrefix(command, "get rolebinding kubephos-") {
		if !runner.installed {
			return "", nil
		}
		if strings.Contains(command, "jsonpath=") {
			return runner.marker, nil
		}
		return "present", nil
	}
	if command == "apply -f -" {
		runner.installed = true
		var namespace struct {
			Kind     string `json:"kind"`
			Metadata struct {
				Name        string            `json:"name"`
				Annotations map[string]string `json:"annotations"`
			} `json:"metadata"`
		}
		first := strings.Split(string(stdin), "\n---\n")[0]
		if err := json.Unmarshal([]byte(first), &namespace); err != nil {
			return "", err
		}
		if namespace.Kind == "Namespace" {
			runner.schedulerName = namespace.Metadata.Name
			runner.marker = namespace.Metadata.Annotations[ownershipKey]
		}
		return "created", nil
	}
	if strings.HasPrefix(command, "rollout status deployment/scheduler ") {
		if !runner.installed {
			return "", errors.New("scheduler not found")
		}
		return "ready", nil
	}
	if strings.HasPrefix(command, "get pods -n kubephos-") && strings.Contains(command, "app.kubernetes.io/instance=") {
		return `{"items":[{"metadata":{"name":"scheduler-123"},"status":{"conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"state":{}}]}}]}`, nil
	}
	if strings.HasPrefix(command, "patch deployment/frontend ") && !strings.Contains(command, "--dry-run=server") {
		var patch struct {
			Spec struct {
				Template struct {
					Metadata struct {
						Annotations map[string]*string `json:"annotations"`
					} `json:"metadata"`
					Spec struct {
						SchedulerName *string `json:"schedulerName"`
					} `json:"spec"`
				} `json:"template"`
			} `json:"spec"`
		}
		payload := args[len(args)-1]
		if err := json.Unmarshal([]byte(payload), &patch); err != nil {
			return "", err
		}
		if owner := patch.Spec.Template.Metadata.Annotations[ownershipKey]; owner != nil {
			runner.workloadOwner = *owner
		} else {
			runner.workloadOwner = ""
		}
		if previous := patch.Spec.Template.Metadata.Annotations[previousKey]; previous != nil {
			runner.previous = *previous
		} else {
			runner.previous = ""
		}
		if patch.Spec.Template.Spec.SchedulerName == nil {
			runner.workloadScheduler = ""
		} else {
			runner.workloadScheduler = *patch.Spec.Template.Spec.SchedulerName
		}
		return "patched", nil
	}
	if strings.HasPrefix(command, "rollout status deployment/frontend ") {
		return "ready", nil
	}
	if strings.HasPrefix(command, "get pods -n kubephos-app-one ") {
		value, _ := json.Marshal(map[string]any{"items": []map[string]any{{"spec": map[string]string{"schedulerName": runner.workloadScheduler, "nodeName": "worker-1"}, "status": map[string]any{"conditions": []map[string]string{{"type": "Ready", "status": "True"}}}}}})
		return string(value), nil
	}
	if strings.HasPrefix(command, "delete --ignore-not-found") {
		runner.installed = false
		return "deleted", nil
	}
	return "", errors.New("unexpected command: " + command)
}

func (runner *fakeRunner) workloadJSON() string {
	annotations := map[string]string{}
	if runner.workloadOwner != "" {
		annotations[ownershipKey] = runner.workloadOwner
	}
	if runner.previous != "" {
		annotations[previousKey] = runner.previous
	}
	value, _ := json.Marshal(map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": map[string]string{"name": "frontend"}, "spec": map[string]any{"template": map[string]any{"metadata": map[string]any{"labels": map[string]string{"app": "frontend"}, "annotations": annotations}, "spec": map[string]string{"schedulerName": runner.workloadScheduler}}}})
	return string(value)
}
