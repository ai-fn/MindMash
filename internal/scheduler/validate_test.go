package scheduler

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

// unreachableRepo panics on any call, so a case passes only if validation rejects the input first.
type unreachableRepo struct{ Repository }

func TestServiceRejectsInvalidInput(t *testing.T) {
	const (
		u   = "0190f3a8-7b2c-7d4e-9f00-123456789abc"
		bad = "not-a-uuid"
	)
	ctx := context.Background()
	s := NewService(unreachableRepo{}, nil, zap.NewNop())

	tests := []struct {
		name string
		call func() error
	}{
		{"BuildQueue deck", func() error { _, err := s.BuildQueue(ctx, u, u, bad, 10); return err }},
		{"BuildQueue empty deck", func() error { _, err := s.BuildQueue(ctx, u, u, "", 10); return err }},
		{"Preview card", func() error { _, err := s.Preview(ctx, u, bad); return err }},
		{"Answer card", func() error { _, _, err := s.Answer(ctx, u, u, bad, "", Good, 1000); return err }},
		{"Answer unspecified grade", func() error { _, _, err := s.Answer(ctx, u, u, u, "", 0, 1000); return err }},
		{"Answer grade above easy", func() error { _, _, err := s.Answer(ctx, u, u, u, "", Easy+1, 1000); return err }},
		{"Answer client review id", func() error { _, _, err := s.Answer(ctx, u, u, u, bad, Good, 1000); return err }},
		{"Reschedule unknown action", func() error { _, err := s.Reschedule(ctx, u, u, 0); return err }},
		{"GetCounts deck", func() error { _, err := s.GetCounts(ctx, u, bad); return err }},
		{"Reschedule card", func() error { _, err := s.Reschedule(ctx, u, bad, ActionSuspend); return err }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("reached the repository: input was not validated")
				}
			}()
			if err := tt.call(); !apperr.IsInvalidArgument(err) {
				t.Fatalf("want InvalidArgument, got %v", err)
			}
		})
	}
}
