package networkinjection

import (
	"strings"
	"testing"
)

func TestBidirectionalLinkAppliesAllEffectsInBothDirections(t *testing.T) {
	latency, jitter := 25, 3
	bandwidth, loss := int64(1250000), 0.5
	spec := Spec{LinkOverrides: []LinkOverride{{SourceZone: "a", TargetZone: "b", Bidirectional: true, LatencyMilliseconds: &latency, JitterMilliseconds: &jitter, BandwidthBytesPerSecond: &bandwidth, PacketLossPercent: &loss}}}
	if err := validateOverrides(spec.LinkOverrides, []string{"a", "b"}, spec); err != nil {
		t.Fatal(err)
	}
	forward, reverse := effective(spec, "a", "b"), effective(spec, "b", "a")
	if forward != reverse || !linkEnabled(reverse) {
		t.Fatal("bidirectional link does not have identical effects", forward, reverse)
	}
	spec.NetworkInterface = "flannel.1"
	script := shapingScript(spec, "b", []string{"a"}, map[string][]string{"a": {"10.42.1.0/24"}})
	if !strings.Contains(script, "delay 25ms 3ms 0% rate 1250000bps loss 0.5% 0%") {
		t.Fatal("reverse shaper omitted configured effects", script)
	}
	spec.LinkOverrides[0].Bidirectional = false
	if linkEnabled(effective(spec, "b", "a")) {
		t.Fatal("legacy directed link unexpectedly changed its reverse direction")
	}
}

func TestBidirectionalLinkRejectsOverlapRegardlessOfOrder(t *testing.T) {
	latency := 10
	both := LinkOverride{SourceZone: "a", TargetZone: "b", Bidirectional: true, LatencyMilliseconds: &latency}
	reverse := LinkOverride{SourceZone: "b", TargetZone: "a", LatencyMilliseconds: &latency}
	for _, links := range [][]LinkOverride{{both, reverse}, {reverse, both}} {
		if err := validateOverrides(links, []string{"a", "b"}, Spec{LinkOverrides: links}); err == nil {
			t.Fatal("overlapping bidirectional link accepted")
		}
	}
	both.Bidirectional = false
	if err := validateOverrides([]LinkOverride{both, reverse}, []string{"a", "b"}, Spec{LinkOverrides: []LinkOverride{both, reverse}}); err != nil {
		t.Fatal("legacy asymmetric pair rejected", err)
	}
}

func TestBidirectionalPhasesIncludeReverseAndClearNeutralProfile(t *testing.T) {
	latency := 40
	spec := Spec{NodeGroupLabel: "topology.kubernetes.io/zone", NetworkInterface: "flannel.1", Phases: []Phase{{DurationSeconds: 60, LinkOverrides: []LinkOverride{{SourceZone: "a", TargetZone: "b", Bidirectional: true, LatencyMilliseconds: &latency}}}, {DurationSeconds: 60}}}
	if _, err := ValidatePhases(spec); err != nil {
		t.Fatal(err)
	}
	if !anyPhaseLink(spec, "a", "b") || !anyPhaseLink(spec, "b", "a") {
		t.Fatal("reverse phase link omitted")
	}
	if linkEnabled(effective(phaseSpec(spec, spec.Phases[1]), "b", "a")) {
		t.Fatal("neutral phase retained the previous reverse profile")
	}
}
