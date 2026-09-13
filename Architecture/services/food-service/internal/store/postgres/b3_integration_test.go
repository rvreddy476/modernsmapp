package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/shared/events"
	"github.com/atpost/shared/kyc"
	"github.com/google/uuid"
)

// Wave 1 B3 integration tests on TEST_PG_DSN (food_it_test, run with -p 1).
// Identifiers are synthetic: the PANs are not issued, the GSTIN check digits
// are computed.

const (
	b3PlatformPAN   = "ZZZCZ9999Z"
	b3RestaurantPAN = "ZZZPZ0000Z"
)

func b3GSTIN(t *testing.T, state, pan string) string {
	t.Helper()
	d, err := kyc.GSTINCheckDigit(state + pan + "1Z")
	if err != nil {
		t.Fatal(err)
	}
	return state + pan + "1Z" + string(d)
}

func testPricingConfig(t *testing.T) pricing.Config {
	t.Helper()
	cfg := pricing.DefaultConfig()
	cfg.PlatformGSTIN = b3GSTIN(t, "29", b3PlatformPAN)
	return cfg
}

type orderMoneyRow struct {
	totals         pricing.Totals
	numericMatches bool
	breakdown      pricing.Breakdown
	needsAdviser   bool
	commission     float64
}

func readOrderMoney(t *testing.T, s *Store, orderID uuid.UUID) orderMoneyRow {
	t.Helper()
	var m orderMoneyRow
	var raw []byte
	if err := s.db.QueryRow(context.Background(), `
		SELECT item_subtotal_paise, addon_total_paise, packaging_fee_paise, delivery_fee_paise,
			platform_fee_paise, tax_total_paise, discount_total_paise, final_amount_paise,
			(item_subtotal * 100 = item_subtotal_paise AND addon_total * 100 = addon_total_paise
			 AND packaging_fee * 100 = packaging_fee_paise AND delivery_fee * 100 = delivery_fee_paise
			 AND platform_fee * 100 = platform_fee_paise AND tax_total * 100 = tax_total_paise
			 AND (restaurant_discount + coupon_discount) * 100 = discount_total_paise
			 AND final_amount * 100 = final_amount_paise),
			tax_breakdown, needs_adviser_confirmation, commission_amount::float8
		FROM food.orders WHERE id = $1
	`, orderID).Scan(&m.totals.ItemSubtotalPaise, &m.totals.AddonTotalPaise, &m.totals.PackagingFeePaise,
		&m.totals.DeliveryFeePaise, &m.totals.PlatformFeePaise, &m.totals.TaxTotalPaise, &m.totals.DiscountTotalPaise,
		&m.totals.FinalAmountPaise, &m.numericMatches, &raw, &m.needsAdviser, &m.commission); err != nil {
		t.Fatalf("read order money: %v", err)
	}
	if err := json.Unmarshal(raw, &m.breakdown); err != nil {
		t.Fatalf("tax_breakdown is not a breakdown: %v", err)
	}
	return m
}

