package errors_test

import (
	stderrors "errors"
	"net/http"
	"testing"

	"google.golang.org/grpc/codes"

	apperrors "github.com/mindmesh/mindmesh/internal/platform/errors"
)

func TestAppErrorConstructors(t *testing.T) {
	sentinel := stderrors.New("root cause")

	cases := []struct {
		name         string
		err          *apperrors.AppError
		wantGRPC     codes.Code
		wantHTTP     int
		wantMsgInErr bool
	}{
		{
			name:     "NotFound",
			err:      apperrors.NotFound("user not found", sentinel),
			wantGRPC: codes.NotFound,
			wantHTTP: http.StatusNotFound,
		},
		{
			name:     "AlreadyExists",
			err:      apperrors.AlreadyExists("email taken", sentinel),
			wantGRPC: codes.AlreadyExists,
			wantHTTP: http.StatusConflict,
		},
		{
			name:     "InvalidArgument",
			err:      apperrors.InvalidArgument("bad input", sentinel),
			wantGRPC: codes.InvalidArgument,
			wantHTTP: http.StatusBadRequest,
		},
		{
			name:     "FailedPrecondition",
			err:      apperrors.FailedPrecondition("note type in use", sentinel),
			wantGRPC: codes.FailedPrecondition,
			wantHTTP: http.StatusBadRequest,
		},
		{
			name:     "Aborted",
			err:      apperrors.Aborted("stale write", sentinel),
			wantGRPC: codes.Aborted,
			wantHTTP: http.StatusConflict,
		},
		{
			name:     "Unauthenticated",
			err:      apperrors.Unauthenticated("missing token", sentinel),
			wantGRPC: codes.Unauthenticated,
			wantHTTP: http.StatusUnauthorized,
		},
		{
			name:     "PermissionDenied",
			err:      apperrors.PermissionDenied("forbidden", sentinel),
			wantGRPC: codes.PermissionDenied,
			wantHTTP: http.StatusForbidden,
		},
		{
			name:     "Internal",
			err:      apperrors.Internal("db failure", sentinel),
			wantGRPC: codes.Internal,
			wantHTTP: http.StatusInternalServerError,
		},
		{
			name:     "Unavailable",
			err:      apperrors.Unavailable("service down", sentinel),
			wantGRPC: codes.Unavailable,
			wantHTTP: http.StatusServiceUnavailable,
		},
		{
			name:     "ResourceExhausted",
			err:      apperrors.ResourceExhausted("slow down", sentinel),
			wantGRPC: codes.ResourceExhausted,
			wantHTTP: http.StatusTooManyRequests,
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

			if got := apperrors.HTTPStatus(tc.err); got != tc.wantHTTP {
				t.Errorf("HTTPStatus: got %d, want %d", got, tc.wantHTTP)
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
	ae := apperrors.Internal("operation failed", wrapped)
	outer := stderrors.Join(stderrors.New("handler:"), ae)

	var target *apperrors.AppError
	if !stderrors.As(outer, &target) {
		t.Fatal("errors.As should find *AppError through the join wrapper")
	}
	if target.Code != codes.Internal {
		t.Errorf("expected Internal, got %v", target.Code)
	}
}

func TestHTTPStatusNoCause(t *testing.T) {
	err := apperrors.NotFound("not found")
	if err.Unwrap() != nil {
		t.Error("Unwrap should return nil when no cause provided")
	}
	if got := apperrors.HTTPStatus(err); got != http.StatusNotFound {
		t.Errorf("HTTPStatus without cause: got %d, want 404", got)
	}
}

func TestHTTPStatusUnknownError(t *testing.T) {
	plain := stderrors.New("something went wrong")
	if got := apperrors.HTTPStatus(plain); got != http.StatusInternalServerError {
		t.Errorf("HTTPStatus for non-AppError: got %d, want 500", got)
	}
}

func TestAppErrorWithCause(t *testing.T) {
	cause := stderrors.New("db timeout")
	err := apperrors.NotFound("user not found", cause)
	want := "user not found: db timeout"
	if got := err.Error(); got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
