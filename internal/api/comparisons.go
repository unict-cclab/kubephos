package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"kubephos.dev/kubephos/internal/domain"
	"kubephos.dev/kubephos/internal/id"
	"kubephos.dev/kubephos/internal/storage"
)

type comparisonView struct {
	Comparison           domain.ResultComparison    `json:"comparison"`
	Result               domain.Experiment          `json:"result"`
	Artifacts            []domain.Artifact          `json:"artifacts"`
	SourceConfigurations map[string]json.RawMessage `json:"sourceConfigurations"`
	Sources              []domain.Experiment        `json:"sources"`
}

func validateComparison(item *domain.ResultComparison) error {
	return validateComparisonSelection(item, false)
}

func validateComparisonSelection(item *domain.ResultComparison, wholeExperiments bool) error {
	item.Name = strings.TrimSpace(item.Name)
	if len(item.Name) < 1 || len(item.Name) > 120 {
		return errors.New("Name must contain between 1 and 120 characters.")
	}
	if len(item.Series) < 1 || len(item.Series) > 80 {
		return errors.New("Select up to 8 experiments.")
	}
	labels, runs := map[string]bool{}, map[string]bool{}
	for i := range item.Series {
		series := &item.Series[i]
		series.Label = strings.TrimSpace(series.Label)
		key := strings.ToLower(series.Label)
		if len(key) < 1 || len(series.Label) > 240 || !wholeExperiments && labels[key] {
			return errors.New("Each series needs a distinct label of at most 240 characters.")
		}
		labels[key] = true
		if !regexp.MustCompile(`^#[0-9a-fA-F]{6}$`).MatchString(series.Color) {
			return errors.New("Select a valid color for every series.")
		}
		if len(series.Runs) == 0 {
			return errors.New("Each series must include at least one completed run.")
		}
		for _, run := range series.Runs {
			key := run.ExperimentID + "/" + run.TrialID
			if run.ExperimentID == "" || run.TrialID == "" || runs[key] {
				return errors.New("Select each run only once.")
			}
			runs[key] = true
		}
	}
	if len(runs) < 2 || len(runs) > 8000 {
		return errors.New("Select experiments with completed results.")
	}
	return nil
}

func (s *Server) listComparisons(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListResultComparisons(r.Context(), requestUser(r).ID)
	if err != nil {
		writeError(w, 500, "database_error", "Could not list comparisons.")
		return
	}
	count, err := s.store.CountComparisonCandidates(r.Context(), viewerID(requestUser(r)))
	if err != nil {
		writeError(w, 500, "database_error", "Could not load available experiments.")
		return
	}
	writeJSON(w, 200, map[string]any{"items": items, "eligibleExperimentCount": count})
}

