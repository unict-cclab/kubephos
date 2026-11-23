package api

import (
	"fmt"
	"strings"
)

const maxRestSecondsBetweenTrials = 3600

// trialScheduleInput carries the optional execution settings shared by every
// experiment creation request. Omitted values keep the saved configuration's
// settings, or the historical behavior: no rest and no pilot run.
type trialScheduleInput struct {
	RestSecondsBetweenTrials *int  `json:"restSecondsBetweenTrials,omitempty"`
	FirstTrialPilot          *bool `json:"firstTrialPilot,omitempty"`
	// ManualStartScope chooses which runs of a manual-start ("twoPhaseStart")
	// configuration wait for the user's Start: "first" or "every".
	ManualStartScope *string `json:"manualStartScope,omitempty"`
}

type trialSchedule struct {
	RestSeconds int
	Pilot       bool
	// ManualStartScope is "" when each configuration keeps its saved choice.
	ManualStartScope string
}

// Manual start scopes. With "first" only the first run of each manual-start
// configuration (the pilot when enabled, otherwise run 1) waits for Start;
// later runs start by themselves after the rest interval. "every" pauses every
// run, which was the only behavior before the setting existed. Configurations
// saved without a scope behave as "first".
const (
	manualStartFirst = "first"
	manualStartEvery = "every"
)

var errInvalidManualStartScope = fmt.Errorf("Manual start must wait for the first run (%q) or every run (%q).", manualStartFirst, manualStartEvery)

func validManualStartScope(value string) bool {
	return value == "" || value == manualStartFirst || value == manualStartEvery
}

// effectiveManualStartScope applies an explicit execution choice over the
// configuration's saved one, defaulting to "first".
func effectiveManualStartScope(override, saved string) string {
	if override != "" {
		return override
	}
	if saved != "" {
		return saved
	}
	return manualStartFirst
}

// waitsForStart tells whether the run at slotIndex (execution order within one
// configuration, pilot included) pauses before the load.
func waitsForStart(scope string, slotIndex int) bool {
	return scope == manualStartEvery || slotIndex == 0
}

type trialSlot struct {
	Position int
	Pilot    bool
}

var errInvalidTrialSchedule = fmt.Errorf("Rest between runs must be between 0 and %d seconds.", maxRestSecondsBetweenTrials)

func validRestSeconds(value int) bool {
	return value >= 0 && value <= maxRestSecondsBetweenTrials
}

// resolve applies explicit request values over the saved defaults.
func (input trialScheduleInput) resolve(savedRest int, savedPilot bool, savedScope string) (trialSchedule, error) {
	schedule := trialSchedule{RestSeconds: savedRest, Pilot: savedPilot, ManualStartScope: savedScope}
	if input.RestSecondsBetweenTrials != nil {
		schedule.RestSeconds = *input.RestSecondsBetweenTrials
	}
	if input.FirstTrialPilot != nil {
		schedule.Pilot = *input.FirstTrialPilot
	}
	if input.ManualStartScope != nil {
		schedule.ManualStartScope = *input.ManualStartScope
	}
	if !validRestSeconds(schedule.RestSeconds) {
		return trialSchedule{}, errInvalidTrialSchedule
	}
	if !validManualStartScope(schedule.ManualStartScope) {
		return trialSchedule{}, errInvalidManualStartScope
	}
	return schedule, nil
}

// slots lists the trials of one configuration. The optional pilot run takes
// position 0, so measured runs keep positions 1..runs.
func (schedule trialSchedule) slots(runs int) []trialSlot {
	result := make([]trialSlot, 0, runs+1)
	if schedule.Pilot {
		result = append(result, trialSlot{Position: 0, Pilot: true})
	}
	for position := 1; position <= runs; position++ {
		result = append(result, trialSlot{Position: position})
	}
	return result
}

func (slot trialSlot) runLabel(word string) string {
	if slot.Pilot {
		return "Pilot " + strings.ToLower(word)
	}
	return fmt.Sprintf("%s %d", word, slot.Position)
}

func validateDefinitionTrialSchedule(definition experimentConfigurationDefinitionInput) error {
	if !validRestSeconds(definition.RestSecondsBetweenTrials) {
		return errInvalidTrialSchedule
	}
	if !validManualStartScope(definition.ManualStartScope) {
		return errInvalidManualStartScope
	}
	return nil
}
