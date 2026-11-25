package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"kubephos.dev/kubephos/internal/domain"
)

// comparisonCompatibility warns when a comparison mixes runs measured with
// different load profiles or metric definitions. It never blocks a comparison.
type comparisonCompatibility struct {
	Compatible  bool                        `json:"compatible"`
	Differences []comparisonDifference      `json:"differences"`
	Sources     []comparisonSourceSignature `json:"sources"`
}

type comparisonDifference struct {
	Field  string         `json:"field"`
	Values map[string]any `json:"values"`
}

type comparisonSourceSignature struct {
	Label       string `json:"label"`
	Fingerprint string `json:"fingerprint"`
}

// Normalized fields, in report order. Absent values take the plug-in
// defaults, so configurations saved before a field existed compare equal.
var comparisonFingerprintFields = []string{
	"load.scenarioId", "load.taskWeights", "load.steps", "load.geographicSteps", "load.durationSeconds", "load.warmupSeconds",
	"load.replicas", "load.maxUsers", "load.spawnRate", "network.phases",
	"metrics.resolution", "metrics.rateWindowSeconds", "metrics.sloMilliseconds",
}

type comparisonComponent struct {
	Capability    string          `json:"capability"`
	Configuration json.RawMessage `json:"configuration"`
}

// comparisonFingerprint extracts the normalized measurement settings of one
// source configuration (an experiment configuration definition).
func comparisonFingerprint(configuration json.RawMessage) map[string]any {
	var definition struct {
		Components []comparisonComponent `json:"components"`
	}
	_ = json.Unmarshal(configuration, &definition)
	load, metrics, network := map[string]any{}, map[string]any{}, map[string]any{}
	hasLoad, hasMetrics := false, false
	for _, component := range definition.Components {
		var target map[string]any
		switch component.Capability {
		case "load.session.control":
			target, hasLoad = load, true
		case "metrics.timeseries.collect":
			target, hasMetrics = metrics, true
		case "chaos.network.zones":
			target = network
		default:
			continue
		}
		var values map[string]any
		if json.Unmarshal(component.Configuration, &values) == nil {
			for key, value := range values {
				target[key] = value
			}
		}
	}
	result := map[string]any{}
	if hasLoad {
		result["load.scenarioId"] = load["scenarioId"]
		// The request mix changes the workload as much as the scenario does.
		result["load.taskWeights"] = emptyAsNil(load["taskWeights"])
		result["load.steps"] = emptyAsNil(load["steps"])
		result["load.geographicSteps"] = emptyAsNil(load["geographicSteps"])
		result["load.durationSeconds"] = profileDuration(load)
		result["load.warmupSeconds"] = withDefault(load["warmupSeconds"], float64(0))
		result["load.replicas"] = withDefault(load["replicas"], float64(1))
		result["load.maxUsers"] = withDefault(load["maxUsers"], float64(1000))
		result["load.spawnRate"] = withDefault(load["spawnRate"], float64(10))
	}
	result["network.phases"] = emptyAsNil(network["phases"])
	if hasMetrics {
		result["metrics.resolution"] = withDefault(metrics["resolution"], "15s")
		rateWindow := metrics["rateWindowSeconds"]
		if rateWindow == nil {
			rateWindow = metrics["rateWindow"]
		}
		result["metrics.rateWindowSeconds"] = withDefault(rateWindow, float64(60))
		result["metrics.sloMilliseconds"] = metrics["sloMilliseconds"]
	}
	for _, field := range comparisonFingerprintFields {
		if _, ok := result[field]; !ok {
			result[field] = nil
		}
	}
	return result
}

func withDefault(value, fallback any) any {
	if value == nil {
		return fallback
	}
	return value
}

func emptyAsNil(value any) any {
	if items, ok := value.([]any); ok && len(items) == 0 {
		return nil
	}
	if fields, ok := value.(map[string]any); ok && len(fields) == 0 {
		return nil
	}
	return value
}

func profileDuration(load map[string]any) any {
	total, found := 0.0, false
	for _, key := range []string{"steps", "geographicSteps"} {
		items, _ := load[key].([]any)
		if len(items) == 0 || found {
			continue
		}
		for _, item := range items {
			if step, ok := item.(map[string]any); ok {
				if seconds, ok := step["durationSeconds"].(float64); ok {
					total += seconds
					found = true
				}
			}
		}
	}
	if !found {
		return nil
	}
	return total
}

func fingerprintValue(value any) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

// comparisonCompatibilityReport compares the fingerprints of every series.
// Labels are made unique so each source keeps its own value in the report.
func comparisonCompatibilityReport(variants []domain.ExperimentVariant) comparisonCompatibility {
	report := comparisonCompatibility{Compatible: true, Differences: []comparisonDifference{}, Sources: []comparisonSourceSignature{}}
	labels := make([]string, len(variants))
	fingerprints := make([]map[string]any, len(variants))
	used := map[string]int{}
	for i, variant := range variants {
		label := firstNonEmpty(variant.Alias, variant.Name, fmt.Sprintf("Series %d", i+1))
		used[label]++
		if used[label] > 1 {
			label = fmt.Sprintf("%s (%d)", label, used[label])
		}
		labels[i] = label
		fingerprints[i] = comparisonFingerprint(variant.Configuration)
		encoded, _ := json.Marshal(fingerprints[i])
		digest := sha256.Sum256(encoded)
		report.Sources = append(report.Sources, comparisonSourceSignature{Label: label, Fingerprint: hex.EncodeToString(digest[:])})
	}
	for _, field := range comparisonFingerprintFields {
		distinct := map[string]bool{}
		for _, fingerprint := range fingerprints {
			distinct[fingerprintValue(fingerprint[field])] = true
		}
		if len(distinct) < 2 {
			continue
		}
		values := map[string]any{}
		for i, fingerprint := range fingerprints {
			values[labels[i]] = fingerprint[field]
		}
		report.Differences = append(report.Differences, comparisonDifference{Field: field, Values: values})
	}
	report.Compatible = len(report.Differences) == 0
	return report
}
