package postgres

// Coupon release when an order dies unpaid (audit 2026-10-30 M3).
//
// Checkout claims a coupon's capacity (`uses_count + 1`, claimCoupon) and the
// buyer's use (`coupon_usages`) in the checkout transaction, and nothing gave
// either back. An order that expired, failed payment or was cancelled before
// payment still held its use: a rival opening 100 unpaid checkouts exhausted
// a `max_uses = 100` coupon for every real buyer, and an honest buyer whose
// UPI app timed out had burned their one use and was refused on re-checkout.
//
// releaseCouponTx runs inside the transaction that terminates the order, so
// the release commits or rolls back with the state change. It is idempotent:
// the usage row is deleted once, and the capacity is returned only for a row
// this call deleted, so a repeat (or a second terminating path) releases
// nothing.
//
// A payment-failed order can be retried (RetryPaymentReservation). The retry
// re-claims the coupon with reclaimCouponTx, under the same capacity and
// per-buyer rules as checkout, and refuses with ErrCouponExhausted when it is
// gone, so a retried order never keeps a discount nobody holds a use for.

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// couponUnpaidPaymentStatuses are the payment statuses in which an order has
// taken no money, so cancelling it releases its coupon. A paid order that is
// cancelled keeps the use: the coupon was redeemed and the refund returns the
// discounted amount.
var couponUnpaidPaymentStatuses = map[string]bool{
	"pending": true, "processing": true, "failed": true,
}

// releasesCouponOnCancel reports whether cancelling an order in this payment
// status gives its coupon back.
func releasesCouponOnCancel(paymentStatus string) bool {
	return couponUnpaidPaymentStatuses[paymentStatus]
}

// releaseCouponTx gives back the coupon use the order claimed at checkout:
// its coupon_usages row and one unit of the coupon's capacity. It returns how
// many uses it released (0 when there was nothing to release).
func releaseCouponTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) (int, error) {
	tag, err := tx.Exec(ctx, `
		WITH released AS (
		       DELETE FROM coupon_usages WHERE order_id = $1 RETURNING coupon_id)
		UPDATE coupons c
		   SET uses_count = c.uses_count - 1
		  FROM released r
		 WHERE c.id = r.coupon_id AND c.uses_count > 0`, orderID)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// reclaimCouponTx re-claims the coupon an order was priced with, for a
// payment retry. The order's price was fixed at checkout and is not
// recomputed, so only the claim is repeated: one unit of capacity and the
// buyer's use, refused with ErrCouponExhausted when either is gone. An order
// with no coupon, or one whose use is still held (it failed before uses were
// released), claims nothing.
func reclaimCouponTx(ctx context.Context, tx pgx.Tx, orderID, userID uuid.UUID) error {
	var code *string
	if err := tx.QueryRow(ctx, `SELECT coupon_code FROM orders WHERE id = $1`, orderID).Scan(&code); err != nil {
		return err
	}
	if code == nil || *code == "" {
		return nil
	}
	var (
		couponID   uuid.UUID
		maxPerUser int
	)
	err := tx.QueryRow(ctx,
		`SELECT id, max_uses_per_user FROM coupons WHERE code = $1 FOR UPDATE`, *code).
		Scan(&couponID, &maxPerUser)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrCouponExhausted
	}
	if err != nil {
		return err
	}
	var held bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM coupon_usages WHERE coupon_id = $1 AND order_id = $2)`,
		couponID, orderID).Scan(&held); err != nil {
		return err
	}
	if held {
		return nil
	}
	var used int
	if err := tx.QueryRow(ctx,
		`SELECT count(*) FROM coupon_usages WHERE coupon_id = $1 AND user_id = $2`,
		couponID, userID).Scan(&used); err != nil {
		return err
	}
	if used >= maxPerUser {
		return ErrCouponExhausted
	}
	tag, err := tx.Exec(ctx,
		`UPDATE coupons SET uses_count = uses_count + 1
		  WHERE id = $1 AND (max_uses IS NULL OR uses_count < max_uses)`, couponID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrCouponExhausted
	}
	_, err = tx.Exec(ctx,
		`INSERT INTO coupon_usages (coupon_id, user_id, order_id) VALUES ($1,$2,$3)
		 ON CONFLICT DO NOTHING`, couponID, userID, orderID)
	return err
}
