//go:build integration

package postgres

// MStore coupons (migration 038) against a real database: the quote and the
// checkout price a coupon identically, the discount lowers the taxable value
// of the lines it applies to and no others, every refusal is typed and
// changes nothing, capacity is claimed exactly once under a race, platform
// coupons are refused while the switch is off, and the bank-offer keys of a
// payment.succeeded event land on the order without touching the amount rule.
//
//	source scratchpad/c1dsn.sh   # commerce_it_test, never commerce_db
//	go test -p 1 -count=1 -tags integration ./internal/store/postgres/ -run Coupon

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// ─── Helpers ────────────────────────────────────────────────────────────

// addProduct gives the fixture's seller another live product (one variant,
// 50 in stock) at unitMinor, GST taxPct.
func (f *fixture) addProduct(unitMinor int64, taxPct string) (productID, variantID uuid.UUID) {
	f.t.Helper()
	ctx := context.Background()
	productID, variantID = uuid.New(), uuid.New()
	var taxID uuid.UUID
	if err := testPool.QueryRow(ctx, `SELECT id FROM tax_classes WHERE name = $1`, "GST "+taxPct+"%").Scan(&taxID); err != nil {
		f.t.Fatalf("tax class %s: %v", taxPct, err)
	}
	mustExec(f.t, `INSERT INTO products (id,seller_id,title,slug,status,approval_status,return_policy_type,tax_class_id,weight_grams)
	               VALUES ($1,$2,'Coupon Product',$3,'active','approved','7_days',$4,300)`,
		productID, f.sellerID, "cp-"+productID.String()[:8], taxID)
	mustExec(f.t, `INSERT INTO product_variants (id,product_id,sku,mrp,selling_price,mrp_minor,selling_price_minor,weight_grams)
	               VALUES ($1,$2,$3,$4,$4,$5,$5,300)`,
		variantID, productID, "CP-"+variantID.String()[:8], float64(unitMinor)/100.0, unitMinor)
	mustExec(f.t, `INSERT INTO inventory_items (variant_id,seller_id,total_qty,reserved_qty) VALUES ($1,$2,50,0)`,
		variantID, f.sellerID)
	seedOfferFor(f.t, productID)
	return productID, variantID
}

func (f *fixture) addLine(productID, variantID uuid.UUID, qty int, unitMinor int64) {
	f.t.Helper()
	mustExec(f.t, `INSERT INTO cart_items (id,cart_id,variant_id,product_id,quantity,price_snapshot,price_snapshot_minor)
	               VALUES (gen_random_uuid(),$1,$2,$3,$4,$5,$6)`,
		f.cartID, variantID, productID, qty, float64(unitMinor)/100.0, unitMinor)
}

// couponSeed is a coupon written straight to the table, so a test can make
// states the write path refuses (expired, not started, used up).
type couponSeed struct {
	seller    *uuid.UUID
	discType  string
	bps       int
	value     int64
	maxDisc   *int64
	minOrder  int64
	maxUses   *int
	usesCount int
	perUser   int
	scope     string
	ids       []uuid.UUID
	startsAt  time.Time
	expiresAt *time.Time
	public    bool
	inactive  bool
}

func seedCoupon(t *testing.T, s couponSeed) string {
	t.Helper()
	code := "CT" + strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:10])
	if s.discType == "" {
		s.discType = "percentage"
	}
	if s.perUser == 0 {
		s.perUser = 5
	}
	if s.scope == "" {
		s.scope = "all"
	}
	if s.startsAt.IsZero() {
		s.startsAt = time.Now().Add(-time.Hour)
	}
	if s.ids == nil {
		s.ids = []uuid.UUID{}
	}
	var bps *int
	var val *int64
	legacy := 0.0
	if s.discType == "percentage" {
		bps = &s.bps
		legacy = float64(s.bps) / 100
	} else {
		val = &s.value
		legacy = float64(s.value) / 100
	}
	mustExec(t, `INSERT INTO coupons (seller_id, code, discount_type, discount_value, discount_basis_points,
	                 discount_value_minor, max_discount_amount_minor, min_order_amount_minor, max_uses, uses_count,
	                 max_uses_per_user, applicable_to, applicable_ids, starts_at, expires_at, is_public, is_active)
	             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)`,
		s.seller, code, s.discType, legacy, bps, val, s.maxDisc, s.minOrder, s.maxUses, s.usesCount,
		s.perUser, s.scope, s.ids, s.startsAt, s.expiresAt, s.public, !s.inactive)
	return code
}

func ptrI(v int) *int       { return &v }
func ptrI64(v int64) *int64 { return &v }
func ptrT(v time.Time) *time.Time {
	return &v
}

// quoteCoupon prices the cart with a coupon the way PrepareQuote does and
// persists the quote with the coupon bound.
func (f *fixture) quoteCoupon(shippingMinor int64, code string, platform bool) (uuid.UUID, *QuotePricing, error) {
	f.t.Helper()
	ctx := context.Background()
	meta, err := f.store.CartMetaForQuote(ctx, f.userID)
	if err != nil {
		f.t.Fatalf("cart meta: %v", err)
	}
	pricing, err := f.store.PriceCartForQuote(ctx, QuotePricingInput{
		UserID: f.userID, CartID: meta.CartID, ShippingMinor: money.Paise(shippingMinor),
		CouponCode: code, SellerState: "KA", DestinationState: "KA", PlatformCouponsEnabled: platform,
	})
	if err != nil {
		return uuid.Nil, nil, err
	}
	q := f.saveQuoteBound(meta, shippingMinor, code, pricing)
	return q, pricing, nil
}

