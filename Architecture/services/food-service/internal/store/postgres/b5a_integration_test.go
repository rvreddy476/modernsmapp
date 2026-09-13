package postgres

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// B5a on TEST_PG_DSN (food_it_test, -p 1).

func sortedUUIDs(in []uuid.UUID) []string {
	out := make([]string, len(in))
	for i, id := range in {
		out[i] = id.String()
	}
	sort.Strings(out)
	return out
}

func frameOrders(res *DeliveryLocationResult) []string {
	ids := make([]uuid.UUID, 0, len(res.Frames))
	for _, f := range res.Frames {
		ids = append(ids, f.OrderID)
	}
	return sortedUUIDs(ids)
}

func countLocationPings(t *testing.T, s *Store, assignmentID uuid.UUID) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(context.Background(), `
		SELECT COUNT(*) FROM food.delivery_tracking_events
		WHERE assignment_id = $1 AND note = 'location update'
	`, assignmentID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// One ping updates EVERY active assignment the rider holds, and a
// rider.location frame is cleared only for orders inside the accepted-to-
// delivered window, at most once per order per RiderLocationMinInterval.
func TestUpdateDeliveryLocation_EveryActiveAssignmentOnlyInsideTheWindow(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	user, partnerID := seedDeliveryPartner(t, s)
	accepted, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	aAccepted := seedDeliveryAssignmentWithStatus(t, s, accepted, &partnerID, "ACCEPTED")
	onWay, _, _ := seedOrderWithItem(t, s, "OUT_FOR_DELIVERY")
	aOnWay := seedDeliveryAssignmentWithStatus(t, s, onWay, &partnerID, "ARRIVED_AT_CUSTOMER")
	notAccepted, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	aNotAccepted := seedDeliveryAssignmentWithStatus(t, s, notAccepted, &partnerID, "ASSIGNED")
	cancelled, _, _ := seedOrderWithItem(t, s, "CANCELLED_BY_ADMIN")
	aCancelled := seedDeliveryAssignmentWithStatus(t, s, cancelled, &partnerID, "PICKED_UP")
	delivered, _, _ := seedOrderWithItem(t, s, "DELIVERED")
	aDelivered := seedDeliveryAssignmentWithStatus(t, s, delivered, &partnerID, "DELIVERED")

	ping := func() *DeliveryLocationResult {
		t.Helper()
		res, err := s.UpdateDeliveryLocation(ctx, user, LocationUpdate{Latitude: 12.9716, Longitude: 77.5946, Heading: f64(90)})
		if err != nil {
			t.Fatalf("ping: %v", err)
		}
		return res
	}

	res := ping()
	wantActive := sortedUUIDs([]uuid.UUID{aAccepted, aOnWay, aNotAccepted, aCancelled})
	if got := sortedUUIDs(res.AssignmentIDs); strings.Join(got, ",") != strings.Join(wantActive, ",") {
		t.Fatalf("assignment_ids = %v, want every active assignment %v", got, wantActive)
	}
	for _, aid := range []uuid.UUID{aAccepted, aOnWay, aNotAccepted, aCancelled} {
		if n := countLocationPings(t, s, aid); n != 1 {
			t.Fatalf("assignment %s has %d location events, want 1", aid, n)
		}
	}
	if n := countLocationPings(t, s, aDelivered); n != 0 {
		t.Fatalf("delivered assignment got %d location events", n)
	}
	wantFrames := sortedUUIDs([]uuid.UUID{accepted, onWay})
	if got := frameOrders(res); strings.Join(got, ",") != strings.Join(wantFrames, ",") {
		t.Fatalf("frames for %v, want only the in-window orders %v", got, wantFrames)
	}
	var heading float64
	if err := s.db.QueryRow(ctx, `SELECT heading::float8 FROM food.delivery_partner_locations WHERE id = $1`, res.ID).Scan(&heading); err != nil || heading != 90 {
		t.Fatalf("heading = %v (%v)", heading, err)
	}

	// Immediately again: every assignment still tracked, no frame (throttled).
	res = ping()
	if len(res.Frames) != 0 {
		t.Fatalf("throttle let frames through: %v", frameOrders(res))
	}
	if n := countLocationPings(t, s, aAccepted); n != 2 {
		t.Fatalf("second ping not tracked: %d", n)
	}

	// Once the interval has passed for one order, only that order gets a frame.
	if _, err := s.db.Exec(ctx, `
		UPDATE food.delivery_assignments SET location_published_at = NOW() - INTERVAL '6 seconds' WHERE id = $1
	`, aAccepted); err != nil {
		t.Fatal(err)
	}
	res = ping()
	if got := frameOrders(res); len(got) != 1 || got[0] != accepted.String() {
		t.Fatalf("frames after the interval = %v, want [%s]", got, accepted)
	}
}

// pickup_code is in the rider's assignment responses only after they accept
// the job, and only until pickup.
func TestPickupCodeReachesTheRiderOnlyAfterAccept(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	user, partnerID := seedDeliveryPartner(t, s)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ASSIGNED")
	pickup, _, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil || pickup == "" {
		t.Fatalf("codes: %q %v", pickup, err)
	}

	visible := func(stage string, want string) {
		t.Helper()
		list, err := s.ListDeliveryAssignments(ctx, user)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, a := range list {
			if a.ID == aid {
				found = true
				if a.PickupCode != want {
					t.Fatalf("%s: list pickup_code = %q, want %q", stage, a.PickupCode, want)
				}
			}
		}
		if !found {
			t.Fatalf("%s: assignment missing from list", stage)
		}
		tr, err := s.GetAssignmentTracking(ctx, user, aid)
		if err != nil {
			t.Fatal(err)
		}
		if got := tr["assignment"].(DeliveryAssignment).PickupCode; got != want {
			t.Fatalf("%s: tracking pickup_code = %q, want %q", stage, got, want)
		}
		raw, _ := json.Marshal(list)
		if want == "" && strings.Contains(string(raw), pickup) {
			t.Fatalf("%s: the code leaked into the list JSON", stage)
		}
	}

	visible("assigned", "")
	if cur, err := s.GetCurrentDeliveryAssignment(ctx, user); err != nil || cur.PickupCode != "" {
		t.Fatalf("current before accept: %+v %v", cur, err)
	}

	acc, err := s.DeliveryUpdateAssignment(ctx, user, aid, "ACCEPTED", "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	if acc.PickupCode != pickup {
		t.Fatalf("accept response pickup_code = %q", acc.PickupCode)
	}
	visible("accepted", pickup)
	if cur, err := s.GetCurrentDeliveryAssignment(ctx, user); err != nil || cur.PickupCode != pickup {
		t.Fatalf("current after accept: %+v %v", cur, err)
	}
	if _, err := s.DeliveryUpdateAssignment(ctx, user, aid, "ARRIVED_AT_RESTAURANT", ""); err != nil {
		t.Fatal(err)
	}
	visible("arrived at restaurant", pickup)

	if err := s.VerifyPickupCode(ctx, readRestaurantOwner(t, s, orderID), orderID, pickup); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	visible("picked up", "")
}

// delivery_code is on the customer's order detail only while the food is with
// the rider.
func TestDeliveryCodeReachesTheCustomerOnlyWhileWithTheRider(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, customerID := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	user, partnerID := seedDeliveryPartner(t, s)
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "ACCEPTED")
	pickup, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}

	detail := func(stage, want string) {
		t.Helper()
		o, err := s.GetOrder(ctx, customerID, orderID)
		if err != nil {
			t.Fatalf("%s: get order: %v", stage, err)
		}
		if o.DeliveryCode != want {
			t.Fatalf("%s (%s): delivery_code = %q, want %q", stage, o.Status, o.DeliveryCode, want)
		}
		raw, _ := json.Marshal(o)
		if want == "" && strings.Contains(string(raw), "delivery_code") {
			t.Fatalf("%s: delivery_code key present in %s", stage, raw)
		}
	}

	detail("assigned", "")
	if err := s.VerifyPickupCode(ctx, readRestaurantOwner(t, s, orderID), orderID, pickup); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	detail("picked up", delivery)
	if _, err := s.DeliveryUpdateAssignment(ctx, user, aid, "ARRIVED_AT_CUSTOMER", ""); err != nil {
		t.Fatalf("arrived customer: %v", err)
	}
	detail("out for delivery", delivery)
	if err := s.VerifyDeliveryCode(ctx, customerID, orderID, delivery); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	detail("delivered", "")
}

