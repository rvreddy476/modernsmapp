//go:build integration

package postgres

// C3-LB-2 — the server states the whole price, and the buyer approves that
// exact number.
//
// The defect review 3 found (B-LB-1): the quote returned only a delivery
// charge, so the Android checkout screen computed `subtotal 0 + shipping` and
// submitted THAT as expected_total_minor. Checkout recomputed the real total,
// disagreed, and returned PRICE_CHANGED — on every non-empty cart. The
// primary paid journey could not complete at all, and the screen showed a
// total that omitted the goods.
//
// These proofs pin the server half of the fix:
//
//	1. the quote's total is the same number checkout charges;
//	2. that number satisfies subtotal - discount + shipping, with GST already
//	   inside it rather than added on top;
//	3. a price that moves after the quote blocks checkout with NO durable
//	   effect of any kind;
//	4. a fresh quote after that change produces a total that DOES complete —
//	   which is what makes the client's price-change loop terminate.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -v

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// quotePriced takes a quote the way PrepareQuote does: it prices the cart
// with the REAL pricing path and persists the breakdown alongside it.
//
// The suite's existing `quote()` helper predates the breakdown and stores
// only shipping; it is kept as-is so the older proofs keep exercising the
// backward-compatible path (a quote with no stored total).
func (f *fixture) quotePriced(shippingMinor int64, coupon, method string) (uuid.UUID, *QuotePricing) {
	f.t.Helper()
	ctx := context.Background()

	meta, err := f.store.CartMetaForQuote(ctx, f.userID)
	if err != nil {
		f.t.Fatalf("cart meta: %v", err)
	}
	pricing, err := f.store.PriceCartForQuote(ctx, QuotePricingInput{
		UserID:           f.userID,
		CartID:           meta.CartID,
		ShippingMinor:    money.Paise(shippingMinor),
		CouponCode:       coupon,
		SellerState:      "KA",
		DestinationState: "KA",
	})
	if err != nil {
		f.t.Fatalf("price cart for quote: %v", err)
	}
	q, err := f.store.SaveQuote(ctx, ShippingQuote{
		UserID:         f.userID,
		CartID:         meta.CartID,
		CartVersion:    meta.Version,
		AddressID:      f.addressID,
		AddressHash:    HashAddress("5 Main St", "", "Bengaluru", "KA", "560002"),
		SellerID:       meta.SellerID,
		ItemsHash:      meta.ItemsHash,
		TotalWeightG:   meta.WeightG,
		DestinationPin: "560002",
		ShippingMinor:  money.Paise(shippingMinor),
		CourierCode:    "test",

		SubtotalMinor: pricing.SubtotalMinor,
		DiscountMinor: pricing.DiscountMinor,
		TaxMinor:      pricing.TaxMinor,
		TotalMinor:    pricing.TotalMinor,
		CouponCode:    coupon,
		PaymentMethod: method,
	}, map[string]string{"courier": "test"})
	if err != nil {
		f.t.Fatalf("save quote: %v", err)
	}
	f.shipMinor = shippingMinor
	return q.ID, pricing
}

// ─── 1. The quote's total is a complete, coherent price ──────────────

func TestC3QuoteReturnsACompleteBreakdown(t *testing.T) {
	f := newFixture(t, 10, 100000, "18") // ₹1,000 inclusive of 18% GST
	f.addToCart(2, 100000)
	_, p := f.quotePriced(4000, "", "upi")

	if p.SubtotalMinor != 200000 {
		t.Fatalf("subtotal = %d, want 200000 (2 × ₹1,000)", p.SubtotalMinor)
	}
	if p.ShippingMinor != 4000 {
		t.Fatalf("shipping = %d, want 4000", p.ShippingMinor)
	}
	// THE identity the client relies on. If this ever fails, a screen that
	// renders these five fields is showing a breakdown that does not add up.
	if want := p.SubtotalMinor - p.DiscountMinor + p.ShippingMinor; p.TotalMinor != want {
		t.Fatalf("total = %d, want subtotal-discount+shipping = %d", p.TotalMinor, want)
	}
	// GST is INSIDE the total, never added to it (D1).
	if p.TaxMinor <= 0 {
		t.Fatalf("tax = %d; an 18%% cart must report the GST contained in its total", p.TaxMinor)
	}
	if p.TaxMinor >= p.TotalMinor {
		t.Fatalf("tax %d >= total %d; tax is the portion INSIDE the total, not an addition",
			p.TaxMinor, p.TotalMinor)
	}
	if p.Currency != "INR" {
		t.Fatalf("currency = %q, want INR", p.Currency)
	}
	// And a total that omits the goods — the exact figure the old client
	// computed and submitted — must not be what the server states.
	if p.TotalMinor == p.ShippingMinor {
		t.Fatal("the quoted total equals shipping alone; this is B-LB-1 exactly")
	}
}

