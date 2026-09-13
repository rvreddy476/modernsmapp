package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/foodevents"
	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// B5c on TEST_PG_DSN (food_it_test, -p 1): order events are written in the
// transaction that makes the change and name their push recipients; the rider
// enters the delivery code; pickup needs an accepted job; a cancellation
// closes the assignment.

type orderOutboxRow struct {
	EventType string
	Key       string
	Raw       string
	Event     foodevents.OrderEvent
}

func readOrderOutbox(t *testing.T, s *Store, orderID uuid.UUID) []orderOutboxRow {
	t.Helper()
	rows, err := s.db.Query(context.Background(), `
		SELECT event_type, partition_key, payload::text
		FROM food.outbox_events
		WHERE partition_key = $1
		ORDER BY id
	`, foodevents.OrderTopic(orderID))
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []orderOutboxRow
	for rows.Next() {
		var r orderOutboxRow
		if err := rows.Scan(&r.EventType, &r.Key, &r.Raw); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(r.Raw), &r.Event); err != nil {
			t.Fatalf("payload %s: %v", r.Raw, err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func outboxTypes(rows []orderOutboxRow) string {
	types := make([]string, len(rows))
	for i, r := range rows {
		types[i] = r.EventType
	}
	return strings.Join(types, ",")
}

// assertAddressed: every row names the order, the customer (user_id) and the
// kitchen (restaurant_owner_user_id), and carries no code.
func assertAddressed(t *testing.T, rows []orderOutboxRow, orderID, customer, owner uuid.UUID) {
	t.Helper()
	for _, r := range rows {
		if r.Key != foodevents.OrderTopic(orderID) || r.Event.ID != orderID.String() || r.Event.OrderID != orderID.String() {
			t.Fatalf("%s: key %s payload %s", r.EventType, r.Key, r.Raw)
		}
		if r.Event.UserID != customer.String() {
			t.Fatalf("%s: user_id = %q, want %s", r.EventType, r.Event.UserID, customer)
		}
		if r.Event.RestaurantOwnerUserID != owner.String() {
			t.Fatalf("%s: restaurant_owner_user_id = %q, want %s", r.EventType, r.Event.RestaurantOwnerUserID, owner)
		}
		if strings.Contains(r.Raw, "code") {
			t.Fatalf("%s carries a code: %s", r.EventType, r.Raw)
		}
	}
}

func countOutboxInTx(t *testing.T, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, orderID uuid.UUID) int {
	t.Helper()
	var n int
	if err := q.QueryRow(context.Background(), `SELECT COUNT(*) FROM food.outbox_events WHERE partition_key = $1`,
		foodevents.OrderTopic(orderID)).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The event row exists only inside the transition's transaction until it
// commits, and a rollback takes it away with the status change.
func TestOrderEventIsWrittenInsideTheTransitionTx(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	owner := readRestaurantOwner(t, s, orderID)
	tx, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if err := transitionOrderTx(ctx, tx, OrderTransition{
		OrderID: orderID, From: orderstate.Confirmed, To: orderstate.Preparing,
		Actor: orderstate.ActorRestaurant, ChangedBy: &owner, Reason: "accepted",
	}); err != nil {
		t.Fatal(err)
	}
	if n := countOutboxInTx(t, tx, orderID); n != 1 {
		t.Fatalf("outbox rows inside the transition tx = %d, want 1", n)
	}
	if n := countOutboxInTx(t, s.db, orderID); n != 0 {
		t.Fatalf("outbox rows visible before commit = %d, want 0", n)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := countOutboxInTx(t, s.db, orderID); n != 0 {
		t.Fatalf("outbox rows after rollback = %d, want 0", n)
	}
	assertOrderStatus(t, s, orderID, "CONFIRMED")
}

// A whole delivery: each committed step writes its event, in order, addressed
// to the customer and the kitchen.
func TestOrderEventsCommitWithTheirTransition(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "CONFIRMED")
	owner := readRestaurantOwner(t, s, orderID)
	seedDeliveryAssignmentWithStatus(t, s, orderID, nil, "CREATED")

	for _, st := range []string{"PREPARING", "READY_FOR_PICKUP"} {
		if _, err := s.PartnerUpdateOrderStatus(ctx, owner, orderID, st, "", ""); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
	}
	rider, partnerID := seedDeliveryPartner(t, s)
	if _, err := s.AcceptDeliveryOfferTx(ctx, rider, seedPendingOffer(t, s, orderID, partnerID)); err != nil {
		t.Fatalf("accept offer: %v", err)
	}
	aid, _, _ := readAssignment(t, s, orderID)
	if _, err := s.DeliveryUpdateAssignment(ctx, rider, aid, "ACCEPTED", ""); err != nil {
		t.Fatalf("rider accept: %v", err)
	}
	pickup, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPickupCode(ctx, owner, orderID, pickup); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, delivery); err != nil {
		t.Fatalf("delivery: %v", err)
	}

	rows := readOrderOutbox(t, s, orderID)
	want := strings.Join([]string{
		foodevents.OrderRestaurantAccepted, foodevents.OrderReadyForPickup, foodevents.DeliveryAssigned,
		foodevents.DeliveryPickedUp, foodevents.DeliveryDelivered,
	}, ",")
	if got := outboxTypes(rows); got != want {
		t.Fatalf("outbox events = %s\nwant %s", got, want)
	}
	assertAddressed(t, rows, orderID, customerID, owner)
	wantStatus := []string{"PREPARING", "READY_FOR_PICKUP", "DELIVERY_ASSIGNED", "PICKED_UP", "DELIVERED"}
	wantPrev := []string{"CONFIRMED", "PREPARING", "DELIVERY_ASSIGNING", "DELIVERY_ASSIGNED", "OUT_FOR_DELIVERY"}
	for i, r := range rows {
		if r.Event.Status != wantStatus[i] || r.Event.PreviousStatus != wantPrev[i] {
			t.Fatalf("%s: status %s from %s, want %s from %s", r.EventType, r.Event.Status, r.Event.PreviousStatus, wantStatus[i], wantPrev[i])
		}
	}
	for _, r := range rows {
		if strings.Contains(r.Raw, pickup) || strings.Contains(r.Raw, delivery) {
			t.Fatalf("%s carries a code value: %s", r.EventType, r.Raw)
		}
	}
}

// Placement is an INSERT, not a transition: PlaceOrder writes
// food.order.placed itself. A cash-on-delivery order is placed CONFIRMED, and
// this event is what tells the kitchen, so it must name the owner.
func TestPlaceOrderEventNamesTheKitchen(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	lat, lng := 12.9, 77.6

	customer, _, owner, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
	addr := seedAddress(t, s, customer, f64(kmNorth(lat, 1)), f64(lng))
	order, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	rows := readOrderOutbox(t, s, order.ID)
	if got := outboxTypes(rows); got != foodevents.OrderPlaced {
		t.Fatalf("events = %s, want %s", got, foodevents.OrderPlaced)
	}
	assertAddressed(t, rows, order.ID, customer, owner)
	if rows[0].Event.Status != "CONFIRMED" || rows[0].Event.OrderNumber != order.OrderNumber {
		t.Fatalf("placed payload = %s", rows[0].Raw)
	}
	if strings.Contains(rows[0].Raw, "amount") || strings.Contains(rows[0].Raw, "address") {
		t.Fatalf("placed payload carries money or an address: %s", rows[0].Raw)
	}
}

// A step that rolls back leaves no event behind.
func TestOrderEventRollsBackWithItsTransition(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	_, holder := seedDeliveryPartner(t, s)
	seedDeliveryAssignmentWithStatus(t, s, orderID, &holder, "ASSIGNED")
	rider, partnerID := seedDeliveryPartner(t, s)
	// The order moves to DELIVERY_ASSIGNED (and its event is written) before
	// the claim finds the assignment already held and fails.
	if _, err := s.AcceptDeliveryOfferTx(ctx, rider, seedPendingOffer(t, s, orderID, partnerID)); !errors.Is(err, ErrOrderStatusConflict) {
		t.Fatalf("accept: want ErrOrderStatusConflict, got %v", err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNING")
	if rows := readOrderOutbox(t, s, orderID); len(rows) != 0 {
		t.Fatalf("rolled-back accept left events: %s", outboxTypes(rows))
	}
}

// A captured payment announces payment_succeeded (with the kitchen), and a
// restaurant rejection of the paid order announces the rejection and the
// system refund request, all from their transactions.
func TestPaymentAndRejectionEventsNameTheirRecipients(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, customerID, intentID := seedPaymentOrder(t, s, "PAYMENT_PENDING", "PENDING")
	owner := readRestaurantOwner(t, s, orderID)
	if _, err := s.ApplyPaymentEvent(ctx, succeeded(orderID, customerID, intentID)); err != nil {
		t.Fatalf("apply: %v", err)
	}
	rows := readOrderOutbox(t, s, orderID)
	if got := outboxTypes(rows); got != foodevents.OrderPaymentSucceeded {
		t.Fatalf("after capture: %s, want only %s", got, foodevents.OrderPaymentSucceeded)
	}
	if rows[0].Event.Status != "CONFIRMED" {
		t.Fatalf("payment_succeeded status = %s", rows[0].Event.Status)
	}

	if _, err := s.PartnerUpdateOrderStatus(ctx, owner, orderID, "RESTAURANT_REJECTED", "out of stock", ""); err != nil {
		t.Fatalf("reject: %v", err)
	}
	rows = readOrderOutbox(t, s, orderID)
	want := strings.Join([]string{foodevents.OrderPaymentSucceeded, foodevents.OrderRestaurantRejected, foodevents.OrderRefundRequested}, ",")
	if got := outboxTypes(rows); got != want {
		t.Fatalf("after rejection: %s\nwant %s", got, want)
	}
	assertAddressed(t, rows, orderID, customerID, owner)
	if strings.Contains(rows[1].Raw, "out of stock") {
		t.Fatalf("free-text reason travelled: %s", rows[1].Raw)
	}
}

// Wrong delivery codes are counted and the assignment locks.
func TestRiderVerifyDeliveryCode_LocksAfterTooManyWrongCodes(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "PICKED_UP")
	rider, partnerID := seedDeliveryPartner(t, s)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "PICKED_UP")
	_, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	wrong := "0000"
	if delivery == wrong {
		wrong = "1111"
	}
	for i := 0; i < MaxDeliveryCodeAttempts; i++ {
		if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, wrong); !errors.Is(err, ErrDeliveryCodeInvalid) {
			t.Fatalf("wrong code %d: want ErrDeliveryCodeInvalid, got %v", i+1, err)
		}
	}
	if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, delivery); !errors.Is(err, ErrDeliveryCodeLocked) {
		t.Fatalf("right code after %d wrong ones: want ErrDeliveryCodeLocked, got %v", MaxDeliveryCodeAttempts, err)
	}
	assertOrderStatus(t, s, orderID, "PICKED_UP")
	var failed int
	if err := s.db.QueryRow(ctx, `SELECT delivery_code_failed_attempts FROM food.delivery_assignments WHERE id = $1`, aid).Scan(&failed); err != nil || failed != MaxDeliveryCodeAttempts {
		t.Fatalf("failed attempts = %d (%v)", failed, err)
	}
	if rows := readOrderOutbox(t, s, orderID); len(rows) != 0 {
		t.Fatalf("a refused verify wrote events: %s", outboxTypes(rows))
	}
}

// A partner who is not ACTIVE cannot complete even their own delivery.
func TestRiderVerifyDeliveryCode_InactiveRiderRefused(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	rider, partnerID := seedDeliveryPartnerWithStatus(t, s, "SUSPENDED", false)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ARRIVED_AT_CUSTOMER")
	_, delivery, _ := s.EnsureDeliveryCodes(ctx, orderID)
	if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, delivery); !errors.Is(err, ErrDeliveryPartnerNotActive) {
		t.Fatalf("suspended rider: want ErrDeliveryPartnerNotActive, got %v", err)
	}
	assertOrderStatus(t, s, orderID, "OUT_FOR_DELIVERY")
}