// saveQuoteBound persists a quote bound to `code` (with or without a pricing
// behind it — a refusal at checkout is tested on a quote whose preview was
// skipped).
func (f *fixture) saveQuoteBound(meta *CartMeta, shippingMinor int64, code string, pricing *QuotePricing) uuid.UUID {
	f.t.Helper()
	sq := ShippingQuote{
		UserID: f.userID, CartID: meta.CartID, CartVersion: meta.Version, AddressID: f.addressID,
		AddressHash: HashAddress("5 Main St", "", "Bengaluru", "KA", "560002"),
		SellerID:    meta.SellerID, ItemsHash: meta.ItemsHash, TotalWeightG: meta.WeightG,
		DestinationPin: "560002", ShippingMinor: money.Paise(shippingMinor), CourierCode: "test",
		CouponCode: code, PaymentMethod: "upi",
	}
	if pricing != nil {
		sq.SubtotalMinor, sq.DiscountMinor, sq.TaxMinor, sq.TotalMinor =
			pricing.SubtotalMinor, pricing.DiscountMinor, pricing.TaxMinor, pricing.TotalMinor
	}
	q, err := f.store.SaveQuote(context.Background(), sq, map[string]string{"courier": "test"})
	if err != nil {
		f.t.Fatalf("save quote: %v", err)
	}
	f.shipMinor = shippingMinor
	return q.ID
}

func (f *fixture) checkoutCoupon(quoteID uuid.UUID, code string, total money.Paise, platform bool) (*CheckoutResult, error) {
	p := f.paramsExpecting(quoteID, "cpn-"+uuid.NewString(), total)
	p.CouponCode = code
	p.PlatformCouponsEnabled = platform
	return f.store.Checkout(context.Background(), p)
}

func couponUses(t *testing.T, code string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT uses_count FROM coupons WHERE code=$1`, code).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// gstExtract is the inclusive-GST split, computed here independently of the
// tax package: taxable = floor(net × 10000 / (10000 + bp)).
func gstExtract(net int64, bp int) (taxable, tax int64) {
	if bp == 0 {
		return net, 0
	}
	taxable = net * 10000 / int64(10000+bp)
	return taxable, net - taxable
}

type orderLine struct {
	product                  uuid.UUID
	gross, disc, ship, net   int64
	taxable, tax, cgst, sgst int64
	rate                     int
}

func orderLines(t *testing.T, orderID uuid.UUID) []orderLine {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `
		SELECT product_id, unit_price_minor * quantity, allocated_discount_minor, allocated_shipping_minor,
		       net_inclusive_minor, taxable_minor, tax_amount_minor, cgst_minor, sgst_minor, tax_rate_bp
		  FROM order_items WHERE order_id = $1 ORDER BY variant_id`, orderID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []orderLine
	for rows.Next() {
		var l orderLine
		if err := rows.Scan(&l.product, &l.gross, &l.disc, &l.ship, &l.net, &l.taxable, &l.tax, &l.cgst, &l.sgst, &l.rate); err != nil {
			t.Fatal(err)
		}
		out = append(out, l)
	}
	return out
}

// ─── The money: quote = checkout, GST on the discounted lines ──────────

func TestCouponSellerPercentage_QuoteEqualsCheckout_GSTOnDiscountedLines(t *testing.T) {
	f := newFixture(t, 20, 118000, "18") // ₹1,180 at 18%
	p2, v2 := f.addProduct(52500, "5")   // ₹525 at 5%
	f.addToCart(2, 118000)
	f.addLine(p2, v2, 1, 52500)
	code := seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1250, public: true}) // 12.5%

	quoteID, p, err := f.quoteCoupon(4900, code, false)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	subtotal := int64(2*118000 + 52500)
	wantDisc := subtotal * 1250 / 10000
	if p.SubtotalMinor.Int64() != subtotal || p.DiscountMinor.Int64() != wantDisc {
		t.Fatalf("quote subtotal/discount = %d/%d, want %d/%d", p.SubtotalMinor, p.DiscountMinor, subtotal, wantDisc)
	}
	if p.TotalMinor != p.SubtotalMinor-p.DiscountMinor+p.ShippingMinor {
		t.Fatalf("total %d != subtotal %d - discount %d + shipping %d", p.TotalMinor, p.SubtotalMinor, p.DiscountMinor, p.ShippingMinor)
	}

	res, err := f.checkoutCoupon(quoteID, code, p.TotalMinor, false)
	if err != nil {
		t.Fatalf("checkout at the quoted total: %v", err)
	}
	if res.TotalMinor != p.TotalMinor || res.TaxMinor != p.TaxMinor {
		t.Fatalf("checkout charged %d (tax %d), the quote said %d (tax %d)", res.TotalMinor, res.TaxMinor, p.TotalMinor, p.TaxMinor)
	}

	var orderDisc, orderTotal, orderTax int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT coupon_discount_minor, final_amount_minor, tax_amount_minor FROM orders WHERE id=$1`, res.OrderID).
		Scan(&orderDisc, &orderTotal, &orderTax); err != nil {
		t.Fatal(err)
	}
	if orderDisc != wantDisc {
		t.Fatalf("order coupon_discount_minor = %d, want %d", orderDisc, wantDisc)
	}
	lines := orderLines(t, res.OrderID)
	var sumDisc, sumNet, sumTax int64
	for _, l := range lines {
		if l.net != l.gross-l.disc+l.ship {
			t.Fatalf("line net %d != gross %d - discount %d + shipping %d", l.net, l.gross, l.disc, l.ship)
		}
		taxable, tax := gstExtract(l.net, l.rate)
		if l.taxable != taxable || l.tax != tax {
			t.Fatalf("line at %dbp: taxable/tax %d/%d, want %d/%d on the DISCOUNTED value %d", l.rate, l.taxable, l.tax, taxable, tax, l.net)
		}
		if l.cgst+l.sgst != l.tax {
			t.Fatalf("cgst %d + sgst %d != tax %d", l.cgst, l.sgst, l.tax)
		}
		if l.disc <= 0 {
			t.Fatalf("an all-products coupon left a line undiscounted: %+v", l)
		}
		sumDisc += l.disc
		sumNet += l.net
		sumTax += l.tax
	}
	if sumDisc != wantDisc || sumNet != orderTotal || sumTax != orderTax {
		t.Fatalf("lines sum disc/net/tax %d/%d/%d, order %d/%d/%d", sumDisc, sumNet, sumTax, wantDisc, orderTotal, orderTax)
	}
	if n := couponUses(t, code); n != 1 {
		t.Fatalf("uses_count = %d, want 1", n)
	}
}

