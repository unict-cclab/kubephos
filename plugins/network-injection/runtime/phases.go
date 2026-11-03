package networkinjection

import (
	"errors"
	"fmt"
	"strings"
)

// Phase describes a complete channel profile. Omitted effects are disabled,
// including effects enabled by the preceding phase.
type Phase struct {
	DurationSeconds         int            `json:"durationSeconds"`
	EnableLatency           bool           `json:"enableLatency"`
	LatencyMilliseconds     int            `json:"latencyMilliseconds,omitempty"`
	JitterMilliseconds      int            `json:"jitterMilliseconds,omitempty"`
	CorrelationPercent      float64        `json:"correlationPercent,omitempty"`
	EnableBandwidth         bool           `json:"enableBandwidth"`
	BandwidthBytesPerSecond int64          `json:"bandwidthBytesPerSecond,omitempty"`
	EnablePacketLoss        bool           `json:"enablePacketLoss"`
	PacketLossPercent       float64        `json:"packetLossPercent,omitempty"`
	LinkOverrides           []LinkOverride `json:"linkOverrides,omitempty"`
}

func phaseSpec(spec Spec, phase Phase) Spec {
	spec.Phases = nil
	spec.EnableLatency = phase.EnableLatency
	spec.LatencyMilliseconds = phase.LatencyMilliseconds
	spec.JitterMilliseconds = phase.JitterMilliseconds
	spec.CorrelationPercent = phase.CorrelationPercent
	spec.EnableBandwidth = phase.EnableBandwidth
	spec.BandwidthBytesPerSecond = phase.BandwidthBytesPerSecond
	spec.EnablePacketLoss = phase.EnablePacketLoss
	spec.PacketLossPercent = phase.PacketLossPercent
	spec.LinkOverrides = phase.LinkOverrides
	return spec
}

func profiles(spec Spec) []Spec {
	if len(spec.Phases) == 0 {
		return []Spec{spec}
	}
	result := make([]Spec, len(spec.Phases))
	for i, phase := range spec.Phases {
		result[i] = phaseSpec(spec, phase)
	}
	return result
}

func anyPhaseLink(spec Spec, source, target string) bool {
	for _, profile := range profiles(spec) {
		if linkEnabled(effective(profile, source, target)) {
			return true
		}
	}
	return false
}

func ValidatePhases(spec Spec) (int, error) {
	if len(spec.Phases) == 0 {
		return 0, nil
	}
	if len(spec.Phases) > 32 {
		return 0, errors.New("At most 32 network phases are supported.")
	}
	total := 0
	active := false
	for i, phase := range spec.Phases {
		if phase.DurationSeconds < 1 || phase.DurationSeconds > 86400 {
			return 0, fmt.Errorf("Network phase %d duration must be between 1 and 86400 seconds.", i+1)
		}
		profile := phaseSpec(spec, phase)
		// A neutral phase explicitly restores an unimpaired channel.
		if profile.EnableLatency || profile.EnableBandwidth || profile.EnablePacketLoss || len(profile.LinkOverrides) > 0 {
			active = true
			if err := validateConfiguration(profile); err != nil {
				return 0, fmt.Errorf("Network phase %d: %w", i+1, err)
			}
			zones := []string{}
			for _, link := range profile.LinkOverrides {
				zones = append(zones, link.SourceZone, link.TargetZone)
			}
			if err := validateOverrides(profile.LinkOverrides, zones, profile); err != nil {
				return 0, fmt.Errorf("Network phase %d: %w", i+1, err)
			}
		}
		total += phase.DurationSeconds
	}
	if total > 86400 {
		return 0, errors.New("Network phases cannot exceed 24 hours.")
	}
	if !active {
		return 0, errors.New("Enable a network effect in at least one phase.")
	}
	return total, nil
}

func phasedShapingScript(spec Spec, sourceZone string, targetZones []string, networks map[string][]string) string {
	// Keep the root classifier and its filters stable. Only child netem qdiscs are
	// replaced, so phase transitions do not inherit omitted settings.
	initial := phaseSpec(spec, Phase{})
	base := shapingScript(initial, sourceZone, targetZones, networks)
	base = strings.TrimSuffix(base, "while :; do sleep 3600 & wait $!; done\n")
	var script strings.Builder
	script.WriteString(base)
	script.WriteString("applied=-1\nwhile :; do\n phase=-1\n if test -s /tmp/kubephos-network-start; then\n  start=$(cat /tmp/kubephos-network-start)\n  case \"$start\" in ''|*[!0-9]*) echo 'invalid network start timestamp' >&2; exit 1 ;; esac\n  elapsed=$(( $(date +%s) - start ))\n  if test \"$elapsed\" -ge 0; then phase=0; fi\n")
	offset := 0
	for i, phase := range spec.Phases {
		if i > 0 {
			script.WriteString(fmt.Sprintf("  if test \"$elapsed\" -ge %d; then phase=%d; fi\n", offset, i))
		}
		offset += phase.DurationSeconds
	}
	script.WriteString(fmt.Sprintf("  if test \"$elapsed\" -ge %d; then phase=-1; fi\n", offset))
	script.WriteString(" fi\n if test \"$phase\" != \"$applied\"; then\n  case \"$phase\" in\n")
	all := append([]Phase{{}}, spec.Phases...)
	for index, phase := range all {
		i := index - 1
		profile := phaseSpec(spec, phase)
		script.WriteString(fmt.Sprintf("  %d)\n", i))
		for index, zone := range targetZones {
			link := effective(profile, sourceZone, zone)
			script.WriteString(fmt.Sprintf("   host tc qdisc replace dev %s parent 1:%d handle %d: netem", spec.NetworkInterface, index+2, (index+2)*10))
			script.WriteString(netemArguments(link))
			script.WriteByte('\n')
		}
		script.WriteString("   ;;\n")
	}
	script.WriteString("  esac\n  applied=$phase\n  printf 'Applied network phase %s\\n' \"$((phase+1))\"\n fi\n sleep 0.1 & wait $!\ndone\n")
	return script.String()
}

func netemArguments(link effectiveLink) string {
	var value strings.Builder
	if link.EnableLatency {
		value.WriteString(fmt.Sprintf(" delay %dms", link.Latency))
		if link.Jitter > 0 {
			value.WriteString(fmt.Sprintf(" %dms %.4g%%", link.Jitter, link.Correlation))
		}
	}
	if link.EnableBandwidth {
		value.WriteString(fmt.Sprintf(" rate %dbps", link.Bandwidth))
	}
	if link.EnablePacketLoss {
		value.WriteString(fmt.Sprintf(" loss %.4g%% %.4g%%", link.PacketLoss, link.Correlation))
	}
	return value.String()
}
