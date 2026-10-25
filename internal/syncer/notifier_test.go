package syncer

import (
	"slices"
	"sort"
	"testing"
)

// TestNotifyTopics_MatchesKnownAggregateTypes pins notifyTopics to the exact
// AggregateType strings currently emitted by Collection's and Scheduler's
// outbox events (internal/collection/events.go, internal/scheduler/events.go).
// internal/<service> packages can't be imported cross-service, so this list
// can't be checked at compile time — this test is the deliberate, visible
// substitute: if either service adds, removes, or renames an aggregate type,
// update this test's expected list AND notifyTopics together, or the SSE
// notifier will silently stop nudging clients for that entity type.
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
