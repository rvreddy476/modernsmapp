package service

// The customer-facing payment status of an order:
// GET /v1/commerce/orders/:orderId/payment.
//
// Three states, exactly the shape food-service's
// internal/payments/status.go reports for a food order, so a client that
// polls one can poll the other with the same code. The rules are copied
// rather than imported: the two services have different order vocabularies,
// and a shared package would have to know both.
//
//	paid        payment_status is paid — or a refund state that can only be
//	            reached FROM paid — which in commerce is written only by the
//	            signed payment.succeeded event (store.ApplyPaymentSucceeded)
//	            or, on a dev stack, the stub settlement. refund_status says
//	            whether that money is on its way back.
//	failed      payment_status is failed, or the order ended (payment_failed,
//	            cancelled, expired) before it was paid. The app stops polling.
//	confirming  everything else: the order can still be paid and no signed
//	            capture has been applied. The app keeps polling.
//
// The older GET …/payment/status stays as it is for the shipped Android
// build; this is the read new clients are built against.

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Customer payment states. There are exactly three.
const (
	CustomerPaymentConfirming = "confirming"
	CustomerPaymentPaid       = "paid"
	CustomerPaymentFailed     = "failed"
)

// Refund states reported beside `paid`. Nil means no refund.
const (
	RefundStatusPending           = "pending"
	RefundStatusPartiallyRefunded = "partially_refunded"
	RefundStatusRefunded          = "refunded"
)

// ErrNotOrderCustomer: the caller is not the order's customer. Unlike
// ErrNotOrderOwner (a 404, so order ids cannot be probed through the order
// screen) this is a 403: the poll must stop on a permanent answer, and the
// contract names 403 as one.
var ErrNotOrderCustomer = errors.New("only the order's customer may read its payment")

// CustomerPaymentStatus is the wire shape. Field order is the contract.
type CustomerPaymentStatus struct {
	OrderID      uuid.UUID   `json:"order_id"`
	Status       string      `json:"status"`
	AmountMinor  money.Paise `json:"amount_minor"`
	Currency     string      `json:"currency"`
	RefundStatus *string     `json:"refund_status"`
	UpdatedAt    time.Time   `json:"updated_at"`
}

// moneyTaken are the payment_status values that mean a capture was applied:
// paid itself, and the three refund states, which the store writes only on a
// row that was paid (CancelOrder, SettleRefund, the late-capture branch).
var moneyTaken = map[string]bool{
	"paid": true, "refund_pending": true, "partially_refunded": true, "refunded": true,
}

// endedUnpaid are the order states from which an unpaid order will never be
// paid: a late capture on them becomes a refund command, never a
// confirmation (store/postgres/p0.go).
var endedUnpaid = map[string]bool{
	"payment_failed": true, "cancelled": true, "expired": true,
}

// CustomerPaymentState maps an order's two status columns to the customer's
// (status, refund_status). Pure, so the rules are unit-testable on their own.
func CustomerPaymentState(orderStatus, paymentStatus string) (status string, refundStatus *string) {
	if moneyTaken[paymentStatus] {
		var refund string
		switch paymentStatus {
		case "refund_pending":
			refund = RefundStatusPending
		case "partially_refunded":
			refund = RefundStatusPartiallyRefunded
		case "refunded":
			refund = RefundStatusRefunded
		}
		if refund != "" {
			return CustomerPaymentPaid, &refund
		}
		return CustomerPaymentPaid, nil
	}
	if paymentStatus == "failed" || endedUnpaid[orderStatus] {
		return CustomerPaymentFailed, nil
	}
	return CustomerPaymentConfirming, nil
}

// CustomerPaymentStatusFor reads the order and answers for its customer only.
func (s *Service) CustomerPaymentStatusFor(ctx context.Context, orderID, userID uuid.UUID) (*CustomerPaymentStatus, error) {
	order, err := s.store.GetOrderByID(ctx, orderID)
	if err != nil || order == nil {
		return nil, ErrOrderNotFound
	}
	if order.CustomerUserID != userID {
		return nil, ErrNotOrderCustomer
	}
	status, refund := CustomerPaymentState(order.Status, order.PaymentStatus)
	return &CustomerPaymentStatus{
		OrderID:      order.ID,
		Status:       status,
		AmountMinor:  money.Paise(order.TotalMinor()),
		Currency:     coalesceStr(order.CurrencyCode, "INR"),
		RefundStatus: refund,
		UpdatedAt:    order.UpdatedAt.UTC(),
	}, nil
}

// CanRetryPayment: the order is in payment_failed and the caller is its
// payer. This is what OrderDetail.can_retry_payment reports and what
// RetryPaymentForOrder requires. A pure rule so the two cannot drift.
func CanRetryPayment(order *postgres.Order, userID uuid.UUID) bool {
	return order != nil && order.Status == "payment_failed" && order.CustomerUserID == userID
}
