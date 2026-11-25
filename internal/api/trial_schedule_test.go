package api

import "testing"

func TestTrialScheduleDefaultsOverridesAndPilotPositions(t *testing.T) {
	schedule, err := trialScheduleInput{}.resolve(0, false, "")
	if err != nil || schedule.RestSeconds != 0 || schedule.Pilot || len(schedule.slots(3)) != 3 || schedule.slots(3)[0].Position != 1 {
		t.Fatalf("defaults changed the historical schedule: %+v %v", schedule, err)
	}
	rest, pilot := 90, true
	schedule, err = trialScheduleInput{RestSecondsBetweenTrials: &rest}.resolve(30, true, "")
	if err != nil || schedule.RestSeconds != 90 || !schedule.Pilot {
		t.Fatalf("override: %+v %v", schedule, err)
	}
	slots := trialSchedule{Pilot: pilot}.slots(2)
	if len(slots) != 3 || !slots[0].Pilot || slots[0].Position != 0 || slots[2].Position != 2 || slots[0].runLabel("Run") != "Pilot run" || slots[1].runLabel("Run") != "Run 1" {
		t.Fatalf("pilot slots: %+v", slots)
	}
	for _, invalid := range []int{-1, 3601} {
		value := invalid
		if _, err := (trialScheduleInput{RestSecondsBetweenTrials: &value}).resolve(0, false, ""); err == nil {
			t.Fatalf("rest %d accepted", invalid)
		}
	}
	if validateDefinitionTrialSchedule(experimentConfigurationDefinitionInput{RestSecondsBetweenTrials: 3600}) != nil {
		t.Fatal("maximum rest rejected")
	}
}
