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
}

type trialSchedule struct {
	RestSeconds int
	Pilot       bool
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
func (input trialScheduleInput) resolve(savedRest int, savedPilot bool) (trialSchedule, error) {
	schedule := trialSchedule{RestSeconds: savedRest, Pilot: savedPilot}
	if input.RestSecondsBetweenTrials != nil {
		schedule.RestSeconds = *input.RestSecondsBetweenTrials
	}
	if input.FirstTrialPilot != nil {
		schedule.Pilot = *input.FirstTrialPilot
	}
	if !validRestSeconds(schedule.RestSeconds) {
		return trialSchedule{}, errInvalidTrialSchedule
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
	return nil
}
