package postgres

// Payment writers: intent creation (online, and cash on delivery when the
// launch flag lets it through), the wallet confirmation, and the shared
// "order is paid" step the payment-event consumer uses. Every order status
// change here goes through transitionOrderTx with orderstate.ActorPayment.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/shared/paymentmethod"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	// ErrCODNotAllowed: cash on delivery may replace an online payment only
	// before any money moved, from PLACED or PAYMENT_PENDING.
	// HTTP 409 FOOD_COD_NOT_ALLOWED_FROM_STATE.
	ErrCODNotAllowed = errors.New("cash on delivery can only be chosen from PLACED or PAYMENT_PENDING before any capture")
	// ErrPaymentNotAllowedFromState: a payment cannot be started (or a wallet
	// confirmed) from the order's current state.
	// HTTP 409 FOOD_PAYMENT_NOT_ALLOWED_FROM_STATE.
	ErrPaymentNotAllowedFromState = errors.New("payment cannot be started from the order's current state")
)

// paymentTaken reports whether money has moved for this payment status.
func paymentTaken(paymentStatus string) bool {
	switch paymentStatus {
	case "CAPTURED", "REFUND_PENDING", "PARTIALLY_REFUNDED", "REFUNDED":
		return true
	}
	return false
}

func awaitingPayment(status string) bool {
	switch status {
	case orderstate.Placed, orderstate.PaymentPending, orderstate.PaymentFailed:
		return true
	}
	return false
}

// placeOrderPaymentState maps a store payment method to the order's initial
// status. There is no default: an empty method used to become COD.
func placeOrderPaymentState(method string) (status, paymentStatus string, err error) {
	switch method {
	case payments.StoreOnline, payments.StoreWallet:
		return orderstate.PaymentPending, "PENDING", nil
	case payments.StoreCOD:
		return orderstate.Confirmed, "NOT_REQUIRED", nil
	}
	return "", "", fmt.Errorf("%w: %q", payments.ErrPaymentMethodInvalid, method)
}

// placeOrderMetadata is the orders.metadata a new order starts with: the
// checkout instrument for an online order, nothing otherwise.
func placeOrderMetadata(method, instrument string) ([]byte, error) {
	if instrument == "" {
		return []byte(`{}`), nil
	}
	if method != payments.StoreOnline || !paymentmethod.IsAllowed(instrument) {
		return nil, fmt.Errorf("%w: instrument %q for %s", payments.ErrPaymentMethodInvalid, instrument, method)
	}
	return json.Marshal(map[string]string{"payment_instrument": instrument})
}

func (s *Store) WalletPaymentChargeDetails(ctx context.Context, userID, orderID uuid.UUID) (*WalletPaymentChargeDetails, error) {
	var details WalletPaymentChargeDetails
	if err := s.db.QueryRow(ctx, `
		SELECT o.id, o.order_number, o.user_id, r.owner_user_id,
			o.payment_method::text, o.payment_status::text, o.final_amount::float8,
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint), COALESCE(o.metadata->>'payment_instrument', '')
		FROM food.orders o
		JOIN food.restaurants r ON r.id = o.restaurant_id
		WHERE o.id = $1 AND o.user_id = $2
	`, orderID, userID).Scan(
		&details.OrderID,
		&details.OrderNumber,
		&details.UserID,
		&details.RestaurantOwnerID,
		&details.PaymentMethod,
		&details.PaymentStatus,
		&details.Amount,
		&details.AmountMinor,
		&details.PaymentInstrument,
	); err != nil {
		return nil, err
	}
	return &details, nil
}

