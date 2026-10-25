package syncer

import (
	"slices"
	"sort"
	"testing"
)

// Collection's and Scheduler's aggregate types can't be imported here; if either changes, update
// this list and notifyTopics together, or SSE silently stops nudging for that type.
func TestNotifyTopics_MatchesKnownAggregateTypes(t *testing.T) {
	want := []string{
		// internal/collection/events.go
		"note_type", "note", "card", "deck", "deck_config", "filtered_deck",
		// internal/scheduler/events.go ("card" is already listed above)
		"review_log",
	}

	got := slices.Clone(notifyTopics)
	sort.Strings(got)
	sort.Strings(want)
	want = slices.Compact(want)

	if !slices.Equal(got, want) {
		t.Fatalf("notifyTopics = %v, want %v (update both this test and notifier.go's notifyTopics together if Collection/Scheduler's aggregate types changed)", got, want)
	}
}