// A rider silent for 5 minutes goes offline; one still carrying an order keeps
// ACTIVE (to finish it) but stops being offered work.
func TestAutoOfflineStaleDeliveryPartners(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	partner := func(lastPing *time.Duration, onlineAgo *time.Duration) uuid.UUID {
		t.Helper()
		_, pid := seedDeliveryPartner(t, s)
		if _, err := s.db.Exec(ctx, `UPDATE food.delivery_partners SET created_at = NOW() - INTERVAL '1 hour' WHERE id = $1`, pid); err != nil {
			t.Fatal(err)
		}
		if lastPing != nil {
			if _, err := s.db.Exec(ctx, `
				INSERT INTO food.delivery_partner_locations (delivery_partner_id, latitude, longitude, recorded_at)
				VALUES ($1, 12.97, 77.59, NOW() - make_interval(secs => $2::float8))
			`, pid, lastPing.Seconds()); err != nil {
				t.Fatal(err)
			}
		}
		if onlineAgo != nil {
			if _, err := s.db.Exec(ctx, `
				INSERT INTO food.delivery_partner_availability (delivery_partner_id, is_online, created_at)
				VALUES ($1, TRUE, NOW() - make_interval(secs => $2::float8))
			`, pid, onlineAgo.Seconds()); err != nil {
				t.Fatal(err)
			}
		}
		return pid
	}
	tenMin, oneMin := 10*time.Minute, time.Minute
	stale := partner(&tenMin, nil)
	fresh := partner(&oneMin, nil)
	justOnline := partner(nil, &oneMin)
	staleOnJob := partner(&tenMin, nil)
	jobOrder, _, _ := seedOrderWithItem(t, s, "PICKED_UP")
	seedDeliveryAssignmentWithStatus(t, s, jobOrder, &staleOnJob, "PICKED_UP")

	ids, err := s.AutoOfflineStaleDeliveryPartners(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("auto offline: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got[stale] || !got[staleOnJob] || got[fresh] || got[justOnline] {
		t.Fatalf("offlined = %v; stale=%v onJob=%v fresh=%v justOnline=%v", ids, got[stale], got[staleOnJob], got[fresh], got[justOnline])
	}
	state := func(pid uuid.UUID) (bool, string) {
		t.Helper()
		var online bool
		var status string
		if err := s.db.QueryRow(ctx, `SELECT is_online, status::text FROM food.delivery_partners WHERE id = $1`, pid).Scan(&online, &status); err != nil {
			t.Fatal(err)
		}
		return online, status
	}
	if online, status := state(stale); online || status != "OFFLINE" {
		t.Fatalf("stale rider: online=%v status=%s", online, status)
	}
	if online, status := state(staleOnJob); online || status != "ACTIVE" {
		t.Fatalf("stale rider on a job: online=%v status=%s", online, status)
	}
	for _, pid := range []uuid.UUID{fresh, justOnline} {
		if online, status := state(pid); !online || status != "ACTIVE" {
			t.Fatalf("live rider switched off: online=%v status=%s", online, status)
		}
	}
	var logged int
	if err := s.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM food.delivery_partner_availability
		WHERE delivery_partner_id = $1 AND NOT is_online AND reason = 'auto-offline: no location ping'
	`, stale).Scan(&logged); err != nil || logged != 1 {
		t.Fatalf("availability log rows = %d (%v)", logged, err)
	}
	again, err := s.AutoOfflineStaleDeliveryPartners(ctx, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range again {
		if id == stale || id == staleOnJob {
			t.Fatalf("already-offline rider switched off twice")
		}
	}
}

// Location history older than the retention is deleted; newer history and
// status-change tracking rows stay.
func TestPurgeDeliveryLocationHistory(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	_, pid := seedDeliveryPartner(t, s)
	insertLocation := func(age string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.db.QueryRow(ctx, `
			INSERT INTO food.delivery_partner_locations (delivery_partner_id, latitude, longitude, recorded_at)
			VALUES ($1, 12.97, 77.59, NOW() - $2::interval) RETURNING id
		`, pid, age).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	oldLoc, recentLoc := insertLocation("31 days"), insertLocation("29 days")
	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERED")
	aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &pid, "DELIVERED")
	insertEvent := func(note string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := s.db.QueryRow(ctx, `
			INSERT INTO food.delivery_tracking_events (assignment_id, delivery_partner_id, status, latitude, longitude, note, created_at)
			VALUES ($1, $2, 'DELIVERED', 12.97, 77.59, $3, NOW() - INTERVAL '31 days') RETURNING id
		`, aid, pid, note).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	oldPing, oldStatus := insertEvent("location update"), insertEvent("status update")

	res, err := s.PurgeDeliveryLocationHistory(ctx, 30*24*time.Hour, 2)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if res.Locations < 1 || res.TrackingPings < 1 {
		t.Fatalf("purge result = %+v", res)
	}
	exists := func(table string, id uuid.UUID) bool {
		t.Helper()
		var ok bool
		if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM food.`+table+` WHERE id = $1)`, id).Scan(&ok); err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if exists("delivery_partner_locations", oldLoc) || !exists("delivery_partner_locations", recentLoc) {
		t.Fatal("location purge removed the wrong rows")
	}
	if exists("delivery_tracking_events", oldPing) || !exists("delivery_tracking_events", oldStatus) {
		t.Fatal("tracking purge removed the wrong rows")
	}
}