func (s *Store) PaymentIntegrationDetails(ctx context.Context, orderID uuid.UUID) (*PaymentIntegrationDetails, error) {
	var details PaymentIntegrationDetails
	if err := s.db.QueryRow(ctx, `
		SELECT o.id, o.order_number, o.user_id, r.owner_user_id,
			o.payment_method::text, o.payment_status::text,
			COALESCE(p.provider_payment_id, ''), COALESCE(p.provider_order_id, ''),
			o.final_amount::float8, COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint)
		FROM food.orders o
		JOIN food.restaurants r ON r.id = o.restaurant_id
		LEFT JOIN LATERAL (
			SELECT provider_payment_id, provider_order_id
			FROM food.payments
			WHERE order_id = o.id
			ORDER BY created_at DESC
			LIMIT 1
		) p ON TRUE
		WHERE o.id = $1
	`, orderID).Scan(
		&details.OrderID,
		&details.OrderNumber,
		&details.UserID,
		&details.RestaurantOwnerID,
		&details.PaymentMethod,
		&details.PaymentStatus,
		&details.ProviderPaymentID,
		&details.ProviderOrderID,
		&details.Amount,
		&details.AmountMinor,
	); err != nil {
		return nil, err
	}
	return &details, nil
}

// CreatePaymentIntent records the local side of a payment attempt. method is
// the store enum (ONLINE|WALLET|COD, already gated by the service's launch
// flags); instrument is upi|card for ONLINE.
//
//   - ONLINE/WALLET: allowed from PLACED, PAYMENT_PENDING or PAYMENT_FAILED
//     with no money taken; the order moves to PAYMENT_PENDING through the
//     guard. The payments-service intent is opened by the service afterwards.
//   - COD: allowed only from PLACED or PAYMENT_PENDING with no money taken;
//     the order is confirmed through the guard.
func (s *Store) CreatePaymentIntent(ctx context.Context, userID, orderID uuid.UUID, method, instrument, idempotencyKey string) (map[string]any, error) {
	method = strings.ToUpper(strings.TrimSpace(method))
	switch method {
	case payments.StoreOnline, payments.StoreWallet, payments.StoreCOD:
	default:
		return nil, fmt.Errorf("%w: %q", payments.ErrPaymentMethodInvalid, method)
	}
	if method != payments.StoreOnline {
		instrument = ""
	} else if instrument != "" && !paymentmethod.IsAllowed(instrument) {
		return nil, fmt.Errorf("%w: instrument %q", payments.ErrPaymentMethodInvalid, instrument)
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	if idempotencyKey != "" {
		existingOrderID, handled, err := s.lockIdempotency(ctx, tx, userID, idempotencyKey)
		if err != nil {
			return nil, err
		}
		if handled {
			if existingOrderID == uuid.Nil || existingOrderID != orderID {
				return nil, ErrIdempotencyInProgress
			}
			return s.paymentIntentTx(ctx, tx, userID, orderID)
		}
	}

	var status, paymentStatus, orderNumber string
	var amount, deliveryFee float64
	if err := tx.QueryRow(ctx, `
		SELECT status::text, payment_status::text, order_number,
			final_amount::float8, delivery_fee::float8
		FROM food.orders
		WHERE id = $1 AND user_id = $2
		FOR UPDATE
	`, orderID, userID).Scan(&status, &paymentStatus, &orderNumber, &amount, &deliveryFee); err != nil {
		return nil, err
	}

	if method == payments.StoreCOD {
		// The COD state guard. PAYMENT_FAILED -> CONFIRMED is a legal payment
		// edge (a late capture on the same intent), so the transition table
		// alone would let COD confirm an order whose online attempt failed.
		if (status != orderstate.Placed && status != orderstate.PaymentPending) || paymentTaken(paymentStatus) {
			return nil, fmt.Errorf("%w: order is %s, payment %s", ErrCODNotAllowed, status, paymentStatus)
		}
	} else if !awaitingPayment(status) || paymentTaken(paymentStatus) {
		return nil, fmt.Errorf("%w: order is %s, payment %s", ErrPaymentNotAllowedFromState, status, paymentStatus)
	}

	provider, rowStatus, intentStatus := "payments-service", "PENDING", "PENDING"
	switch method {
	case payments.StoreWallet:
		provider = "monetization-service"
	case payments.StoreCOD:
		provider, rowStatus, intentStatus = "cod", "NOT_REQUIRED", "NOT_REQUIRED"
	}
	providerOrderID := fmt.Sprintf("food_order_%s_%d", orderID.String(), time.Now().UnixNano())
	raw, _ := json.Marshal(map[string]any{
		"reference_type": payments.RefTypeFoodOrder,
		"reference_id":   orderID.String(),
		"order_number":   orderNumber,
		"method":         method,
		"instrument":     instrument,
		"provider":       provider,
	})

	var paymentID uuid.UUID
	err = tx.QueryRow(ctx, `
		UPDATE food.payments
		SET payment_method = $2::text::food.payment_method,
			status = $3::text::food.payment_status,
			provider = $4,
			provider_order_id = $5,
			amount = $6,
			currency = 'INR',
			raw_response = raw_response || $7::jsonb
		WHERE id = (
			SELECT id FROM food.payments WHERE order_id = $1 ORDER BY created_at DESC LIMIT 1
		)
		RETURNING id
	`, orderID, method, rowStatus, provider, providerOrderID, amount, raw).Scan(&paymentID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO food.payments (
				order_id, payment_method, status, provider, provider_order_id, amount, currency, raw_response
			)
			VALUES ($1, $2::text::food.payment_method, $3::text::food.payment_status, $4, $5, $6, 'INR', $7)
			RETURNING id
		`, orderID, method, rowStatus, provider, providerOrderID, amount, raw).Scan(&paymentID)
	}
	if err != nil {
		return nil, err
	}

	if method == payments.StoreCOD {
		if _, err := tx.Exec(ctx, `UPDATE food.orders SET payment_method = 'COD' WHERE id = $1`, orderID); err != nil {
			return nil, err
		}
		if err := s.confirmOrderPaidTx(ctx, tx, paidConfirmation{
			OrderID: orderID, From: status, PaymentID: &paymentID, PaymentStatus: "NOT_REQUIRED",
			DeliveryFee: deliveryFee, ChangedBy: &userID, Reason: "cash on delivery selected",
		}); err != nil {
			return nil, err
		}
	} else {
		if _, err := tx.Exec(ctx, `
			UPDATE food.orders
			SET payment_status = 'PENDING',
				payment_method = $2::text::food.payment_method,
				metadata = CASE WHEN $3::text = '' THEN metadata
					ELSE metadata || jsonb_build_object('payment_instrument', $3::text) END
			WHERE id = $1
		`, orderID, method, instrument); err != nil {
			return nil, err
		}
		if status != orderstate.PaymentPending {
			if err := transitionOrderTx(ctx, tx, OrderTransition{
				OrderID: orderID, From: status, To: orderstate.PaymentPending,
				Actor: orderstate.ActorPayment, ChangedBy: &userID, Reason: "payment started",
			}); err != nil {
				return nil, err
			}
		}
	}

	if idempotencyKey != "" {
		body, _ := json.Marshal(map[string]string{"order_id": orderID.String()})
		if _, err := tx.Exec(ctx, `
			UPDATE food.idempotency_keys
			SET response_status = 201, response_body = $3, completed_at = NOW()
			WHERE user_id = $1 AND key = $2
		`, userID, idempotencyKey, body); err != nil {
			return nil, err
		}
	}

	intent, err := s.paymentIntentTx(ctx, tx, userID, orderID)
	if err != nil {
		return nil, err
	}
	intent["payment_intent"] = map[string]any{
		"id":             intent["id"],
		"reference_type": payments.RefTypeFoodOrder,
		"reference_id":   orderID,
		"amount":         amount,
		"currency":       "INR",
		"method":         method,
		"instrument":     instrument,
		"status":         intentStatus,
		"provider":       provider,
		"provider_ref":   providerOrderID,
	}
	return intent, tx.Commit(ctx)
}

// MarkWalletPaid confirms a WALLET order after the monetization charge
// succeeded. Reachable only with FOOD_WALLET_PAYMENTS_ENABLED on.
func (s *Store) MarkWalletPaid(ctx context.Context, userID, orderID uuid.UUID) (*Order, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var status, paymentStatus, method string
	var deliveryFee float64
	var paymentID *uuid.UUID
	if err := tx.QueryRow(ctx, `
		SELECT o.status::text, o.payment_status::text, o.payment_method::text, o.delivery_fee::float8, p.id
		FROM food.orders o
		LEFT JOIN LATERAL (
			SELECT id FROM food.payments WHERE order_id = o.id ORDER BY created_at DESC LIMIT 1
		) p ON TRUE
		WHERE o.id = $1 AND o.user_id = $2
		FOR UPDATE OF o
	`, orderID, userID).Scan(&status, &paymentStatus, &method, &deliveryFee, &paymentID); err != nil {
		return nil, err
	}
	if method != payments.StoreWallet {
		return nil, fmt.Errorf("%w: order is not a wallet order", ErrPaymentNotAllowedFromState)
	}
	if paymentStatus != "CAPTURED" {
		if !awaitingPayment(status) || paymentTaken(paymentStatus) {
			return nil, fmt.Errorf("%w: order is %s, payment %s", ErrPaymentNotAllowedFromState, status, paymentStatus)
		}
		if err := s.confirmOrderPaidTx(ctx, tx, paidConfirmation{
			OrderID: orderID, From: status, PaymentID: paymentID, PaymentStatus: "CAPTURED",
			DeliveryFee: deliveryFee, ChangedBy: &userID, Reason: "wallet charged",
		}); err != nil {
			return nil, err
		}
	}
	order, err := s.getOrderTx(ctx, tx, userID, orderID, true)
	if err != nil {
		return nil, err
	}
	return order, tx.Commit(ctx)
}

type paidConfirmation struct {
	OrderID uuid.UUID
	From    string
	// PaymentID is the latest food.payments row; nil when none exists.
	PaymentID *uuid.UUID
	// IntentID is stamped on the payment row only if it has none yet.
	IntentID      string
	PaymentStatus string // CAPTURED, or NOT_REQUIRED for COD
	DeliveryFee   float64
	ChangedBy     *uuid.UUID
	Reason        string
}

// confirmOrderPaidTx is the one "this order is paid" step: payment row,
// guarded order transition to CONFIRMED with history, payment_status and the
// restaurant accept deadline, and the unclaimed delivery assignment. Callers
// hold the order row FOR UPDATE.
func (s *Store) confirmOrderPaidTx(ctx context.Context, tx pgx.Tx, c paidConfirmation) error {
	if c.PaymentID != nil {
		if _, err := tx.Exec(ctx, `
			UPDATE food.payments
			SET status = $2::text::food.payment_status,
				paid_at = CASE WHEN $2::text = 'CAPTURED' THEN NOW() ELSE paid_at END,
				provider_payment_id = COALESCE(NULLIF(provider_payment_id, ''), NULLIF($3::text, ''))
			WHERE id = $1
		`, *c.PaymentID, c.PaymentStatus, c.IntentID); err != nil {
			return err
		}
	}
	if err := transitionOrderTx(ctx, tx, OrderTransition{
		OrderID: c.OrderID, From: c.From, To: orderstate.Confirmed,
		Actor: orderstate.ActorPayment, ChangedBy: c.ChangedBy, Reason: c.Reason,
	}); err != nil {
		return err
	}
	// B1: the accept deadline starts when the order is paid.
	if _, err := tx.Exec(ctx, `
		UPDATE food.orders
		SET payment_status = $2::text::food.payment_status,
			accept_deadline_at = NOW() + (
				COALESCE(
					(SELECT sla_accept_seconds FROM food.restaurants WHERE id = food.orders.restaurant_id),
					180
				) * INTERVAL '1 second'
			)
		WHERE id = $1
	`, c.OrderID, c.PaymentStatus); err != nil {
		return err
	}
	return s.ensureDeliveryAssignmentTx(ctx, tx, c.OrderID, c.DeliveryFee)
}

func (s *Store) ensureDeliveryAssignmentTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, deliveryFee float64) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO food.delivery_assignments (order_id, status, delivery_fee, delivery_partner_payout)
		VALUES ($1, 'CREATED', $2, $3)
		ON CONFLICT (order_id) DO NOTHING
	`, orderID, deliveryFee, riderPayoutForFee(deliveryFee))
	return err
}
