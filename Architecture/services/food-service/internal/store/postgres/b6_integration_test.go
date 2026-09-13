package postgres

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/routing"
	"github.com/google/uuid"
)

// B6 on TEST_PG_DSN (food_it_test, -p 1): the pickup-code attempt cap, the
// placement ETA, the once-a-minute ETA claim and its guarded write-back.

// Wrong pickup codes are counted and the assignment locks, exactly as the
// delivery code does; a caller who does not own the restaurant is not counted.
func TestVerifyPickupCode_LocksAfterTooManyWrongCodes(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, partnerID := seedDeliveryPartner(t, s)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ACCEPTED")
	pickup, _, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	owner := readRestaurantOwner(t, s, orderID)
	wrong := "0000"
	if pickup == wrong {
		wrong = "1111"
	}
	readFailed := func() int {
		t.Helper()
		var n int
		if err := s.db.QueryRow(ctx, `SELECT pickup_code_failed_attempts FROM food.delivery_assignments WHERE id = $1`, aid).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if err := s.VerifyPickupCode(ctx, uuid.New(), orderID, wrong); err == nil {
		t.Fatal("a stranger verified a pickup")
	}
	if n := readFailed(); n != 0 {
		t.Fatalf("a stranger's attempt was counted: %d", n)
	}
	for i := 0; i < MaxPickupCodeAttempts; i++ {
		if err := s.VerifyPickupCode(ctx, owner, orderID, wrong); !errors.Is(err, ErrDeliveryCodeInvalid) {
			t.Fatalf("wrong code %d: want ErrDeliveryCodeInvalid, got %v", i+1, err)
		}
	}
	if err := s.VerifyPickupCode(ctx, owner, orderID, pickup); !errors.Is(err, ErrPickupCodeLocked) {
		t.Fatalf("right code after %d wrong ones: want ErrPickupCodeLocked, got %v", MaxPickupCodeAttempts, err)
	}
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
	if _, _, status := readAssignment(t, s, orderID); status != "ACCEPTED" {
		t.Fatalf("assignment moved to %s", status)
	}
	if n := readFailed(); n != MaxPickupCodeAttempts {
		t.Fatalf("failed attempts = %d, want %d", n, MaxPickupCodeAttempts)
	}
	if rows := readOrderOutbox(t, s, orderID); len(rows) != 0 {
		t.Fatalf("a refused pickup wrote events: %s", outboxTypes(rows))
	}
}

// Below the cap the right code still works.
func TestVerifyPickupCode_RightCodeBelowTheCapPicksUp(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	_, partnerID := seedDeliveryPartner(t, s)
	seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ARRIVED_AT_RESTAURANT")
	pickup, _, _ := s.EnsureDeliveryCodes(ctx, orderID)
	owner := readRestaurantOwner(t, s, orderID)
	wrong := "0000"
	if pickup == wrong {
		wrong = "1111"
	}
	for i := 0; i < MaxPickupCodeAttempts-1; i++ {
		_ = s.VerifyPickupCode(ctx, owner, orderID, wrong)
	}
	if err := s.VerifyPickupCode(ctx, owner, orderID, pickup); err != nil {
		t.Fatalf("right code on attempt %d: %v", MaxPickupCodeAttempts, err)
	}
	assertOrderStatus(t, s, orderID, "PICKED_UP")
}

// ─── Placement ETA ─────────────────────────────────────────────────────────

type stubRouter struct {
	route routing.Route
	err   error
	calls int
}

func (r *stubRouter) Route(context.Context, routing.LatLng, routing.LatLng) (routing.Route, error) {
	r.calls++
	return r.route, r.err
}

func readOrderETA(t *testing.T, s *Store, orderID uuid.UUID) (etaMinusPlaced float64, source string, computed bool) {
	t.Helper()
	var computedAt *time.Time
	if err := s.db.QueryRow(context.Background(), `
		SELECT EXTRACT(EPOCH FROM (eta_at - placed_at))::float8, COALESCE(eta_source, ''), eta_computed_at
		FROM food.orders WHERE id = $1
	`, orderID).Scan(&etaMinusPlaced, &source, &computedAt); err != nil {
		t.Fatalf("read eta: %v", err)
	}
	return etaMinusPlaced, source, computedAt != nil
}

func TestPlaceOrder_WritesThePlacementETA(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	lat, lng := 12.9, 77.6
	place := func() *Order {
		t.Helper()
		customerID, _, _, _ := seedPlaceableCart(t, s, f64(lat), f64(lng))
		addr := seedAddress(t, s, customerID, f64(lat+2.9/kmPerDegLat), f64(lng))
		order, err := s.PlaceOrder(ctx, customerID, PlaceOrderInput{AddressID: addr, PaymentMethod: "COD"}, uuid.NewString())
		if err != nil {
			t.Fatalf("place: %v", err)
		}
		return order
	}

	t.Run("no router: prep plus the haversine ride", func(t *testing.T) {
		order := place()
		secs, source, computed := readOrderETA(t, s, order.ID)
		// 20 min prep + 2.9 km at 20 km/h (522 s).
		if math.Abs(secs-1722) > 1 || source != routing.SourceHaversine || !computed {
			t.Fatalf("eta - placed = %v s, source %q, computed %v", secs, source, computed)
		}
		if order.EstimatedDeliveryMins != 29 || order.ETASource != routing.SourceHaversine || order.ETAAt == "" {
			t.Fatalf("response: minutes %d eta_at %q source %q", order.EstimatedDeliveryMins, order.ETAAt, order.ETASource)
		}
	})

	t.Run("router: the priced ride", func(t *testing.T) {
		router := &stubRouter{route: routing.Route{DistanceMeters: 4100, Duration: 15 * time.Minute, Source: routing.SourceGoogle}}
		s.WithRouter(router)
		defer s.WithRouter(nil)
		order := place()
		secs, source, _ := readOrderETA(t, s, order.ID)
		if router.calls != 1 || math.Abs(secs-2100) > 1 || source != routing.SourceGoogle {
			t.Fatalf("calls %d, eta - placed = %v s, source %q", router.calls, secs, source)
		}
		if order.EstimatedDeliveryMins != 35 || order.ETASource != routing.SourceGoogle {
			t.Fatalf("response: minutes %d source %q", order.EstimatedDeliveryMins, order.ETASource)
		}
	})

	t.Run("router fails: haversine", func(t *testing.T) {
		s.WithRouter(&stubRouter{err: errors.New("down")})
		defer s.WithRouter(nil)
		order := place()
		if secs, source, _ := readOrderETA(t, s, order.ID); math.Abs(secs-1722) > 1 || source != routing.SourceHaversine {
			t.Fatalf("eta - placed = %v s, source %q", secs, source)
		}
	})
}

// ─── Ping-driven recompute ─────────────────────────────────────────────────

// seedTrackableOrder gives a seeded order map points, a prep estimate and a
// CONFIRMED history row placed `confirmedAgo` in the past.
func seedTrackableOrder(t *testing.T, s *Store, orderStatus string, confirmedAgo time.Duration) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	orderID, _, _ := seedOrderWithItem(t, s, orderStatus)
	if _, err := s.db.Exec(ctx, `
		UPDATE food.orders
		SET restaurant_address_snapshot = '{"latitude":12.9716,"longitude":77.5946}'::jsonb,
			delivery_address_snapshot = '{"latitude":12.9784,"longitude":77.6408}'::jsonb,
			estimated_preparation_minutes = 20
		WHERE id = $1
	`, orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.order_status_history (order_id, from_status, to_status, reason, created_at)
		VALUES ($1, 'PAYMENT_PENDING', 'CONFIRMED', 'seed', NOW() - make_interval(secs => $2::float8))
	`, orderID, confirmedAgo.Seconds()); err != nil {
		t.Fatal(err)
	}
	return orderID
}

func setETAComputedAgo(t *testing.T, s *Store, orderID uuid.UUID, ago time.Duration) {
	t.Helper()
	if _, err := s.db.Exec(context.Background(), `
		UPDATE food.orders SET eta_computed_at = NOW() - make_interval(secs => $2::float8) WHERE id = $1
	`, orderID, ago.Seconds()); err != nil {
		t.Fatal(err)
	}
}

func jobsFor(res *DeliveryLocationResult, orderID uuid.UUID) []ETAJob {
	var out []ETAJob
	for _, j := range res.ETAJobs {
		if j.OrderID == orderID {
			out = append(out, j)
		}
	}
	return out
}

// A ping claims an order's ETA recompute at most once per 60 s, only inside
// the accepted-to-delivered window, and hands the service what it needs.
func TestUpdateDeliveryLocation_ClaimsTheETARecomputeAtMostOncePerMinute(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, partnerID := seedDeliveryPartner(t, s)
	accepted := seedTrackableOrder(t, s, "DELIVERY_ASSIGNED", 5*time.Minute)
	aAccepted := seedDeliveryAssignmentWithStatus(t, s, accepted, &partnerID, "ACCEPTED")
	notAccepted := seedTrackableOrder(t, s, "DELIVERY_ASSIGNED", 5*time.Minute)
	seedDeliveryAssignmentWithStatus(t, s, notAccepted, &partnerID, "ASSIGNED")

	ping := func() *DeliveryLocationResult {
		t.Helper()
		res, err := s.UpdateDeliveryLocation(ctx, user, LocationUpdate{Latitude: 12.95, Longitude: 77.60})
		if err != nil {
			t.Fatalf("ping: %v", err)
		}
		return res
	}

	res := ping()
	jobs := jobsFor(res, accepted)
	if len(jobs) != 1 || len(res.ETAJobs) != 1 {
		t.Fatalf("first ping claimed %d jobs (%d for the accepted order), want exactly the accepted order", len(res.ETAJobs), len(jobs))
	}
	job := jobs[0]
	if job.AssignmentID != aAccepted || job.AssignmentStatus != "ACCEPTED" || job.OrderStatus != "DELIVERY_ASSIGNED" || job.ClaimedAt.IsZero() {
		t.Fatalf("job = %+v", job)
	}
	if job.Restaurant == nil || *job.Restaurant != (routing.LatLng{Lat: 12.9716, Lng: 77.5946}) ||
		job.Customer == nil || *job.Customer != (routing.LatLng{Lat: 12.9784, Lng: 77.6408}) {
		t.Fatalf("job points = %v %v", job.Restaurant, job.Customer)
	}
	// Confirmed 5 minutes ago with 20 minutes of prep: ready in about 15.
	if job.FoodReadyAt == nil || math.Abs(time.Until(*job.FoodReadyAt).Minutes()-15) > 1 {
		t.Fatalf("food ready at %v", job.FoodReadyAt)
	}
	var notAcceptedClaimed *time.Time
	if err := s.db.QueryRow(ctx, `SELECT eta_computed_at FROM food.orders WHERE id = $1`, notAccepted).Scan(&notAcceptedClaimed); err != nil || notAcceptedClaimed != nil {
		t.Fatalf("an unaccepted order was claimed: %v (%v)", notAcceptedClaimed, err)
	}

	if res := ping(); len(res.ETAJobs) != 0 {
		t.Fatalf("second ping at once claimed %d jobs", len(res.ETAJobs))
	}
	setETAComputedAgo(t, s, accepted, 59*time.Second)
	if res := ping(); len(res.ETAJobs) != 0 {
		t.Fatalf("a ping 59 s after the claim claimed %d jobs", len(res.ETAJobs))
	}
	setETAComputedAgo(t, s, accepted, 61*time.Second)
	if res := ping(); len(jobsFor(res, accepted)) != 1 {
		t.Fatalf("a ping 61 s after the claim claimed %d jobs, want 1", len(res.ETAJobs))
	}

	// Once the kitchen marked the food ready there is no wait for it.
	if _, err := s.db.Exec(ctx, `
		INSERT INTO food.order_status_history (order_id, from_status, to_status, reason) VALUES ($1, 'PREPARING', 'READY_FOR_PICKUP', 'seed')
	`, accepted); err != nil {
		t.Fatal(err)
	}
	setETAComputedAgo(t, s, accepted, 61*time.Second)
	if jobs := jobsFor(ping(), accepted); len(jobs) != 1 || jobs[0].FoodReadyAt != nil {
		t.Fatalf("after READY_FOR_PICKUP: %+v", jobs)
	}
}

// The write-back applies only under the latest claim; the customer's detail
// and tracking show the ETA while the order can arrive, and a frame carries
// the stored ETA.
func TestRecordOrderETA_GuardedAndShownToTheCustomer(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, partnerID := seedDeliveryPartner(t, s)
	orderID := seedTrackableOrder(t, s, "OUT_FOR_DELIVERY", 30*time.Minute)
	seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "PICKED_UP")
	var customerID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT user_id FROM food.orders WHERE id = $1`, orderID).Scan(&customerID); err != nil {
		t.Fatal(err)
	}

	res, err := s.UpdateDeliveryLocation(ctx, user, LocationUpdate{Latitude: 12.975, Longitude: 77.62})
	if err != nil {
		t.Fatal(err)
	}
	jobs := jobsFor(res, orderID)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d", len(jobs))
	}
	claimed := jobs[0].ClaimedAt
	eta := time.Now().Add(9 * time.Minute).Truncate(time.Second)

	if ok, err := s.RecordOrderETA(ctx, orderID, claimed.Add(-time.Microsecond), eta, routing.SourceGoogle); err != nil || ok {
		t.Fatalf("a stale claim wrote: ok=%v err=%v", ok, err)
	}
	if _, err := s.RecordOrderETA(ctx, orderID, claimed, eta, "guess"); err == nil {
		t.Fatal("an unknown source was accepted")
	}
	if ok, err := s.RecordOrderETA(ctx, orderID, claimed, eta, routing.SourceGoogle); err != nil || !ok {
		t.Fatalf("the current claim did not write: ok=%v err=%v", ok, err)
	}

	order, err := s.GetOrder(ctx, customerID, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if order.ETAAt != FormatETA(eta) || order.ETASource != routing.SourceGoogle {
		t.Fatalf("detail eta_at %q source %q, want %q google", order.ETAAt, order.ETASource, FormatETA(eta))
	}
	tracking, err := s.GetOrderTracking(ctx, customerID, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if tracking["eta_at"] != FormatETA(eta) || tracking["eta_source"] != routing.SourceGoogle {
		t.Fatalf("tracking eta = %v %v", tracking["eta_at"], tracking["eta_source"])
	}

	// The next frame carries the stored ETA.
	if _, err := s.db.Exec(ctx, `UPDATE food.delivery_assignments SET location_published_at = NOW() - INTERVAL '6 seconds' WHERE order_id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	res, err = s.UpdateDeliveryLocation(ctx, user, LocationUpdate{Latitude: 12.976, Longitude: 77.63})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Frames) != 1 || res.Frames[0].ETAAt == nil || !res.Frames[0].ETAAt.Equal(eta) || res.Frames[0].ETASource != routing.SourceGoogle {
		t.Fatalf("frames = %+v", res.Frames)
	}

	// Delivered: no arrival left to promise.
	if _, err := s.db.Exec(ctx, `UPDATE food.orders SET status = 'DELIVERED' WHERE id = $1`, orderID); err != nil {
		t.Fatal(err)
	}
	order, err = s.GetOrder(ctx, customerID, orderID)
	if err != nil {
		t.Fatal(err)
	}
	if order.ETAAt != "" || order.ETASource != "" {
		t.Fatalf("delivered order shows eta %q %q", order.ETAAt, order.ETASource)
	}
	if tracking, _ := s.GetOrderTracking(ctx, customerID, orderID); tracking["eta_at"] != nil || tracking["eta_source"] != nil {
		t.Fatalf("delivered tracking shows eta %v %v", tracking["eta_at"], tracking["eta_source"])
	}
}
