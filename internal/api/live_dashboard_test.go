package api

import (
	"testing"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

func TestGrafanaAccessPairsEndpointAndCredential(t *testing.T) {
	capability := []byte(`{"kind":"ObservabilityCapability","spec":{"endpoints":[{"name":"prometheus","serviceName":"prom","nodePort":32090},{"name":"grafana","serviceName":"kubephos-observability-grafana","nodePort":32000}]}}`)
	credential := []byte(`{"kind":"ServiceCredential","spec":{"service":"kubephos-observability-grafana","username":"admin","password":"secret"}}`)
	base, identity, err := grafanaAccess("192.0.2.10", capability, credential)
	if err != nil || base != "http://192.0.2.10:32000" || identity.Spec.Username != "admin" || identity.Spec.Password != "secret" {
		t.Fatalf("base=%q identity=%#v err=%v", base, identity, err)
	}
	for name, input := range map[string][3]string{
		"missing host":        {"", string(capability), string(credential)},
		"missing endpoint":    {"192.0.2.10", `{"spec":{"endpoints":[]}}`, string(credential)},
		"mismatched service":  {"192.0.2.10", string(capability), `{"spec":{"service":"other","username":"admin","password":"secret"}}`},
		"missing credentials": {"192.0.2.10", string(capability), `{"spec":{"service":"kubephos-observability-grafana"}}`},
	} {
		if _, _, err := grafanaAccess(input[0], []byte(input[1]), []byte(input[2])); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

func TestLiveDashboardPrefersTheCurrentRunThenTheLatestStarted(t *testing.T) {
	earlier, later := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC), time.Date(2026, 11, 16, 10, 0, 0, 0, time.UTC)
	experiment := domain.Experiment{Variants: []domain.ExperimentVariant{
		{Trials: []domain.ExperimentTrial{{ID: "done", Status: "succeeded", PipelineRunID: "run-1", StartedAt: &later}, {ID: "pending", Status: "queued"}}},
		{Trials: []domain.ExperimentTrial{{ID: "live", Status: "running", PipelineRunID: "run-2", StartedAt: &earlier}}},
	}}
	if trial, ok := liveDashboardTrial(experiment); !ok || trial.ID != "live" {
		t.Fatalf("trial = %#v", trial)
	}
	experiment.Variants[1].Trials[0].Status = "succeeded"
	if trial, ok := liveDashboardTrial(experiment); !ok || trial.ID != "done" {
		t.Fatalf("latest started trial = %#v", trial)
	}
	if _, ok := liveDashboardTrial(domain.Experiment{Variants: []domain.ExperimentVariant{{Trials: []domain.ExperimentTrial{{ID: "queued", Status: "queued"}}}}}); ok {
		t.Fatal("a run that has not started opened the dashboard")
	}
}
