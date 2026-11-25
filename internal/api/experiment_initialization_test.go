package api

import (
	"kubephos.dev/kubephos/internal/domain"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInitializationSettingsRemainOptionalAndAreFrozenPerRun(t *testing.T) {
	definition := experimentConfigurationDefinitionInput{Components: []experimentConfigurationComponentInput{{Capability: "metrics.timeseries.collect"}, {Capability: "load.session.control"}}}
	var automatic domain.PipelineRun
	configureInitializationRun(&automatic, definition, manualStartEvery, 0)
	if automatic.PauseBeforeStage != "" || automatic.ApplicationNamespace != "" {
		t.Fatal("automatic defaults changed")
	}
	definition.TwoPhaseStart = true
	definition.Namespace = "benchmark"
	for i := 0; i < 3; i++ {
		var run domain.PipelineRun
		configureInitializationRun(&run, definition, manualStartEvery, i)
		if run.PauseBeforeStage != "capability-2" || run.ApplicationNamespace != "benchmark" {
			t.Fatal("not all runs were initialized", run)
		}
	}
	var frozen domain.PipelineRun
	configureInitializationRun(&frozen, definition, manualStartEvery, 0)
	definition.TwoPhaseStart = false
	definition.Namespace = "another"
	var next domain.PipelineRun
	configureInitializationRun(&next, definition, manualStartEvery, 0)
	if frozen.PauseBeforeStage != "capability-2" || frozen.ApplicationNamespace != "benchmark" || next.PauseBeforeStage != "" || next.ApplicationNamespace != "another" {
		t.Fatal("editing configuration changed an existing run")
	}
}

func TestManualStartFirstScopePausesOnlyTheFirstRunIncludingPilot(t *testing.T) {
	definition := experimentConfigurationDefinitionInput{TwoPhaseStart: true, Namespace: "benchmark", Components: []experimentConfigurationComponentInput{{Capability: "load.session.control"}}}
	for _, pilot := range []bool{false, true} {
		scope := effectiveManualStartScope("", definition.ManualStartScope)
		if scope != manualStartFirst {
			t.Fatal("configurations without a scope must wait only for the first run", scope)
		}
		for index, slot := range (trialSchedule{Pilot: pilot}).slots(3) {
			var run domain.PipelineRun
			configureInitializationRun(&run, definition, scope, index)
			waits := run.PauseBeforeStage == "capability-1"
			if waits != (index == 0) || run.ApplicationNamespace != "benchmark" {
				t.Fatalf("pilot=%v slot %+v waits=%v", pilot, slot, waits)
			}
			if pilot && index == 0 && !slot.Pilot {
				t.Fatal("the pilot run must be the one waiting for Start")
			}
		}
	}
	if effectiveManualStartScope(manualStartEvery, manualStartFirst) != manualStartEvery || effectiveManualStartScope("", manualStartEvery) != manualStartEvery {
		t.Fatal("explicit execution choice must override the saved scope")
	}
}

func TestManualStartScopeValidation(t *testing.T) {
	for _, valid := range []string{"", manualStartFirst, manualStartEvery} {
		if err := validateDefinitionTrialSchedule(experimentConfigurationDefinitionInput{ManualStartScope: valid}); err != nil {
			t.Fatal(valid, err)
		}
	}
	if validateDefinitionTrialSchedule(experimentConfigurationDefinitionInput{ManualStartScope: "never"}) == nil {
		t.Fatal("unknown scope accepted in a definition")
	}
	every, bad := manualStartEvery, "sometimes"
	schedule, err := trialScheduleInput{ManualStartScope: &every}.resolve(0, false, manualStartFirst)
	if err != nil || schedule.ManualStartScope != manualStartEvery {
		t.Fatal("override", schedule, err)
	}
	if schedule, err := (trialScheduleInput{}).resolve(0, false, manualStartEvery); err != nil || schedule.ManualStartScope != manualStartEvery {
		t.Fatal("saved scope lost", schedule, err)
	}
	if _, err := (trialScheduleInput{ManualStartScope: &bad}).resolve(0, false, ""); err == nil {
		t.Fatal("unknown scope accepted in a request")
	}
	server := &Server{}
	for _, endpoint := range []struct {
		path    string
		handler http.HandlerFunc
		body    string
	}{
		{"/api/v1/experiment-suites", server.createExperimentSuite, `{"name":"Suite","configurationIds":["a","b"],"runs":2,"manualStartScope":"sometimes"}`},
		{"/api/v1/experiment-runs", server.createPipelineExperiment, `{"workspaceId":"ws","name":"Runs","repetitions":2,"variants":[{"name":"A","pipelineId":"p"},{"name":"B","pipelineId":"q"}],"manualStartScope":"sometimes"}`},
	} {
		response := httptest.NewRecorder()
		endpoint.handler(response, httptest.NewRequest(http.MethodPost, endpoint.path, strings.NewReader(endpoint.body)))
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), "invalid_trial_schedule") {
			t.Fatalf("%s: %d %s", endpoint.path, response.Code, response.Body.String())
		}
	}
}
