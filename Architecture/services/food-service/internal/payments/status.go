package payments

// The customer-facing payment status of a food order
// (GET /v1/food/orders/:id/payment) and the public checkout session relayed on
// the intent response. Both are pure; the store supplies the snapshot.

import (
	"errors"
	"strings"

	"github.com/atpost/food-service/internal/orderstate"
)

// Customer payment states. There are exactly three.
const (
	// CustomerStatusConfirming: no applied signed capture yet, and the order can
	// still be paid. The app keeps polling.
	CustomerStatusConfirming = "confirming"
	// CustomerStatusPaid: a payment.succeeded event was applied to this order
	// by the consumer. Nothing else (a client confirm, a verify verdict, a
	// payment_status written some other way) ever produces it.
	CustomerStatusPaid = "paid"
	// CustomerStatusFailed: the payment failed, or the order ended before it
	// was paid. The app stops polling.
	CustomerStatusFailed = "failed"
)

// Refund states reported next to `paid`. Empty means no refund.
const (
	RefundStatusPending           = "pending"
	RefundStatusPartiallyRefunded = "partially_refunded"
	RefundStatusRefunded          = "refunded"
)

// ErrPaymentNotOnline: the order is cash on delivery or wallet, which has no
// signed payment event to report on. HTTP 409 FOOD_PAYMENT_NOT_ONLINE.
var ErrPaymentNotOnline = errors.New("order is not paid online")

// CustomerPaymentSnapshot is what the status is derived from, read for the
// order's own customer.
type CustomerPaymentSnapshot struct {
	OrderStatus   string
	PaymentStatus string
	PaymentMethod string
	// CaptureApplied: food.payment_event_inbox holds a payment.succeeded event
	// for this order with outcome `confirmed`, i.e. the consumer applied it.
	CaptureApplied bool
}

// endedUnpaid are the order states from which an unpaid order will never be
// paid: a late capture on them is refunded by payments, never revives them.
var endedUnpaid = set(orderstate.CancelledByCustomer, orderstate.CancelledByRestaurant,
	orderstate.CancelledByAdmin, orderstate.RestaurantRejected, orderstate.Failed,
	orderstate.RefundPending, orderstate.Refunded)

// CustomerStatus maps a snapshot to (status, refundStatus).
//
//	method COD / WALLET                              -> ErrPaymentNotOnline
//	capture applied AND money taken                  -> paid (+ refund status)
//	payment_status FAILED                            -> failed
//	order cancelled / rejected / failed, not paid    -> failed
//	anything else (PLACED, PAYMENT_PENDING, and a
//	  payment_status CAPTURED with no applied event) -> confirming
func CustomerStatus(s CustomerPaymentSnapshot) (status, refundStatus string, err error) {
	if strings.ToUpper(strings.TrimSpace(s.PaymentMethod)) != StoreOnline {
		return "", "", ErrPaymentNotOnline
	}
	if s.CaptureApplied && moneyTaken[s.PaymentStatus] {
		switch s.PaymentStatus {
		case "REFUND_PENDING":
			refundStatus = RefundStatusPending
		case "PARTIALLY_REFUNDED":
			refundStatus = RefundStatusPartiallyRefunded
		case "REFUNDED":
			refundStatus = RefundStatusRefunded
		}
		return CustomerStatusPaid, refundStatus, nil
	}
	if s.PaymentStatus == "FAILED" || endedUnpaid[s.OrderStatus] {
		return CustomerStatusFailed, "", nil
	}
	return CustomerStatusConfirming, "", nil
}

// ClientSession is what the Android app needs to open Razorpay Checkout.
// It is a struct, not a map, so no field other than these three can ever be
// relayed: the key_id is Razorpay's publishable identifier; the key secret is
// never here.
type ClientSession struct {
	Provider string `json:"provider"`
	OrderID  string `json:"order_id"`
	KeyID    string `json:"key_id"`
}

// PublicClientSession extracts the relayable session from payments-service's
// intent. It returns nil (the field is then omitted, exactly as commerce and
// payments-service omit it) when payments attached none (stub gateway,
// Cashfree), when any of the three values is missing, or when the session's
// order_id is not this intent's provider order.
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
	return &cs
}

// PublicIntent is the payments intent as the customer sees it. payer_id,
// payee_id (the restaurant owner's user id) and the raw client_session are
// deliberately absent.
func (i *Intent) PublicIntent() map[string]any {
	return map[string]any{
		"id":             i.ID.String(),
		"status":         i.Status,
		"amount_minor":   i.AmountMinor,
		"currency":       i.Currency,
		"method":         i.Method,
		"provider_ref":   i.ProviderRef,
		"reference_type": i.ReferenceType,
		"reference_id":   i.ReferenceID.String(),
	}
}
