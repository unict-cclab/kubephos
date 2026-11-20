package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"time"

	"kubephos.dev/kubephos/internal/domain"
)

// captureComparisonSnapshot freezes the resolved series and the selected runs
// of every source: experiment and variant identity, presentation,
// configuration, exact trials and result artifact ids.
func captureComparisonSnapshot(view comparisonView) *domain.ComparisonSnapshot {
	sources := make([]domain.Experiment, len(view.Sources))
	copy(sources, view.Sources)
	series := make([]domain.ComparisonSeries, len(view.Comparison.Series))
	copy(series, view.Comparison.Series)
	return &domain.ComparisonSnapshot{CapturedAt: time.Now().UTC(), Series: series, Sources: sources}
}

type snapshotRun struct {
	source  domain.Experiment
	variant domain.ExperimentVariant
	trial   domain.ExperimentTrial
}

// comparisonSnapshotView rebuilds the comparison exactly as it was frozen.
// Sources that still exist must remain readable by the viewer; deleted
// sources are served from the snapshot and reported as stale.
func (s *Server) comparisonSnapshotView(r *http.Request, item domain.ResultComparison) (comparisonView, error) {
	snapshot := item.Snapshot
	item.Series = snapshot.Series
	view := comparisonView{Comparison: item, Artifacts: []domain.Artifact{}, SourceConfigurations: map[string]json.RawMessage{}, Snapshot: &comparisonSnapshotInfo{CapturedAt: snapshot.CapturedAt}, Result: domain.Experiment{ID: item.ID, Name: item.Name, Kind: "comparison", Status: "succeeded", OwnerUserID: item.OwnerUserID, OwnerUsername: item.CreatedBy, CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt, ResultType: "TimeSeriesDataset", ResultVersion: "v1alpha1", Variants: []domain.ExperimentVariant{}}}
	ids := make([]string, 0, len(snapshot.Sources))
	for _, source := range snapshot.Sources {
		ids = append(ids, source.ID)
	}
	existing, err := s.store.ExistingExperimentIDs(r.Context(), ids)
	if err != nil {
		return view, errors.New("Could not check the comparison sources.")
	}
	runs := map[string]snapshotRun{}
	for _, source := range snapshot.Sources {
		// Live sources keep their sharing rules. A deleted source stays in the
		// snapshot only for its own owner; for anyone else deletion revokes it.
		available := s.canReadResource(r, "experiments", source.ID)
		if !existing[source.ID] {
			available = source.OwnerUserID == item.OwnerUserID
		}
		if !available {
			return view, errors.New("A source experiment is unavailable or is no longer shared with you. Edit the comparison to remove its runs.")
		}
		for _, variant := range source.Variants {
			for _, trial := range variant.Trials {
				runs[source.ID+"/"+trial.ID] = snapshotRun{source: source, variant: variant, trial: trial}
			}
		}
	}
	for i, series := range snapshot.Series {
		variant := domain.ExperimentVariant{ID: fmt.Sprintf("%s-series-%d", item.ID, i), ExperimentID: item.ID, Position: i + 1, Name: series.Label, Alias: series.Label, Color: series.Color, Trials: []domain.ExperimentTrial{}}
		for j, ref := range series.Runs {
			frozen, ok := runs[ref.ExperimentID+"/"+ref.TrialID]
			if !ok || frozen.trial.ResultArtifactID == "" {
				return view, errors.New("The comparison snapshot is incomplete. Refresh the comparison.")
			}
			artifact, err := s.store.GetArtifact(r.Context(), frozen.trial.ResultArtifactID)
			if err != nil || artifact.Sensitive || artifact.Type != "TimeSeriesDataset" || artifact.Version != "v1alpha1" || artifact.VerifiedAt.IsZero() {
				return view, errors.New("A selected run's verified results are no longer available.")
			}
			trial := frozen.trial
			trial.VariantID = variant.ID
			trial.Position = j + 1
			variant.Trials = append(variant.Trials, trial)
			if len(variant.Configuration) == 0 {
				variant.Configuration = frozen.variant.Configuration
			}
			view.SourceConfigurations[artifact.ID] = frozen.variant.Configuration
			view.Artifacts = append(view.Artifacts, artifact)
		}
		view.Result.Variants = append(view.Result.Variants, variant)
	}
	view.Sources = append([]domain.Experiment{}, snapshot.Sources...)
	sort.Slice(view.Sources, func(i, j int) bool { return view.Sources[i].ID < view.Sources[j].ID })
	reasons, err := s.comparisonStaleReasons(r, snapshot, existing)
	if err != nil {
		return view, errors.New("Could not compare the snapshot with its sources.")
	}
	view.Stale, view.StaleReasons = len(reasons) > 0, reasons
	view.Compatibility = comparisonCompatibilityReport(view.Result.Variants)
	return view, nil
}

