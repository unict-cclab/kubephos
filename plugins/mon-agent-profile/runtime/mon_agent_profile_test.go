package monagentprofile

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"kubephos.dev/kubephos/internal/domain"
)

func TestMonAgentProfileLifecycle(t *testing.T) {
	runner := &fakeRunner{period: "30", queryRange: "5m", annotations: map[string]string{}}
	plugin := Plugin{Runner: runner}
	raw := json.RawMessage(`{"clusterConnectionRef":"art_cluster","applicationDeploymentRef":"art_application","observabilityRef":"art_observability","scrapePeriodSeconds":10,"promQLRange":"1m"}`)
	if report := plugin.Validate(context.Background(), Invocation{Input: raw}); !report.Valid {
		t.Fatalf("unexpected validation report: %#v", report)
	}
	plan, err := plugin.Plan(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	step := plan.Steps[0]
	step.ResolvedInputs = profileArtifacts()
	if health, err := plugin.Precheck(context.Background(), step, discardLog); err != nil || health.Status != domain.HealthHealthy {
		t.Fatalf("precheck failed: %#v %v", health, err)
	}
	value, err := plugin.Execute(context.Background(), step, discardLog)
	if err != nil {
		t.Fatal(err)
	}
	if health, err := plugin.Verify(context.Background(), step, value, discardLog); err != nil || health.Status != domain.HealthHealthy || runner.period != "10" || runner.queryRange != "1m" {
		t.Fatalf("verify failed: %#v %v", health, err)
	}
	step.Cleanup = true
	if err := plugin.Cleanup(context.Background(), step, value, discardLog); err != nil {
		t.Fatal(err)
	}
	if health, err := plugin.Verify(context.Background(), step, nil, discardLog); err != nil || health.Status != domain.HealthHealthy || runner.period != "30" || runner.queryRange != "5m" {
		t.Fatalf("cleanup verify failed: %#v %v", health, err)
	}
}

func TestMonAgentProfileValidationRejectsUnsafeValues(t *testing.T) {
	for _, raw := range []json.RawMessage{json.RawMessage(`{}`), json.RawMessage(`{"clusterConnectionRef":"art_cluster","applicationDeploymentRef":"art_application","observabilityRef":"art_observability","scrapePeriodSeconds":1,"promQLRange":"5m"}`), json.RawMessage(`{"clusterConnectionRef":"art_cluster","applicationDeploymentRef":"art_application","observabilityRef":"art_observability","scrapePeriodSeconds":30,"promQLRange":"forever"}`)} {
		if (Plugin{}).Validate(context.Background(), Invocation{Input: raw}).Valid {
			t.Fatalf("expected invalid configuration: %s", raw)
		}
	}
}

func profileArtifacts() map[string]domain.ResolvedArtifact {
	cluster := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"ClusterConnection","metadata":{"name":"cluster","version":"v1"},"spec":{"server":"https://10.0.0.1:6443","kubeconfig":"config"}}`)
	application := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"ApplicationDeployment","metadata":{"name":"app","version":"v1alpha1","ownershipMarker":"app-marker"},"spec":{"applicationRef":"app:test@1.0.0","clusterServer":"https://10.0.0.1:6443","namespace":"app"}}`)
	observability := json.RawMessage(`{"apiVersion":"artifacts.kubephos.dev/v1alpha1","kind":"ObservabilityCapability","spec":{"clusterServer":"https://10.0.0.1:6443","namespace":"kubephos-observability","monAgent":{"name":"kubephos-mon-agent","version":"v0.0.7","namespace":"kubephos-observability","scrapePeriodSeconds":30,"promQLRange":"5m","status":"ready"}}}`)
	return map[string]domain.ResolvedArtifact{"cluster-connection": {Value: cluster}, "application-deployment": {Value: application}, "observability": {Value: observability}}
}

func discardLog(string, string) error { return nil }

type fakeRunner struct {
	period      string
	queryRange  string
	annotations map[string]string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, _ []byte, args ...string) (string, error) {
	command := strings.Join(args, " ")
	if command == "get --raw=/readyz" || strings.HasPrefix(command, "rollout status ") {
		return "ready", nil
	}
	if strings.HasPrefix(command, "get deployment/") {
		state := deploymentState{}
		state.Metadata.Annotations = runner.annotations
		state.Spec.Template.Spec.Containers = append(state.Spec.Template.Spec.Containers, struct {
			Name string `json:"name"`
			Env  []struct {
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"env"`
		}{Name: "kubephos-mon-agent", Env: []struct {
			Name  string `json:"name"`
			Value string `json:"value"`
		}{{Name: "SCRAPE_PERIOD_SECONDS", Value: runner.period}, {Name: "PROMQL_RANGE", Value: runner.queryRange}}})
		value, _ := json.Marshal(state)
		return string(value), nil
	}
	if strings.HasPrefix(command, "set env ") {
		for _, argument := range args {
			if strings.HasPrefix(argument, "SCRAPE_PERIOD_SECONDS=") {
				runner.period = strings.TrimPrefix(argument, "SCRAPE_PERIOD_SECONDS=")
			}
			if strings.HasPrefix(argument, "PROMQL_RANGE=") {
				runner.queryRange = strings.TrimPrefix(argument, "PROMQL_RANGE=")
			}
		}
		return "configured", nil
	}
	if strings.HasPrefix(command, "annotate ") {
		for _, argument := range args {
			if strings.HasSuffix(argument, "-") && !strings.Contains(argument, "=") {
				delete(runner.annotations, strings.TrimSuffix(argument, "-"))
				continue
			}
			parts := strings.SplitN(argument, "=", 2)
			if len(parts) == 2 && strings.HasPrefix(parts[0], "kubephos.dev/") {
				runner.annotations[parts[0]] = parts[1]
			}
		}
		return "annotated", nil
	}
	return "", errors.New("unexpected command: " + command)
}
