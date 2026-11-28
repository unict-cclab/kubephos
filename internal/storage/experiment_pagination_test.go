package storage

import (
	"context"
	"testing"
)

func TestExperimentPagesIncludeOlderResultsAndPreserveVisibility(t *testing.T) {
	store, _ := batchedListStore(t)
	ctx := context.Background()
	_, err := store.pool.Exec(ctx, `CREATE FUNCTION can_view_experiment(viewer text, experiment_id text) RETURNS boolean LANGUAGE sql AS $$ SELECT EXISTS(SELECT 1 FROM experiments WHERE id=experiment_id AND (owner_user_id=viewer OR viewer='alice' AND id='experiment-29')) $$; UPDATE experiments SET status='queued' WHERE id<>'empty-experiment';`)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		user  string
		total int
	}{{"", 31}, {"alice", 17}, {"bob", 15}} {
		t.Run(tc.user, func(t *testing.T) {
			seen := map[string]bool{}
			for offset := 0; ; offset += 7 {
				page, err := store.ListExperimentsVisibleToUserPage(ctx, tc.user, 7, offset)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range page {
					if seen[item.ID] {
						t.Fatalf("duplicate experiment across pages: %s", item.ID)
					}
					seen[item.ID] = true
					if tc.user != "" && item.OwnerUserID != tc.user && !(tc.user == "alice" && item.ID == "experiment-29") {
						t.Fatalf("unshared experiment leaked: %s", item.ID)
					}
					if item.Variants == nil {
						t.Fatal("missing variant array")
					}
				}
				if len(page) < 7 {
					break
				}
			}
			if len(seen) != tc.total {
				t.Fatalf("got %d experiments, want %d", len(seen), tc.total)
			}
			if tc.user != "bob" && !seen["empty-experiment"] {
				t.Fatal("older completed experiment hidden behind scheduled runs")
			}
		})
	}
}
