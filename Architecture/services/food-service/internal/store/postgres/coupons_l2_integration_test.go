package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Audit 2026-10-30 L2: the per-user limit holds, and a cancelled order gives
// its coupon use back in the cancelling transaction.
func TestCoupon_PerUserLimitAndReleaseOnCancel(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	cfg := s.pricingCfg
	cfg.CouponsEnabled = true
	s = s.WithPricingConfig(cfg)

	customerID, _, _, menuItemID := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	code := "ONCE" + uuid.NewString()[:6]
	var couponID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.coupons (code, title, coupon_type, discount_value, max_discount_amount, min_order_amount,
			per_user_usage_limit, starts_at, ends_at)
		VALUES ($1, 'Once', 'FLAT', 50, 50, 0, 1, NOW() - INTERVAL '1 day', NOW() + INTERVAL '1 day')
		RETURNING id
	`, code).Scan(&couponID); err != nil {
		t.Fatal(err)
	}
	addr := seedAddress(t, s, customerID, f64(kmNorth(12.9, 1)), f64(77.6))
	place := func() (*Order, error) {
		return s.PlaceOrder(ctx, customerID, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD", CouponCode: code}, uuid.NewString())
	}
	refill := func() {
		if _, err := s.AddCartItem(ctx, customerID, AddCartItemInput{MenuItemID: menuItemID, Quantity: 1}); err != nil {
			t.Fatalf("refill cart: %v", err)
		}
	}
	usage := func() (used, redemptions int) {
		if err := s.db.QueryRow(ctx, `SELECT used_count, (SELECT COUNT(*) FROM food.coupon_redemptions WHERE coupon_id = $1)::int FROM food.coupons WHERE id = $1`, couponID).Scan(&used, &redemptions); err != nil {
			t.Fatal(err)
		}
		return
	}

	first, err := place()
	if err != nil {
		t.Fatalf("first use: %v", err)
	}
	var discount int64
	if err := s.db.QueryRow(ctx, `SELECT discount_total_paise FROM food.orders WHERE id = $1`, first.ID).Scan(&discount); err != nil || discount != 5000 {
		t.Fatalf("discount = %d paise (%v), want 5000", discount, err)
	}
	if u, r := usage(); u != 1 || r != 1 {
		t.Fatalf("after first use: used %d redemptions %d", u, r)
	}

	refill()
	if _, err := place(); !errors.Is(err, ErrCouponInvalid) {
		t.Fatalf("second use by the same user: want ErrCouponInvalid, got %v", err)
	}

	if _, err := s.CancelOrder(ctx, customerID, first.ID, "changed my mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if u, r := usage(); u != 0 || r != 0 {
		t.Fatalf("after cancel: used %d redemptions %d, want 0/0", u, r)
	}
	if _, err := place(); err != nil {
		t.Fatalf("use after the cancelled order released it: %v", err)
	}
}
