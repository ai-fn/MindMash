package errors

import (
	"errors"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AppError is a domain error that carries both a gRPC status code and an
// optional wrapped cause. It satisfies the grpc Status interface so it can be
// returned directly from gRPC handlers and translated by grpc-gateway.
type AppError struct {
	Code    codes.Code
	Message string
	Err     error
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

func Unimplemented(msg string, cause ...error) *AppError {
	return newAppError(codes.Unimplemented, msg, cause)
}

// grpcToHTTP mirrors the mapping used by grpc-gateway's runtime.HTTPStatusFromCode.
var grpcToHTTP = map[codes.Code]int{
	codes.NotFound:           http.StatusNotFound,
	codes.AlreadyExists:      http.StatusConflict,
	codes.Aborted:            http.StatusConflict,
	codes.FailedPrecondition: http.StatusBadRequest,
	codes.InvalidArgument:    http.StatusBadRequest,
	codes.Unauthenticated:    http.StatusUnauthorized,
	codes.PermissionDenied:   http.StatusForbidden,
	codes.Internal:           http.StatusInternalServerError,
	codes.Unavailable:        http.StatusServiceUnavailable,
	codes.Unimplemented:      http.StatusNotImplemented,
}

// HTTPStatus extracts the HTTP status code that corresponds to err.
// It unwraps the error chain looking for an *AppError; if none is found it
// returns 500.
func HTTPStatus(err error) int {
	var ae *AppError
	if errors.As(err, &ae) {
		if code, ok := grpcToHTTP[ae.Code]; ok {
			return code
		}
	}
	return http.StatusInternalServerError
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
