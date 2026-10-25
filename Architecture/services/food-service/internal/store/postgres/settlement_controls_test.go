package postgres

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/settlement"
	"github.com/google/uuid"
)

func TestRestaurantFinanceSummaryUsesSupplyNotCustomerCharges(t *testing.T) {
	line := settlement.ComputeRestaurant([]settlement.Order{{ItemSubtotalPaise: 25000, PlatformFeePaise: 500, DeliveryFeePaise: 2900, FinalAmountPaise: 30262, CommissionBP: 1000}}, settlement.DefaultRules())
	result := restaurantFinanceSummary(uuid.New(), 3, 1, 0, line, 5000, 17050)
	if result["gross_amount_paise"] != int64(25000) || result["payout_amount_paise"] != int64(22050) || result["unpaid_estimate_paise"] != int64(17050) {
		t.Fatalf("summary=%v", result)
	}
}

func TestDeliverySettlementIndiaDayConcurrentReconciliation(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	customer, _, _, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	oid := placeAndDeliver(t, s, customer)
	user, pid := seedDeliveryPartner(t, s)
	if _, err := s.db.Exec(ctx, `UPDATE food.delivery_assignments SET delivery_partner_id=$2,status='DELIVERED',delivered_at=(date_trunc('day',NOW() AT TIME ZONE 'Asia/Kolkata') AT TIME ZONE 'Asia/Kolkata') + interval '30 minutes',delivery_partner_payout=23.20 WHERE order_id=$1`, oid, pid); err != nil {
		t.Fatal(err)
	}
	result, err := s.DeliveryEarnings(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if result["earnings_today_paise"] != int64(2320) {
		t.Fatalf("India-day earnings=%v", result)
	}
	var day string
	if err = s.db.QueryRow(ctx, `SELECT (NOW() AT TIME ZONE 'Asia/Kolkata')::date::text`).Scan(&day); err != nil {
		t.Fatal(err)
	}
	noRestaurant := uuid.New()
	admin := uuid.New()
	in := SettlementGenerateInput{PeriodStart: day, PeriodEnd: day, RestaurantID: &noRestaurant, DeliveryPartnerID: &pid}
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := s.AdminGenerateSettlements(ctx, admin, in); errors <- err }()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	var id uuid.UUID
	var count int
	if err = s.db.QueryRow(ctx, `SELECT id,COUNT(*) OVER() FROM food.delivery_partner_settlements WHERE delivery_partner_id=$1`, pid).Scan(&id, &count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate settlement count=%d", count)
	}
	reference := uuid.NewString()
	errors = make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.AdminMarkDeliverySettlementPaid(ctx, admin, id, reference)
			errors <- err
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err = s.AdminMarkDeliverySettlementPaid(ctx, admin, id, reference+"-different"); err == nil {
		t.Fatal("conflicting paid reference accepted")
	}
	if err = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.outbox_events WHERE event_type='food.delivery_partner.settlement_paid' AND payload->>'settlement_id'=$1`, id.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("duplicate paid event count=%d", count)
	}
	result, err = s.DeliveryEarnings(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if result["paid_paise"] != int64(2320) || result["unpaid_paise"] != int64(0) {
		t.Fatalf("earnings paid balance=%v", result)
	}
}

func TestSettlementControlsReplayOverlapStaleAndCarryForward(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	admin := uuid.New()
	reference := uuid.NewString()
	noDelivery := uuid.New()
	customer, rid, owner, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	oid := placeAndDeliver(t, s, customer)
	// Fixed past dates make the reporting window deterministic in every DB timezone.
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET placed_at='2026-08-10T18:45:00Z',delivered_at='2026-08-10T19:00:00Z' WHERE id=$1`, oid); err != nil {
		t.Fatal(err)
	}
	input := SettlementGenerateInput{PeriodStart: "2026-08-11", PeriodEnd: "2026-08-11", RestaurantID: &rid, DeliveryPartnerID: &noDelivery}
	if _, err := s.AdminGenerateSettlements(ctx, admin, input); err != nil {
		t.Fatal(err)
	}
	var sid uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT id FROM food.restaurant_settlements WHERE restaurant_id=$1`, rid).Scan(&sid); err != nil {
		t.Fatal(err)
	}
	summary, err := s.PartnerRestaurantSummary(ctx, owner, rid)
	if err != nil {
		t.Fatal(err)
	}
	if summary["payout_amount_paise"] != int64(22050) {
		t.Fatalf("summary=%v", summary)
	}
	if _, err = s.PartnerRestaurantSummary(ctx, uuid.New(), rid); err == nil {
		t.Fatal("foreign summary allowed")
	}
	overlap := input
	overlap.PeriodEnd = "2026-08-12"
	if _, err = s.AdminGenerateSettlements(ctx, admin, overlap); err == nil {
		t.Fatal("overlap allowed")
	}
	if _, err = s.AdminMarkRestaurantSettlementPaid(ctx, admin, sid, ""); err == nil {
		t.Fatal("blank reference allowed")
	}
	// A processed refund after generation must fail closed until regenerated.
	if _, err = s.db.Exec(ctx, `INSERT INTO food.refunds(order_id,amount,reason,status,processed_at) VALUES($1,10,'test','PROCESSED',NOW())`, oid); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdminMarkRestaurantSettlementPaid(ctx, admin, sid, reference); err == nil || !strings.Contains(err.Error(), "regenerate") {
		t.Fatalf("stale=%v", err)
	}
	if _, err = s.AdminGenerateSettlements(ctx, admin, input); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdminMarkRestaurantSettlementPaid(ctx, admin, sid, reference); err != nil {
		t.Fatal(err)
	}
	var paidAt time.Time
	if err = s.db.QueryRow(ctx, `SELECT paid_at FROM food.restaurant_settlements WHERE id=$1`, sid).Scan(&paidAt); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdminMarkRestaurantSettlementPaid(ctx, admin, sid, reference); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdminMarkRestaurantSettlementPaid(ctx, admin, sid, "other"); err == nil {
		t.Fatal("paid reference overwritten")
	}
	var events int
	if err = s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.outbox_events WHERE event_type='food.restaurant.settlement_paid' AND payload->>'settlement_id'=$1`, sid.String()).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if events != 1 {
		t.Fatalf("paid event count=%d", events)
	}
	var replayAt time.Time
	if err = s.db.QueryRow(ctx, `SELECT paid_at FROM food.restaurant_settlements WHERE id=$1`, sid).Scan(&replayAt); err != nil {
		t.Fatal(err)
	}
	if !paidAt.Equal(replayAt) {
		t.Fatal("retry changed payment timestamp")
	}
	before := readSettlement(t, s, rid, input.PeriodStart)
	// A late partial refund changes the next payable, not the paid row.
	if _, err = s.db.Exec(ctx, `INSERT INTO food.refunds(order_id,amount,reason,status,processed_at) VALUES($1,10,'late','PROCESSED',NOW())`, oid); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AdminGenerateSettlements(ctx, admin, input); err != nil {
		t.Fatal(err)
	}
	if after := readSettlement(t, s, rid, input.PeriodStart); after.payout != before.payout {
		t.Fatal("paid payout changed")
	}
	nextCustomer, _, _, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	nextOrder := placeAndDeliver(t, s, nextCustomer)
	if _, err = s.db.Exec(ctx, `UPDATE food.orders SET restaurant_id=$2,placed_at='2026-08-12T10:00:00Z',delivered_at='2026-08-12T11:00:00Z' WHERE id=$1`, nextOrder, rid); err != nil {
		t.Fatal(err)
	}
	input.PeriodStart, input.PeriodEnd = "2026-08-12", "2026-08-12"
	if _, err = s.AdminGenerateSettlements(ctx, admin, input); err != nil {
		t.Fatal(err)
	}
	var adjustment, payout int64
	var nextID uuid.UUID
	if err = s.db.QueryRow(ctx, `SELECT id,(breakdown->>'prior_period_adjustment_paise')::bigint,payout_paise FROM food.restaurant_settlements WHERE restaurant_id=$1 AND period_start='2026-08-12'`, rid).Scan(&nextID, &adjustment, &payout); err != nil {
		t.Fatal(err)
	}
	if adjustment <= 0 || payout != 22050-adjustment {
		t.Fatalf("adjustment=%d payout=%d", adjustment, payout)
	}
	if _, err = s.AdminMarkRestaurantSettlementPaid(ctx, admin, nextID, reference); err == nil {
		t.Fatal("duplicate external transfer reference allowed")
	}
	if _, err = s.AdminMarkRestaurantSettlementPaid(ctx, admin, nextID, reference+"-second"); err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit
	carry, err := s.restaurantCarryForwardTx(ctx, tx, rid)
	if err != nil {
		t.Fatal(err)
	}
	if carry != 0 {
		t.Fatalf("late refund deducted twice: remaining %d", carry)
	}
}