// comparisonStaleReasons summarizes how the live sources differ from the
// snapshot: deleted experiments, runs re-run or removed, new completed runs,
// and changed presentation or configuration.
func (s *Server) comparisonStaleReasons(r *http.Request, snapshot *domain.ComparisonSnapshot, existing map[string]bool) ([]string, error) {
	reasons := []string{}
	for _, frozen := range snapshot.Sources {
		name := frozen.Name
		if !existing[frozen.ID] {
			reasons = append(reasons, fmt.Sprintf("Experiment %q was deleted; its frozen results are still shown.", name))
			continue
		}
		live, err := s.store.GetComparisonSource(r.Context(), frozen.ID)
		if err != nil {
			return nil, err
		}
		reasons = append(reasons, sourceStaleReasons(frozen, live)...)
	}
	return reasons, nil
}

func sourceStaleReasons(frozen, live domain.Experiment) []string {
	reasons := []string{}
	name := frozen.Name
	if live.Name != frozen.Name {
		reasons = append(reasons, fmt.Sprintf("Experiment %q was renamed to %q.", frozen.Name, live.Name))
	}
	liveVariants := map[string]domain.ExperimentVariant{}
	for _, variant := range live.Variants {
		liveVariants[variant.ID] = variant
	}
	for _, variant := range frozen.Variants {
		label := name
		if len(live.Variants) > 1 || len(frozen.Variants) > 1 {
			label = name + " · " + firstNonEmpty(variant.Alias, variant.Name)
		}
		current, ok := liveVariants[variant.ID]
		if !ok {
			reasons = append(reasons, fmt.Sprintf("%s: the configuration was removed from the experiment.", label))
			continue
		}
		if current.Name != variant.Name || current.Alias != variant.Alias || current.Color != variant.Color {
			reasons = append(reasons, fmt.Sprintf("%s: the name, alias or color changed.", label))
		}
		if canonicalJSON(current.Configuration) != canonicalJSON(variant.Configuration) {
			reasons = append(reasons, fmt.Sprintf("%s: the recorded configuration changed.", label))
		}
		liveTrials := map[string]domain.ExperimentTrial{}
		for _, trial := range current.Trials {
			liveTrials[trial.ID] = trial
		}
		selected := map[string]bool{}
		changed, removed := 0, 0
		for _, trial := range variant.Trials {
			selected[trial.ID] = true
			now, ok := liveTrials[trial.ID]
			switch {
			case !ok:
				removed++
			case now.Status != "succeeded" || now.ResultArtifactID != trial.ResultArtifactID:
				changed++
			}
		}
		added := 0
		for _, trial := range current.Trials {
			if !selected[trial.ID] && trial.Status == "succeeded" && trial.ResultArtifactID != "" && !trial.Pilot {
				added++
			}
		}
		if removed > 0 {
			reasons = append(reasons, fmt.Sprintf("%s: %s removed from the experiment.", label, plural(removed, "frozen run was", "frozen runs were")))
		}
		if changed > 0 {
			reasons = append(reasons, fmt.Sprintf("%s: %s re-run or no longer completed.", label, plural(changed, "frozen run was", "frozen runs were")))
		}
		if added > 0 {
			reasons = append(reasons, fmt.Sprintf("%s: %s not included.", label, plural(added, "newer completed run is", "newer completed runs are")))
		}
	}
	return reasons
}

func plural(count int, one, many string) string {
	if count == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", count, many)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// canonicalJSON normalizes key order and whitespace, so JSON read back from
// jsonb compares equal to the JSON that was stored.
func canonicalJSON(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "null"
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	normalized, _ := json.Marshal(value)
	return string(normalized)
}

// refreshComparison re-resolves the comparison from the current sources and
// replaces its snapshot. It also freezes legacy comparisons.
func (s *Server) refreshComparison(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.GetResultComparison(r.Context(), r.PathValue("id"), requestUser(r).ID)
	if err != nil {
		comparisonStorageError(w, err)
		return
	}
	live, err := s.liveComparisonView(r, item)
	if err != nil {
		writeError(w, 409, "source_unavailable", err.Error())
		return
	}
	item.Snapshot = captureComparisonSnapshot(live)
	saved, err := s.store.SaveResultComparison(r.Context(), item, true)
	if err != nil {
		comparisonStorageError(w, err)
		return
	}
	view, err := s.comparisonSnapshotView(r, saved)
	if err != nil {
		writeError(w, 409, "source_unavailable", err.Error())
		return
	}
	writeJSON(w, 200, view)
}
