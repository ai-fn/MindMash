package ids

import (
	"github.com/google/uuid"
	"github.com/oklog/ulid/v2"
)

// NewULID returns a new cryptographically random, time-sortable ULID string.
func NewULID() string {
	return ulid.Make().String()
}

// NewUUID returns a new time-sortable UUIDv7 formatted as a lowercase hyphenated string.
// Panics only if the system entropy source is broken, which is unrecoverable anyway.
func NewUUID() string {
	id, err := uuid.NewV7()
	if err != nil {
		panic("ids: failed to generate UUIDv7: " + err.Error())
	}
	return id.String()
}
