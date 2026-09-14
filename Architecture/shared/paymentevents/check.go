package paymentevents

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// ErrMismatch matches every *MismatchError.
var ErrMismatch = errors.New("paymentevents: payment does not match the record")

// Mismatch fields.
const (
	FieldAmount   = "amount"
	FieldCurrency = "currency"
	FieldPayer    = "payer"
	FieldIntent   = "intent"
)

// MismatchError is a refused money check. Error() is the detail string a
// consumer records (food writes it to its inbox row's detail column).
type MismatchError struct {
	Field  string
	Detail string
}

func (e *MismatchError) Error() string { return e.Detail }

// Is matches ErrMismatch.
func (e *MismatchError) Is(target error) bool { return target == ErrMismatch }

// Expected is what the consumer's own record says, read inside the applying
// transaction.
type Expected struct {
	// AmountMinor is the record's total in minor units.
	AmountMinor int64
	// Currency is the record's currency.
	Currency string
	// PayerID is the record's customer.
	PayerID uuid.UUID
	// IntentID is the intent bound to the record; empty when none is bound,
	// in which case the intent is not compared.
	IntentID string
}

// Observed is what the payment event claims.
type Observed struct {
	AmountMinor int64
	Currency    string
	PayerID     uuid.UUID
	IntentID    string
}

// Policy decides how an event that does not STATE a value is treated.
type Policy int

const (
	// RequireStated: a blank currency, a nil payer, or a blank intent when
	// the record has one bound, is a mismatch. food-service's rule.
	RequireStated Policy = iota
	// AllowUnstated: a blank currency, a nil payer or a blank intent on the
	// event is not compared; only a stated, different value is a mismatch.
	// commerce-service's rule.
	AllowUnstated
)

// CheckCapture verifies a captured payment (payment.succeeded) against the
// record before anything is marked paid: amount, then currency
// (case-insensitive), then payer, then intent. The first difference is
// returned as a *MismatchError; nil means the payment is this record's.
//
// Checking only the amount is not enough: a payment of the right amount for
// a different record, or by a different payer, must not settle this one.
func CheckCapture(want Expected, got Observed, policy Policy) error {
	if got.AmountMinor != want.AmountMinor {
		return mismatch(FieldAmount, "amount_minor %d != order %d", got.AmountMinor, want.AmountMinor)
	}
	switch {
	case got.Currency == "" && policy == AllowUnstated:
	case got.Currency == "" || !strings.EqualFold(got.Currency, want.Currency):
		return mismatch(FieldCurrency, "currency %q != order %q", got.Currency, want.Currency)
	}
	switch {
	case got.PayerID == uuid.Nil && policy == AllowUnstated:
	case got.PayerID == uuid.Nil || got.PayerID != want.PayerID:
		return mismatch(FieldPayer, "payer %s is not the order's customer", got.PayerID)
	}
	switch {
	case want.IntentID == "":
	case got.IntentID == "" && policy == AllowUnstated:
	case got.IntentID != want.IntentID:
		return mismatch(FieldIntent, "intent %s is not the order's intent", got.IntentID)
	}
	return nil
}

// CheckRefund verifies a settled refund (payment.refunded) against the
// record: the refund amount must be positive and no more than the record's
// total, and a bound intent must be the refunded one. want.Currency and
// want.PayerID are not used (the refund payload carries neither).
func CheckRefund(want Expected, got Observed) error {
	if got.AmountMinor <= 0 || got.AmountMinor > want.AmountMinor {
		return mismatch(FieldAmount, "refund amount_minor %d outside order %d", got.AmountMinor, want.AmountMinor)
	}
	if want.IntentID != "" && got.IntentID != want.IntentID {
		return mismatch(FieldIntent, "refund intent %s is not the order's intent", got.IntentID)
	}
	return nil
}

func mismatch(field, format string, args ...any) error {
	return &MismatchError{Field: field, Detail: fmt.Sprintf(format, args...)}
}
