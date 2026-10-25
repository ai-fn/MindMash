package apperr

import (
	"context"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"golang.org/x/text/message"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AppError is a domain error that carries both a gRPC status code and an
// optional wrapped cause. It satisfies the grpc Status interface so it can be
// returned directly from gRPC handlers and translated by grpc-gateway.
//
// Message is the English (fallback) text always present. For user-facing errors that should be
// localized, callers additionally set a MsgID (a platform/i18n message key) via WithMsgID; the
// HTTP edge then renders it in the request's locale via Localize. Errors without a MsgID (the
// vast majority — internal/500-class) are unaffected and stay English.
type AppError struct {
	Code    codes.Code
	Message string
	Err     error
	MsgID   string
	Args    []any
}

func (e *AppError) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	return e.Err
}

// WithMsgID marks this error as localizable, tagging it with a platform/i18n message key and
// optional format args. Returns the same error for fluent use:
//
//	return apperr.Unauthenticated("invalid credentials").WithMsgID(i18n.MsgInvalidCredentials)
func (e *AppError) WithMsgID(id string, args ...any) *AppError {
	e.MsgID = id
	e.Args = args
	return e
}

// Localize renders the user-facing message: the localized MsgID via p when both are set,
// otherwise the plain English Message. p may be nil (e.g. no request locale resolved).
func (e *AppError) Localize(p *message.Printer) string {
	if e.MsgID != "" && p != nil {
		return p.Sprintf(e.MsgID, e.Args...)
	}
	return e.Message
}

// GRPCStatus implements the interface checked by status.FromError, allowing
// gRPC and grpc-gateway to map this error to the right HTTP/gRPC status codes
// without any extra middleware.
func (e *AppError) GRPCStatus() *status.Status {
	return status.New(e.Code, e.Message)
}

func newAppError(code codes.Code, msg string, cause []error) *AppError {
	var err error
	if len(cause) > 0 {
		err = cause[0]
	}
	return &AppError{Code: code, Message: msg, Err: err}
}

func NotFound(msg string, cause ...error) *AppError {
	return newAppError(codes.NotFound, msg, cause)
}

func AlreadyExists(msg string, cause ...error) *AppError {
	return newAppError(codes.AlreadyExists, msg, cause)
}

func InvalidArgument(msg string, cause ...error) *AppError {
	return newAppError(codes.InvalidArgument, msg, cause)
}

// FailedPrecondition signals that a resource is in a state incompatible with the
// operation — e.g. deleting a note type that still has notes, or a deck that
// still holds cards (an ON DELETE RESTRICT violation). Per the gRPC↔HTTP
// convention (and grpc-gateway's default mapping) this is HTTP 400, not 412/409.
// For optimistic-concurrency (expected_usn) conflicts use Aborted instead.
func FailedPrecondition(msg string, cause ...error) *AppError {
	return newAppError(codes.FailedPrecondition, msg, cause)
}

// Aborted signals that the operation was rejected due to a concurrency conflict
// the client can retry after refetching — e.g. an optimistic-concurrency
// (expected_usn) mismatch on a write. Maps to gRPC Aborted / HTTP 409.
func Aborted(msg string, cause ...error) *AppError {
	return newAppError(codes.Aborted, msg, cause)
}

func Unauthenticated(msg string, cause ...error) *AppError {
	return newAppError(codes.Unauthenticated, msg, cause)
}

func PermissionDenied(msg string, cause ...error) *AppError {
	return newAppError(codes.PermissionDenied, msg, cause)
}

func Internal(msg string, cause ...error) *AppError {
	return newAppError(codes.Internal, msg, cause)
}

func Unavailable(msg string, cause ...error) *AppError {
	return newAppError(codes.Unavailable, msg, cause)
}

// ResourceExhausted signals a rate limit or quota hit; maps to HTTP 429.
func ResourceExhausted(msg string, cause ...error) *AppError {
	return newAppError(codes.ResourceExhausted, msg, cause)
}

func Unimplemented(msg string, cause ...error) *AppError {
	return newAppError(codes.Unimplemented, msg, cause)
}

func is(err error, code codes.Code) bool {
	var ae *AppError
	return errors.As(err, &ae) && ae.Code == code
}

func IsNotFound(err error) bool { return is(err, codes.NotFound) }

func IsAlreadyExists(err error) bool { return is(err, codes.AlreadyExists) }

func IsInvalidArgument(err error) bool { return is(err, codes.InvalidArgument) }

func IsFailedPrecondition(err error) bool { return is(err, codes.FailedPrecondition) }

func IsAborted(err error) bool { return is(err, codes.Aborted) }

func IsUnauthenticated(err error) bool { return is(err, codes.Unauthenticated) }

// serverFaults hide their message from clients: it describes our internals, not the request.
var serverFaults = map[codes.Code]string{
	codes.Unknown:          "internal error",
	codes.Internal:         "internal error",
	codes.DataLoss:         "internal error",
	codes.Unavailable:      "service unavailable",
	codes.DeadlineExceeded: "deadline exceeded",
}

// ServerFault reports whether code describes our failure rather than the request.
func ServerFault(code codes.Code) bool { return serverFaults[code] != "" }

// downstreamPassthrough are the codes from an internal call that describe the user's request
// (its resource, its credentials); any other client code means the calling service erred.
var downstreamPassthrough = map[codes.Code]bool{
	codes.NotFound:         true,
	codes.AlreadyExists:    true,
	codes.Aborted:          true,
	codes.PermissionDenied: true,
	codes.Unauthenticated:  true,
	codes.Canceled:         true,
}

// Public returns what a client may see of err: the unwrapped *AppError for client errors (edges
// localize it), else a status with a fixed message. logCause reports that err itself should be
// logged: a server fault, or bad input that only SQL caught.
func Public(err error) (public error, logCause bool) {
	if err == nil {
		return nil, false
	}
	var ae *AppError
	if errors.As(err, &ae) {
		if ServerFault(ae.Code) {
			return status.Error(ae.Code, serverFaults[ae.Code]), true
		}
		return ae, false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case strings.HasPrefix(pgErr.Code, "22"): // data exception: the input, not the server
			return status.Error(codes.InvalidArgument, "invalid argument"), true
		case pgErr.Code == "40001" || pgErr.Code == "40P01":
			return status.Error(codes.Aborted, "conflict, retry"), true
		}
		return status.Error(codes.Internal, "internal error"), true
	}
	switch {
	case errors.Is(err, context.Canceled):
		return status.Error(codes.Canceled, "request canceled"), false
	case errors.Is(err, context.DeadlineExceeded):
		return status.Error(codes.DeadlineExceeded, "deadline exceeded"), true
	}
	// The inner status, not status.FromError: that one would surface the wrapper's text.
	var gs interface{ GRPCStatus() *status.Status }
	if errors.As(err, &gs) {
		st := gs.GRPCStatus()
		switch code := st.Code(); {
		case code == codes.Internal: // our services log their own faults (and recovery logs panics)
			return status.Error(codes.Internal, "internal error"), false
		case ServerFault(code):
			if code == codes.Unknown || code == codes.DataLoss {
				code = codes.Internal
			}
			return status.Error(code, serverFaults[st.Code()]), true
		case downstreamPassthrough[code]:
			return status.Error(code, st.Message()), false
		}
	}
	return status.Error(codes.Internal, "internal error"), true
}
