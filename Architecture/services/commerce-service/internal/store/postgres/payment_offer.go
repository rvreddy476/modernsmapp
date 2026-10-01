package postgres

// The bank offer on an order (migration 038), as payment.succeeded reports
// it. payments-service owns the offer rule (its migration 014 and
// gateway.MatchOfferCapture); commerce records the result for the buyer's
// order screen and the invoice note, and nothing else reads it.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// recordPaymentOfferTx writes the offer's figures onto the order inside the
// payment's own transaction. A nil offer is a no-op. An offer whose figures
// do not add up to the order value is NOT written — and is logged loudly —
// because a display that disagrees with the money is worse than none; the
// payment itself is still applied, since the order-value check already
// passed and that rule is not this function's to change.
func recordPaymentOfferTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID, orderValue money.Paise, o *PaymentOffer) error {
	if o == nil {
		return nil
	}
	if !o.consistentWith(orderValue) {
		slog.ErrorContext(ctx, "commerce: payment.succeeded carried bank-offer figures that do not reconcile to the order value; not recorded",
			"order_id", orderID, "order_value_minor", orderValue.Int64(),
			"captured_minor", o.CapturedMinor.Int64(), "offer_discount_minor", o.DiscountMinor.Int64(),
			"offer_id", o.OfferID)
		return nil
	}
	var fundedBy *string
	if o.FundedBy != "" {
		fundedBy = &o.FundedBy
	}
	var title *string
	if o.Title != "" {
		title = &o.Title
	}
	if _, err := tx.Exec(ctx, `
		UPDATE orders
		   SET offer_id = $2::uuid, offer_title = $3, offer_discount_minor = $4,
		       offer_funded_by = $5, captured_minor = $6, updated_at = NOW()
		 WHERE id = $1`,
		orderID, o.OfferID, title, o.DiscountMinor.Int64(), fundedBy, o.CapturedMinor.Int64()); err != nil {
		return fmt.Errorf("record bank offer on order %s: %w", orderID, err)
	}
	return nil
}

// OrderPaymentOffer is the bank offer recorded on an order, and what the
// buyer actually paid.
type OrderPaymentOffer struct {
	OfferID       *uuid.UUID
	Title         *string
	DiscountMinor *int64
	FundedBy      *string
	CapturedMinor *int64
	// PlatformDiscountMinor is a platform coupon's discount (adviser-pending;
	// only ever written while COMMERCE_PLATFORM_COUPONS_ENABLED is on).
	PlatformDiscountMinor *int64
}

// GetOrderPaymentOffer reads an order's bank-offer columns.
func (s *Store) GetOrderPaymentOffer(ctx context.Context, orderID uuid.UUID) (*OrderPaymentOffer, error) {
	var o OrderPaymentOffer
	err := s.db.QueryRow(ctx, `
		SELECT offer_id, offer_title, offer_discount_minor, offer_funded_by,
		       captured_minor, platform_discount_minor
		  FROM orders WHERE id = $1`, orderID).
		Scan(&o.OfferID, &o.Title, &o.DiscountMinor, &o.FundedBy, &o.CapturedMinor, &o.PlatformDiscountMinor)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrOrderNotFound
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}
