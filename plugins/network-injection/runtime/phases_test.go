package networkinjection

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPhasedNetworkRestoresOmittedEffectsAndStartsWithTraffic(t *testing.T) {
	spec := Spec{NodeGroupLabel: "topology.kubernetes.io/zone", NetworkInterface: "flannel.1", Phases: []Phase{{DurationSeconds: 60, EnableLatency: true, LatencyMilliseconds: 10, EnableBandwidth: true, BandwidthBytesPerSecond: 1000, EnablePacketLoss: true, PacketLossPercent: 5}, {DurationSeconds: 60}, {DurationSeconds: 60, EnableLatency: true, LatencyMilliseconds: 40, EnablePacketLoss: true, PacketLossPercent: 20}}}
	if total, err := ValidatePhases(spec); err != nil || total != 180 {
		t.Fatalf("duration %d: %v", total, err)
	}
	dir := t.TempDir()
	logFile := filepath.Join(dir, "commands")
	clock := filepath.Join(dir, "clock")
	counter := filepath.Join(dir, "counter")
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0755); err != nil {
			t.Fatal(err)
		}
	}
	write("clock", "100")
	write("counter", "0")
	write("nsenter", "#!/bin/sh\nwhile test \"$1\" != '--'; do shift; done; shift; exec \"$@\"\n")
	write("tc", "#!/bin/sh\nif test \"$1 $2\" = 'qdisc show'; then echo 'qdisc noqueue 0: root'; else printf '%s\\n' \"$*\" >> \"$TC_RECORD\"; fi\n")
	write("date", "#!/bin/sh\nn=$(cat \"$CLOCK_COUNTER\"); echo $((n+1)) > \"$CLOCK_COUNTER\"; case $n in 0) echo 99;;1) echo 100;;2) echo 160;;3) echo 220;;*) echo 280;;esac\n")
	script := phasedShapingScript(spec, "cloud", []string{"edge"}, map[string][]string{"edge": {"10.42.2.0/24"}})
	script = strings.ReplaceAll(script, "/tmp/kubephos-network-start", clock)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/sh", "-ec", script)
	cmd.Env = append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"), "TC_RECORD="+logFile, "CLOCK_COUNTER="+counter)
	output, err := cmd.CombinedOutput()
	if ctx.Err() == nil && err != nil {
		t.Fatalf("shaper: %v: %s", err, output)
	}
	raw, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	var transitions []string
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "qdisc replace ") {
			transitions = append(transitions, line)
		}
	}
	if len(transitions) != 4 {
		t.Fatalf("expected four clocked transitions, got %v; output=%s", transitions, output)
	}
	if !strings.HasSuffix(transitions[0], "netem delay 10ms rate 1000bps loss 5% 0%") {
		t.Fatal(transitions[0])
	}
	if !strings.HasSuffix(transitions[1], " netem") {
		t.Fatal("neutral phase retained previous effects: ", transitions[1])
	}
	if strings.Contains(transitions[2], "rate") || !strings.HasSuffix(transitions[2], "netem delay 40ms loss 20% 0%") {
		t.Fatal(transitions[2])
	}
	if !strings.HasSuffix(transitions[3], " netem") {
		t.Fatal("network was not restored at the end: ", transitions[3])
	}
}

func TestNetworkPhaseValidationRejectsUnsafeAndInvalidSettings(t *testing.T) {
	spec := Spec{NodeGroupLabel: "topology.kubernetes.io/zone", NetworkInterface: "flannel.1", Phases: []Phase{{DurationSeconds: 60, EnableLatency: true, LatencyMilliseconds: 10}}}
	spec.Phases[0].DurationSeconds = 0
	if _, err := ValidatePhases(spec); err == nil {
		t.Fatal("zero duration accepted")
	}
	spec.Phases[0].DurationSeconds = 60
	spec.NetworkInterface = "eth0; false"
	if err := validateConfiguration(spec); err == nil {
		t.Fatal("unsafe interface accepted")
	}
	spec.NetworkInterface = "flannel.1"
	spec.Phases[0].JitterMilliseconds = 20
	if _, err := ValidatePhases(spec); err == nil {
		t.Fatal("jitter larger than latency accepted")
	}
}

func TestPhaseLinkValidationBeforeProvisioning(t *testing.T) {
	latency, jitter := 20, 30
	spec := Spec{NodeGroupLabel: "topology.kubernetes.io/zone", NetworkInterface: "flannel.1", Phases: []Phase{{DurationSeconds: 60, LinkOverrides: []LinkOverride{{SourceZone: "a", TargetZone: "b", LatencyMilliseconds: &latency, JitterMilliseconds: &jitter}}}}}
	if _, err := ValidatePhases(spec); err == nil {
		t.Fatal("invalid directed jitter accepted")
	}
	jitter = 5
	if _, err := ValidatePhases(spec); err != nil {
		t.Fatal(err)
	}
	spec.Phases[0].LinkOverrides = append(spec.Phases[0].LinkOverrides, spec.Phases[0].LinkOverrides[0])
	if _, err := ValidatePhases(spec); err == nil {
		t.Fatal("duplicate directed phase link accepted")
	}
}
