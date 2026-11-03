package api

import (
	"encoding/json"
	"fmt"

	networkinjection "kubephos.dev/kubephos/plugins/network-injection/runtime"
)

func validateExperimentNetworkTimeline(definition experimentConfigurationDefinitionInput) error {
	if !hasNetworkTimeline(definition) {
		return nil
	}
	loadDuration := 0
	for _, component := range definition.Components {
		if component.Capability == "load.session.control" {
			var value struct {
				Steps []struct {
					DurationSeconds int `json:"durationSeconds"`
				} `json:"steps"`
			}
			if err := json.Unmarshal(component.Configuration, &value); err != nil {
				return fmt.Errorf("Load profile is invalid.")
			}
			for _, step := range value.Steps {
				loadDuration += step.DurationSeconds
			}
		}
	}
	for _, component := range definition.Components {
		if component.Capability == "chaos.network.zones" {
			var profile networkinjection.Spec
			if err := json.Unmarshal(component.Configuration, &profile); err != nil {
				return fmt.Errorf("Network profile is invalid.")
			}
			if len(profile.Phases) == 0 {
				continue
			}
			total, err := networkinjection.ValidatePhases(profile)
			if err != nil {
				return err
			}
			if loadDuration < 1 || loadDuration != total {
				return fmt.Errorf("Network phases cover %d s; the load profile covers %d s. Their total durations must match.", total, loadDuration)
			}
		}
	}
	return nil
}

func hasNetworkTimeline(definition experimentConfigurationDefinitionInput) bool {
	for _, component := range definition.Components {
		if component.Capability == "chaos.network.zones" {
			var value struct {
				Phases []json.RawMessage `json:"phases"`
			}
			if json.Unmarshal(component.Configuration, &value) == nil && len(value.Phases) > 0 {
				return true
			}
		}
	}
	return false
}
