package payments

// The customer-facing payment status of a ride
// (GET /v1/rider/rides/:id/payment) and the public checkout session relayed
// on the intent response. Both are pure; the store supplies the row.

import (
	"strings"

	"github.com/atpost/shared/paymentsclient"
)

// Public payment states.
const (
	// PublicCashPending: cash, the captain has not confirmed collection.
	PublicCashPending = "cash_pending"
	// PublicCashConfirmed: cash, the captain confirmed collection.
	PublicCashConfirmed = "cash_confirmed"
	// PublicPending: online, no intent opened yet.
	PublicPending = "pending"
	// PublicConfirming: online, an intent is open and no signed capture has
	// been applied. The app keeps polling.
	PublicConfirming = "confirming"
	// PublicPaid: a payment.succeeded event was applied by the consumer.
	// Nothing else produces it.
	PublicPaid = "paid"
	// PublicFailed: the payment failed. The customer may retry or switch to
	// cash.
	PublicFailed = "failed"
	// PublicRefunded / PublicPartiallyRefunded: after paid.
	PublicRefunded          = "refunded"
	PublicPartiallyRefunded = "partially_refunded"
)

// PublicStatus maps a row (method, status) to the public state:
//
//	pending_cash_confirmation      -> cash_pending
//	succeeded + cash               -> cash_confirmed
//	succeeded + upi/card           -> paid
//	pending | confirming | failed  -> as is
//	refunded | partially_refunded  -> as is
func PublicStatus(method, status string) string {
	switch status {
	case StatusPendingCash:
		return PublicCashPending
	case StatusSucceeded:
		if method == MethodCash {
			return PublicCashConfirmed
		}
		return PublicPaid
	case StatusPending:
		return PublicPending
	case StatusConfirming:
		return PublicConfirming
	case StatusFailed:
		return PublicFailed
	case StatusRefunded:
		return PublicRefunded
	case StatusPartiallyRefunded:
		return PublicPartiallyRefunded
	}
	return status
}

// ClientSession is what the Android app needs to open Razorpay Checkout:
// the provider, its order id, the PUBLISHABLE key id and optionally the
// merchant display name. It is the shared client's struct, so nothing else
// can be relayed.
type ClientSession = paymentsclient.ClientSession

// PublicClientSession extracts the relayable session from payments-service's
// intent, exactly as food and dating do. It returns nil when payments
// attached none (stub gateway, Cashfree), when any of the three values is
// missing, or when the session's order_id is not this intent's provider
// order.
func (i *Intent) PublicClientSession() *ClientSession {
	if i == nil || len(i.ClientSession) == 0 {
		return nil
	}
	cs := ClientSession{
		Provider: strings.TrimSpace(i.ClientSession["provider"]),
		OrderID:  strings.TrimSpace(i.ClientSession["order_id"]),
		KeyID:    strings.TrimSpace(i.ClientSession["key_id"]),
	}
	if cs.Provider == "" || cs.OrderID == "" || cs.KeyID == "" {
		return nil
	}
	if cs.OrderID != i.ProviderRef {
		return nil
	}
	cs.MerchantDisplayName = paymentsclient.NormalizeMerchantDisplayName(i.ClientSession["merchant_display_name"])
	return &cs
}
