// Coupon codes: lookup by code and per-user usage counting and increment.
// Moved out of store.go unchanged.

package postgres

import (
	"context"

	"github.com/google/uuid"
)

// ─── Coupons ─────────────────────────────────────────────────

func (s *Store) GetCouponByCode(ctx context.Context, code string) (*Coupon, error) {
	var c Coupon
	err := s.db.QueryRow(ctx, `SELECT id,seller_id,code,description,discount_type,discount_value,
		max_discount_amount,min_order_amount,max_uses,uses_count,max_uses_per_user,
		applicable_to,is_active,starts_at,expires_at
		FROM coupons WHERE code=$1 AND is_active=TRUE AND starts_at<=NOW()
		AND (expires_at IS NULL OR expires_at>NOW())`, code).Scan(
		&c.ID, &c.SellerID, &c.Code, &c.Description, &c.DiscountType, &c.DiscountValue,
		&c.MaxDiscountAmount, &c.MinOrderAmount, &c.MaxUses, &c.UsesCount, &c.MaxUsesPerUser,
		&c.ApplicableTo, &c.IsActive, &c.StartsAt, &c.ExpiresAt,
	)
	return &c, err
}

// CountCouponUsagesByUser returns how many times a user has already
// redeemed a coupon. Audit O10: the service uses this against
// max_uses_per_user before applying the discount at checkout.
func (s *Store) CountCouponUsagesByUser(ctx context.Context, couponID, userID uuid.UUID) (int, error) {
	var n int
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM coupon_usages WHERE coupon_id = $1 AND user_id = $2`,
		couponID, userID).Scan(&n)
	return n, err
}

func (s *Store) IncrCouponUsage(ctx context.Context, couponID, userID, orderID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	if _, err = tx.Exec(ctx, `UPDATE coupons SET uses_count=uses_count+1 WHERE id=$1`, couponID); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO coupon_usages (coupon_id,user_id,order_id) VALUES ($1,$2,$3)`,
		couponID, userID, orderID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
