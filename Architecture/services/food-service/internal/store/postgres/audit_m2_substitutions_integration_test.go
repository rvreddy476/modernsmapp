package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Audit 2026-10-30 M2: a substitution never changes what the customer is
// charged without moving money.

func ptrInt64(v int64) *int64 { return &v }

// seedPaidSubstitutableOrder places a real order at restaurant A pricing
// (one 250.00 item, s.9(5) GST 5%), then marks it paid online and CONFIRMED
// with a payments intent. It returns the order, its line, the customer, the
// owner and the restaurant.
func seedPaidSubstitutableOrder(t *testing.T, s *Store) (orderID, lineID, customerID, ownerID, restaurantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	customerID, restaurantID, ownerID, _ = seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	addr := seedAddress(t, s, customerID, f64(kmNorth(12.9, 1)), f64(77.6))
	order, err := s.PlaceOrder(ctx, customerID, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	orderID = order.ID
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET status = 'CONFIRMED', payment_status = 'CAPTURED', payment_method = 'ONLINE' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.payments (order_id, payment_method, status, provider, provider_payment_id, amount, currency)
		SELECT id, 'ONLINE', 'CAPTURED', 'payments-service', $2, final_amount, 'INR' FROM food.orders WHERE id = $1
	`, orderID, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	lineID = readFirstOrderItemID(t, s, orderID)
	return
}

func seedSubstituteItem(t *testing.T, s *Store, restaurantID uuid.UUID, name string, rupees int) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	if err := s.db.QueryRow(context.Background(), `
		INSERT INTO food.menu_items (restaurant_id, category_id, name, base_price, food_type, preparation_minutes, is_available, is_active, tax_percentage)
		SELECT $1, id, $2, $3, 'VEG', 15, TRUE, TRUE, 5 FROM food.menu_categories WHERE restaurant_id = $1 LIMIT 1
		RETURNING id
	`, restaurantID, name, rupees).Scan(&id); err != nil {
		t.Fatalf("seed menu item: %v", err)
	}
	return id
}

func TestSubstitution_CheaperItemRefundsTheDifferenceAndSettlesTheSupply(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, lineID, customerID, ownerID, restaurantID := seedPaidSubstitutableOrder(t, s)
	cheaper := seedSubstituteItem(t, s, restaurantID, "Veg Biryani", 150)

	// The partner's float is ignored when an item is named: the server prices
	// it from the menu.
	sub, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID: orderID, OriginalItemID: lineID, SuggestedItemID: &cheaper, PriceDiff: 999.99, ProposedBy: ownerID,
	})
	if err != nil {
		t.Fatalf("propose: %v", err)
	}
	// (150 - 250) x 1 = -10000 supply; 5% GST on it = -500.
	if sub.SupplyDiffPaise == nil || *sub.SupplyDiffPaise != -10000 || *sub.TaxDiffPaise != -500 || *sub.PriceDiffPaise != -10500 || sub.PriceDiff != -105 {
		t.Fatalf("substitution priced %+v", sub)
	}

	got, err := s.RespondToSubstitution(ctx, customerID, sub.ID, "approved")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if got.RefundID == nil {
		t.Fatal("approval queued no refund")
	}
	var amount int64
	var status string
	if err := s.db.QueryRow(ctx, `SELECT ROUND(amount * 100)::bigint, status FROM food.refunds WHERE id = $1 AND order_id = $2`, *got.RefundID, orderID).Scan(&amount, &status); err != nil {
		t.Fatal(err)
	}
	if amount != 10500 || status != "PENDING" {
		t.Fatalf("refund = %d %s, want 10500 PENDING", amount, status)
	}
	plan, err := s.OpenRefundPlan(ctx, orderID)
	if err != nil || plan.RefundID != *got.RefundID || plan.OrderStatus != "CONFIRMED" {
		t.Fatalf("open plan %+v %v", plan, err)
	}

	// The refund settles, the order is delivered and settled for the
	// Asia/Kolkata day it was placed on.
	if _, err := s.db.Exec(ctx, `UPDATE food.refunds SET status = 'PROCESSED', processed_at = NOW() WHERE id = $1`, *got.RefundID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET status = 'DELIVERED', delivered_at = NOW() WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	var day string
	if err := s.db.QueryRow(ctx, `SELECT (placed_at AT TIME ZONE 'Asia/Kolkata')::date::text FROM food.orders WHERE id = $1`, orderID).Scan(&day); err != nil {
		t.Fatal(err)
	}
	rid := restaurantID
	if _, err := s.AdminGenerateSettlements(ctx, uuid.New(), SettlementGenerateInput{PeriodStart: day, PeriodEnd: day, RestaurantID: &rid}); err != nil {
		t.Fatalf("generate: %v", err)
	}
	// Net supply 25000 - 10000 = 15000; commission 1500; GST on it 270;
	// payout 13230. The substitution refund is not charged again as a refund
	// share, and gross is what the customer paid in the end (30262 - 10500).
	r := readSettlement(t, s, restaurantID, day)
	if r.net != 15000 || r.commission != 1500 || r.commissionGST != 270 || r.refund != 0 || r.payout != 13230 || r.gross != 19762 {
		t.Fatalf("settlement = %+v", r)
	}

	// The invoice names what was supplied and carries the credit.
	inv, err := s.GetInvoiceData(ctx, customerID, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(inv.Substitutions) != 1 || inv.Substitutions[0].PriceDiffPaise != -10500 || inv.Substitutions[0].SuggestedName != "Veg Biryani" ||
		inv.Substitutions[0].ItemRef != orderItemRef(lineID) {
		t.Fatalf("invoice substitutions = %+v", inv.Substitutions)
	}
}

func TestSubstitution_PriceIncreaseRefused(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, lineID, _, ownerID, restaurantID := seedPaidSubstitutableOrder(t, s)
	dearer := seedSubstituteItem(t, s, restaurantID, "Mutton Biryani", 450)
	if _, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID: orderID, OriginalItemID: lineID, SuggestedItemID: &dearer, ProposedBy: ownerID,
	}); !errors.Is(err, ErrSubstitutionPriceIncrease) {
		t.Fatalf("dearer item: want ErrSubstitutionPriceIncrease, got %v", err)
	}
	name := "Something nicer"
	if _, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID: orderID, OriginalItemID: lineID, SuggestedItemName: &name, PriceDiffPaise: ptrInt64(1), ProposedBy: ownerID,
	}); !errors.Is(err, ErrSubstitutionPriceIncrease) {
		t.Fatalf("free-text +1 paise: want ErrSubstitutionPriceIncrease, got %v", err)
	}
	if _, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID: orderID, OriginalItemID: lineID, SuggestedItemName: &name, PriceDiff: -20.5, ProposedBy: ownerID,
	}); !errors.Is(err, ErrSubstitutionPricePaiseRequired) {
		t.Fatalf("float diff: want ErrSubstitutionPricePaiseRequired, got %v", err)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.order_substitutions WHERE order_id = $1`, orderID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused proposals wrote %d rows (%v)", n, err)
	}
}