// An offer accept the rider has not confirmed (ASSIGNED) is not a job the
// restaurant may hand food to.
func TestVerifyPickupCode_RefusesUnacceptedAssignment(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ASSIGNED")
	pickup, _, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPickupCode(ctx, readRestaurantOwner(t, s, orderID), orderID, pickup); !errors.Is(err, ErrAssignmentNotReady) {
		t.Fatalf("pickup on ASSIGNED: want ErrAssignmentNotReady, got %v", err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
	if _, _, status := readAssignment(t, s, orderID); status != "ASSIGNED" {
		t.Fatalf("assignment moved to %s", status)
	}
	if rows := readOrderOutbox(t, s, orderID); len(rows) != 0 {
		t.Fatalf("a refused pickup wrote events: %s", outboxTypes(rows))
	}
}

// An admin cancel closes the rider's assignment and its pending offers: it no
// longer counts as the rider's job, for location pings, for step buttons or
// for the verifies.
func TestAdminCancelClosesTheAssignment(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	owner := readRestaurantOwner(t, s, orderID)
	rider, partnerID := seedDeliveryPartner(t, s)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ACCEPTED")
	_, other := seedDeliveryPartner(t, s)
	offerID := seedPendingOffer(t, s, orderID, other)
	pickup, _, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if cur, err := s.GetCurrentDeliveryAssignment(ctx, rider); err != nil || cur.ID != aid {
		t.Fatalf("before cancel the job is current: %+v %v", cur, err)
	}

	if _, err := s.AdminCancelOrder(ctx, uuid.New(), orderID, "ops"); err != nil {
		t.Fatalf("admin cancel: %v", err)
	}

	var status string
	var stamped bool
	if err := s.db.QueryRow(ctx, `SELECT status::text, cancelled_at IS NOT NULL FROM food.delivery_assignments WHERE id = $1`, aid).Scan(&status, &stamped); err != nil {
		t.Fatal(err)
	}
	if status != "CANCELLED" || !stamped {
		t.Fatalf("assignment after admin cancel: status=%s cancelled_at set=%v", status, stamped)
	}
	var offerStatus string
	if err := s.db.QueryRow(ctx, `SELECT status::text FROM food.delivery_offers WHERE id = $1`, offerID).Scan(&offerStatus); err != nil || offerStatus != "superseded" {
		t.Fatalf("pending offer after cancel = %s (%v)", offerStatus, err)
	}
	if _, err := s.GetCurrentDeliveryAssignment(ctx, rider); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("cancelled job still current: %v", err)
	}
	res, err := s.UpdateDeliveryLocation(ctx, rider, LocationUpdate{Latitude: 12.9716, Longitude: 77.5946})
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	for _, id := range res.AssignmentIDs {
		if id == aid {
			t.Fatalf("location ping still tracks the cancelled assignment")
		}
	}
	if len(res.Frames) != 0 {
		t.Fatalf("location frames for a cancelled order: %+v", res.Frames)
	}
	if _, err := s.DeliveryUpdateAssignment(ctx, rider, aid, "ARRIVED_AT_RESTAURANT", ""); err == nil {
		t.Fatal("step button moved a cancelled assignment")
	}
	if err := s.VerifyPickupCode(ctx, owner, orderID, pickup); !errors.Is(err, ErrAssignmentNotReady) {
		t.Fatalf("pickup on a cancelled assignment: %v", err)
	}
	rows := readOrderOutbox(t, s, orderID)
	if got := outboxTypes(rows); got != foodevents.OrderCancelled {
		t.Fatalf("events = %s, want %s", got, foodevents.OrderCancelled)
	}
	assertAddressed(t, rows, orderID, customerID, owner)
	if strings.Contains(rows[0].Raw, "ops") {
		t.Fatalf("admin's free-text reason travelled: %s", rows[0].Raw)
	}
}

// A customer cancel closes the unclaimed assignment the payment created.
func TestCustomerCancelClosesTheUnclaimedAssignment(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "CONFIRMED")
	seedDeliveryAssignmentWithStatus(t, s, orderID, nil, "CREATED")
	if _, err := s.CancelOrder(ctx, customerID, orderID, "changed my mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if _, _, status := readAssignment(t, s, orderID); status != "CANCELLED" {
		t.Fatalf("assignment after customer cancel = %s", status)
	}
}
