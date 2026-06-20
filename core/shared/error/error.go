// Package coreerror provides a set of error types and functions for handling errors in a gRPC system.
package coreerror

import (
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Error represents a system error with a code and a message.
type Error struct {
	// CodeError is the error code.
	CodeError string
	// Message is the error message.
	Message string
}

// Error returns a string representation of the error.
func (e *Error) Error() string {
	return fmt.Sprintf("%s: %s", e.CodeError, e.Message)
}

// GRPCStatus returns a gRPC status object for the error.
func (e *Error) GRPCStatus(code codes.Code) *status.Status {
	return status.New(code, e.Error())
}

// newError creates a new error with the given code, message, and code error.
func newError(code codes.Code, msg, codeError string) error {
	return (&Error{
		CodeError: codeError,
		Message:   msg,
	}).GRPCStatus(code).Err()
}

// NewInvalidArgumentError creates a new error with an InvalidArgument code.
func NewInvalidArgumentError(codeError, msg string) error {
	return newError(codes.InvalidArgument, msg, codeError)
}

// NewNotFoundError creates a new error with a NotFound code.
func NewNotFoundError(codeError, msg string) error {
	return newError(codes.NotFound, msg, codeError)
}

// NewPermissionDeniedError creates a new error with a PermissionDenied code.
func NewPermissionDeniedError(codeError, msg string) error {
	return newError(codes.PermissionDenied, msg, codeError)
}

// NewUnauthenticatedError creates a new error with an Unauthenticated code.
func NewUnauthenticatedError(codeError, msg string) error {
	return newError(codes.Unauthenticated, msg, codeError)
}

// NewInternalError creates a new error with an Internal code.
func NewInternalError(codeError, msg string) error {
	return newError(codes.Internal, msg, codeError)
}

// NewAbortedError creates a new error with an Aborted code.
func NewAbortedError(codeError, msg string) error {
	return newError(codes.Aborted, msg, codeError)
}

// NewOutOfRangeError creates a new error with an OutOfRange code.
func NewOutOfRangeError(codeError, msg string) error {
	return newError(codes.OutOfRange, msg, codeError)
}

// NewResourceExhaustedError creates a new error with a ResourceExhausted code.
func NewResourceExhaustedError(codeError, msg string) error {
	return newError(codes.ResourceExhausted, msg, codeError)
}

// NewAlreadyExistsError creates a new error with an AlreadyExists code.
func NewAlreadyExistsError(codeError, msg string) error {
	return newError(codes.AlreadyExists, msg, codeError)
}

// NewFailedPreconditionError creates a new error with a FailedPrecondition code.
func NewFailedPreconditionError(codeError, msg string) error {
	return newError(codes.FailedPrecondition, msg, codeError)
}
