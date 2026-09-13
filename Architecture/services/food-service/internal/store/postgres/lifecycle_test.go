package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ─── Claim bypass ─────────────────────────────────────────────────────────

// A stranger partner must not be able to claim an unassigned (CREATED)
// assignment through the bare accept endpoint.
func TestDeliveryUpdateAssignment_StrangerCannotClaimUnassigned(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	assignmentID := seedDeliveryAssignmentWithStatus(t, s, orderID, nil, "CREATED")
	stranger, _ := seedDeliveryPartner(t, s)

	// Not even "invalid transition": to a partner who does not hold it, the
	// assignment does not exist.
	for _, step := range []string{"ACCEPTED", "REJECTED", "ARRIVED_AT_RESTAURANT"} {
		if _, err := s.DeliveryUpdateAssignment(ctx, stranger, assignmentID, step, ""); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("stranger %s on an unassigned assignment: want ErrNoRows, got %v", step, err)
		}
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNING")
	_, partner, status := readAssignment(t, s, orderID)
	if partner != nil || status != "CREATED" {
		t.Fatalf("assignment changed: partner=%v status=%s", partner, status)
	}

	// Someone else's held assignment is equally invisible.
	heldOrder, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, holder := seedDeliveryPartner(t, s)
	held := seedDeliveryAssignmentWithStatus(t, s, heldOrder, &holder, "ASSIGNED")
	if _, err := s.DeliveryUpdateAssignment(ctx, stranger, held, "REJECTED", ""); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("stranger on a held assignment: want ErrNoRows, got %v", err)
	}
	assertOrderStatus(t, s, heldOrder, "DELIVERY_ASSIGNED")
}

// Only an ACTIVE partner may act on an assignment, even their own.
func TestDeliveryUpdateAssignment_InactiveOwnerRefused(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, st := range []string{"PENDING_REVIEW", "SUSPENDED"} {
		t.Run(st, func(t *testing.T) {
			orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
			user, partnerID := seedDeliveryPartnerWithStatus(t, s, st, false)
			assignmentID := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ASSIGNED")
			_, err := s.DeliveryUpdateAssignment(ctx, user, assignmentID, "ACCEPTED", "")
			assertDeliveryPartnerNotActive(t, err)
			_, _, status := readAssignment(t, s, orderID)
			if status != "ASSIGNED" {
				t.Fatalf("assignment moved to %s", status)
			}
		})
	}
}

// Bare picked-up / delivered are refused even for the assigned partner; they
// complete only through the OTP verifies.
func TestDeliveryUpdateAssignment_BarePickupAndDeliveryRefused(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	t.Run("picked-up", func(t *testing.T) {
		orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
		user, partnerID := seedDeliveryPartner(t, s)
		aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ARRIVED_AT_RESTAURANT")
		_, err := s.DeliveryUpdateAssignment(ctx, user, aid, "PICKED_UP", "")
		assertDeliveryOTPRequired(t, err)
		assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
	})
	t.Run("delivered", func(t *testing.T) {
		orderID, _, _ := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
		user, partnerID := seedDeliveryPartner(t, s)
		aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ARRIVED_AT_CUSTOMER")
		_, err := s.DeliveryUpdateAssignment(ctx, user, aid, "DELIVERED", "")
		assertDeliveryOTPRequired(t, err)
		assertOrderStatus(t, s, orderID, "OUT_FOR_DELIVERY")
	})
}

