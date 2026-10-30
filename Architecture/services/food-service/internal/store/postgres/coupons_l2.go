package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Audit 2026-10-30 L2 (coupons, behind FOOD_COUPONS_ENABLED): integer paise
// maths, the per-user limit, and redemptions released when the order dies.

// CouponTerms are a coupon's terms in integer units. ValueHundredths is
// discount_value x 100: paise for a FLAT coupon, basis points for a
// PERCENTAGE one (10.00% = 1000).
type CouponTerms struct {
	Type             string
	ValueHundredths  int64
	MaxDiscountPaise int64
	MinOrderPaise    int64
}

// CouponDiscountPaise is the discount on subtotalPaise: a FLAT value or a
// percentage rounded half up, capped at the coupon's maximum and at the
// subtotal. A subtotal under the minimum order is refused.
func CouponDiscountPaise(c CouponTerms, subtotalPaise int64) (int64, error) {
	if subtotalPaise <= 0 || subtotalPaise < c.MinOrderPaise || c.ValueHundredths < 0 {
		return 0, ErrCouponInvalid
	}
	discount := c.ValueHundredths
	if c.Type == "PERCENTAGE" {
		discount = (subtotalPaise*c.ValueHundredths + 5000) / 10000
	}
	if c.MaxDiscountPaise >= 0 && discount > c.MaxDiscountPaise {
		discount = c.MaxDiscountPaise
	}
	if discount > subtotalPaise {
		discount = subtotalPaise
	}
	return discount, nil
}

// releaseCouponRedemptionTx frees the order's coupon redemption, in the
// transaction that ends the order before delivery (cancelled by anyone,
// rejected), so the use counts neither against the coupon's total nor the
// user's limit. Idempotent: a second call finds no row.
func releaseCouponRedemptionTx(ctx context.Context, tx pgx.Tx, orderID uuid.UUID) error {
	var couponID uuid.UUID
	err := tx.QueryRow(ctx, `
		DELETE FROM food.coupon_redemptions WHERE order_id = $1 RETURNING coupon_id
	`, orderID).Scan(&couponID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		UPDATE food.coupons SET used_count = GREATEST(used_count - 1, 0) WHERE id = $1
	`, couponID)
	return err
}
