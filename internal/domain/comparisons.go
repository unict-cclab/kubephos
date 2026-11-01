package domain

import "time"

type ComparisonRun struct {
	ExperimentID string `json:"experimentId"`
	TrialID      string `json:"trialId"`
}

type ComparisonSeries struct {
	Label string          `json:"label"`
	Color string          `json:"color"`
	Runs  []ComparisonRun `json:"runs"`
}

type ResultComparison struct {
	ID          string             `json:"id"`
	Name        string             `json:"name"`
	OwnerUserID string             `json:"ownerUserId"`
	CreatedBy   string             `json:"createdBy"`
	ModifiedBy  string             `json:"modifiedBy"`
	Series      []ComparisonSeries `json:"series"`
	CreatedAt   time.Time          `json:"createdAt"`
	UpdatedAt   time.Time          `json:"updatedAt"`
}
