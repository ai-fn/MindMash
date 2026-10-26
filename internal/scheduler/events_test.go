package scheduler

import (
	"encoding/json"
	"testing"
	"time"
)

// Analytics consumes these fields by name: changing them breaks it.
func TestReviewLogEntry_PayloadContract(t *testing.T) {
	ts := time.Date(2026, 6, 24, 12, 0, 0, 0, time.UTC)
	rl := &ReviewLog{
		ID:        "rev-1",
		UserID:    "user-1",
		ProfileID: "prof-1",
		CardID:    "card-1",
		DeckID:    "deck-1",
		Grade:     Good,
		PrevState: StateLearning,
		NewState:  StateReview,
		PrevIvl:   60,
		NewIvl:    86400,
		TimeMs:    4200,
		Ts:        ts,
	}

	entry := reviewLogEntry(rl)
	if entry.EventType != EventReviewRecorded {
		t.Fatalf("event type = %q, want %q", entry.EventType, EventReviewRecorded)
	}
	if entry.AggregateType != "review_log" {
		t.Fatalf("aggregate type = %q, want review_log", entry.AggregateType)
	}
	if entry.UserID != rl.UserID || entry.AggregateID != rl.ID {
		t.Fatalf("entry routing = (%q,%q), want (%q,%q)", entry.UserID, entry.AggregateID, rl.UserID, rl.ID)
	}

	var got reviewLogPayload
	if err := json.Unmarshal(entry.Payload, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	want := reviewLogPayload{
		ID: "rev-1", UserID: "user-1", ProfileID: "prof-1", CardID: "card-1",
		DeckID: "deck-1", Grade: int8(Good), PrevState: int16(StateLearning),
		NewState: int16(StateReview), NewIvl: 86400, TimeMs: 4200, Ts: ts,
	}
	if got != want {
		t.Fatalf("payload = %+v, want %+v", got, want)
	}
}