func TestCouponProductScoped_DiscountsOnlyItsLines(t *testing.T) {
	f := newFixture(t, 20, 100000, "18")
	p2, v2 := f.addProduct(40000, "12")
	f.addToCart(1, 100000)
	f.addLine(p2, v2, 2, 40000)
	code := seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, scope: "product", ids: []uuid.UUID{p2}, public: true})

	quoteID, p, err := f.quoteCoupon(4000, code, false)
	if err != nil {
		t.Fatalf("quote: %v", err)
	}
	// 10% of the scoped product's lines (2 × ₹400), not of the bag.
	if p.DiscountMinor != 8000 {
		t.Fatalf("discount = %d, want 8000 (10%% of the scoped lines only)", p.DiscountMinor)
	}
	res, err := f.checkoutCoupon(quoteID, code, p.TotalMinor, false)
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	for _, l := range orderLines(t, res.OrderID) {
		taxable, tax := gstExtract(l.net, l.rate)
		if l.taxable != taxable || l.tax != tax {
			t.Fatalf("line tax not on its own net: %+v", l)
		}
		switch l.product {
		case p2:
			if l.disc != 8000 {
				t.Fatalf("scoped line discount = %d, want 8000", l.disc)
			}
		case f.productID:
			if l.disc != 0 {
				t.Fatalf("the unscoped line was discounted by %d — its GST was lowered by a coupon that does not apply to it", l.disc)
			}
		}
	}
}

func TestCouponFlat_LargestRemainderAndNeverBelowZero(t *testing.T) {
	f := newFixture(t, 20, 33333, "18")
	p2, v2 := f.addProduct(33333, "18")
	p3, v3 := f.addProduct(33334, "5")
	f.addToCart(1, 33333)
	f.addLine(p2, v2, 1, 33333)
	f.addLine(p3, v3, 1, 33334)
	code := seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 1001, public: true})
	quoteID, p, err := f.quoteCoupon(100, code, false)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.checkoutCoupon(quoteID, code, p.TotalMinor, false)
	if err != nil {
		t.Fatal(err)
	}
	var sum int64
	for _, l := range orderLines(t, res.OrderID) {
		sum += l.disc
	}
	if sum != 1001 {
		t.Fatalf("allocated discount sums to %d, want exactly 1001 (largest remainder, nothing dropped)", sum)
	}

	// A flat coupon worth more than the bag discounts the bag to zero, not below.
	g := newFixture(t, 5, 500, "0")
	g.addToCart(1, 500)
	big := seedCoupon(t, couponSeed{seller: &g.sellerID, discType: "flat", value: 99900, public: true})
	_, gp, err := g.quoteCoupon(4000, big, false)
	if err != nil {
		t.Fatal(err)
	}
	if gp.DiscountMinor != 500 || gp.TotalMinor != 4000 {
		t.Fatalf("discount/total = %d/%d, want 500/4000 (capped at the bag)", gp.DiscountMinor, gp.TotalMinor)
	}
}

func TestCouponPercentageCap(t *testing.T) {
	f := newFixture(t, 5, 200000, "18")
	f.addToCart(1, 200000)
	code := seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 2000, maxDisc: ptrI64(15000), public: true})
	_, p, err := f.quoteCoupon(0, code, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.DiscountMinor != 15000 {
		t.Fatalf("discount = %d, want the 15000 cap (20%% would be 40000)", p.DiscountMinor)
	}
}

// ─── Refusals: typed, at quote and at checkout, changing nothing ───────