func TestPlaceOrder_WritesPaiseColumnsAndTaxBreakdown(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	lat, lng := 12.9, 77.6

	customer, restaurantID, _, item := seedPlaceableCart(t, s, f64(lat), f64(lng))
	if _, err := s.db.Exec(ctx, `UPDATE food.restaurants SET packaging_fee = 20 WHERE id = $1`, restaurantID); err != nil {
		t.Fatal(err)
	}
	_, cheese := seedAddon(t, s, item, 0, 2, false, "Extra cheese", 30)
	if err := s.ClearCart(ctx, customer); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 2, Addons: []CartAddonInput{{AddonID: cheese, Quantity: 1}}}); err != nil {
		t.Fatal(err)
	}
	// 2 x 25000 + cheese 2 x 3000 + packaging 2000 = 58000 at 5% (s.9(5)) = 2900;
	// platform fee 500 and delivery 2900 at 18% = 90 + 522.
	want := pricing.Totals{ItemSubtotalPaise: 50000, AddonTotalPaise: 6000, PackagingFeePaise: 2000, DeliveryFeePaise: 2900,
		PlatformFeePaise: 500, TaxTotalPaise: 3512, FinalAmountPaise: 64912}

	cart, err := s.GetCart(ctx, customer)
	if err != nil {
		t.Fatal(err)
	}
	if cart.PricingError != nil || cart.TotalsPaise == nil || *cart.TotalsPaise != want {
		t.Fatalf("cart money = %+v / %+v, want %+v", cart.TotalsPaise, cart.PricingError, want)
	}
	if cart.TaxesAndCharges == nil || cart.TaxesAndCharges.AdviserNotice != pricing.AdviserNotice || cart.TaxesAndCharges.TotalTaxPaise != 3512 {
		t.Fatalf("taxes_and_charges = %+v", cart.TaxesAndCharges)
	}
	if cart.Totals.FinalAmount != 649.12 || cart.Totals.TaxTotal != 35.12 || cart.Totals.PlatformFee != 5 || cart.Totals.DeliveryFee != 29 {
		t.Fatalf("legacy cart totals = %+v", cart.Totals)
	}

	addr := seedAddress(t, s, customer, f64(kmNorth(lat, 1)), f64(lng))
	order, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	m := readOrderMoney(t, s, order.ID)
	if m.totals != want || !m.numericMatches {
		t.Fatalf("order paise = %+v (numeric matches %v), want %+v", m.totals, m.numericMatches, want)
	}
	if !m.needsAdviser || !m.breakdown.NeedsAdviserConfirmation || m.breakdown.TotalPaise != 64912 || len(m.breakdown.Lines) != 5 || m.breakdown.Version != pricing.BreakdownVersion {
		t.Fatalf("breakdown = %+v", m.breakdown)
	}
	// Commission is now on the whole net supply, add-ons and packaging included.
	if m.commission != 58.00 {
		t.Fatalf("commission_amount = %v, want 58.00 (10%% of 580.00)", m.commission)
	}
	if order.Money == nil || order.Money.TotalsPaise != want || order.Money.TaxesAndCharges == nil || !order.Money.NeedsAdviserConfirmation {
		t.Fatalf("order money block = %+v", order.Money)
	}

	var unitP, lineP, taxP int64
	var taxNumeric, pct float64
	if err := s.db.QueryRow(ctx, `
		SELECT unit_price_paise, line_total_paise, tax_amount_paise, tax_amount::float8, tax_percentage_snapshot::float8
		FROM food.order_items WHERE order_id = $1`, order.ID).Scan(&unitP, &lineP, &taxP, &taxNumeric, &pct); err != nil {
		t.Fatal(err)
	}
	// 5% of the item line (50000 -> 2500) plus its add-on (6000 -> 300).
	if unitP != 25000 || lineP != 50000 || taxP != 2800 || taxNumeric != 28 || pct != 5 {
		t.Fatalf("order item = %d %d %d %v %v", unitP, lineP, taxP, taxNumeric, pct)
	}
	var addonUnit, addonLine int64
	if err := s.db.QueryRow(ctx, `
		SELECT oia.unit_price_paise, oia.line_total_paise FROM food.order_item_addons oia
		JOIN food.order_items oi ON oi.id = oia.order_item_id WHERE oi.order_id = $1`, order.ID).Scan(&addonUnit, &addonLine); err != nil {
		t.Fatal(err)
	}
	if addonUnit != 3000 || addonLine != 6000 {
		t.Fatalf("add-on paise = %d %d", addonUnit, addonLine)
	}
	var paymentAmount float64
	if err := s.db.QueryRow(ctx, `SELECT amount::float8 FROM food.payments WHERE order_id = $1`, order.ID).Scan(&paymentAmount); err != nil {
		t.Fatal(err)
	}
	if paymentAmount != 649.12 {
		t.Fatalf("payment amount = %v", paymentAmount)
	}
}

// The payment consumer compares the event with the order's paise total.
func TestPaymentEventAmountIsTheOrderPaiseTotal(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	customer, _, _, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	addr := seedAddress(t, s, customer, f64(kmNorth(12.9, 1)), f64(77.6))
	order, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "ONLINE"}, uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	// 25000 + 1250 (5%) + 500 + 90 + 2900 + 522.
	ev := func(amount int64) payments.Event {
		return payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentSucceeded, OrderID: order.ID,
			PayerID: customer, AmountMinor: amount, Currency: "INR", Status: "succeeded"}
	}
	short, err := s.ApplyPaymentEvent(ctx, ev(30261))
	if err != nil || short.Decision.Outcome != payments.OutcomeAmountMismatch {
		t.Fatalf("one paise short = %+v, %v", short.Decision, err)
	}
	exact, err := s.ApplyPaymentEvent(ctx, ev(30262))
	if err != nil || exact.Decision.Outcome != payments.OutcomeConfirmed {
		t.Fatalf("exact = %+v, %v", exact.Decision, err)
	}
}