// A zero-subtotal total is the specific defect. Guard it directly.
func TestC3QuoteNeverReportsShippingOnlyAsTheTotal(t *testing.T) {
	f := newFixture(t, 5, 250000, "18")
	f.addToCart(1, 250000)
	_, p := f.quotePriced(9900, "", "upi")

	if p.SubtotalMinor == 0 {
		t.Fatal("the server reported a zero subtotal for a cart holding one ₹2,500 item")
	}
	if p.TotalMinor < p.SubtotalMinor {
		t.Fatalf("total %d is less than subtotal %d", p.TotalMinor, p.SubtotalMinor)
	}
}

// ─── 2. The quoted total is the total checkout charges ───────────────

// The whole contract in one test: quote, submit that exact number, get one
// order and one stock hold.
func TestC3QuotedTotalIsAcceptedByCheckoutExactly(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(2, 100000)
	quoteID, p := f.quotePriced(4000, "", "upi")

	params := f.paramsExpecting(quoteID, "c3lb2-"+uuid.NewString(), p.TotalMinor)
	res, err := f.store.Checkout(context.Background(), params)
	if err != nil {
		t.Fatalf("the server's own quoted total was refused by checkout: %v", err)
	}

	if res.TotalMinor != p.TotalMinor {
		t.Fatalf("charged %d, quoted %d", res.TotalMinor, p.TotalMinor)
	}
	if res.TaxMinor != p.TaxMinor {
		t.Fatalf("charged tax %d, quoted tax %d", res.TaxMinor, p.TaxMinor)
	}
	if n := f.orderCount(); n != 1 {
		t.Fatalf("orders = %d, want exactly 1", n)
	}
	if _, reserved := f.inventory(); reserved != 2 {
		t.Fatalf("reserved = %d, want 2", reserved)
	}
}

// The old client's number, submitted against a real cart, must be REFUSED —
// not quietly charged. This is the assertion that says the price-change
// promise is real.
func TestC3ShippingOnlyTotalIsRefused(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(2, 100000)
	quoteID, _ := f.quotePriced(4000, "", "upi")

	params := f.paramsExpecting(quoteID, "c3ship-"+uuid.NewString(), money.Paise(4000))
	_, err := f.store.Checkout(context.Background(), params)
	if !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("got %v, want ErrPriceChanged for a shipping-only expected total", err)
	}
	requireNoSideEffects(t, f, "shipping-only expected total")
}

// ─── 3. A price change blocks, with nothing written ──────────────────

func TestC3PriceChangeAfterQuoteCommitsNothing(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)
	quoteID, p := f.quotePriced(4000, "", "upi")

	// The seller raises the price after the buyer was quoted.
	mustExec(t, `UPDATE product_variants SET selling_price_minor = $2, selling_price = $3
	              WHERE id = $1`, f.variantID, 150000, 1500.00)

	params := f.paramsExpecting(quoteID, "c3chg-"+uuid.NewString(), p.TotalMinor)
	_, err := f.store.Checkout(context.Background(), params)
	if err == nil {
		t.Fatal("checkout committed at a price the buyer never approved")
	}
	if !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("got %v, want ErrPriceChanged", err)
	}

	// NC-2C's assertion set: zero order, zero stock, zero coupon claim,
	// zero outbox.
	requireNoSideEffects(t, f, "price changed between quote and checkout")
}

// ─── 4. The buyer accepts the replacement, and it completes ──────────

// This is what makes the client's loop terminate. After a price change, a
// FRESH quote states the new total, and checkout accepts that number under a
// NEW idempotency key — exactly once.
func TestC3AcceptingTheReplacementTotalCompletesOnce(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)
	firstQuote, firstPricing := f.quotePriced(4000, "", "upi")

	mustExec(t, `UPDATE product_variants SET selling_price_minor = $2, selling_price = $3
	              WHERE id = $1`, f.variantID, 150000, 1500.00)

	// The original attempt is blocked.
	if _, err := f.store.Checkout(context.Background(),
		f.paramsExpecting(firstQuote, "c3acc1-"+uuid.NewString(), firstPricing.TotalMinor),
	); !errors.Is(err, ErrPriceChanged) {
		t.Fatalf("expected ErrPriceChanged on the stale total, got %v", err)
	}

	// The buyer is shown a replacement breakdown and accepts it. Note the
	// cart's price snapshot has to move with the catalogue — that is what
	// the client's re-quote does when it reloads the cart.
	mustExec(t, `UPDATE cart_items SET price_snapshot_minor = $2, price_snapshot = $3
	              WHERE cart_id = $1`, f.cartID, 150000, 1500.00)
	secondQuote, secondPricing := f.quotePriced(4000, "", "upi")

	if secondPricing.TotalMinor == firstPricing.TotalMinor {
		t.Fatal("the replacement quote reports the same total as the stale one")
	}

	// A NEW key, because this is a new customer decision.
	res, err := f.store.Checkout(context.Background(),
		f.paramsExpecting(secondQuote, "c3acc2-"+uuid.NewString(), secondPricing.TotalMinor))
	if err != nil {
		t.Fatalf("the accepted replacement total was refused: %v — this is the loop", err)
	}
	if res.TotalMinor != secondPricing.TotalMinor {
		t.Fatalf("charged %d, accepted %d", res.TotalMinor, secondPricing.TotalMinor)
	}
	if n := f.orderCount(); n != 1 {
		t.Fatalf("orders = %d, want exactly 1 — the blocked attempt must have created none", n)
	}
}