func TestCouponRefusals_TypedAndSideEffectFree(t *testing.T) {
	past := time.Now().Add(-time.Minute)
	future := time.Now().Add(24 * time.Hour)
	cases := []struct {
		name  string
		seed  func(f *fixture) string
		check func(error) bool
	}{
		{"unknown code", func(*fixture) string { return "NOSUCHCODE1" },
			func(e error) bool { return errors.Is(e, ErrCouponInvalid) }},
		{"inactive", func(f *fixture) string {
			return seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true, inactive: true})
		}, func(e error) bool { return errors.Is(e, ErrCouponInvalid) }},
		{"not started", func(f *fixture) string {
			return seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true, startsAt: future})
		}, func(e error) bool {
			var ns *CouponNotStartedError
			return errors.As(e, &ns) && errors.Is(e, ErrCouponNotStarted)
		}},
		{"expired", func(f *fixture) string {
			return seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true,
				startsAt: past.Add(-time.Hour), expiresAt: ptrT(past)})
		}, func(e error) bool { return errors.Is(e, ErrCouponExpired) }},
		{"below the minimum", func(f *fixture) string {
			return seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true, minOrder: 500000})
		}, func(e error) bool {
			var mo *CouponMinOrderError
			return errors.As(e, &mo) && mo.MinOrderMinor == 500000
		}},
		{"used up", func(f *fixture) string {
			return seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true, maxUses: ptrI(3), usesCount: 3})
		}, func(e error) bool { return errors.Is(e, ErrCouponExhausted) }},
		{"another seller's coupon", func(*fixture) string {
			other := newFixture(t, 1, 100, "18")
			return seedCoupon(t, couponSeed{seller: &other.sellerID, bps: 1000, public: true})
		}, func(e error) bool { return errors.Is(e, ErrCouponNotApplicable) }},
		{"scoped to a product not in the bag", func(f *fixture) string {
			p2, _ := f.addProduct(1000, "18")
			return seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true, scope: "product", ids: []uuid.UUID{p2}})
		}, func(e error) bool { return errors.Is(e, ErrCouponNotApplicable) }},
		{"platform coupon, switch off", func(*fixture) string {
			return seedCoupon(t, couponSeed{bps: 1000, public: true})
		}, func(e error) bool { return errors.Is(e, ErrCouponNotAvailable) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t, 10, 100000, "18")
			f.addToCart(1, 100000)
			code := tc.seed(f)
			usesBefore := 0
			if tc.name != "unknown code" {
				usesBefore = couponUses(t, code)
			}

			// At the quote.
			if _, _, err := f.quoteCoupon(4000, code, false); !tc.check(err) {
				t.Fatalf("quote: got %v", err)
			}
			// At checkout, on a quote bound to the code (the claim path).
			meta, err := f.store.CartMetaForQuote(context.Background(), f.userID)
			if err != nil {
				t.Fatal(err)
			}
			qid := f.saveQuoteBound(meta, 4000, code, nil)
			if _, err := f.checkoutCoupon(qid, code, 104000, false); !tc.check(err) {
				t.Fatalf("checkout: got %v", err)
			}
			requireNoSideEffects(t, f, tc.name)
			if tc.name != "unknown code" {
				if n := couponUses(t, code); n != usesBefore {
					t.Fatalf("uses_count moved %d -> %d on a refusal", usesBefore, n)
				}
			}
		})
	}
}

// ─── Use limits ─────────────────────────────────────────────────────────

func TestCouponPerBuyerLimit(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	code := seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true, perUser: 1, maxUses: ptrI(10)})

	f.addToCart(1, 100000)
	q, p, err := f.quoteCoupon(4000, code, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.checkoutCoupon(q, code, p.TotalMinor, false); err != nil {
		t.Fatalf("first use: %v", err)
	}
	f.addToCart(1, 100000)
	if _, _, err := f.quoteCoupon(4000, code, false); !errors.Is(err, ErrCouponExhausted) {
		t.Fatalf("second use by the same buyer at the quote: got %v, want ErrCouponExhausted", err)
	}
	meta, _ := f.store.CartMetaForQuote(context.Background(), f.userID)
	qid := f.saveQuoteBound(meta, 4000, code, nil)
	if _, err := f.checkoutCoupon(qid, code, 104000, false); !errors.Is(err, ErrCouponExhausted) {
		t.Fatalf("second use by the same buyer at checkout: got %v, want ErrCouponExhausted", err)
	}
	if n := couponUses(t, code); n != 1 {
		t.Fatalf("uses_count = %d, want 1", n)
	}
}

// Two buyers race for the LAST use: exactly one order, one claim, and the
// loser gets the typed refusal rather than an order the coupon cannot fund.
func TestCouponLastUseRace(t *testing.T) {
	for round := 0; round < 5; round++ {
		a := newFixture(t, 20, 100000, "18")
		b := newFixtureSharingVariant(t, a)
		code := seedCoupon(t, couponSeed{seller: &a.sellerID, bps: 1000, public: true, maxUses: ptrI(1)})
		a.addToCart(1, 100000)
		b.addToCart(1, 100000)
		qa, pa, err := a.quoteCoupon(4000, code, false)
		if err != nil {
			t.Fatal(err)
		}
		qb, pb, err := b.quoteCoupon(4000, code, false)
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		start := make(chan struct{})
		errs := make([]error, 2)
		for i, run := range []func() error{
			func() error { _, err := a.checkoutCoupon(qa, code, pa.TotalMinor, false); return err },
			func() error { _, err := b.checkoutCoupon(qb, code, pb.TotalMinor, false); return err },
		} {
			wg.Add(1)
			go func(i int, run func() error) {
				defer wg.Done()
				<-start
				errs[i] = run()
			}(i, run)
		}
		close(start)
		wg.Wait()
		ok, refused := 0, 0
		for _, err := range errs {
			switch {
			case err == nil:
				ok++
			case errors.Is(err, ErrCouponExhausted):
				refused++
			default:
				t.Fatalf("round %d: unexpected error %v", round, err)
			}
		}
		if ok != 1 || refused != 1 {
			t.Fatalf("round %d: %d succeeded, %d refused; want exactly one of each", round, ok, refused)
		}
		var usages int
		if err := testPool.QueryRow(context.Background(),
			`SELECT count(*) FROM coupon_usages cu JOIN coupons c ON c.id = cu.coupon_id WHERE c.code = $1`, code).Scan(&usages); err != nil {
			t.Fatal(err)
		}
		if n := couponUses(t, code); n != 1 || usages != 1 {
			t.Fatalf("round %d: uses_count=%d usages=%d, want 1/1", round, n, usages)
		}
	}
}

