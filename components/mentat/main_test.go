package main

import (
	"testing"
	"time"
)

func TestNextBandwidthDelayIncludesBoundedJitter(t *testing.T) {
	configuration := config{
		bandwidthInterval: 90 * time.Second,
		bandwidthJitter:   30 * time.Second,
	}

	for i := 0; i < 100; i++ {
		delay := nextBandwidthDelay(configuration)
		if delay < 90*time.Second || delay > 120*time.Second {
			t.Fatalf("nextBandwidthDelay() = %s, want within 90s..120s", delay)
		}
	}
}

func TestNextBandwidthDelayAllowsDisabledJitter(t *testing.T) {
	configuration := config{bandwidthInterval: 90 * time.Second}

	if delay := nextBandwidthDelay(configuration); delay != 90*time.Second {
		t.Fatalf("nextBandwidthDelay() = %s, want 90s", delay)
	}
}

func TestBandwidthMetricLabelsFollowResponseDirection(t *testing.T) {
	origin, destination := bandwidthMetricLabels("node-a", "node-b")
	if origin != "node-b" || destination != "node-a" {
		t.Fatalf("bandwidthMetricLabels() = %q -> %q, want node-b -> node-a", origin, destination)
	}
}
