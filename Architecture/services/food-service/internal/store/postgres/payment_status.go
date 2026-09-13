package postgres

import (
	"context"
	"time"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// CustomerPaymentState is the read behind GET /v1/food/orders/:id/payment.
type CustomerPaymentState struct {
	OrderID       uuid.UUID
	OrderStatus   string
	PaymentStatus string
	PaymentMethod string
	// AmountMinor is the order total in integer paise.
	AmountMinor int64
	Currency    string
	// CaptureApplied: the consumer applied a payment.succeeded event to this
	// order (inbox outcome `confirmed`).
	CaptureApplied bool
	// UpdatedAt is the latest of the order's placement, its latest payment
	// row's update and its latest applied payment event.
	UpdatedAt time.Time
}

// CustomerPaymentStatus reads an order's payment state for its own customer.
// The user_id predicate is in the WHERE clause, so another customer's order
// and a missing one are the same pgx.ErrNoRows.
func (s *Store) CustomerPaymentStatus(ctx context.Context, userID, orderID uuid.UUID) (*CustomerPaymentState, error) {
	st := CustomerPaymentState{OrderID: orderID}
	if err := s.db.QueryRow(ctx, `
		SELECT o.status::text, o.payment_status::text, o.payment_method::text,
			COALESCE(o.final_amount_paise, ROUND(o.final_amount * 100)::bigint),
			COALESCE(btrim(p.currency::text), 'INR'),
			EXISTS (
				SELECT 1 FROM food.payment_event_inbox i
				WHERE i.order_id = o.id AND i.event_type = $3 AND i.outcome = $4
			),
			GREATEST(o.placed_at, p.updated_at,
				(SELECT MAX(i.processed_at) FROM food.payment_event_inbox i WHERE i.order_id = o.id))
		FROM food.orders o
		LEFT JOIN LATERAL (
			SELECT currency, updated_at
			FROM food.payments
			WHERE order_id = o.id
			ORDER BY created_at DESC
			LIMIT 1
		) p ON TRUE
		WHERE o.id = $1 AND o.user_id = $2
	`, orderID, userID, events.EventPaymentSucceeded, string(payments.OutcomeConfirmed)).Scan(
		&st.OrderStatus, &st.PaymentStatus, &st.PaymentMethod, &st.AmountMinor,
		&st.Currency, &st.CaptureApplied, &st.UpdatedAt,
	); err != nil {
		return nil, err
	}
	return &st, nil
}
