package apperr_test

import (
	stderrors "errors"
	"testing"

	"google.golang.org/grpc/codes"

	"github.com/mindmesh/mindmesh/internal/platform/apperr"
)

func TestAppErrorConstructors(t *testing.T) {
	sentinel := stderrors.New("root cause")

	cases := []struct {
		name         string
		err          *apperr.AppError
		wantGRPC     codes.Code
		wantMsgInErr bool
	}{
		{
			name:     "NotFound",
			err:      apperr.NotFound("user not found", sentinel),
			wantGRPC: codes.NotFound,
		},
		{
			name:     "AlreadyExists",
			err:      apperr.AlreadyExists("email taken", sentinel),
			wantGRPC: codes.AlreadyExists,
		},
		{
			name:     "InvalidArgument",
			err:      apperr.InvalidArgument("bad input", sentinel),
			wantGRPC: codes.InvalidArgument,
		},
		{
			name:     "FailedPrecondition",
			err:      apperr.FailedPrecondition("note type in use", sentinel),
			wantGRPC: codes.FailedPrecondition,
		},
		{
			name:     "Aborted",
			err:      apperr.Aborted("stale write", sentinel),
			wantGRPC: codes.Aborted,
		},
		{
			name:     "Unauthenticated",
			err:      apperr.Unauthenticated("missing token", sentinel),
			wantGRPC: codes.Unauthenticated,
		},
		{
			name:     "PermissionDenied",
			err:      apperr.PermissionDenied("forbidden", sentinel),
			wantGRPC: codes.PermissionDenied,
		},
		{
			name:     "Internal",
			err:      apperr.Internal("db failure", sentinel),
			wantGRPC: codes.Internal,
		},
		{
			name:     "Unavailable",
			err:      apperr.Unavailable("service down", sentinel),
			wantGRPC: codes.Unavailable,
		},
		{
			name:     "ResourceExhausted",
			err:      apperr.ResourceExhausted("slow down", sentinel),
			wantGRPC: codes.ResourceExhausted,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.err.Code != tc.wantGRPC {
				t.Errorf("Code: got %v, want %v", tc.err.Code, tc.wantGRPC)
			}

			if tc.err.Message == "" {
				t.Error("Message must not be empty")
			}

			// GRPCStatus must carry the same code and message.
			gs := tc.err.GRPCStatus()
			if gs.Code() != tc.wantGRPC {
				t.Errorf("GRPCStatus.Code: got %v, want %v", gs.Code(), tc.wantGRPC)
			}
			if gs.Message() != tc.err.Message {
				t.Errorf("GRPCStatus.Message: got %q, want %q", gs.Message(), tc.err.Message)
			}
		})
	}
}

func TestErrorsAsWrapping(t *testing.T) {
	sentinel := stderrors.New("db: connection refused")
	wrapped := stderrors.New("service layer: " + sentinel.Error())
	// Simulate a higher layer wrapping an AppError.
	ae := apperr.Internal("operation failed", wrapped)
	outer := stderrors.Join(stderrors.New("handler:"), ae)

	var target *apperr.AppError
	if !stderrors.As(outer, &target) {
		t.Fatal("errors.As should find *AppError through the join wrapper")
	}
	if target.Code != codes.Internal {
		t.Errorf("expected Internal, got %v", target.Code)
	}
}

func TestAppErrorWithCause(t *testing.T) {
	cause := stderrors.New("db timeout")
	err := apperr.NotFound("user not found", cause)
	want := "user not found: db timeout"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