func TestRestaurantWithoutTaxCategoryCannotTakeAnOrder(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	customer, restaurantID, _, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	if _, err := s.db.Exec(ctx, `UPDATE food.restaurants SET tax_category = NULL WHERE id = $1`, restaurantID); err != nil {
		t.Fatal(err)
	}
	cart, err := s.GetCart(ctx, customer)
	if err != nil {
		t.Fatal(err)
	}
	if cart.PricingError == nil || cart.PricingError.Code != "FOOD_RESTAURANT_TAX_CATEGORY_MISSING" || cart.TaxesAndCharges != nil || cart.TotalsPaise != nil {
		t.Fatalf("cart = %+v / %+v", cart.PricingError, cart.TaxesAndCharges)
	}
	addr := seedAddress(t, s, customer, f64(kmNorth(12.9, 1)), f64(77.6))
	if _, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString()); !errors.Is(err, pricing.ErrRestaurantTaxCategoryMissing) {
		t.Fatalf("place = %v, want ErrRestaurantTaxCategoryMissing", err)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.orders WHERE user_id = $1`, customer).Scan(&n); err != nil || n != 0 {
		t.Fatalf("orders written = %d (%v)", n, err)
	}
}

// No TEST_PG_DSN needed: a nil pool panics if the flag check is ever moved
// after the first query.
func TestCouponsFlagRefusesBeforeTheDatabase(t *testing.T) {
	s := New(nil)
	ctx := context.Background()
	if _, err := s.ApplyCoupon(ctx, uuid.New(), "FIGO50"); !errors.Is(err, pricing.ErrCouponsDisabled) {
		t.Fatalf("apply coupon = %v", err)
	}
	if _, err := s.PlaceOrder(ctx, uuid.New(), PlaceOrderInput{AddressID: uuid.New(), PaymentMethod: "COD", CouponCode: "FIGO50"}, "k"); !errors.Is(err, pricing.ErrCouponsDisabled) {
		t.Fatalf("place with coupon = %v", err)
	}
}

func placeAndDeliver(t *testing.T, s *Store, customer uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	addr := seedAddress(t, s, customer, f64(kmNorth(12.9, 1)), f64(77.6))
	order, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET status = 'DELIVERED', delivered_at = NOW() WHERE id = $1`, order.ID); err != nil {
		t.Fatal(err)
	}
	return order.ID
}

type settlementRow struct {
	net, commission, commissionGST, passthrough, tcs, refund, payout, gross int64
	payoutAmount                                                            float64
	status                                                                  string
	hasBreakdown                                                            bool
}

func readSettlement(t *testing.T, s *Store, restaurantID uuid.UUID, day string) settlementRow {
	t.Helper()
	var r settlementRow
	var n int
	if err := s.db.QueryRow(context.Background(), `
		SELECT net_supply_paise, commission_paise, commission_gst_paise, gst_passthrough_paise, tcs_paise,
			refund_share_paise, payout_paise, gross_order_paise, payout_amount::float8, status::text,
			breakdown IS NOT NULL, COUNT(*) OVER ()
		FROM food.restaurant_settlements WHERE restaurant_id = $1 AND period_start = $2::date`, restaurantID, day).Scan(
		&r.net, &r.commission, &r.commissionGST, &r.passthrough, &r.tcs, &r.refund, &r.payout, &r.gross,
		&r.payoutAmount, &r.status, &r.hasBreakdown, &n); err != nil {
		t.Fatalf("read settlement: %v", err)
	}
	if n != 1 {
		t.Fatalf("settlement rows = %d", n)
	}
	return r
}