// ─── The platform switch ────────────────────────────────────────────────

func TestPlatformCoupon_RefusedWhileOff_AppliedAndRecordedWhenOn(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)
	code := seedCoupon(t, couponSeed{bps: 1000, public: true})

	if _, _, err := f.quoteCoupon(4000, code, false); !errors.Is(err, ErrCouponNotAvailable) {
		t.Fatalf("quote with the switch off: got %v, want ErrCouponNotAvailable", err)
	}
	meta, _ := f.store.CartMetaForQuote(context.Background(), f.userID)
	qid := f.saveQuoteBound(meta, 4000, code, nil)
	if _, err := f.checkoutCoupon(qid, code, 94000, false); !errors.Is(err, ErrCouponNotAvailable) {
		t.Fatalf("checkout with the switch off: got %v, want ErrCouponNotAvailable", err)
	}
	requireNoSideEffects(t, f, "platform coupon while off")
	if n := couponUses(t, code); n != 0 {
		t.Fatalf("uses_count = %d after refusals", n)
	}

	q, p, err := f.quoteCoupon(4000, code, true)
	if err != nil {
		t.Fatalf("quote with the switch on: %v", err)
	}
	if p.DiscountMinor != 10000 {
		t.Fatalf("discount = %d, want 10000", p.DiscountMinor)
	}
	res, err := f.checkoutCoupon(q, code, p.TotalMinor, true)
	if err != nil {
		t.Fatalf("checkout with the switch on: %v", err)
	}
	var platformDisc *int64
	if err := testPool.QueryRow(context.Background(),
		`SELECT platform_discount_minor FROM orders WHERE id=$1`, res.OrderID).Scan(&platformDisc); err != nil {
		t.Fatal(err)
	}
	if platformDisc == nil || *platformDisc != 10000 {
		t.Fatalf("platform_discount_minor = %v, want 10000", platformDisc)
	}
}

// A seller coupon's order records no platform discount.
func TestSellerCouponRecordsNoPlatformDiscount(t *testing.T) {
	f := newFixture(t, 10, 100000, "18")
	f.addToCart(1, 100000)
	code := seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true})
	q, p, err := f.quoteCoupon(4000, code, true)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.checkoutCoupon(q, code, p.TotalMinor, true)
	if err != nil {
		t.Fatal(err)
	}
	var platformDisc *int64
	_ = testPool.QueryRow(context.Background(), `SELECT platform_discount_minor FROM orders WHERE id=$1`, res.OrderID).Scan(&platformDisc)
	if platformDisc != nil {
		t.Fatalf("a seller coupon recorded platform_discount_minor = %d", *platformDisc)
	}
}

// ─── Writes ─────────────────────────────────────────────────────────────

