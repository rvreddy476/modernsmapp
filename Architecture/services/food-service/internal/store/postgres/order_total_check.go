package postgres

import (
	"errors"
	"fmt"
)

// Food server lane (9 Nov 2026), food audit L2: the customer's app sends the
// total it showed (expected_total_paise). If the server prices the order
// differently (a menu price, a fee or tax changed while they checked out),
// placement is refused before the order exists, with the server's total, so
// the app says "Your total has changed" before anything can be charged. The
// server's own total is always the one charged; the client's figure is only
// ever compared, never used.

// ErrOrderTotalChanged is the sentinel every OrderTotalChangedError matches.
var ErrOrderTotalChanged = errors.New("the order total has changed")

// OrderTotalChangedError carries both totals, in paise.
type OrderTotalChangedError struct {
	ExpectedTotalPaise int64
	ServerTotalPaise   int64
}

func (e *OrderTotalChangedError) Error() string {
	return fmt.Sprintf("the order total has changed: you were shown %d paise, it is now %d paise",
		e.ExpectedTotalPaise, e.ServerTotalPaise)
}

// Is makes errors.Is(err, ErrOrderTotalChanged) hold.
func (e *OrderTotalChangedError) Is(target error) bool { return target == ErrOrderTotalChanged }

// CheckExpectedTotal refuses a placement whose expected total differs from
// the server's. No expectation (an older client) is never refused.
func CheckExpectedTotal(expectedPaise *int64, serverPaise int64) error {
	if expectedPaise == nil || *expectedPaise == serverPaise {
		return nil
	}
	return &OrderTotalChangedError{ExpectedTotalPaise: *expectedPaise, ServerTotalPaise: serverPaise}
}
