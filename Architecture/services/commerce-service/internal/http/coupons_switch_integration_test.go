//go:build integration

package http

// COMMERCE_PLATFORM_COUPONS_ENABLED through the real handlers, service and
// store: a platform code quoted while the switch was on is refused at
// checkout once it is off (422 COUPON_NOT_AVAILABLE, nothing created, no
// capacity taken), and applies — recorded as platform_discount_minor — while
// it stays on. Runs on the contract fixtures' scratch database.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPlatformCouponSwitch_QuoteToCheckout(t *testing.T) {
	e := newContractEnv(t)
	ctx := context.Background()
	defer e.svc.WithPlatformCoupons(false)

	// A sellable bag: one P1.
	e.exec(`DELETE FROM cart_items WHERE cart_id = $1`, ctCart)
	e.exec(`INSERT INTO cart_items (cart_id,variant_id,product_id,quantity,price_snapshot,price_snapshot_minor)
	        VALUES ($1,$2,$3,1,1299.00,129900)`, ctCart, ctV1, ctP1)

	quote := func() (string, int64, int64) {
		t.Helper()
		w := quoteWithCoupon(e, "mstore50")
		if w.Code != http.StatusOK {
			t.Fatalf("quote: %d %s", w.Code, w.Body.String())
		}
		var env struct {
			Data struct {
				QuoteID       string `json:"quote_id"`
				TotalMinor    int64  `json:"total_minor"`
				DiscountMinor int64  `json:"discount_minor"`
			} `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Data.QuoteID, env.Data.TotalMinor, env.Data.DiscountMinor
	}
	checkout := func(idem, quoteID string, total int64) (int, string) {
		w := e.do(ctReq{method: http.MethodPost, path: "/v1/commerce/v2/orders/checkout", user: ctBuyer,
			headers: map[string]string{"Idempotency-Key": idem},
			body: map[string]any{"address_id": ctBuyerAddr.String(), "quote_id": quoteID, "payment_method": "upi",
				"coupon_code": "mstore50", "expected_total_minor": total}})
		return w.Code, w.Body.String()
	}
	count := func(sql string) int {
		var n int
		if err := e.pool.QueryRow(ctx, sql).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	ordersBefore := count(`SELECT count(*) FROM orders`)

	// Quoted with the switch on, checked out with it off.
	e.svc.WithPlatformCoupons(true)
	qid, total, disc := quote()
	if disc != 5000 {
		t.Fatalf("platform discount quoted as %d, want 5000", disc)
	}
	e.svc.WithPlatformCoupons(false)
	code, body := checkout("switch-off", qid, total)
	if code != http.StatusUnprocessableEntity || !strings.Contains(body, `"COUPON_NOT_AVAILABLE"`) {
		t.Fatalf("checkout with the switch off: %d %s", code, body)
	}
	if n := count(`SELECT count(*) FROM orders`); n != ordersBefore {
		t.Fatalf("an order was created (%d -> %d)", ordersBefore, n)
	}
	if n := count(`SELECT uses_count FROM coupons WHERE code = 'MSTORE50'`); n != 0 {
		t.Fatalf("uses_count = %d after a refusal", n)
	}

	// On throughout: applied, and recorded apart from the seller's money.
	e.svc.WithPlatformCoupons(true)
	qid, total, _ = quote()
	code, body = checkout("switch-on", qid, total)
	if code != http.StatusCreated {
		t.Fatalf("checkout with the switch on: %d %s", code, body)
	}
	if n := count(`SELECT count(*) FROM orders WHERE platform_discount_minor = 5000 AND coupon_code = 'MSTORE50'`); n != 1 {
		t.Fatalf("orders recording the platform discount = %d, want 1", n)
	}
}
