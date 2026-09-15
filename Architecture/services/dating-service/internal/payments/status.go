package payments

// The customer-facing payment status of a premium purchase
// (GET /v1/dating/premium/purchases/:id/payment) and the public checkout
// session relayed on the purchase response. Both are pure; the store supplies
// the snapshot.

import (
	"strings"

	"github.com/atpost/shared/paymentsclient"
)

// Customer payment states. There are exactly three.
const (
	// CustomerStatusConfirming: no applied signed capture yet and the purchase
	// can still be paid. The app keeps polling.
	CustomerStatusConfirming = "confirming"
	// CustomerStatusPaid: a payment.succeeded event was applied to this
	// purchase by the consumer. Nothing else produces it.
	CustomerStatusPaid = "paid"
	// CustomerStatusFailed: the payment failed. The app stops polling.
	CustomerStatusFailed = "failed"
)

// Refund states reported next to `paid`. Empty means no refund.
const (
	RefundStatusPartiallyRefunded = "partially_refunded"
	RefundStatusRefunded          = "refunded"
)

// PurchasePaymentSnapshot is what the status is derived from, read for the
// purchase's own buyer.
type PurchasePaymentSnapshot struct {
	Status string
	// CaptureApplied: dating_payment_inbox holds a payment.succeeded event for
	// this purchase with outcome `granted`, i.e. the consumer applied it.
	CaptureApplied bool
}

// CustomerStatus maps a snapshot to (status, refundStatus).
//
//	capture applied AND money taken  -> paid (+ refund status)
//	status failed                     -> failed
//	anything else (created, confirming, and a status of paid with no applied
//	  event)                          -> confirming
func CustomerStatus(s PurchasePaymentSnapshot) (status, refundStatus string) {
	if s.CaptureApplied && moneyTaken[s.Status] {
		switch s.Status {
		case StatusPartiallyRefunded:
			refundStatus = RefundStatusPartiallyRefunded
		case StatusRefunded:
			refundStatus = RefundStatusRefunded
		}
		return CustomerStatusPaid, refundStatus
	}
	if s.Status == StatusFailed {
		return CustomerStatusFailed, ""
	}
	return CustomerStatusConfirming, ""
}

// ClientSession is what the Android app needs to open Razorpay Checkout: the
// provider, its order id, the PUBLISHABLE key id and optionally the merchant
// display name. It is the shared client's struct, so nothing else can be
// relayed.
type ClientSession = paymentsclient.ClientSession

// PublicClientSession extracts the relayable session from payments-service's
// intent, exactly as food does. It returns nil when payments attached none
// (stub gateway, Cashfree), when any of the three values is missing, or when
// the session's order_id is not this intent's provider order.
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