func TestAdminGenerateSettlements_PaysTheRestaurantItsSupplyOnly(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	admin := uuid.New()

	// Restaurant A, s.9(5) at 5%, commission 10%: order 30262 paise.
	aCustomer, aRestaurant, _, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	aOrder := placeAndDeliver(t, s, aCustomer)

	// Restaurant B, specified premises at 18% with a GSTIN: order 33512 paise,
	// with a processed partial refund of 10000.
	bCustomer, bRestaurant, _, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	if _, err := s.db.Exec(ctx, `UPDATE food.restaurants SET tax_category = 'RESTAURANT_SPECIFIED_PREMISES', gstin = $2 WHERE id = $1`,
		bRestaurant, b3GSTIN(t, "29", b3RestaurantPAN)); err != nil {
		t.Fatal(err)
	}
	bOrder := placeAndDeliver(t, s, bCustomer)
	if _, err := s.db.Exec(ctx, `INSERT INTO food.refunds (order_id, amount, reason, status, processed_at) VALUES ($1, 100.00, 'partial', 'PROCESSED', NOW())`, bOrder); err != nil {
		t.Fatal(err)
	}

	var day string
	if err := s.db.QueryRow(ctx, `SELECT placed_at::date::text FROM food.orders WHERE id = $1`, aOrder).Scan(&day); err != nil {
		t.Fatal(err)
	}
	for _, rid := range []uuid.UUID{aRestaurant, bRestaurant} {
		id := rid
		if _, err := s.AdminGenerateSettlements(ctx, admin, SettlementGenerateInput{PeriodStart: day, PeriodEnd: day, RestaurantID: &id}); err != nil {
			t.Fatalf("generate: %v", err)
		}
	}

	// A: net 25000 - commission 2500 - GST on commission 450 = 22050. The
	// platform fee, delivery fee and all GST (s.9(5)) are excluded.
	a := readSettlement(t, s, aRestaurant, day)
	if a.net != 25000 || a.commission != 2500 || a.commissionGST != 450 || a.passthrough != 0 || a.tcs != 0 || a.refund != 0 ||
		a.payout != 22050 || a.payoutAmount != 220.50 || a.gross != 30262 || a.status != "PENDING" || !a.hasBreakdown {
		t.Fatalf("restaurant A settlement = %+v", a)
	}
	// B: 25000 - 2500 - 450 + passthrough 4500 - TCS 125 - refund share 8803
	// (10000 split 29500 : 4012 by largest remainder) = 17622.
	b := readSettlement(t, s, bRestaurant, day)
	if b.net != 25000 || b.commission != 2500 || b.commissionGST != 450 || b.passthrough != 4500 || b.tcs != 125 ||
		b.refund != 8803 || b.payout != 17622 || b.payoutAmount != 176.22 || b.gross != 33512 {
		t.Fatalf("restaurant B settlement = %+v", b)
	}

	// Regenerating an unpaid period rewrites the same row.
	id := aRestaurant
	if _, err := s.AdminGenerateSettlements(ctx, admin, SettlementGenerateInput{PeriodStart: day, PeriodEnd: day, RestaurantID: &id}); err != nil {
		t.Fatal(err)
	}
	if again := readSettlement(t, s, aRestaurant, day); again.payout != 22050 {
		t.Fatalf("regenerated payout = %d", again.payout)
	}

	// Delivery partner: the sum of stored rider payouts in paise (80% of 2900).
	_, partnerID := seedDeliveryPartner(t, s)
	if _, err := s.db.Exec(ctx, `UPDATE food.delivery_assignments SET delivery_partner_id = $2, status = 'DELIVERED', delivered_at = NOW() WHERE order_id = $1`, aOrder, partnerID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AdminGenerateSettlements(ctx, admin, SettlementGenerateInput{PeriodStart: day, PeriodEnd: day, RestaurantID: &id, DeliveryPartnerID: &partnerID}); err != nil {
		t.Fatal(err)
	}
	var riderPaise int64
	var riderAmount float64
	var count int
	if err := s.db.QueryRow(ctx, `SELECT payout_paise, payout_amount::float8, delivery_count FROM food.delivery_partner_settlements WHERE delivery_partner_id = $1`, partnerID).Scan(&riderPaise, &riderAmount, &count); err != nil {
		t.Fatal(err)
	}
	if riderPaise != 2320 || riderAmount != 23.20 || count != 1 {
		t.Fatalf("rider settlement = %d %v %d", riderPaise, riderAmount, count)
	}

	rows, err := s.AdminListRestaurantSettlements(ctx, Pagination{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row["restaurant_id"] == bRestaurant.String() {
			found = true
			if row["payout_paise"] != int64(17622) || row["payout_amount"] != 176.22 || row["breakdown"] == nil {
				t.Fatalf("admin row = %v", row)
			}
		}
	}
	if !found {
		t.Fatal("admin list does not carry restaurant B")
	}
}

var (
	platformInvoiceRe   = regexp.MustCompile(`^FP/\d{4}/\d{6}$`)
	restaurantInvoiceRe = regexp.MustCompile(`^FR/\d{4}/(\d{6})$`)
)

func TestInvoiceNumbersAndDataFromARealOrder(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	customer, restaurantID, _, item := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	if _, err := s.db.Exec(ctx, `UPDATE food.restaurants SET tax_category = 'RESTAURANT_SPECIFIED_PREMISES', gstin = $2, legal_name = 'Invoice Kitchens LLP' WHERE id = $1`,
		restaurantID, b3GSTIN(t, "29", b3RestaurantPAN)); err != nil {
		t.Fatal(err)
	}
	first := placeAndDeliver(t, s, customer)
	if _, err := s.AddCartItem(ctx, customer, AddCartItemInput{MenuItemID: item, Quantity: 1}); err != nil {
		t.Fatal(err)
	}
	second := placeAndDeliver(t, s, customer)

	p1, r1, err := s.AllocateOrderInvoiceNumbers(ctx, first, restaurantID, "2026-27", true, true)
	if err != nil {
		t.Fatal(err)
	}
	if !platformInvoiceRe.MatchString(p1) || !restaurantInvoiceRe.MatchString(r1) {
		t.Fatalf("numbers = %q %q", p1, r1)
	}
	if again1, againR, err := s.AllocateOrderInvoiceNumbers(ctx, first, restaurantID, "2026-27", true, true); err != nil || again1 != p1 || againR != r1 {
		t.Fatalf("allocation not idempotent: %q %q %v", again1, againR, err)
	}
	if m := restaurantInvoiceRe.FindStringSubmatch(r1); m[1] != "000001" {
		t.Fatalf("a restaurant's own series starts at 000001, got %s", r1)
	}
	_, r2, err := s.AllocateOrderInvoiceNumbers(ctx, second, restaurantID, "2026-27", true, true)
	if err != nil {
		t.Fatal(err)
	}
	n1, _ := strconv.Atoi(restaurantInvoiceRe.FindStringSubmatch(r1)[1])
	n2, _ := strconv.Atoi(restaurantInvoiceRe.FindStringSubmatch(r2)[1])
	if n2 != n1+1 {
		t.Fatalf("restaurant series %s then %s", r1, r2)
	}

	d, err := s.GetInvoiceData(ctx, customer, first)
	if err != nil {
		t.Fatal(err)
	}
	if d.Breakdown == nil || d.PlatformInvoiceNumber != p1 || d.RestaurantInvoiceNumber != r1 || d.RestaurantGSTIN == "" ||
		d.RestaurantLegalName != "Invoice Kitchens LLP" || d.FinalAmountPaise != 33512 || d.RestaurantID != restaurantID {
		t.Fatalf("invoice data = %+v", d)
	}
	for _, l := range d.Breakdown.Lines {
		if l.Kind == pricing.KindItem || l.Kind == pricing.KindAddon {
			if ref, ok := d.Lines[l.Ref]; !ok || ref.Name == "" || ref.Quantity == 0 {
				t.Fatalf("line %s has no description (%v)", l.Ref, d.Lines)
			}
		}
	}
}

func seedReadyRestaurant(t *testing.T, s *Store) (ownerID, restaurantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	ownerID, restaurantID = seedDraftRestaurant(t, s)
	if _, err := s.SetRestaurantLocation(ctx, ownerID, restaurantID, testLocation()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ReplaceOperatingHours(ctx, ownerID, restaurantID, []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:00", ClosesAt: "22:00"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetRestaurantCompliance(ctx, ownerID, restaurantID, testSealedCompliance()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SubmitRestaurantFSSAI(ctx, ownerID, restaurantID, testFSSAI(time.Now().AddDate(1, 0, 0))); err != nil {
		t.Fatal(err)
	}
	payout := testSealedPayout()
	payout.AccountLookup = "ready-" + uuid.NewString()
	if _, err := s.UpsertRestaurantPayoutAccount(ctx, ownerID, restaurantID, payout); err != nil {
		t.Fatal(err)
	}
	seedMenuItem(t, s, restaurantID, true)
	return ownerID, restaurantID
}

func TestSubmitAfterRejection(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedReadyRestaurant(t, s)
	if _, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID); err != nil {
		t.Fatalf("first submit: %v", err)
	}
	if err := s.AdminApproveRestaurant(ctx, uuid.New(), restaurantID, false, "photos unreadable"); err != nil {
		t.Fatalf("reject: %v", err)
	}
	// The same readiness check applies to a resubmission.
	if _, err := s.db.Exec(ctx, `DELETE FROM food.restaurant_operating_hours WHERE restaurant_id = $1`, restaurantID); err != nil {
		t.Fatal(err)
	}
	_, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID)
	if got := strings.Join(missingOf(t, err), ","); got != onboarding.StepOperatingHours {
		t.Fatalf("resubmit missing = %s", got)
	}
	if _, err := s.ReplaceOperatingHours(ctx, ownerID, restaurantID, []OperatingHoursInput{{DayOfWeek: dayPtr(1), OpensAt: "10:00", ClosesAt: "22:00"}}); err != nil {
		t.Fatal(err)
	}
	sub, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID)
	if err != nil || sub.Status != "PENDING_REVIEW" {
		t.Fatalf("resubmit = %+v, %v", sub, err)
	}
	var restaurantStatus, partnerStatus string
	if err := s.db.QueryRow(ctx, `SELECT r.status::text, p.status::text FROM food.restaurants r JOIN food.restaurant_partners p ON p.id = r.partner_id WHERE r.id = $1`, restaurantID).Scan(&restaurantStatus, &partnerStatus); err != nil {
		t.Fatal(err)
	}
	if restaurantStatus != "PENDING_REVIEW" || partnerStatus != "PENDING_REVIEW" {
		t.Fatalf("statuses = %s / %s", restaurantStatus, partnerStatus)
	}
	if _, err := s.SubmitRestaurantForReview(ctx, ownerID, restaurantID); !errors.Is(err, ErrRestaurantNotDraft) {
		t.Fatalf("submit while pending = %v", err)
	}
}

