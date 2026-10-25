package apperr_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

func TestPublic(t *testing.T) {
	secret := errors.New("dial tcp 10.0.3.7:5432: password=hunter2")
	pg := func(code string) error {
		return fmt.Errorf("collection: get card: %w", &pgconn.PgError{Code: code, Message: `invalid input syntax for type uuid: "x"`})
	}
	tests := []struct {
		name     string
		err      error
		code     codes.Code
		msg      string
		logCause bool
	}{
		{"plain error", fmt.Errorf("repo: %w", secret), codes.Internal, "internal error", true},
		{"wrapped app error keeps only its message", fmt.Errorf("svc: %w", apperr.NotFound("card not found", secret)), codes.NotFound, "card not found", false},
		{"app error wins over a wrapped pg error", apperr.NotFound("card not found", pg("22P02")), codes.NotFound, "card not found", false},
		{"app internal is logged, its message hidden", apperr.Internal("render: " + secret.Error()), codes.Internal, "internal error", true},
		{"app unavailable", apperr.Unavailable("redis down", secret), codes.Unavailable, "service unavailable", true},
		// Unvalidated input reached SQL: answer 400, but log it as a missing check.
		{"bad text representation", pg("22P02"), codes.InvalidArgument, "invalid argument", true},
		{"other data exception", pg("22003"), codes.InvalidArgument, "invalid argument", true},
		{"serialization failure", pg("40001"), codes.Aborted, "conflict, retry", true},
		{"deadlock", pg("40P01"), codes.Aborted, "conflict, retry", true},
		{"unmapped pg error", pg("42P01"), codes.Internal, "internal error", true},
		{"pg error inside errors.Join", errors.Join(secret, pg("22P02")), codes.InvalidArgument, "invalid argument", true},
		{"downstream not found passes through", status.Error(codes.NotFound, "deck not found"), codes.NotFound, "deck not found", false},
		{"wrapped downstream keeps its code, not the wrapper text", fmt.Errorf("sched: %w: %w", status.Error(codes.NotFound, "deck not found"), secret), codes.NotFound, "deck not found", false},
		{"downstream unauthenticated passes through", status.Error(codes.Unauthenticated, "token expired"), codes.Unauthenticated, "token expired", false},
		{"downstream permission denied passes through", status.Error(codes.PermissionDenied, "not yours"), codes.PermissionDenied, "not yours", false},
		// A rejected internal request is the calling service's bug, not the user's.
		{"downstream invalid argument is our bug", status.Error(codes.InvalidArgument, "too many card_ids"), codes.Internal, "internal error", true},
		{"downstream resource exhausted from grpc-go", status.Error(codes.ResourceExhausted, "received message larger than max (5 vs 4)"), codes.Internal, "internal error", true},
		{"downstream internal was logged where it happened", status.Error(codes.Internal, "internal error"), codes.Internal, "internal error", false},
		{"downstream unavailable hides transport detail", status.Error(codes.Unavailable, "connection error: "+secret.Error()), codes.Unavailable, "service unavailable", true},
		{"downstream unknown", status.Error(codes.Unknown, secret.Error()), codes.Internal, "internal error", true},
		{"downstream data loss", status.Error(codes.DataLoss, secret.Error()), codes.Internal, "internal error", true},
		{"canceled", fmt.Errorf("q: %w", context.Canceled), codes.Canceled, "request canceled", false},
		{"deadline", fmt.Errorf("q: %w", context.DeadlineExceeded), codes.DeadlineExceeded, "deadline exceeded", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pub, logCause := apperr.Public(tc.err)
			st := status.Convert(pub)
			if st.Code() != tc.code || st.Message() != tc.msg || logCause != tc.logCause {
				t.Fatalf("got %v %q logCause=%v, want %v %q logCause=%v", st.Code(), st.Message(), logCause, tc.code, tc.msg, tc.logCause)
			}
			if strings.Contains(st.Message(), "hunter2") || strings.Contains(st.Message(), "10.0.3.7") {
				t.Fatalf("leaked cause: %q", st.Message())
			}
		})
	}
	if pub, logCause := apperr.Public(nil); pub != nil || logCause {
		t.Fatalf("nil: got %v logCause=%v", pub, logCause)
	}
	var ae *apperr.AppError
	if pub, _ := apperr.Public(fmt.Errorf("x: %w", apperr.Unauthenticated("bad login").WithMsgID("m"))); !errors.As(pub, &ae) || ae.MsgID != "m" {
		t.Fatalf("an app error must come back unwrapped so edges can still localize it, got %#v", pub)
	}
}
