package search

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
)

// encodeCursor round-trips the hit's sort values verbatim, so search_after matches the index's
// own representation.
func encodeCursor(sortValues []any) (string, error) {
	b, err := json.Marshal(sortValues)
	if err != nil {
		return "", fmt.Errorf("search: encode cursor: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// decodeCursor reverses encodeCursor. An empty cursor yields a nil slice (first
// page). A malformed cursor is a client error, surfaced to the caller.
func decodeCursor(cursor string) ([]any, error) {
	if cursor == "" {
		return nil, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, fmt.Errorf("search: decode cursor: %w", err)
	}
	var sortValues []any
	if err := json.Unmarshal(b, &sortValues); err != nil {
		return nil, fmt.Errorf("search: unmarshal cursor: %w", err)
	}
	return sortValues, nil
}