func (s *Server) comparisonSources(r *http.Request, item domain.ResultComparison) (comparisonView, error) {
	view := comparisonView{Comparison: item, Artifacts: []domain.Artifact{}, SourceConfigurations: map[string]json.RawMessage{}, Result: domain.Experiment{ID: item.ID, Name: item.Name, Kind: "comparison", Status: "succeeded", OwnerUserID: item.OwnerUserID, OwnerUsername: item.CreatedBy, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, ResultType: "TimeSeriesDataset", ResultVersion: "v1alpha1", Variants: []domain.ExperimentVariant{}}}
	sources := map[string]domain.Experiment{}
	seen := map[string]bool{}
	for i, series := range item.Series {
		variant := domain.ExperimentVariant{ID: fmt.Sprintf("%s-series-%d", item.ID, i), ExperimentID: item.ID, Position: i + 1, Name: series.Label, Alias: series.Label, Color: series.Color, Trials: []domain.ExperimentTrial{}}
		for j, ref := range series.Runs {
			source, ok := sources[ref.ExperimentID]
			if !ok {
				if !s.canReadResource(r, "experiments", ref.ExperimentID) {
					return view, errors.New("A source experiment is unavailable or is no longer shared with you. Edit the comparison to remove its runs.")
				}
				var err error
				source, err = s.store.GetComparisonSource(r.Context(), ref.ExperimentID)
				if err != nil || source.Kind == "comparison" || !comparisonSourceFinished(source.Status) {
					return view, errors.New("Select runs from completed experiments.")
				}
				sources[ref.ExperimentID] = source
			}
			var trial *domain.ExperimentTrial
			var config json.RawMessage
			for _, v := range source.Variants {
				for _, t := range v.Trials {
					if t.ID == ref.TrialID {
						copy := t
						trial = &copy
						config = v.Configuration
					}
				}
			}
			if trial == nil || trial.Status != "succeeded" || trial.ResultArtifactID == "" {
				return view, errors.New("A selected run has no completed results. Edit the comparison to select another run.")
			}
			if seen[trial.ResultArtifactID] {
				return view, errors.New("The same result cannot appear more than once in a comparison.")
			}
			seen[trial.ResultArtifactID] = true
			artifact, err := s.store.GetArtifact(r.Context(), trial.ResultArtifactID)
			if err != nil || artifact.Sensitive || artifact.Type != "TimeSeriesDataset" || artifact.Version != "v1alpha1" || artifact.VerifiedAt.IsZero() {
				return view, errors.New("A selected run's verified results are no longer available.")
			}
			// Copies are presentation data only. Source trials and operations are never updated.
			trial.VariantID = variant.ID
			trial.Position = j + 1
			variant.Trials = append(variant.Trials, *trial)
			if len(variant.Configuration) == 0 {
				variant.Configuration = config
			}
			view.SourceConfigurations[artifact.ID] = config
			view.Artifacts = append(view.Artifacts, artifact)
		}
		view.Result.Variants = append(view.Result.Variants, variant)
	}
	// Preserve source identities and original run numbers for provenance and editing
	// comparisons whose sources are outside the first history page.
	selected := map[string]bool{}
	for _, series := range item.Series {
		for _, ref := range series.Runs {
			selected[ref.ExperimentID+"/"+ref.TrialID] = true
		}
	}
	view.Sources = []domain.Experiment{}
	for _, source := range sources {
		copy := source
		copy.Variants = []domain.ExperimentVariant{}
		for _, variant := range source.Variants {
			v := variant
			v.Trials = []domain.ExperimentTrial{}
			for _, trial := range variant.Trials {
				if selected[source.ID+"/"+trial.ID] {
					v.Trials = append(v.Trials, trial)
				}
			}
			if len(v.Trials) > 0 {
				copy.Variants = append(copy.Variants, v)
			}
		}
		view.Sources = append(view.Sources, copy)
	}
	sort.Slice(view.Sources, func(i, j int) bool { return view.Sources[i].ID < view.Sources[j].ID })
	return view, nil
}

func comparisonSourceFinished(status string) bool {
	return status == "succeeded" || status == "failed" || status == "canceled"
}

func (s *Server) getComparison(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetResultComparison(r.Context(), r.PathValue("id"), requestUser(r).ID)
	if err != nil {
		comparisonStorageError(w, err)
		return
	}
	// Older comparisons may group runs from different experiments together.
	// Resolve them with the same whole-experiment semantics as new comparisons.
	selections := []comparisonExperimentInput{}
	seenSources := map[string]bool{}
	for _, group := range item.Series {
		for _, run := range group.Runs {
			if !seenSources[run.ExperimentID] {
				seenSources[run.ExperimentID] = true
				selections = append(selections, comparisonExperimentInput{ExperimentID: run.ExperimentID, Color: group.Color, Label: group.LabelOverride})
			}
		}
	}
	if len(selections) >= 2 {
		expanded, err := s.comparisonExperimentSeries(r, selections)
		if err != nil {
			writeError(w, 409, "source_unavailable", err.Error())
			return
		}
		item.Series = expanded
	}
	view, err := s.comparisonSources(r, item)
	if err != nil {
		writeError(w, 409, "source_unavailable", err.Error())
		return
	}
	writeJSON(w, 200, view)
}

