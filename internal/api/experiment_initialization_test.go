package api

import (
	"kubephos.dev/kubephos/internal/domain"
	"testing"
)

func TestInitializationSettingsRemainOptionalAndAreFrozenPerRun(t *testing.T) {
	definition := experimentConfigurationDefinitionInput{Components: []experimentConfigurationComponentInput{{Capability: "metrics.timeseries.collect"}, {Capability: "load.session.control"}}}
	var automatic domain.PipelineRun
	configureInitializationRun(&automatic, definition)
	if automatic.PauseBeforeStage != "" || automatic.ApplicationNamespace != "" {
		t.Fatal("automatic defaults changed")
	}
	definition.TwoPhaseStart = true
	definition.Namespace = "benchmark"
	for i := 0; i < 3; i++ {
		var run domain.PipelineRun
		configureInitializationRun(&run, definition)
		if run.PauseBeforeStage != "capability-2" || run.ApplicationNamespace != "benchmark" {
			t.Fatal("not all runs were initialized", run)
		}
	}
	var frozen domain.PipelineRun
	configureInitializationRun(&frozen, definition)
	definition.TwoPhaseStart = false
	definition.Namespace = "another"
	var next domain.PipelineRun
	configureInitializationRun(&next, definition)
	if frozen.PauseBeforeStage != "capability-2" || frozen.ApplicationNamespace != "benchmark" || next.PauseBeforeStage != "" || next.ApplicationNamespace != "another" {
		t.Fatal("editing configuration changed an existing run")
	}
}
