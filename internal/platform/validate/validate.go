// Package validate holds request-boundary checks that turn bad input into InvalidArgument instead of a DB error 500.
package validate

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

const (
	// MaxBulkIDs caps id lists so one request can't lock or load an unbounded number of rows.
	MaxBulkIDs = 1000
	// MaxQueryBytes caps search DSL input.
	MaxQueryBytes = 1024
)

// IsUUID rejects the braced, urn:, uppercase and dashless spellings uuid.Parse also accepts.
func IsUUID(s string) bool {
	u, err := uuid.Parse(s)
	return err == nil && u.String() == s
}

func ID(field, s string) error {
	if !IsUUID(s) {
		return apperr.InvalidArgument(field + " must be a UUID")
	}
	return nil
}

// OptionalID accepts "" as unset.
func OptionalID(field, s string) error {
	if s == "" {
		return nil
	}
	return ID(field, s)
}

func IDs(field string, ids []string) error {
	if len(ids) > MaxBulkIDs {
		return apperr.InvalidArgument(fmt.Sprintf("%s: at most %d ids", field, MaxBulkIDs))
	}
	for i, id := range ids {
		if !IsUUID(id) {
			return apperr.InvalidArgument(fmt.Sprintf("%s[%d] must be a UUID", field, i))
		}
	}
	return nil
}

// Name requires a non-blank, single-line label of at most maxRunes characters.
func Name(field, s string, maxRunes int) error {
	if !utf8.ValidString(s) {
		return apperr.InvalidArgument(field + " must be valid UTF-8")
	}
	if strings.TrimSpace(s) == "" {
		return apperr.InvalidArgument(field + " is required")
	}
	if utf8.RuneCountInString(s) > maxRunes || strings.ContainsFunc(s, unicode.IsControl) {
		return apperr.InvalidArgument(fmt.Sprintf("%s must be at most %d characters on one line", field, maxRunes))
	}
	return nil
}

// Text bounds free text by bytes; NUL and invalid UTF-8 are rejected because Postgres text/JSONB can't store them.
func Text(field, s string, maxBytes int) error {
	if len(s) > maxBytes {
		return apperr.InvalidArgument(fmt.Sprintf("%s must be at most %d bytes", field, maxBytes))
	}
	if !utf8.ValidString(s) || strings.IndexByte(s, 0) >= 0 {
		return apperr.InvalidArgument(field + " must be valid UTF-8 without NUL")
	}
	return nil
}

func Count(field string, n, limit int) error {
	if n > limit {
		return apperr.InvalidArgument(fmt.Sprintf("%s: at most %d items", field, limit))
	}
	return nil
}