func TestSubstitution_ApprovalAfterKitchenIsRefusedAndOneOpenPerLine(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, lineID, customerID, ownerID, restaurantID := seedPaidSubstitutableOrder(t, s)
	cheaper := seedSubstituteItem(t, s, restaurantID, "Dal", 100)
	sub, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID: orderID, OriginalItemID: lineID, SuggestedItemID: &cheaper, ProposedBy: ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID: orderID, OriginalItemID: lineID, SuggestedItemID: &cheaper, ProposedBy: ownerID,
	}); !errors.Is(err, ErrSubstitutionNotAllowed) {
		t.Fatalf("second open substitution on a line: %v", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET status = 'OUT_FOR_DELIVERY' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RespondToSubstitution(ctx, customerID, sub.ID, "approved"); !errors.Is(err, ErrSubstitutionNotAllowed) {
		t.Fatalf("approve on the road: want ErrSubstitutionNotAllowed, got %v", err)
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.refunds WHERE order_id = $1`, orderID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("refused approval queued %d refunds (%v)", n, err)
	}
}

func TestSubstitution_CheaperOnCODIsRefused(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, lineID, customerID, ownerID, restaurantID := seedPaidSubstitutableOrder(t, s)
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET payment_method = 'COD', payment_status = 'NOT_REQUIRED' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	cheaper := seedSubstituteItem(t, s, restaurantID, "Dal", 100)
	sub, err := s.ProposeSubstitution(ctx, ownerID, ProposeSubstitutionInput{
		OrderID: orderID, OriginalItemID: lineID, SuggestedItemID: &cheaper, ProposedBy: ownerID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RespondToSubstitution(ctx, customerID, sub.ID, "approved"); !errors.Is(err, ErrSubstitutionRefundUnavailable) {
		t.Fatalf("cheaper on COD: want ErrSubstitutionRefundUnavailable, got %v", err)
	}
}