func TestUpdatePartnerRestaurantLeavesLocationAlone(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()
	ownerID, restaurantID := seedDraftRestaurant(t, s)
	if _, err := s.SetRestaurantLocation(ctx, ownerID, restaurantID, testLocation()); err != nil {
		t.Fatal(err)
	}
	// The slug is derived from the name and unique across restaurants, and
	// food_it_test keeps rows between runs, so the new name is unique per run.
	renamed := "Renamed Kitchen " + uuid.NewString()[:8]
	if _, err := s.UpdatePartnerRestaurant(ctx, ownerID, restaurantID, PartnerRestaurantInput{
		Name: renamed, AddressLine1: "999 Elsewhere", City: "Mysuru", State: "Goa", PostalCode: "403001",
		Latitude: f64(15.5), Longitude: f64(73.8), PackagingFee: 5,
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	var lat, lng, packaging, areaLat float64
	var line1, city, name string
	if err := s.db.QueryRow(ctx, `
		SELECT r.latitude::float8, r.longitude::float8, r.address_line1, r.city, r.name, r.packaging_fee::float8,
			(SELECT center_latitude::float8 FROM food.restaurant_service_areas WHERE restaurant_id = r.id AND is_active)
		FROM food.restaurants r WHERE r.id = $1`, restaurantID).Scan(&lat, &lng, &line1, &city, &name, &packaging, &areaLat); err != nil {
		t.Fatal(err)
	}
	if lat != 12.9716 || lng != 77.5946 || line1 != "1 Test Lane" || city != "Bengaluru" || areaLat != 12.9716 {
		t.Fatalf("location drifted: %v %v %s %s (area %v)", lat, lng, line1, city, areaLat)
	}
	if name != renamed || packaging != 5 {
		t.Fatalf("non-location fields not updated: %s %v", name, packaging)
	}
}

func TestPayoutAccountShared(t *testing.T) {
	partner := uuid.New()
	other := uuid.New()
	me := payoutOwner{Type: PayoutOwnerRestaurant, ID: uuid.New(), PartnerID: &partner}
	cases := []struct {
		name   string
		me     payoutOwner
		others []payoutOwner
		want   bool
	}{
		{"nobody else", me, nil, false},
		{"another outlet of the same partner", me, []payoutOwner{{Type: PayoutOwnerRestaurant, ID: uuid.New(), PartnerID: &partner}}, false},
		{"a restaurant of another partner", me, []payoutOwner{{Type: PayoutOwnerRestaurant, ID: uuid.New(), PartnerID: &other}}, true},
		{"a rider", me, []payoutOwner{{Type: PayoutOwnerDeliveryPartner, ID: uuid.New()}}, true},
		{"two riders", payoutOwner{Type: PayoutOwnerDeliveryPartner, ID: uuid.New()}, []payoutOwner{{Type: PayoutOwnerDeliveryPartner, ID: uuid.New()}}, true},
		{"its own row", me, []payoutOwner{me}, false},
	}
	for _, tc := range cases {
		if got := payoutAccountShared(tc.me, tc.others); got != tc.want {
			t.Errorf("%s: shared = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func readPayoutReview(t *testing.T, s *Store, ownerType string, ownerID uuid.UUID) (bool, string) {
	t.Helper()
	var flagged bool
	var reason *string
	if err := s.db.QueryRow(context.Background(), `SELECT needs_review, review_reason FROM food.payout_accounts WHERE owner_type = $1 AND owner_id = $2`, ownerType, ownerID).Scan(&flagged, &reason); err != nil {
		t.Fatal(err)
	}
	if reason == nil {
		return flagged, ""
	}
	return flagged, *reason
}

func TestSharedPayoutAccountIsFlaggedNotBlocked(t *testing.T) {
	s, done := foodTestStore(t)
	defer done()
	ctx := context.Background()

	shared := testSealedPayout()
	shared.AccountLookup = "shared-" + uuid.NewString()
	rider1, rider1ID := seedDeliveryPartner(t, s)
	rider2, rider2ID := seedDeliveryPartner(t, s)
	if _, err := s.UpsertDeliveryPartnerPayoutAccount(ctx, rider1, shared); err != nil {
		t.Fatal(err)
	}
	if flagged, _ := readPayoutReview(t, s, PayoutOwnerDeliveryPartner, rider1ID); flagged {
		t.Fatal("the first holder of an account is not flagged")
	}
	if _, err := s.UpsertDeliveryPartnerPayoutAccount(ctx, rider2, shared); err != nil {
		t.Fatalf("a shared account must not be blocked: %v", err)
	}
	for _, id := range []uuid.UUID{rider1ID, rider2ID} {
		if flagged, reason := readPayoutReview(t, s, PayoutOwnerDeliveryPartner, id); !flagged || reason != PayoutReviewSharedAccount {
			t.Fatalf("rider %s flagged = %v %q", id, flagged, reason)
		}
	}

	// Two outlets of one partner share an account without a flag.
	ownerID, outlet1 := seedDraftRestaurant(t, s)
	var outlet2 uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.restaurants (partner_id, owner_user_id, name, slug, status, address_line1, city)
		SELECT partner_id, owner_user_id, 'Outlet Two', 'outlet-two-' || gen_random_uuid(), 'DRAFT', '2 Test Lane', 'Bengaluru'
		FROM food.restaurants WHERE id = $1 RETURNING id`, outlet1).Scan(&outlet2); err != nil {
		t.Fatal(err)
	}
	outlets := testSealedPayout()
	outlets.AccountLookup = "outlets-" + uuid.NewString()
	for _, rid := range []uuid.UUID{outlet1, outlet2} {
		if _, err := s.UpsertRestaurantPayoutAccount(ctx, ownerID, rid, outlets); err != nil {
			t.Fatal(err)
		}
		if flagged, _ := readPayoutReview(t, s, PayoutOwnerRestaurant, rid); flagged {
			t.Fatalf("outlet %s of the same partner flagged", rid)
		}
	}

	list, err := s.AdminListPayoutAccounts(ctx, true, Pagination{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, a := range list {
		if !a.NeedsReview {
			t.Fatalf("needs_review filter returned an unflagged row: %+v", a)
		}
		if a.OwnerID == rider1ID || a.OwnerID == rider2ID {
			seen++
			if a.ReviewReason == nil || *a.ReviewReason != PayoutReviewSharedAccount || a.AccountNumberMasked != "****6789" {
				t.Fatalf("admin row = %+v", a)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("admin view shows %d of the 2 flagged riders", seen)
	}
	own, err := s.GetDeliveryPartnerPayoutAccount(ctx, rider2)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(own)
	if strings.Contains(string(raw), "needs_review") || strings.Contains(string(raw), "review_reason") {
		t.Fatalf("the partner's own view exposes the review flag: %s", raw)
	}
}

func TestAutoReject_PaidOrderRequestsRefund(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, intentID := seedPaymentOrder(t, s, "CONFIRMED", "CAPTURED")
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET accept_deadline_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AutoRejectExpiredOrders(ctx, 500); err != nil {
		t.Fatalf("auto reject: %v", err)
	}
	status, orderPay, rowPay := readPaymentState(t, s, orderID)
	if status != "REFUND_PENDING" || orderPay != "REFUND_PENDING" || rowPay != "REFUND_PENDING" {
		t.Fatalf("state = %s/%s/%s, want REFUND_PENDING everywhere", status, orderPay, rowPay)
	}
	assertHistoryChain(t, s, orderID, "CONFIRMED", []string{"RESTAURANT_REJECTED", "REFUND_PENDING"})
	plan, err := s.OpenRefundPlan(ctx, orderID)
	if err != nil {
		t.Fatalf("open refund plan: %v", err)
	}
	if plan.AmountMinor != 25000 || plan.Status != "PENDING" || plan.IntentID != intentID || plan.PaymentMethod != "ONLINE" {
		t.Fatalf("plan = %+v", plan)
	}
	pending, err := s.ListUnsubmittedSystemRefunds(ctx, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	listed := false
	for _, p := range pending {
		listed = listed || p.RefundID == plan.RefundID
	}
	if !listed {
		t.Fatal("an unsubmitted system refund is not listed for resubmission")
	}
	if err := s.MarkRefundSubmitted(ctx, plan.RefundID, map[string]any{"payments_command_id": "c"}); err != nil {
		t.Fatal(err)
	}
	// The payment.refunded event finalises it.
	refunded := payments.Event{EventID: "evt-" + uuid.NewString(), EventType: events.EventPaymentRefunded, IntentID: intentID,
		OrderID: orderID, AmountMinor: 25000, Currency: "INR", Status: "refunded"}
	if _, err := s.ApplyPaymentEvent(ctx, refunded); err != nil {
		t.Fatal(err)
	}
	if status, _, _ := readPaymentState(t, s, orderID); status != "REFUNDED" {
		t.Fatalf("after payment.refunded status = %s", status)
	}

	// An unpaid (cash on delivery) order is only rejected.
	cod, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET payment_status = 'NOT_REQUIRED', payment_method = 'COD', accept_deadline_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, cod); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AutoRejectExpiredOrders(ctx, 500); err != nil {
		t.Fatal(err)
	}
	assertOrderStatus(t, s, cod, "RESTAURANT_REJECTED")
	if _, err := s.OpenRefundPlan(ctx, cod); err == nil {
		t.Fatal("a refund was requested for an unpaid order")
	}
}

func TestPartnerReject_PaidOrderRequestsRefund(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	orderID, _, _ := seedPaymentOrder(t, s, "CONFIRMED", "CAPTURED")
	owner := readRestaurantOwner(t, s, orderID)
	order, err := s.PartnerUpdateOrderStatus(ctx, owner, orderID, "RESTAURANT_REJECTED", "out of stock", "")
	if err != nil {
		t.Fatalf("reject: %v", err)
	}
	if order.Status != "REFUND_PENDING" {
		t.Fatalf("status = %s", order.Status)
	}
	if plan, err := s.OpenRefundPlan(ctx, orderID); err != nil || plan.AmountMinor != 25000 {
		t.Fatalf("plan = %+v, %v", plan, err)
	}
}
