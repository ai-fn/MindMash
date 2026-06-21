package collection

import (
	"encoding/json"
	"time"

	"github.com/mindmesh/mindmesh/internal/platform/events"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
)

// Event type constants used as the event_type column in the outbox.
const (
	EventNoteTypeCreated = "note_type.created"
	EventNoteTypeUpdated = "note_type.updated"
	EventNoteTypeDeleted = "note_type.deleted"

	EventNoteCreated = "note.created"
	EventNoteUpdated = "note.updated"
	EventNoteDeleted = "note.deleted"

	EventCardCreated = "card.created"
	EventCardUpdated = "card.updated"
	EventCardDeleted = "card.deleted"

	EventDeckCreated = "deck.created"
	EventDeckUpdated = "deck.updated"
	EventDeckDeleted = "deck.deleted"

	EventDeckConfigCreated = "deck_config.created"
	EventDeckConfigUpdated = "deck_config.updated"
	EventDeckConfigDeleted = "deck_config.deleted"

	EventFilteredDeckCreated = "filtered_deck.created"
	EventFilteredDeckDeleted = "filtered_deck.deleted"
)

type noteTypePayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProfileID string    `json:"profile_id"`
	USN       int64     `json:"usn"`
	Mtime     time.Time `json:"mtime"`
}

type notePayload struct {
	ID         string    `json:"id"`
	UserID     string    `json:"user_id"`
	ProfileID  string    `json:"profile_id"`
	NoteTypeID string    `json:"note_type_id"`
	USN        int64     `json:"usn"`
	Mtime      time.Time `json:"mtime"`
}

type cardPayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProfileID string    `json:"profile_id"`
	NoteID    string    `json:"note_id"`
	DeckID    string    `json:"deck_id"`
	USN       int64     `json:"usn"`
	Mtime     time.Time `json:"mtime"`
}

type deckPayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProfileID string    `json:"profile_id"`
	USN       int64     `json:"usn"`
	Mtime     time.Time `json:"mtime"`
}

type deckConfigPayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProfileID string    `json:"profile_id"`
	USN       int64     `json:"usn"`
	Mtime     time.Time `json:"mtime"`
}

type filteredDeckPayload struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ProfileID string    `json:"profile_id"`
	USN       int64     `json:"usn"`
	Mtime     time.Time `json:"mtime"`
}

func mustMarshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic("collection events: marshal payload: " + err.Error())
	}
	return b
}

func noteTypeEntry(nt *NoteType, eventType string) events.OutboxEntry {
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        nt.UserID,
		AggregateType: "note_type",
		AggregateID:   nt.ID,
		EventType:     eventType,
		Payload: mustMarshal(noteTypePayload{
			ID: nt.ID, UserID: nt.UserID, ProfileID: nt.ProfileID,
			USN: nt.USN, Mtime: nt.Mtime,
		}),
		CreatedAt: time.Now().UTC(),
	}
}

func noteEntry(n *Note, eventType string) events.OutboxEntry {
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        n.UserID,
		AggregateType: "note",
		AggregateID:   n.ID,
		EventType:     eventType,
		Payload: mustMarshal(notePayload{
			ID: n.ID, UserID: n.UserID, ProfileID: n.ProfileID,
			NoteTypeID: n.NoteTypeID, USN: n.USN, Mtime: n.Mtime,
		}),
		CreatedAt: time.Now().UTC(),
	}
}

func cardEntry(c *Card, eventType string) events.OutboxEntry {
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        c.UserID,
		AggregateType: "card",
		AggregateID:   c.ID,
		EventType:     eventType,
		Payload: mustMarshal(cardPayload{
			ID: c.ID, UserID: c.UserID, ProfileID: c.ProfileID,
			NoteID: c.NoteID, DeckID: c.DeckID, USN: c.USN, Mtime: c.Mtime,
		}),
		CreatedAt: time.Now().UTC(),
	}
}

func deckEntry(d *Deck, eventType string) events.OutboxEntry {
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        d.UserID,
		AggregateType: "deck",
		AggregateID:   d.ID,
		EventType:     eventType,
		Payload: mustMarshal(deckPayload{
			ID: d.ID, UserID: d.UserID, ProfileID: d.ProfileID,
			USN: d.USN, Mtime: d.Mtime,
		}),
		CreatedAt: time.Now().UTC(),
	}
}

func deckConfigEntry(dc *DeckConfig, eventType string) events.OutboxEntry {
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        dc.UserID,
		AggregateType: "deck_config",
		AggregateID:   dc.ID,
		EventType:     eventType,
		Payload: mustMarshal(deckConfigPayload{
			ID: dc.ID, UserID: dc.UserID, ProfileID: dc.ProfileID,
			USN: dc.USN, Mtime: dc.Mtime,
		}),
		CreatedAt: time.Now().UTC(),
	}
}

func filteredDeckEntry(fd *FilteredDeck, eventType string) events.OutboxEntry {
	return events.OutboxEntry{
		ID:            ids.NewUUID(), // outbox.id is UUID, not ULID
		UserID:        fd.UserID,
		AggregateType: "filtered_deck",
		AggregateID:   fd.ID,
		EventType:     eventType,
		Payload: mustMarshal(filteredDeckPayload{
			ID: fd.ID, UserID: fd.UserID, ProfileID: fd.ProfileID,
			USN: fd.USN, Mtime: fd.Mtime,
		}),
		CreatedAt: time.Now().UTC(),
	}
}