// Rejecting releases the assignment and puts the order back in the queue.
func TestDeliveryUpdateAssignment_RejectReleasesAssignment(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	user, partnerID := seedDeliveryPartner(t, s)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ACCEPTED")
	if _, err := s.DeliveryUpdateAssignment(ctx, user, aid, "REJECTED", ""); err != nil {
		t.Fatalf("reject: %v", err)
	}
	_, partner, status := readAssignment(t, s, orderID)
	if partner != nil || status != "CREATED" {
		t.Fatalf("assignment not released: partner=%v status=%s", partner, status)
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNING")
	assertHistoryChain(t, s, orderID, "DELIVERY_ASSIGNED", []string{"DELIVERY_ASSIGNING"})
}

// The assignment list shows the partner their own work only; it used to list
// every unclaimed CREATED assignment to any online partner.
func TestListDeliveryAssignments_OwnOnly(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	unclaimedOrder, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	unclaimed := seedDeliveryAssignmentWithStatus(t, s, unclaimedOrder, nil, "CREATED")
	mineOrder, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	user, partnerID := seedDeliveryPartner(t, s)
	mine := seedDeliveryAssignmentWithStatus(t, s, mineOrder, &partnerID, "ASSIGNED")

	list, err := s.ListDeliveryAssignments(ctx, user)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	sawMine := false
	for _, a := range list {
		if a.ID == unclaimed {
			t.Fatal("unclaimed assignment listed to a partner")
		}
		if a.DeliveryPartnerID == nil || *a.DeliveryPartnerID != partnerID {
			t.Fatalf("foreign assignment listed: %+v", a)
		}
		if a.ID == mine {
			sawMine = true
		}
	}
	if !sawMine {
		t.Fatal("own assignment missing")
	}
}

// ─── Guarded transitions end to end ───────────────────────────────────────

// Full happy path: restaurant accept → ready → offer → accept → arrive →
// pickup OTP → arrived customer → delivery OTP. Every hop writes history with
// the real from_status.
func TestOrderLifecycle_HappyPath(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "CONFIRMED")
	owner := readRestaurantOwner(t, s, orderID)
	seedDeliveryAssignmentWithStatus(t, s, orderID, nil, "CREATED")

	if _, err := s.PartnerUpdateOrderStatus(ctx, owner, orderID, "PREPARING", "", ""); err != nil {
		t.Fatalf("preparing: %v", err)
	}
	if _, err := s.PartnerUpdateOrderStatus(ctx, owner, orderID, "READY_FOR_PICKUP", "", ""); err != nil {
		t.Fatalf("ready: %v", err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNING")

	user, partnerID := seedDeliveryPartner(t, s)
	offerID := seedPendingOffer(t, s, orderID, partnerID)
	if _, err := s.AcceptDeliveryOfferTx(ctx, user, offerID); err != nil {
		t.Fatalf("accept offer: %v", err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
	aid, _, _ := readAssignment(t, s, orderID)

	for _, st := range []string{"ACCEPTED", "ARRIVED_AT_RESTAURANT"} {
		if _, err := s.DeliveryUpdateAssignment(ctx, user, aid, st, ""); err != nil {
			t.Fatalf("%s: %v", st, err)
		}
		assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
	}
	pickup, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatalf("codes: %v", err)
	}
	if err := s.VerifyPickupCode(ctx, owner, orderID, pickup); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	assertOrderStatus(t, s, orderID, "PICKED_UP")
	if _, err := s.DeliveryUpdateAssignment(ctx, user, aid, "ARRIVED_AT_CUSTOMER", ""); err != nil {
		t.Fatalf("arrived customer: %v", err)
	}
	assertOrderStatus(t, s, orderID, "OUT_FOR_DELIVERY")
	if err := s.VerifyDeliveryCode(ctx, customerID, orderID, delivery); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERED")
	_, _, astatus := readAssignment(t, s, orderID)
	if astatus != "DELIVERED" {
		t.Fatalf("assignment status %s", astatus)
	}
	assertHistoryChain(t, s, orderID, "CONFIRMED", []string{
		"PREPARING", "READY_FOR_PICKUP", "DELIVERY_ASSIGNING", "DELIVERY_ASSIGNED",
		"PICKED_UP", "OUT_FOR_DELIVERY", "DELIVERED",
	})
}

// An order cancelled while offers are out must not end up with an assignment.
func TestAcceptDeliveryOfferTx_CancelledOrderLeavesNoAssignment(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	user, partnerID := seedDeliveryPartner(t, s)
	offerID := seedPendingOffer(t, s, orderID, partnerID)
	if _, err := s.AdminCancelOrder(ctx, uuid.New(), orderID, "ops"); err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	if _, err := s.AcceptDeliveryOfferTx(ctx, user, offerID); err == nil {
		t.Fatal("accepted an offer on a cancelled order")
	}
	var n int
	if err := s.db.QueryRow(ctx, `SELECT COUNT(*) FROM food.delivery_assignments WHERE order_id = $1 AND delivery_partner_id IS NOT NULL`, orderID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("orphan assignment created on a cancelled order")
	}
	assertOrderStatus(t, s, orderID, "CANCELLED_BY_ADMIN")
}

func TestCancelOrder_GuardedWithHistory(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "CONFIRMED")
	if _, err := s.CancelOrder(ctx, customerID, orderID, "changed mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	assertOrderStatus(t, s, orderID, "CANCELLED_BY_CUSTOMER")
	assertHistoryChain(t, s, orderID, "CONFIRMED", []string{"CANCELLED_BY_CUSTOMER"})
	if _, err := s.CancelOrder(ctx, customerID, orderID, "again"); err == nil {
		t.Fatal("double cancel accepted")
	}
	// A customer cannot cancel once the food is on its way.
	onWay, _, c2 := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	if _, err := s.CancelOrder(ctx, c2, onWay, "late"); err == nil {
		t.Fatal("cancel from OUT_FOR_DELIVERY accepted")
	}
	assertOrderStatus(t, s, onWay, "OUT_FOR_DELIVERY")
}

func TestAutoRejectExpiredOrders_GuardedWithHistory(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	// Unpaid (cash on delivery), so the rejection is the whole effect; a paid
	// order also requests its refund (TestAutoReject_PaidOrderRequestsRefund).
	orderID, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET payment_status = 'NOT_REQUIRED', payment_method = 'COD', accept_deadline_at = NOW() - INTERVAL '1 minute' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	ids, err := s.AutoRejectExpiredOrders(ctx, 500)
	if err != nil {
		t.Fatalf("auto reject: %v", err)
	}
	found := false
	for _, id := range ids {
		if id == orderID {
			found = true
		}
	}
	if !found {
		t.Fatal("expired order not returned")
	}
	assertOrderStatus(t, s, orderID, "RESTAURANT_REJECTED")
	assertHistoryChain(t, s, orderID, "CONFIRMED", []string{"RESTAURANT_REJECTED"})
}

func TestAdminCancelOrder_GuardedWithHistory(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "PICKED_UP")
	if _, err := s.AdminCancelOrder(ctx, uuid.New(), orderID, "fraud"); err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	assertHistoryChain(t, s, orderID, "PICKED_UP", []string{"CANCELLED_BY_ADMIN"})
	done, _, _ := seedOrderWithItem(t, s, "DELIVERED")
	if _, err := s.AdminCancelOrder(ctx, uuid.New(), done, "late"); err == nil {
		t.Fatal("admin cancelled a delivered order")
	}
}

// ─── OTP verifies refuse out-of-order use ────────────────────────────────

// A pickup code on an assignment no partner holds must not move the order.
func TestVerifyPickupCode_RefusesUnclaimedAssignment(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	seedDeliveryAssignmentWithStatus(t, s, orderID, nil, "CREATED")
	pickup, _, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyPickupCode(ctx, readRestaurantOwner(t, s, orderID), orderID, pickup); err == nil {
		t.Fatal("pickup verified on an unclaimed assignment")
	}
	_, _, status := readAssignment(t, s, orderID)
	if status != "CREATED" {
		t.Fatalf("assignment moved to %s", status)
	}
}

// A delivery code used before pickup must not mark anything delivered.
func TestVerifyDeliveryCode_RefusesBeforePickup(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ASSIGNED")
	_, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyDeliveryCode(ctx, customerID, orderID, delivery); err == nil {
		t.Fatal("delivery verified before pickup")
	}
	_, _, status := readAssignment(t, s, orderID)
	if status != "ASSIGNED" {
		t.Fatalf("assignment moved to %s", status)
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
}

// ─── PlaceOrder: payout, ETA, serviceability ──────────────────────────────

const kmPerDegLat = 6371.0 * 3.141592653589793 / 180.0

func TestPlaceOrder_CODWritesRiderPayoutAndDistanceETA(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	lat, lng := 12.9, 77.6
	customerID, _, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
	addr := seedAddress(t, s, customerID, f64(lat+2.9/kmPerDegLat), f64(lng))
	order, err := s.PlaceOrder(ctx, customerID, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	var payout float64
	var dist *float64
	if err := s.db.QueryRow(ctx, `SELECT delivery_partner_payout::float8, distance_km::float8 FROM food.delivery_assignments WHERE order_id = $1`, order.ID).Scan(&payout, &dist); err != nil {
		t.Fatalf("read assignment: %v", err)
	}
	if payout != 23.20 {
		t.Fatalf("COD rider payout = %v, want 23.20", payout)
	}
	if dist == nil || *dist < 2.85 || *dist > 2.95 {
		t.Fatalf("assignment distance_km = %v, want ~2.9", dist)
	}
	// prep 20 + ceil(2.9 km / 20 km/h * 60) = 20 + 9.
	if order.EstimatedDeliveryMins != 29 {
		t.Fatalf("estimated_delivery_minutes = %d, want 29", order.EstimatedDeliveryMins)
	}
}

func TestPlaceOrder_Serviceability(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	lat, lng := 12.9, 77.6
	place := func(customerID, addr uuid.UUID) error {
		_, err := s.PlaceOrder(ctx, customerID, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
		return err
	}

	t.Run("out of default radius", func(t *testing.T) {
		c, _, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
		err := place(c, seedAddress(t, s, c, f64(lat+50/kmPerDegLat), f64(lng)))
		assertAddressOutOfRange(t, err)
	})
	t.Run("service area covers beyond default radius", func(t *testing.T) {
		c, rid, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
		if _, err := s.db.Exec(ctx, `INSERT INTO food.restaurant_service_areas (restaurant_id, area_name, radius_km) VALUES ($1, 'wide', 10)`, rid); err != nil {
			t.Fatal(err)
		}
		if err := place(c, seedAddress(t, s, c, f64(lat+9/kmPerDegLat), f64(lng))); err != nil {
			t.Fatalf("want accepted, got %v", err)
		}
	})
	t.Run("service areas exist and none covers", func(t *testing.T) {
		c, rid, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
		if _, err := s.db.Exec(ctx, `INSERT INTO food.restaurant_service_areas (restaurant_id, area_name, radius_km) VALUES ($1, 'tight', 3)`, rid); err != nil {
			t.Fatal(err)
		}
		err := place(c, seedAddress(t, s, c, f64(lat+5/kmPerDegLat), f64(lng)))
		assertAddressOutOfRange(t, err)
	})
	t.Run("closed every day", func(t *testing.T) {
		c, rid, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
		if _, err := s.db.Exec(ctx, `
			INSERT INTO food.restaurant_operating_hours (restaurant_id, day_of_week, opens_at, closes_at, is_closed)
			SELECT $1, d, '00:00', '23:59', TRUE FROM generate_series(0, 6) d`, rid); err != nil {
			t.Fatal(err)
		}
		err := place(c, seedAddress(t, s, c, f64(lat+1/kmPerDegLat), f64(lng)))
		assertOutsideHours(t, err)
	})
	t.Run("address without coordinates", func(t *testing.T) {
		c, _, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
		err := place(c, seedAddress(t, s, c, nil, nil))
		assertAddressLocationRequired(t, err)
	})
	t.Run("restaurant without coordinates", func(t *testing.T) {
		c, _, _, _ := seedPlaceableCart(t, s, nil, nil)
		err := place(c, seedAddress(t, s, c, f64(lat), f64(lng)))
		assertRestaurantLocationMissing(t, err)
	})
}