func (s *Server) saveComparison(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name        string                      `json:"name"`
		Series      []domain.ComparisonSeries   `json:"series"`
		Experiments []comparisonExperimentInput `json:"experiments"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, 400, "invalid_request", "Could not read the comparison. Check the selected experiments.")
		return
	}
	item := domain.ResultComparison{ID: r.PathValue("id"), Name: input.Name, Series: input.Series, OwnerUserID: requestUser(r).ID}
	update := r.Method == http.MethodPut
	if update {
		if _, err := s.store.GetResultComparison(r.Context(), item.ID, item.OwnerUserID); err != nil {
			comparisonStorageError(w, err)
			return
		}
	} else {
		item.ID = id.New("cmp")
	}
	if input.Experiments != nil {
		if len(input.Series) > 0 {
			writeError(w, 422, "invalid_comparison", "Select experiments only.")
			return
		}
		series, err := s.comparisonExperimentSeries(r, input.Experiments)
		if err != nil {
			writeError(w, 422, "invalid_comparison", err.Error())
			return
		}
		item.Series = series
	}
	if err := validateComparisonSelection(&item, input.Experiments != nil); err != nil {
		writeError(w, 422, "invalid_comparison", err.Error())
		return
	}
	if _, err := s.comparisonSources(r, item); err != nil {
		writeError(w, 422, "invalid_runs", err.Error())
		return
	}
	saved, err := s.store.SaveResultComparison(r.Context(), item, update)
	if err != nil {
		comparisonStorageError(w, err)
		return
	}
	status := http.StatusCreated
	if update {
		status = http.StatusOK
	}
	writeJSON(w, status, saved)
}

func (s *Server) deleteComparison(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteResultComparison(r.Context(), r.PathValue("id"), requestUser(r).ID); err != nil {
		comparisonStorageError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func comparisonStorageError(w http.ResponseWriter, err error) {
	if errors.Is(err, storage.ErrNotFound) {
		writeError(w, 404, "not_found", "Comparison not found.")
	} else {
		writeError(w, 500, "database_error", "Could not save or read the comparison. Try again.")
	}
}

// Expand each experiment into its own approach groups. Repeated executions are
// averaged within a group; distinct suite configurations are never pooled.
type comparisonExperimentInput struct {
	Label        string `json:"label,omitempty"`
	ExperimentID string `json:"experimentId"`
	Color        string `json:"color"`
}

func (s *Server) comparisonExperimentSeries(r *http.Request, selections []comparisonExperimentInput) ([]domain.ComparisonSeries, error) {
	if len(selections) < 2 || len(selections) > 8 {
		return nil, errors.New("Select between 2 and 8 experiments.")
	}
	result := []domain.ComparisonSeries{}
	seen := map[string]bool{}
	for _, selection := range selections {
		if seen[selection.ExperimentID] {
			return nil, errors.New("Select each experiment only once.")
		}
		seen[selection.ExperimentID] = true
		if !s.canReadResource(r, "experiments", selection.ExperimentID) {
			return nil, errors.New("A selected experiment is unavailable.")
		}
		source, err := s.store.GetComparisonSource(r.Context(), selection.ExperimentID)
		if err != nil || source.Kind == "comparison" || !comparisonSourceFinished(source.Status) {
			return nil, errors.New("Select completed experiments with results.")
		}
		label := strings.TrimSpace(selection.Label)
		if len([]rune(label)) > 120 || strings.ContainsAny(label, "\n\r\t") {
			return nil, errors.New("Plot labels must be at most 120 characters on one line.")
		}
		if label != "" {
			source.Name = label
		}
		groups := experimentComparisonSeries(source, selection.Color)
		for i := range groups {
			groups[i].LabelOverride = label
		}
		if len(groups) == 0 {
			return nil, errors.New("A selected experiment has no completed results.")
		}
		result = append(result, groups...)
	}
	return result, nil
}
func experimentComparisonSeries(source domain.Experiment, color string) []domain.ComparisonSeries {
	result := []domain.ComparisonSeries{}
	for _, variant := range source.Variants {
		label := source.Name
		if len(source.Variants) > 1 {
			approach := variant.Alias
			if approach == "" {
				approach = variant.Name
			}
			label += " · " + approach
		}
		group := domain.ComparisonSeries{Label: label, Color: color, Runs: []domain.ComparisonRun{}}
		for _, trial := range variant.Trials {
			if trial.Status == "succeeded" && trial.ResultArtifactID != "" {
				group.Runs = append(group.Runs, domain.ComparisonRun{ExperimentID: source.ID, TrialID: trial.ID})
			}
		}
		if len(group.Runs) > 0 {
			result = append(result, group)
		}
	}
	return result
}

func (s *Server) listComparisonCandidates(w http.ResponseWriter, r *http.Request) {
	offset := 0
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 100000 {
			writeError(w, 400, "invalid_request", "Invalid selection page.")
			return
		}
		offset = value
	}
	search := strings.TrimSpace(r.URL.Query().Get("search"))
	owner := strings.TrimSpace(r.URL.Query().Get("owner"))
	if len([]rune(search)) > 120 || len(owner) > 200 {
		writeError(w, 400, "invalid_request", "Search is too long.")
		return
	}
	page, err := s.store.ListComparisonCandidates(r.Context(), viewerID(requestUser(r)), owner, search, offset)
	if err != nil {
		writeError(w, 500, "database_error", "Could not load available experiments.")
		return
	}
	writeJSON(w, 200, page)
}
