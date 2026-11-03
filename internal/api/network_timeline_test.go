package api

import (
	"encoding/json"
	"testing"
)

func TestNetworkTimelineMatchesLoadDurationAndPreservesStaticConfigurations(t *testing.T) {
	definition := experimentConfigurationDefinitionInput{Components: []experimentConfigurationComponentInput{{Capability: "load.session.control", Configuration: json.RawMessage(`{"steps":[{"durationSeconds":60},{"durationSeconds":120}]}`)}, {Capability: "chaos.network.zones", Configuration: json.RawMessage(`{"nodeGroupLabel":"topology.kubernetes.io/zone","networkInterface":"flannel.1","phases":[{"durationSeconds":60,"enableLatency":true,"latencyMilliseconds":20},{"durationSeconds":120,"enableBandwidth":true,"bandwidthBytesPerSecond":1000,"enablePacketLoss":true,"packetLossPercent":1}]}`)}}}
	if err := validateExperimentNetworkTimeline(definition); err != nil {
		t.Fatal(err)
	}
	definition.Components[0].Configuration = json.RawMessage(`{"steps":[{"durationSeconds":60}]}`)
	if err := validateExperimentNetworkTimeline(definition); err == nil {
		t.Fatal("mismatched durations accepted")
	}
	definition.Components[1].Configuration = json.RawMessage(`{"enableLatency":true,"latencyMilliseconds":30}`)
	if err := validateExperimentNetworkTimeline(definition); err != nil {
		t.Fatalf("legacy static profile rejected: %v", err)
	}
	if hasNetworkTimeline(definition) {
		t.Fatal("legacy profile changed to a clocked profile")
	}
}

func TestNetworkTimelineDoesNotInterpretOtherPluginPhases(t *testing.T) {
	definition := experimentConfigurationDefinitionInput{Components: []experimentConfigurationComponentInput{{Capability: "load.custom", Configuration: json.RawMessage(`{"steps":"custom format"}`)}, {Capability: "chaos.compute", Configuration: json.RawMessage(`{"phases":["plugin-specific phase"]}`)}}}
	if hasNetworkTimeline(definition) {
		t.Fatal("another chaos plugin incorrectly activated network synchronization")
	}
	if err := validateExperimentNetworkTimeline(definition); err != nil {
		t.Fatalf("an unrelated plugin configuration changed: %v", err)
	}
	definition.Components[1] = experimentConfigurationComponentInput{Capability: "chaos.network.zones", Configuration: json.RawMessage(`{"enableLatency":true,"latencyMilliseconds":50}`)}
	if err := validateExperimentNetworkTimeline(definition); err != nil {
		t.Fatalf("static network configuration interpreted an unrelated load plugin: %v", err)
	}
}
