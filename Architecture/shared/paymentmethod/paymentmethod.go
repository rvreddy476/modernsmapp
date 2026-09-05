// Package paymentmethod holds the P0 launch payment-method vocabulary.
//
// C3-LB-3. There was no single vocabulary before this package — there were
// four, and they disagreed:
//
//	Android enum               upi, card
//	commerce service           anything except "" and "cod"
//	commerce CHECK (gated 998) upi, card, net_banking
//	payments-service           upi, card
//
// Commerce forwards an order's `payment_method` verbatim to payments when it
// opens the intent, so `net_banking` passed the commerce handler, passed the
// CHECK, committed an order and RESERVED STOCK — and was then refused at
// intent creation. The buyer was left holding a `payment_pending` order they
// could not pay for, with inventory locked behind it until the expiry sweeper
// ran. Narrowing the Android enum hid the option; it did not close the hole,
// because a direct client never asked Android.
//
// So the list lives here, once, and every server-side authority imports it.
// The Android enum cannot import Go, so a CI gate diffs its wire values
// against Allowed() — see .github/workflows/integration-commerce.yml.
//
// # Widening this list
//
// Adding a method means adding it to payments-service FIRST (it needs a PSP
// leg that can actually be opened, captured, refunded and reconciled), then
// here, then to the gated CHECK, then to Android. In that order. Every method
// removed from this list was removed because it produced an order or an intent
// that no subsequent step could settle:
//
//	net_banking  commerce accepted it, payments did not
//	wallet       skipped provider-order creation, leaving a blank provider
//	             reference that can never be captured, refunded or reconciled
//	cod          has no PSP leg at all, and re-enabling it is a founder scope
//	             change requiring eligibility, value caps, fraud controls,
//	             failed-delivery restock and cash remittance ownership
//	escrow       same blank-reference problem as wallet
package paymentmethod

import (
	"fmt"
	"sort"
	"strings"
)

// The canonical launch methods. Lower-case, exactly as they travel on the
// wire and exactly as they are stored.
const (
	UPI  = "upi"
	Card = "card"
)

// allowed is the single source of truth. Everything else in this package —
// and every caller — derives from it.
var allowed = map[string]bool{
	UPI:  true,
	Card: true,
}

// ErrUnsupported is returned for any value outside the launch vocabulary.
// Callers map it onto their own transport error; the store and the database
// refuse independently, so this is the first of three refusals rather than
// the only one.
var ErrUnsupported = fmt.Errorf("payment method is not enabled for this launch")

// Allowed returns the vocabulary, sorted, for error messages, contract tests
// and the CI drift gate.
func Allowed() []string {
	out := make([]string, 0, len(allowed))
	for m := range allowed {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// IsAllowed reports whether m is exactly a launch method.
//
// Deliberately case-SENSITIVE and whitespace-sensitive. "UPI" is not accepted
// and is not silently folded to "upi": the value is written to a column with a
// CHECK constraint and compared by other services, so accepting a variant here
// would mean either storing a form the constraint rejects, or quietly
// rewriting what the caller asked for. A caller that sends "UPI" has a bug,
// and telling it so is more useful than guessing.
func IsAllowed(m string) bool { return allowed[m] }

// Validate returns nil for a launch method and a caller-facing error
// otherwise. The message names what IS allowed, because a client that sent
// the wrong thing needs to know the right thing.
func Validate(m string) error {
	if IsAllowed(m) {
		return nil
	}
	switch {
	case strings.TrimSpace(m) == "":
		return fmt.Errorf("%w: none was supplied (allowed: %s)",
			ErrUnsupported, strings.Join(Allowed(), ", "))
	case allowed[strings.ToLower(strings.TrimSpace(m))]:
		// A near miss is called out precisely, because "upi" vs "UPI" is
		// otherwise a maddening thing to debug against a 400.
		return fmt.Errorf("%w: %q is not canonical; send %q exactly",
			ErrUnsupported, m, strings.ToLower(strings.TrimSpace(m)))
	default:
		return fmt.Errorf("%w: %q (allowed: %s)",
			ErrUnsupported, m, strings.Join(Allowed(), ", "))
	}
}
