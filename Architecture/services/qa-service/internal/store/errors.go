package store

import (
	"errors"
	"fmt"
)

// Refusals from the user-facing writes. Their text is the prefix the HTTP
// layer maps (respondServiceError): "not_found: ..." is a 404, "forbidden:
// ..." a 403, "conflict: ..." a 409, "invalid: ..." a 400. Anything else is
// a 500 whose detail is logged, never sent.
var (
	ErrNotFound  = errors.New("not_found")
	ErrForbidden = errors.New("forbidden")
	ErrConflict  = errors.New("conflict")
	ErrInvalid   = errors.New("invalid")
)

func refuse(kind error, msg string) error {
	return fmt.Errorf("%w: %s", kind, msg)
}
