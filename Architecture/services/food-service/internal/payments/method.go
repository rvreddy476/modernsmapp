// Package payments is food-service's side of the payments integration: which
// payment methods are open at launch, the service-token client for
// payments-service, and the payment-event consumer with its pure decision
// table.
//
// It never imports the store. The store imports it (for Decide and the event
// types) and the consumer reaches the store through the Applier interface.
package payments

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/atpost/shared/paymentmethod"
)

var (
	// ErrPaymentMethodUnavailable: the method exists but is switched off for
	// launch (cash on delivery, wallet). HTTP 422 PAYMENT_METHOD_UNAVAILABLE.
	ErrPaymentMethodUnavailable = errors.New("payment method is not available")
	// ErrPaymentMethodInvalid: missing, or not a launch instrument.
	// HTTP 422 PAYMENT_METHOD_INVALID.
	ErrPaymentMethodInvalid = errors.New("payment method is invalid")
)

// The food.payment_method enum values the store writes.
const (
	StoreOnline = "ONLINE"
	StoreCOD    = "COD"
	StoreWallet = "WALLET"
)

// Flags are the launch switches. Both default to off: food launches
// online-only (founder decision), so a missing or unparseable value is off.
type Flags struct {
	CODEnabled    bool
	WalletEnabled bool
}

// FlagsFromEnv reads FOOD_COD_ENABLED and FOOD_WALLET_PAYMENTS_ENABLED.
func FlagsFromEnv(getenv func(string) string) Flags {
	return Flags{
		CODEnabled:    envBool(getenv("FOOD_COD_ENABLED")),
		WalletEnabled: envBool(getenv("FOOD_WALLET_PAYMENTS_ENABLED")),
	}
}

func envBool(v string) bool {
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	return err == nil && b
}

// ResolvedMethod is what the client asked for, in store form. Instrument is
// the payments-service method (upi|card) and is empty for COD and wallet.
type ResolvedMethod struct {
	Store      string
	Instrument string
}

// ResolveMethod validates a client-supplied payment method.
//
// There is deliberately no default: an order that names no method used to
// become cash on delivery, which with COD off at launch would have been an
// order nobody pays for. Online instruments are exactly shared/paymentmethod
// (case-sensitive, the same vocabulary payments-service enforces).
func ResolveMethod(raw string, f Flags) (ResolvedMethod, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "cod":
		if !f.CODEnabled {
			return ResolvedMethod{}, fmt.Errorf("%w: cash on delivery is not enabled", ErrPaymentMethodUnavailable)
		}
		return ResolvedMethod{Store: StoreCOD}, nil
	case "wallet":
		if !f.WalletEnabled {
			return ResolvedMethod{}, fmt.Errorf("%w: wallet payments are not enabled", ErrPaymentMethodUnavailable)
		}
		return ResolvedMethod{Store: StoreWallet}, nil
	}
	if err := paymentmethod.Validate(raw); err != nil {
		return ResolvedMethod{}, fmt.Errorf("%w: %v", ErrPaymentMethodInvalid, err)
	}
	return ResolvedMethod{Store: StoreOnline, Instrument: raw}, nil
}
