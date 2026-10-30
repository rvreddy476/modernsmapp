package service

import (
	"errors"
	"fmt"
)

// Error kinds the HTTP layer maps to a status (internal/http/errors.go,
// writeServiceError). A refusal the service decides on carries one of them
// through errors.Is; anything without a kind is an infrastructure failure
// (database, Scylla, Redis, a peer service) and is answered 503 with a
// generic message, so driver text never reaches a client and a client never
// mistakes a blip for a permanent refusal.
var (
	// ErrInvalidInput is a request the caller can fix (400 INVALID_REQUEST).
	ErrInvalidInput = errors.New("invalid request")
	// ErrNotFound is something that does not exist for this caller (404).
	ErrNotFound = errors.New("not found")
	// ErrForbidden is an action the caller is not allowed to take (403).
	ErrForbidden = errors.New("forbidden")
	// ErrConflict is an action the current state does not allow (409).
	ErrConflict = errors.New("conflict")
)

// kindError is a client-facing refusal: Error() is the message shown to the
// client, Is() matches its kind.
type kindError struct {
	kind error
	msg  string
}

func (e *kindError) Error() string        { return e.msg }
func (e *kindError) Is(target error) bool { return target == e.kind }

func invalidInput(msg string) error { return &kindError{kind: ErrInvalidInput, msg: msg} }
func invalidInputf(format string, args ...any) error {
	return &kindError{kind: ErrInvalidInput, msg: fmt.Sprintf(format, args...)}
}
func notFound(msg string) error  { return &kindError{kind: ErrNotFound, msg: msg} }
func forbidden(msg string) error { return &kindError{kind: ErrForbidden, msg: msg} }
func conflict(msg string) error  { return &kindError{kind: ErrConflict, msg: msg} }
func conflictf(format string, args ...any) error {
	return &kindError{kind: ErrConflict, msg: fmt.Sprintf(format, args...)}
}
