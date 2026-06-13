package ids_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/mindmesh/mindmesh/internal/platform/ids"
	"github.com/oklog/ulid/v2"
)

func TestNewULID(t *testing.T) {
	a, b := ids.NewULID(), ids.NewULID()

	if len(a) != 26 {
		t.Errorf("expected ULID length 26, got %d", len(a))
	}
	if len(b) != 26 {
		t.Errorf("expected ULID length 26, got %d", len(b))
	}
	if a == "" || b == "" {
		t.Error("expected non-empty ULIDs")
	}
	if a == b {
		t.Error("expected two distinct ULIDs")
	}

	if _, err := ulid.ParseStrict(a); err != nil {
		t.Errorf("should be valid ULID: %v", err)
	}
}

func TestNewUUID(t *testing.T) {
	a, b := ids.NewUUID(), ids.NewUUID()

	if a == "" || b == "" {
		t.Error("expected non-empty UUIDs")
	}
	if a == b {
		t.Error("expected two distinct UUIDs")
	}

	if _, err := uuid.Parse(a); err != nil {
		t.Errorf("first UUID is not valid: %v", err)
	}
	if _, err := uuid.Parse(b); err != nil {
		t.Errorf("second UUID is not valid: %v", err)
	}

	parsed, err := uuid.Parse(a)
	if err != nil {
		t.Fatalf("failed to parse UUID for version check: %v", err)
	}
	if got := parsed.Version(); got != uuid.Version(7) {
		t.Errorf("expected UUIDv7, got version %d", got)
	}
}