func TestCreateCoupon_CodeFormatUniquenessAndOwnership(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 1, 1000, "18")
	other := newFixture(t, 1, 1000, "18")
	actor := uuid.New()
	base := func(code string) CouponCreate {
		return CouponCreate{SellerID: &f.sellerID, Code: code, DiscountType: "percentage", DiscountValue: 1000,
			MaxUsesPerUser: 1, ApplicableTo: "all", IsPublic: true, IsActive: true, CreatedBy: actor}
	}
	suffix := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:8])

	rec, err := f.store.CreateCoupon(ctx, base("dw "+strings.ToLower(suffix)))
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if rec.Code != "DW"+suffix || rec.FundedBy != "seller" || rec.DiscountValue != 1000 || rec.MinOrderMinor != 0 {
		t.Fatalf("stored %+v", rec)
	}
	var legacy float64
	_ = testPool.QueryRow(ctx, `SELECT discount_value FROM coupons WHERE id=$1`, rec.ID).Scan(&legacy)
	if legacy != 10 {
		t.Fatalf("legacy discount_value = %v, want 10 (percent)", legacy)
	}

	if _, err := f.store.CreateCoupon(ctx, base("DW"+suffix)); !errors.Is(err, ErrCouponCodeTaken) {
		t.Fatalf("duplicate code: got %v, want ErrCouponCodeTaken", err)
	}
	if _, err := f.store.CreateCoupon(ctx, base("dw"+strings.ToLower(suffix))); !errors.Is(err, ErrCouponCodeTaken) {
		t.Fatalf("duplicate code in lower case: got %v, want ErrCouponCodeTaken", err)
	}
	for _, bad := range []string{"AB1", "AB-123", "ABCDEFGHIJKLMNOPQRSTU", "ÄBCD1", ""} {
		if _, err := f.store.CreateCoupon(ctx, base(bad)); !errors.Is(err, ErrInvalidCoupon) {
			t.Fatalf("code %q: got %v, want ErrInvalidCoupon", bad, err)
		}
	}

	own := base("OWN" + suffix)
	own.ApplicableTo, own.ApplicableIDs = "product", []uuid.UUID{f.productID}
	if _, err := f.store.CreateCoupon(ctx, own); err != nil {
		t.Fatalf("scoping to own product: %v", err)
	}
	theirs := base("THR" + suffix)
	theirs.ApplicableTo, theirs.ApplicableIDs = "product", []uuid.UUID{f.productID, other.productID}
	if _, err := f.store.CreateCoupon(ctx, theirs); !errors.Is(err, ErrCouponProductNotOwned) {
		t.Fatalf("scoping to another seller's product: got %v, want ErrCouponProductNotOwned", err)
	}
	cat := base("CAT" + suffix)
	cat.ApplicableTo, cat.ApplicableIDs = "category", []uuid.UUID{uuid.New()}
	if _, err := f.store.CreateCoupon(ctx, cat); !errors.Is(err, ErrInvalidCoupon) {
		t.Fatalf("a seller category coupon: got %v, want ErrInvalidCoupon", err)
	}
	flat := base("FLT" + suffix)
	flat.DiscountType, flat.DiscountValue = "flat", 5000
	rf, err := f.store.CreateCoupon(ctx, flat)
	if err != nil {
		t.Fatal(err)
	}
	_ = testPool.QueryRow(ctx, `SELECT discount_value FROM coupons WHERE id=$1`, rf.ID).Scan(&legacy)
	if legacy != 50 || rf.DiscountValue != 5000 {
		t.Fatalf("flat: legacy %v / paise %d, want 50 / 5000", legacy, rf.DiscountValue)
	}

	// An admin (platform) create writes an audit row.
	plat := base("PLT" + suffix)
	plat.SellerID, plat.Admin = nil, true
	pr, err := f.store.CreateCoupon(ctx, plat)
	if err != nil {
		t.Fatal(err)
	}
	if pr.FundedBy != "platform" || pr.SellerID != nil {
		t.Fatalf("platform coupon stored as %s / %v", pr.FundedBy, pr.SellerID)
	}
	var audits int
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM commerce_admin_audit_log WHERE target_id=$1 AND action='coupon_create'`, pr.ID).Scan(&audits)
	if audits != 1 {
		t.Fatalf("audit rows = %d, want 1", audits)
	}
}

func TestPatchCoupon_ThreeStatesAndScope(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 1, 1000, "18")
	other := newFixture(t, 1, 1000, "18")
	suffix := strings.ToUpper(strings.ReplaceAll(uuid.NewString(), "-", "")[:8])
	exp := time.Now().Add(72 * time.Hour).UTC().Truncate(time.Second)
	rec, err := f.store.CreateCoupon(ctx, CouponCreate{
		SellerID: &f.sellerID, Code: "PT" + suffix, Description: "Ten off", DiscountType: "percentage",
		DiscountValue: 1000, MaxDiscountMinor: ptrI64(20000), MinOrderMinor: 50000, MaxUses: ptrI(5),
		MaxUsesPerUser: 1, ApplicableTo: "all", ExpiresAt: &exp, IsPublic: true, IsActive: true, CreatedBy: uuid.New(),
	})
	if err != nil {
		t.Fatal(err)
	}
	seller := CouponPatchScope{SellerID: &f.sellerID}

	// Absent everywhere: nothing changes.
	got, changed, err := f.store.PatchCoupon(ctx, rec.ID, seller, CouponPatch{})
	if err != nil || changed || !got.UpdatedAt.Equal(rec.UpdatedAt) {
		t.Fatalf("empty patch: changed=%v err=%v", changed, err)
	}
	// A value sets only that field.
	got, changed, err = f.store.PatchCoupon(ctx, rec.ID, seller, CouponPatch{Description: Opt[string]{Set: true, Value: "Twelve"}})
	if err != nil || !changed || got.Description == nil || *got.Description != "Twelve" ||
		got.MaxUses == nil || *got.MaxUses != 5 || got.ExpiresAt == nil || got.MaxDiscountMinor == nil {
		t.Fatalf("value patch: %+v changed=%v err=%v", got, changed, err)
	}
	// Null clears; untouched fields stay.
	got, _, err = f.store.PatchCoupon(ctx, rec.ID, seller, CouponPatch{
		MaxUses: Opt[int]{Set: true, Null: true}, ExpiresAt: Opt[time.Time]{Set: true, Null: true},
		MaxDiscountMinor: Opt[int64]{Set: true, Null: true}, Description: Opt[string]{Set: true, Null: true},
		MinOrderMinor: Opt[int64]{Set: true, Null: true},
	})
	if err != nil || got.MaxUses != nil || got.ExpiresAt != nil || got.MaxDiscountMinor != nil ||
		got.Description != nil || got.MinOrderMinor != 0 || got.MaxUsesPerUser != 1 || !got.IsActive {
		t.Fatalf("null patch: %+v err=%v", got, err)
	}
	var legacyMax *float64
	_ = testPool.QueryRow(ctx, `SELECT max_discount_amount FROM coupons WHERE id=$1`, rec.ID).Scan(&legacyMax)
	if legacyMax != nil {
		t.Fatalf("legacy max_discount_amount = %v after clearing the cap", *legacyMax)
	}
	// Not clearable.
	if _, _, err := f.store.PatchCoupon(ctx, rec.ID, seller, CouponPatch{StartsAt: Opt[time.Time]{Set: true, Null: true}}); !errors.Is(err, ErrInvalidCoupon) {
		t.Fatalf("null starts_at: got %v", err)
	}
	if _, _, err := f.store.PatchCoupon(ctx, rec.ID, seller, CouponPatch{IsActive: Opt[bool]{Set: true, Null: true}}); !errors.Is(err, ErrInvalidCoupon) {
		t.Fatalf("null is_active: got %v", err)
	}
	// Deactivate.
	got, _, err = f.store.PatchCoupon(ctx, rec.ID, seller, CouponPatch{IsActive: Opt[bool]{Set: true, Value: false}})
	if err != nil || got.IsActive {
		t.Fatalf("deactivate: %+v %v", got, err)
	}
	// max_uses below what is used.
	mustExec(t, `UPDATE coupons SET uses_count = 3 WHERE id=$1`, rec.ID)
	if _, _, err := f.store.PatchCoupon(ctx, rec.ID, seller, CouponPatch{MaxUses: Opt[int]{Set: true, Value: 2}}); !errors.Is(err, ErrInvalidCoupon) {
		t.Fatalf("max_uses below uses_count: got %v", err)
	}
	// Another seller cannot see it; the console cannot edit it.
	if _, _, err := f.store.PatchCoupon(ctx, rec.ID, CouponPatchScope{SellerID: &other.sellerID},
		CouponPatch{IsActive: Opt[bool]{Set: true, Value: true}}); !errors.Is(err, ErrCouponNotFound) {
		t.Fatalf("other seller: got %v, want ErrCouponNotFound", err)
	}
	if _, _, err := f.store.PatchCoupon(ctx, rec.ID, CouponPatchScope{Admin: true, Actor: uuid.New()},
		CouponPatch{IsActive: Opt[bool]{Set: true, Value: true}}); !errors.Is(err, ErrCouponSellerReadOnly) {
		t.Fatalf("admin on a seller coupon: got %v, want ErrCouponSellerReadOnly", err)
	}

	// An admin edit of a platform coupon is audited with before and after.
	plat, err := f.store.CreateCoupon(ctx, CouponCreate{Code: "PP" + suffix, DiscountType: "flat", DiscountValue: 1000,
		MaxUsesPerUser: 1, ApplicableTo: "all", IsPublic: true, IsActive: true, CreatedBy: uuid.New(), Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	reason := "festival over"
	if _, _, err := f.store.PatchCoupon(ctx, plat.ID, CouponPatchScope{Admin: true, Actor: uuid.New(), Reason: &reason},
		CouponPatch{IsActive: Opt[bool]{Set: true, Value: false}}); err != nil {
		t.Fatal(err)
	}
	var before, after, gotReason string
	if err := testPool.QueryRow(ctx, `SELECT before_state->>'is_active', after_state->>'is_active', reason
	      FROM commerce_admin_audit_log WHERE target_id=$1 AND action='coupon_update'`, plat.ID).Scan(&before, &after, &gotReason); err != nil {
		t.Fatal(err)
	}
	if before != "true" || after != "false" || gotReason != reason {
		t.Fatalf("audit before/after/reason = %s/%s/%s", before, after, gotReason)
	}
}

// ─── best_coupon ────────────────────────────────────────────────────────

func TestBestCoupons_LargestPublicApplicable(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 5, 100000, "18")
	p2, _ := f.addProduct(70000, "18")
	other := newFixture(t, 5, 100000, "18")
	expired := time.Now().Add(-time.Minute)

	seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, maxDisc: ptrI64(5000), public: true}) // 5000
	seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 8000, public: true})    // 8000
	seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 20000, public: false})  // secret
	seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 30000, public: true, minOrder: 100001})
	seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 40000, public: true, inactive: true})
	seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 50000, public: true,
		startsAt: expired.Add(-time.Hour), expiresAt: ptrT(expired)})
	seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 60000, public: true, maxUses: ptrI(1), usesCount: 1})
	seedCoupon(t, couponSeed{seller: &other.sellerID, discType: "flat", value: 70000, public: true})
	seedCoupon(t, couponSeed{bps: 9000, public: true}) // platform: never a product badge
	scoped := seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 9000, public: true,
		scope: "product", ids: []uuid.UUID{f.productID}})

	best, err := f.store.BestCouponsForProducts(ctx, []uuid.UUID{f.productID, p2, other.productID})
	if err != nil {
		t.Fatal(err)
	}
	b := best[f.productID]
	if b == nil || b.Code != scoped || b.DiscountMinor != 9000 || b.PriceAfterMinor != 91000 || b.Title != "₹90 off" {
		t.Fatalf("best for the hero = %+v, want the scoped ₹90 coupon at 91000", b)
	}
	b2 := best[p2]
	if b2 == nil || b2.DiscountMinor != 8000 || b2.PriceAfterMinor != 62000 {
		t.Fatalf("best for p2 = %+v, want the ₹80 flat", b2)
	}
	if bo := best[other.productID]; bo == nil || bo.DiscountMinor != 70000 {
		t.Fatalf("other seller's product = %+v, want its own seller's coupon only", bo)
	}
}

// ─── The bag's list ─────────────────────────────────────────────────────

func TestCartCoupons_ListsWithDiscountAndReasons(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, 5, 100000, "18")
	f.addToCart(1, 100000)
	ten := seedCoupon(t, couponSeed{seller: &f.sellerID, bps: 1000, public: true})
	flat := seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 15000, public: true})
	minOrder := seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 1000, public: true, minOrder: 200000})
	secret := seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 90000, public: false})
	used := seedCoupon(t, couponSeed{seller: &f.sellerID, discType: "flat", value: 500, public: true, perUser: 1})
	mustExec(t, `INSERT INTO coupon_usages (coupon_id, user_id, order_id) SELECT id, $2, gen_random_uuid() FROM coupons WHERE code=$1`, used, f.userID)
	platform := seedCoupon(t, couponSeed{bps: 500, public: true})

	items, subtotal, err := f.store.CartCoupons(ctx, f.userID, false)
	if err != nil {
		t.Fatal(err)
	}
	if subtotal != 100000 {
		t.Fatalf("subtotal = %d", subtotal)
	}
	byCode := map[string]CartCoupon{}
	for _, it := range items {
		byCode[it.Code] = it
	}
	if _, ok := byCode[secret]; ok {
		t.Fatal("a secret code was listed")
	}
	if _, ok := byCode[platform]; ok {
		t.Fatal("a platform coupon was listed while the switch is off")
	}
	if c := byCode[flat]; !c.Applicable || c.DiscountMinor != 15000 || c.Reason != nil {
		t.Fatalf("flat = %+v", c)
	}
	if c := byCode[ten]; !c.Applicable || c.DiscountMinor != 10000 || c.Title != "10% off" {
		t.Fatalf("ten = %+v", c)
	}
	if c := byCode[minOrder]; c.Applicable || c.Reason == nil || *c.Reason != "COUPON_MIN_ORDER" || c.MinOrderMinor != 200000 {
		t.Fatalf("min order = %+v", c)
	}
	if c := byCode[used]; c.Applicable || c.Reason == nil || *c.Reason != "COUPON_USED_UP" {
		t.Fatalf("used = %+v", c)
	}
	if len(items) < 2 || items[0].Code != flat || items[1].Code != ten {
		t.Fatalf("order = %v, want applicable first, largest first", items)
	}

	on, _, err := f.store.CartCoupons(ctx, f.userID, true)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range on {
		found = found || (it.Code == platform && it.Applicable && it.DiscountMinor == 5000)
	}
	if !found {
		t.Fatal("the platform coupon was not listed with the switch on")
	}
	// Listing claims nothing.
	if n := couponUses(t, ten); n != 0 {
		t.Fatalf("listing moved uses_count to %d", n)
	}
}

// ─── payment.succeeded's bank-offer keys ────────────────────────────────

func TestPaymentSucceeded_RecordsTheBankOffer_AmountRuleUnchanged(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	offerID := uuid.NewString()

	checkoutOne := func() (*fixture, *CheckoutResult) {
		f := newFixture(t, 10, 118000, "18")
		f.addToCart(1, 118000)
		res, err := store.Checkout(ctx, f.params(f.quote(7000), "idem-"+uuid.NewString()))
		if err != nil {
			t.Fatalf("checkout: %v", err)
		}
		return f, res
	}
	read := func(id uuid.UUID) (string, *string, *int64, *int64) {
		var pay string
		var title *string
		var disc, captured *int64
		if err := testPool.QueryRow(ctx, `SELECT payment_status, offer_title, offer_discount_minor, captured_minor
		      FROM orders WHERE id=$1`, id).Scan(&pay, &title, &disc, &captured); err != nil {
			t.Fatal(err)
		}
		return pay, title, disc, captured
	}

	// 1. The sample event: amount_minor is the ORDER VALUE, the offer keys say
	//    what the buyer paid.
	f, res := checkoutOne()
	total := res.TotalMinor
	if err := store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		AmountMinor: total, Currency: "INR", PayerID: f.userID,
		Offer: &PaymentOffer{OfferID: offerID, Title: "10% off with HDFC credit cards",
			DiscountMinor: 12500, FundedBy: "bank", CapturedMinor: total - 12500},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	pay, title, disc, captured := read(res.OrderID)
	if pay != "paid" || title == nil || *title != "10% off with HDFC credit cards" ||
		disc == nil || *disc != 12500 || captured == nil || *captured != total.Int64()-12500 {
		t.Fatalf("recorded pay=%s title=%v disc=%v captured=%v", pay, title, disc, captured)
	}
	off, err := store.GetOrderPaymentOffer(ctx, res.OrderID)
	if err != nil || off.OfferID == nil || off.OfferID.String() != offerID || off.FundedBy == nil || *off.FundedBy != "bank" {
		t.Fatalf("GetOrderPaymentOffer = %+v, %v", off, err)
	}

	// 2. Figures that do not reconcile to the order value are not recorded,
	//    and the payment still applies (the value check passed).
	f2, res2 := checkoutOne()
	if err := store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res2.OrderID,
		AmountMinor: res2.TotalMinor, Currency: "INR", PayerID: f2.userID,
		Offer: &PaymentOffer{OfferID: offerID, Title: "bogus", DiscountMinor: 100, CapturedMinor: res2.TotalMinor},
	}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if pay, title, disc, captured := read(res2.OrderID); pay != "paid" || title != nil || disc != nil || captured != nil {
		t.Fatalf("inconsistent offer: pay=%s title=%v disc=%v captured=%v — want paid, nothing recorded", pay, title, disc, captured)
	}

	// 3. The amount rule is unchanged: an event whose amount_minor is the
	//    CAPTURED (lower) amount is a mismatch, offer or not.
	f3, res3 := checkoutOne()
	err = store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res3.OrderID,
		AmountMinor: res3.TotalMinor - 12500, Currency: "INR", PayerID: f3.userID,
		Offer: &PaymentOffer{OfferID: offerID, DiscountMinor: 12500, CapturedMinor: res3.TotalMinor - 25000},
	})
	if !errors.Is(err, ErrAmountMismatch) {
		t.Fatalf("lower amount_minor: got %v, want ErrAmountMismatch", err)
	}
	if pay, title, _, _ := read(res3.OrderID); pay == "paid" || title != nil {
		t.Fatalf("a mismatched event changed the order: pay=%s title=%v", pay, title)
	}

	// 4. A payment with no offer records nothing.
	f4, res4 := checkoutOne()
	if err := store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res4.OrderID,
		AmountMinor: res4.TotalMinor, Currency: "INR", PayerID: f4.userID,
	}); err != nil {
		t.Fatal(err)
	}
	if pay, title, disc, captured := read(res4.OrderID); pay != "paid" || title != nil || disc != nil || captured != nil {
		t.Fatalf("no-offer payment: pay=%s title=%v disc=%v captured=%v", pay, title, disc, captured)
	}
}
