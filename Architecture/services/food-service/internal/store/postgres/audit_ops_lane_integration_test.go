package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Audit 2026-10-30 L3, L4 and the Android audit's M3 (accept replay): the
// rules only the database proves.

func seedPing(t *testing.T, s *Store, partnerID uuid.UUID, lat, lng float64, ago time.Duration) {
	t.Helper()
	if _, err := s.db.Exec(context.Background(), `
		INSERT INTO food.delivery_partner_locations (delivery_partner_id, latitude, longitude, recorded_at)
		VALUES ($1, $2, $3, NOW() - make_interval(secs => $4::float8))
	`, partnerID, lat, lng, ago.Seconds()); err != nil {
		t.Fatalf("seed ping: %v", err)
	}
}

func countPings(t *testing.T, s *Store, partnerID uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(context.Background(), `SELECT COUNT(*) FROM food.delivery_partner_locations WHERE delivery_partner_id = $1`, partnerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestUpdateDeliveryLocation_RefusesInactiveRidersAndTeleports(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	for _, status := range []string{"PENDING_REVIEW", "SUSPENDED", "APPROVED"} {
		user, partnerID := seedDeliveryPartnerWithStatus(t, s, status, false)
		if _, err := s.UpdateDeliveryLocation(ctx, user, LocationUpdate{Latitude: 12.97, Longitude: 77.59}); !errors.Is(err, ErrDeliveryPartnerNotActive) {
			t.Fatalf("%s rider ping: want ErrDeliveryPartnerNotActive, got %v", status, err)
		}
		if n := countPings(t, s, partnerID); n != 0 {
			t.Fatalf("%s rider ping was recorded (%d rows)", status, n)
		}
	}

	user, partnerID := seedDeliveryPartner(t, s)
	seedPing(t, s, partnerID, 12.9716, 77.5946, 30*time.Second)
	// ~22 km north in 30 s is ~2,600 km/h.
	if _, err := s.UpdateDeliveryLocation(ctx, user, LocationUpdate{Latitude: kmNorth(12.9716, 22), Longitude: 77.5946}); !errors.Is(err, ErrLocationImplausible) {
		t.Fatalf("teleport: want ErrLocationImplausible, got %v", err)
	}
	if n := countPings(t, s, partnerID); n != 1 {
		t.Fatalf("a teleport was recorded (%d rows)", n)
	}
	// 0.5 km in 30 s is 60 km/h: accepted.
	if _, err := s.UpdateDeliveryLocation(ctx, user, LocationUpdate{Latitude: kmNorth(12.9716, 0.5), Longitude: 77.5946}); err != nil {
		t.Fatalf("believable move refused: %v", err)
	}
	// GPS jitter a second later is accepted.
	if _, err := s.UpdateDeliveryLocation(ctx, user, LocationUpdate{Latitude: kmNorth(12.9716, 0.6), Longitude: 77.5946}); err != nil {
		t.Fatalf("jitter refused: %v", err)
	}
}

func TestSetDeliveryAvailability_OfflineMidJobKeepsTheJobWorkable(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	rider, partnerID := seedDeliveryPartner(t, s)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ARRIVED_AT_CUSTOMER")
	_, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.SetDeliveryAvailability(ctx, rider, false)
	if err != nil {
		t.Fatalf("go offline: %v", err)
	}
	if p.IsOnline || p.Status != "ACTIVE" {
		t.Fatalf("offline mid-job: online=%v status=%s, want offline and ACTIVE", p.IsOnline, p.Status)
	}
	if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, delivery); err != nil {
		t.Fatalf("verify after going offline mid-job: %v", err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERED")

	// With no job, offline is OFFLINE as before.
	idle, _ := seedDeliveryPartner(t, s)
	if p, err := s.SetDeliveryAvailability(ctx, idle, false); err != nil || p.Status != "OFFLINE" {
		t.Fatalf("idle offline: %+v %v", p, err)
	}
}

// A rider already OFFLINE (toggled before this fix, or by a race) can still
// verify the job they hold; a suspended one cannot (TestRiderVerifyDeliveryCode_InactiveRiderRefused).
func TestRiderVerifyDeliveryCode_OfflineRiderFinishesHeldJob(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	rider, partnerID := seedDeliveryPartnerWithStatus(t, s, "OFFLINE", false)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ARRIVED_AT_CUSTOMER")
	_, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, delivery); err != nil {
		t.Fatalf("offline rider verify: %v", err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERED")

	// Another OFFLINE rider cannot verify a job they do not hold.
	other, _ := seedDeliveryPartnerWithStatus(t, s, "OFFLINE", false)
	order2, _, _ := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	aid2 := seedDeliveryAssignmentWithStatus(t, s, order2, &partnerID, "ARRIVED_AT_CUSTOMER")
	_, code2, _ := s.EnsureDeliveryCodes(ctx, order2)
	if _, err := s.RiderVerifyDeliveryCode(ctx, other, aid2, code2); err == nil {
		t.Fatal("an offline rider verified someone else's job")
	}
	assertOrderStatus(t, s, order2, "OUT_FOR_DELIVERY")
}

func TestReleaseSilentRiderJobs(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	silentOrder, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, silentPartner := seedDeliveryPartner(t, s)
	silentAid := seedDeliveryAssignmentWithStatus(t, s, silentOrder, &silentPartner, "ACCEPTED")
	seedPing(t, s, silentPartner, 12.97, 77.59, 20*time.Minute)

	liveOrder, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, livePartner := seedDeliveryPartner(t, s)
	seedDeliveryAssignmentWithStatus(t, s, liveOrder, &livePartner, "ACCEPTED")
	seedPing(t, s, livePartner, 12.97, 77.59, time.Minute)

	pickedOrder, _, _ := seedOrderWithItem(t, s, "PICKED_UP")
	_, pickedPartner := seedDeliveryPartner(t, s)
	seedDeliveryAssignmentWithStatus(t, s, pickedOrder, &pickedPartner, "PICKED_UP")
	seedPing(t, s, pickedPartner, 12.97, 77.59, 20*time.Minute)

	for _, id := range []uuid.UUID{silentAid} {
		if _, err := s.db.Exec(ctx, `UPDATE food.delivery_assignments SET assigned_at = NOW() - INTERVAL '20 minutes', accepted_at = NOW() - INTERVAL '20 minutes' WHERE id = $1`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(ctx, `UPDATE food.delivery_assignments SET assigned_at = NOW() - INTERVAL '20 minutes', accepted_at = NOW() - INTERVAL '20 minutes' WHERE order_id = ANY($1)`, []uuid.UUID{liveOrder, pickedOrder}); err != nil {
		t.Fatal(err)
	}

	released, err := s.ReleaseSilentRiderJobs(ctx, SilentRiderJobTimeout, 50)
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	found := false
	for _, id := range released {
		if id == silentAid {
			found = true
		}
	}
	if !found {
		t.Fatalf("silent rider's job not released: %v", released)
	}
	assertOrderStatus(t, s, silentOrder, "DELIVERY_ASSIGNING")
	if _, p, status := readAssignment(t, s, silentOrder); p != nil || status != "CREATED" {
		t.Fatalf("released assignment = partner %v status %s", p, status)
	}
	assertHistoryChain(t, s, silentOrder, "DELIVERY_ASSIGNED", []string{"DELIVERY_ASSIGNING"})
	assertOrderStatus(t, s, liveOrder, "DELIVERY_ASSIGNED")
	assertOrderStatus(t, s, pickedOrder, "PICKED_UP")

	// Running again releases nothing more.
	again, err := s.ReleaseSilentRiderJobs(ctx, SilentRiderJobTimeout, 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range again {
		if id == silentAid {
			t.Fatal("released twice")
		}
	}
}

func TestAcceptDeliveryOfferTx_SameRiderReplayIsSuccessWithoutAWrite(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	seedDeliveryAssignmentWithStatus(t, s, orderID, nil, "CREATED")
	rider, partnerID := seedDeliveryPartner(t, s)
	offerID := seedPendingOffer(t, s, orderID, partnerID)
	other, otherPartner := seedDeliveryPartner(t, s)
	otherOffer := seedPendingOffer(t, s, orderID, otherPartner)

	first, err := s.AcceptDeliveryOfferTx(ctx, rider, offerID)
	if err != nil || first.Replayed {
		t.Fatalf("first accept: %+v %v", first, err)
	}
	historyBefore := len(readHistory(t, s, orderID))
	var assignedAt time.Time
	if err := s.db.QueryRow(ctx, `SELECT assigned_at FROM food.delivery_assignments WHERE order_id = $1`, orderID).Scan(&assignedAt); err != nil {
		t.Fatal(err)
	}

	again, err := s.AcceptDeliveryOfferTx(ctx, rider, offerID)
	if err != nil {
		t.Fatalf("replayed accept refused: %v", err)
	}
	if !again.Replayed || again.Status != "accepted" || again.OrderID != orderID {
		t.Fatalf("replay = %+v", again)
	}
	var assignedAfter time.Time
	if err := s.db.QueryRow(ctx, `SELECT assigned_at FROM food.delivery_assignments WHERE order_id = $1`, orderID).Scan(&assignedAfter); err != nil {
		t.Fatal(err)
	}
	if !assignedAfter.Equal(assignedAt) || len(readHistory(t, s, orderID)) != historyBefore {
		t.Fatal("the replay wrote again")
	}

	// The other rider's sibling offer is still refused.
	if _, err := s.AcceptDeliveryOfferTx(ctx, other, otherOffer); err == nil {
		t.Fatal("a second rider accepted a taken order")
	}
	// And the other rider cannot replay the first rider's offer.
	if _, err := s.AcceptDeliveryOfferTx(ctx, other, offerID); err == nil {
		t.Fatal("another rider replayed this rider's offer")
	}

	// Once the job is no longer held (cancelled), a replay is refused again.
	if _, err := s.AdminCancelOrder(ctx, uuid.New(), orderID, "ops"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptDeliveryOfferTx(ctx, rider, offerID); err == nil {
		t.Fatal("replay succeeded on a cancelled job")
	}
}

func TestRunExclusive_OneHolderAtATime(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	name := "test-" + uuid.NewString()

	inner := false
	ran, err := s.RunExclusive(ctx, name, func(ctx context.Context) error {
		got, err := s.RunExclusive(ctx, name, func(context.Context) error {
			inner = true
			return nil
		})
		if err != nil || got {
			t.Errorf("second holder ran=%v err=%v", got, err)
		}
		return nil
	})
	if err != nil || !ran || inner {
		t.Fatalf("outer ran=%v err=%v inner=%v", ran, err, inner)
	}
	// Released: the next pass runs.
	if ran, err := s.RunExclusive(ctx, name, func(context.Context) error { return nil }); err != nil || !ran {
		t.Fatalf("after release ran=%v err=%v", ran, err)
	}
}
