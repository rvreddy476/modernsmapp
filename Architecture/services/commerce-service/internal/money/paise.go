// Package money is the single representation of currency in commerce-service.
//
// LB-19. Before this package, commerce carried money as float64 rupees and
// crossed the payments-service boundary via int64(math.Round(x*100)). Every
// hop was a lossy IEEE-754 round trip, and the two services disagreed about
// what a rupee was: payments-service has been paise-native since its
// migration 006, commerce was not.
//
// Paise is a distinct type, not an alias, so an int64 that means "quantity"
// or "seconds" cannot be assigned to a money field by accident. Division is
// deliberately absent: splitting money is never a plain divide (it must
// allocate a remainder), so that operation lives in internal/tax where the
// allocation rule is explicit and tested.
package money

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"strings"
)

// Paise is an amount in Indian paise. 100 paise = ₹1.
//
// A negative Paise is legal — it represents a debit direction in the
// inventory/refund ledgers — so callers that require a non-negative amount
// must assert it themselves. Use MustNonNegative at trust boundaries.
type Paise int64

// Zero is the additive identity, named so call sites read as intent rather
// than as a magic literal.
const Zero Paise = 0

// FromRupees converts whole rupees. It exists for constants and config
// (a ₹499 free-shipping threshold), never for arithmetic on a user amount.
func FromRupees(r int64) Paise { return Paise(r * 100) }

// Add returns a + b.
func (p Paise) Add(o Paise) Paise { return p + o }

// Sub returns a - b.
func (p Paise) Sub(o Paise) Paise { return p - o }

// MulQty scales an amount by an integer quantity. Quantity is the only
// multiplier money is ever legitimately scaled by in this domain — a
// percentage is applied through internal/tax, which owns the rounding rule.
func (p Paise) MulQty(qty int) Paise { return p * Paise(qty) }

// IsZero reports whether the amount is exactly zero.
func (p Paise) IsZero() bool { return p == 0 }

// IsNegative reports whether the amount is below zero.
func (p Paise) IsNegative() bool { return p < 0 }

// Int64 returns the raw paise count, for SQL parameters and provider APIs
// that take an integer minor unit (Razorpay's `amount`, for one).
func (p Paise) Int64() int64 { return int64(p) }

// String renders "₹1,234.56"-style text for logs and human-facing surfaces.
// It is never used to compute anything.
func (p Paise) String() string {
	neg := p < 0
	v := int64(p)
	if neg {
		v = -v
	}
	major, minor := v/100, v%100
	s := fmt.Sprintf("%d.%02d", major, minor)
	if neg {
		s = "-" + s
	}
	return s
}

// MarshalJSON emits a bare integer. The wire contract for every money field
// is minor units; a JSON number with a decimal point in a money field is a
// bug that this makes impossible to introduce silently.
func (p Paise) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(int64(p), 10)), nil
}

// UnmarshalJSON accepts an integer or an integer-valued string. It REJECTS a
// fractional number: "1234.56" in a paise field means the caller is still
// thinking in rupees, and silently truncating that is how the original
// float defect propagated.
func (p *Paise) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(strings.Trim(string(b), `"`))
	if s == "" || s == "null" {
		*p = 0
		return nil
	}
	if strings.ContainsAny(s, ".eE") {
		return fmt.Errorf("money: %q is not an integer minor amount; money must be sent in paise", s)
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("money: parse paise %q: %w", s, err)
	}
	*p = Paise(v)
	return nil
}

// Value implements driver.Valuer so Paise binds directly to a BIGINT column.
func (p Paise) Value() (driver.Value, error) { return int64(p), nil }

// Scan implements sql.Scanner for BIGINT columns.
//
// It deliberately accepts float64 as well, because during the dual-write
// migration window a value may still arrive from a NUMERIC(12,2) mirror.
// That path rounds half-away-from-zero exactly once, at the boundary, and is
// removed with the mirrors in the contraction phase.
func (p *Paise) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*p = 0
		return nil
	case int64:
		*p = Paise(v)
		return nil
	case int32:
		*p = Paise(v)
		return nil
	case float64:
		// Legacy NUMERIC rupees mirror. Round half away from zero.
		if v < 0 {
			*p = Paise(int64(v*100 - 0.5))
		} else {
			*p = Paise(int64(v*100 + 0.5))
		}
		return nil
	case []byte:
		return p.UnmarshalJSON(v)
	case string:
		return p.UnmarshalJSON([]byte(v))
	default:
		return fmt.Errorf("money: cannot scan %T into Paise", src)
	}
}

// MustNonNegative returns an error when the amount is negative. Trust
// boundaries (request bodies, provider responses, event payloads) call this
// before the value reaches the domain.
func MustNonNegative(field string, p Paise) error {
	if p < 0 {
		return fmt.Errorf("money: %s must not be negative (got %s)", field, p)
	}
	return nil
}

// Sum adds a slice, used for line totals.
func Sum(ps ...Paise) Paise {
	var t Paise
	for _, p := range ps {
		t += p
	}
	return t
}