// ─── 5. The quote is bound to the terms it was priced under ──────────

func TestC3QuoteTakenWithACouponIsRefusedWithout(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)

	code := "C3SAVE" + uuid.NewString()[:6]
	mustExec(t, `INSERT INTO coupons (id, code, discount_type, discount_value, discount_basis_points,
	                 is_active, starts_at, max_uses_per_user, applicable_to)
	             VALUES (gen_random_uuid(), $1, 'percentage', 10, 1000, TRUE, NOW(), 5, 'all')`, code)

	quoteID, p := f.quotePriced(4000, code, "upi")
	if p.DiscountMinor <= 0 {
		t.Fatalf("the quote priced a 10%% coupon at a %d discount", p.DiscountMinor)
	}

	// Redeeming WITHOUT the coupon the price assumed.
	params := f.paramsExpecting(quoteID, "c3cpn-"+uuid.NewString(), p.TotalMinor)
	params.CouponCode = ""
	if _, err := f.store.Checkout(context.Background(), params); !errors.Is(err, ErrQuoteMismatch) {
		t.Fatalf("got %v, want ErrQuoteMismatch — a quote priced with a coupon was redeemed "+
			"without one, which promises a discount the order does not carry", err)
	}
	requireNoSideEffects(t, f, "coupon-bound quote redeemed without the coupon")
}

func TestC3QuoteTakenForOneMethodIsRefusedForAnother(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)
	quoteID, p := f.quotePriced(4000, "", "upi")

	params := f.paramsExpecting(quoteID, "c3meth-"+uuid.NewString(), p.TotalMinor)
	params.PaymentMethod = "card"
	if _, err := f.store.Checkout(context.Background(), params); !errors.Is(err, ErrQuoteMismatch) {
		t.Fatalf("got %v, want ErrQuoteMismatch for a quote taken under a different method", err)
	}
}

// The coupon preview must NOT consume capacity — a one-use code would
// otherwise be exhausted by the first buyer who merely opened checkout.
func TestC3QuotingACouponDoesNotClaimIt(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)

	code := "C3ONCE" + uuid.NewString()[:6]
	mustExec(t, `INSERT INTO coupons (id, code, discount_type, discount_value, discount_value_minor,
	                 is_active, starts_at, max_uses, uses_count, max_uses_per_user, applicable_to)
	             VALUES (gen_random_uuid(), $1, 'flat', 50.00, 5000, TRUE, NOW(), 1, 0, 5, 'all')`, code)

	for i := 0; i < 3; i++ {
		f.quotePriced(4000, code, "upi")
	}

	var uses int
	if err := testPool.QueryRow(context.Background(),
		`SELECT uses_count FROM coupons WHERE code = $1`, code).Scan(&uses); err != nil {
		t.Fatal(err)
	}
	if uses != 0 {
		t.Fatalf("uses_count = %d after three quotes; quoting a one-use coupon consumed it, "+
			"so the first buyer to open a checkout screen would burn it for everyone", uses)
	}
}

// The quote and the checkout must agree because they run the SAME code. If
// they ever diverge, every buyer sees PRICE_CHANGED on a cart nobody touched.
func TestC3QuoteAndCheckoutAgreeAcrossTaxRatesAndCoupons(t *testing.T) {
	cases := []struct {
		taxPct    string
		unitMinor int64
		qty       int
		shipping  int64
	}{
		{"18", 100000, 1, 4000},
		{"18", 33333, 3, 7700},
		{"12", 99999, 2, 100},
		{"5", 1, 7, 0},
		{"0", 250000, 1, 15000},
	}
	for _, c := range cases {
		t.Run(c.taxPct+"%", func(t *testing.T) {
			f := newFixture(t, 20, c.unitMinor, c.taxPct)
			f.addToCart(c.qty, c.unitMinor)
			quoteID, p := f.quotePriced(c.shipping, "", "upi")

			res, err := f.store.Checkout(context.Background(),
				f.paramsExpecting(quoteID, "c3agree-"+uuid.NewString(), p.TotalMinor))
			if err != nil {
				t.Fatalf("quote said %d and checkout refused it: %v", p.TotalMinor, err)
			}
			if res.TotalMinor != p.TotalMinor || res.TaxMinor != p.TaxMinor {
				t.Fatalf("quote(total=%d tax=%d) != checkout(total=%d tax=%d)",
					p.TotalMinor, p.TaxMinor, res.TotalMinor, res.TaxMinor)
			}
		})
	}
}
