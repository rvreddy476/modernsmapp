// Package apperr carries a stable, client-facing error (HTTP status + UPPER_SNAKE
// code) from the service layer to the HTTP writer. Codes are the contract in
// contracts/doorstep/openapi.yaml x-doorstep-error-codes.
package apperr

import "net/http"

// Error is a client-facing error. Details is serialised as error.details.
type Error struct {
	Status  int
	Code    string
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// New builds an Error without details.
func New(status int, code, message string) *Error {
	return &Error{Status: status, Code: code, Message: message}
}

// WithDetails returns a copy of e carrying details.
func (e *Error) WithDetails(d map[string]any) *Error {
	cp := *e
	cp.Details = d
	return &cp
}

// Doorstep error codes (stable; see the OpenAPI x-doorstep-error-codes).
const (
	CodeInvalidRequest       = "DOORSTEP_INVALID_REQUEST"
	CodeCityNotFound         = "DOORSTEP_CITY_NOT_FOUND"
	CodeCategoryNotFound     = "DOORSTEP_CATEGORY_NOT_FOUND"
	CodeServiceNotFound      = "DOORSTEP_SERVICE_NOT_FOUND"
	CodeServiceNotAvailable  = "DOORSTEP_SERVICE_NOT_AVAILABLE"
	CodeOutsideServiceArea   = "DOORSTEP_OUTSIDE_SERVICE_AREA"
	CodeOptionInvalid        = "DOORSTEP_OPTION_INVALID"
	CodeQuantityInvalid      = "DOORSTEP_QUANTITY_INVALID"
	CodeAddonInvalid         = "DOORSTEP_ADDON_INVALID"
	CodeQuoteNotFound        = "DOORSTEP_QUOTE_NOT_FOUND"
	CodeQuoteExpired         = "DOORSTEP_QUOTE_EXPIRED"
	CodeSlotTaken            = "DOORSTEP_SLOT_TAKEN"
	CodeOutstandingDue       = "DOORSTEP_OUTSTANDING_DUE"
	CodeOTPLocked            = "DOORSTEP_OTP_LOCKED"
	CodePhotosRequired       = "DOORSTEP_PHOTOS_REQUIRED"
	CodeGenderRule           = "DOORSTEP_GENDER_RULE"
	CodeNotFound             = "DOORSTEP_NOT_FOUND"
	CodeConflict             = "DOORSTEP_CONFLICT"
	CodeZoneInvalid          = "DOORSTEP_ZONE_INVALID"
	CodePriceOverlap         = "DOORSTEP_PRICE_OVERLAP"
	CodeInternal             = "DOORSTEP_INTERNAL"
	ReasonOutsideServiceArea = "OUTSIDE_SERVICE_AREA"
)

// Invalid is a 400 DOORSTEP_INVALID_REQUEST naming the offending field.
func Invalid(field, message string) *Error {
	e := New(http.StatusBadRequest, CodeInvalidRequest, message)
	if field != "" {
		e.Details = map[string]any{"field": field}
	}
	return e
}

// Internal is the opaque 500.
func Internal() *Error {
	return New(http.StatusInternalServerError, CodeInternal, "internal error")
}
