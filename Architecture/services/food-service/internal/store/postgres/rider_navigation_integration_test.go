package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Rider navigation over the real schema (TEST_PG_DSN, food_it_test, -p 1).

// rnSeedRiderJob is a job a rider can see end to end: a restaurant with a pin,
// second address line and phone; an order with a full delivery snapshot,
// instructions and a stored ETA; the rider's assignment in assignmentStatus
// with fee 29.00 and payout 23.20.
func rnSeedRiderJob(t *testing.T, s *Store, orderStatus, assignmentStatus string) (orderID, rider, partnerID, assignmentID uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	orderID, _, _ = seedOrderWithItem(t, s, orderStatus)
	if _, err := s.db.Exec(ctx, `
		UPDATE food.restaurants
		SET latitude = 12.9716, longitude = 77.5946, address_line2 = 'Near Test Park', phone = '08040000000'
		WHERE id = (SELECT restaurant_id FROM food.orders WHERE id = $1)
	`, orderID); err != nil {
		t.Fatalf("seed restaurant pin: %v", err)
	}
	if _, err := s.db.Exec(ctx, `
		UPDATE food.orders
		SET delivery_address_snapshot = $2::jsonb, customer_instruction = 'Ring the bell twice',
			eta_at = '2026-09-13T06:52:00Z', eta_source = 'google'
		WHERE id = $1
	`, orderID, rnDropSnapshot); err != nil {
		t.Fatalf("seed delivery snapshot: %v", err)
	}
	rider, partnerID = seedDeliveryPartner(t, s)
	assignmentID = seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, assignmentStatus)
	rnSetMoney(t, s, assignmentID, "29.00", "23.20")
	return orderID, rider, partnerID, assignmentID
}

func rnSetMoney(t *testing.T, s *Store, assignmentID uuid.UUID, fee, payout string) {
	t.Helper()
	if _, err := s.db.Exec(context.Background(), `
		UPDATE food.delivery_assignments
		SET delivery_fee = $2::numeric, delivery_partner_payout = $3::numeric
		WHERE id = $1
	`, assignmentID, fee, payout); err != nil {
		t.Fatalf("seed assignment money: %v", err)
	}
}

func rnFind(t *testing.T, list []DeliveryAssignment, id uuid.UUID) DeliveryAssignment {
	t.Helper()
	for _, a := range list {
		if a.ID == id {
			return a
		}
	}
	t.Fatalf("assignment %s missing from the list", id)
	return DeliveryAssignment{}
}

// The drop-off is absent while ASSIGNED, present from ACCEPTED through
// OUT_FOR_DELIVERY on every rider read, and gone again once DELIVERED. A
// foreign rider reads nothing at any stage.
func TestRiderSeesTheDropOnlyWhileTheJobIsOn(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, rider, _, aid := rnSeedRiderJob(t, s, "DELIVERY_ASSIGNED", "ASSIGNED")
	stranger, _ := seedDeliveryPartner(t, s)
	pickup, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}

	check := func(stage string, wantDrop bool) DeliveryAssignment {
		t.Helper()
		tr, err := s.GetAssignmentTracking(ctx, rider, aid)
		if err != nil {
			t.Fatalf("%s: tracking: %v", stage, err)
		}
		a := tr["assignment"].(DeliveryAssignment)
		rnAssertRiderDrop(t, stage+" tracking", a, wantDrop)
		list, err := s.ListDeliveryAssignments(ctx, rider)
		if err != nil {
			t.Fatalf("%s: list: %v", stage, err)
		}
		rnAssertRiderDrop(t, stage+" list", rnFind(t, list, aid), wantDrop)
		if rnClosedAssignment[a.Status] {
			hist, err := s.DeliveryHistory(ctx, rider)
			if err != nil {
				t.Fatalf("%s: history: %v", stage, err)
			}
			h := rnFind(t, hist, aid)
			rnAssertRiderDrop(t, stage+" history", h, false)
			if h.DropSummary == nil || h.DropSummary.City != "Bengaluru" || h.DropSummary.Locality != "Bengaluru" {
				t.Fatalf("%s: drop_summary = %+v", stage, h.DropSummary)
			}
		} else {
			cur, err := s.GetCurrentDeliveryAssignment(ctx, rider)
			if err != nil || cur.ID != aid {
				t.Fatalf("%s: current = %+v %v", stage, cur, err)
			}
			rnAssertRiderDrop(t, stage+" current", *cur, wantDrop)
		}
		if _, err := s.GetAssignmentTracking(ctx, stranger, aid); !errors.Is(err, pgx.ErrNoRows) {
			t.Fatalf("%s: a foreign rider read the assignment: %v", stage, err)
		}
		return a
	}

	check("assigned", false)
	acc, err := s.DeliveryUpdateAssignment(ctx, rider, aid, "ACCEPTED", "")
	if err != nil {
		t.Fatalf("accept: %v", err)
	}
	rnAssertRiderDrop(t, "accept response", *acc, true)
	a := check("accepted", true)
	if a.ETAAt != "2026-09-13T06:52:00Z" || a.ETASource != "google" || a.Navigation.PickupURL != rnPickupURL ||
		a.Restaurant.Phone != "08040000000" || a.Restaurant.AddressLine2 != "Near Test Park" {
		t.Fatalf("accepted: eta %q %q, navigation %+v, restaurant %+v", a.ETAAt, a.ETASource, a.Navigation, a.Restaurant)
	}
	if _, err := s.DeliveryUpdateAssignment(ctx, rider, aid, "ARRIVED_AT_RESTAURANT", ""); err != nil {
		t.Fatal(err)
	}
	check("arrived at restaurant", true)
	if err := s.VerifyPickupCode(ctx, readRestaurantOwner(t, s, orderID), orderID, pickup); err != nil {
		t.Fatalf("pickup: %v", err)
	}
	if a := check("picked up", true); a.Status != "PICKED_UP" {
		t.Fatalf("picked up: status %s", a.Status)
	}
	if _, err := s.DeliveryUpdateAssignment(ctx, rider, aid, "ARRIVED_AT_CUSTOMER", ""); err != nil {
		t.Fatalf("arrived customer: %v", err)
	}
	if a := check("out for delivery", true); a.OrderStatus != "OUT_FOR_DELIVERY" {
		t.Fatalf("out for delivery: order status %s", a.OrderStatus)
	}
	if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, delivery); err != nil {
		t.Fatalf("delivery: %v", err)
	}
	if a := check("delivered", false); a.Status != "DELIVERED" || a.ETAAt != "" || a.Navigation != nil {
		t.Fatalf("delivered: status %s eta %q navigation %+v", a.Status, a.ETAAt, a.Navigation)
	}
}

// An admin cancel closes the assignment and takes the drop-off with it.
func TestRiderLosesTheDropWhenTheOrderIsCancelled(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, rider, _, aid := rnSeedRiderJob(t, s, "DELIVERY_ASSIGNED", "ACCEPTED")
	cur, err := s.GetCurrentDeliveryAssignment(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	rnAssertRiderDrop(t, "before cancel", *cur, true)

	if _, err := s.AdminCancelOrder(ctx, uuid.New(), orderID, "ops"); err != nil {
		t.Fatalf("admin cancel: %v", err)
	}
	tr, err := s.GetAssignmentTracking(ctx, rider, aid)
	if err != nil {
		t.Fatal(err)
	}
	a := tr["assignment"].(DeliveryAssignment)
	if a.Status != "CANCELLED" {
		t.Fatalf("status after cancel = %s", a.Status)
	}
	rnAssertRiderDrop(t, "cancelled tracking", a, false)
	hist, err := s.DeliveryHistory(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	h := rnFind(t, hist, aid)
	rnAssertRiderDrop(t, "cancelled history", h, false)
	if h.DropSummary == nil || h.DropSummary.City != "Bengaluru" {
		t.Fatalf("drop_summary = %+v", h.DropSummary)
	}
}

// payout_paise on the offer, the assignment and history is the figure the
// delivery settlement pays for that delivery.
func TestRiderPayoutIsTheSettlementFigure(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, rider, partnerID, aid := rnSeedRiderJob(t, s, "DELIVERY_ASSIGNED", "ASSIGNED")
	offerID := seedPendingOffer(t, s, orderID, partnerID)
	contexts, err := s.DeliveryOfferContexts(ctx, []uuid.UUID{offerID})
	if err != nil {
		t.Fatal(err)
	}
	offerPayout := contexts[offerID].PayoutPaise
	cur, err := s.GetCurrentDeliveryAssignment(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	pickup, delivery, err := s.EnsureDeliveryCodes(ctx, orderID)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"ACCEPTED", "ARRIVED_AT_RESTAURANT"} {
		if _, err := s.DeliveryUpdateAssignment(ctx, rider, aid, step, ""); err != nil {
			t.Fatalf("%s: %v", step, err)
		}
	}
	if err := s.VerifyPickupCode(ctx, readRestaurantOwner(t, s, orderID), orderID, pickup); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeliveryUpdateAssignment(ctx, rider, aid, "ARRIVED_AT_CUSTOMER", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, delivery); err != nil {
		t.Fatal(err)
	}

	var today string
	if err := s.db.QueryRow(ctx, `SELECT CURRENT_DATE::text`).Scan(&today); err != nil {
		t.Fatal(err)
	}
	noRestaurant := uuid.New() // settle this rider only
	res, err := s.AdminGenerateSettlements(ctx, uuid.New(), SettlementGenerateInput{
		PeriodStart: today, PeriodEnd: today, RestaurantID: &noRestaurant, DeliveryPartnerID: &partnerID,
	})
	if err != nil {
		t.Fatalf("settle: %v", err)
	}
	items, _ := res["delivery_settlements"].([]map[string]any)
	if len(items) != 1 {
		t.Fatalf("delivery settlements = %v", res["delivery_settlements"])
	}
	settled, _ := items[0]["payout_paise"].(int64)
	hist, err := s.DeliveryHistory(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	h := rnFind(t, hist, aid)
	if settled != 2320 || offerPayout != settled || cur.PayoutPaise != settled || h.PayoutPaise != settled || h.DeliveryPartnerPayoutPaise != settled {
		t.Fatalf("settled %d; offer %d, current %d, history %d / %d", settled, offerPayout, cur.PayoutPaise, h.PayoutPaise, h.DeliveryPartnerPayoutPaise)
	}
}

// Every *_paise sibling is the stored NUMERIC times 100 exactly, including
// amounts whose float form times 100 is not a whole number (0.29, 1.15).
func TestRiderMoneyPaiseSiblingsAreExact(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	rider, partnerID := seedDeliveryPartner(t, s)
	cases := []struct {
		fee, payout           string
		feePaise, payoutPaise int64
	}{
		{"29.00", "23.20", 2900, 2320},
		{"0.29", "0.29", 29, 29},
		{"1.15", "1.15", 115, 115},
		{"12345.67", "9876.54", 1234567, 987654},
	}
	index := map[uuid.UUID]int{}
	var sum int64
	for i, c := range cases {
		orderID, _, _ := seedOrderWithItem(t, s, "DELIVERED")
		aid := seedDeliveryAssignmentWithStatus(t, s, orderID, &partnerID, "DELIVERED")
		rnSetMoney(t, s, aid, c.fee, c.payout)
		if _, err := s.db.Exec(ctx, `UPDATE food.delivery_assignments SET delivered_at = NOW() WHERE id = $1`, aid); err != nil {
			t.Fatal(err)
		}
		index[aid] = i
		sum += c.payoutPaise
	}
	hist, err := s.DeliveryHistory(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, h := range hist {
		i, ok := index[h.ID]
		if !ok {
			continue
		}
		seen++
		c := cases[i]
		if h.DeliveryFeePaise != c.feePaise || h.DeliveryPartnerPayoutPaise != c.payoutPaise || h.PayoutPaise != c.payoutPaise {
			t.Fatalf("%s/%s: paise = %d / %d / %d", c.fee, c.payout, h.DeliveryFeePaise, h.DeliveryPartnerPayoutPaise, h.PayoutPaise)
		}
		if h.DeliveryFee != float64(c.feePaise)/100 || h.DeliveryPartnerPayout != float64(c.payoutPaise)/100 ||
			math.Round(h.DeliveryFee*100) != float64(h.DeliveryFeePaise) || math.Round(h.DeliveryPartnerPayout*100) != float64(h.DeliveryPartnerPayoutPaise) {
			t.Fatalf("%s/%s: float %v / %v disagrees with paise %d / %d", c.fee, c.payout, h.DeliveryFee, h.DeliveryPartnerPayout, h.DeliveryFeePaise, h.DeliveryPartnerPayoutPaise)
		}
	}
	if seen != len(cases) {
		t.Fatalf("history returned %d of %d deliveries", seen, len(cases))
	}
	e, err := s.DeliveryEarnings(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	if e["total_earnings_paise"] != sum || e["earnings_today_paise"] != sum || e["total_earnings"] != float64(sum)/100 ||
		e["earnings_today"] != float64(sum)/100 || e["total_deliveries"] != len(cases) || e["currency"] != "INR" {
		t.Fatalf("earnings = %v, want paise %d", e, sum)
	}
}

// The offer context holds the restaurant, the city and the rider's last ping;
// the view built from it rounds the drop and never names the customer. A
// batch offer pays the stored payouts of every member.
func TestDeliveryOfferContextNamesTheJobButNotTheCustomer(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, rider, partnerID, _ := rnSeedRiderJob(t, s, "DELIVERY_ASSIGNED", "ASSIGNED")
	var locationID uuid.UUID
	if err := s.db.QueryRow(ctx, `
		INSERT INTO food.delivery_partner_locations (delivery_partner_id, latitude, longitude, recorded_at)
		VALUES ($1, 12.9616, 77.5846, NOW() - INTERVAL '20 seconds') RETURNING id
	`, partnerID).Scan(&locationID); err != nil {
		t.Fatal(err)
	}
	offerID := seedPendingOffer(t, s, orderID, partnerID)
	offers, err := s.ListMyPendingDeliveryOffers(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	var offer *DeliveryOffer
	for i := range offers {
		if offers[i].ID == offerID {
			offer = &offers[i]
		}
	}
	if offer == nil {
		t.Fatal("offer missing from the rider's inbox")
	}

	view := func(stage string) DeliveryOfferView {
		t.Helper()
		contexts, err := s.DeliveryOfferContexts(ctx, []uuid.UUID{offerID})
		if err != nil {
			t.Fatalf("%s: %v", stage, err)
		}
		c, ok := contexts[offerID]
		if !ok {
			t.Fatalf("%s: no context", stage)
		}
		if c.RestaurantName != "Test" || c.RestaurantAddressLine1 != "1 Test Lane" || c.RestaurantCity != "Bengaluru" ||
			c.DropCity != "Bengaluru" || c.PayoutPaise != 2320 {
			t.Fatalf("%s: context = %+v", stage, c)
		}
		v := BuildDeliveryOfferView(*offer, &c, time.Now(), 2*time.Minute)
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		rnAssertOfferNamesNoOne(t, string(raw))
		if v.DropArea == nil || v.DropArea.Latitude != 12.98 || v.DropArea.Longitude != 77.64 ||
			v.Restaurant == nil || v.Restaurant.Latitude == nil || *v.Restaurant.Latitude != 12.9716 || v.TripDistanceMeters == nil {
			t.Fatalf("%s: view = %s", stage, raw)
		}
		return v
	}
	if v := view("fresh ping"); v.DistanceToRestaurantMeters == nil {
		t.Fatal("fresh ping: no distance to the restaurant")
	}
	if _, err := s.db.Exec(ctx, `UPDATE food.delivery_partner_locations SET recorded_at = NOW() - INTERVAL '10 minutes' WHERE id = $1`, locationID); err != nil {
		t.Fatal(err)
	}
	if v := view("stale ping"); v.DistanceToRestaurantMeters != nil {
		t.Fatalf("stale ping: distance_to_restaurant_meters = %d", *v.DistanceToRestaurantMeters)
	}

	o1, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	o2, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	rnSetMoney(t, s, seedDeliveryAssignmentWithStatus(t, s, o1, nil, "CREATED"), "29.00", "23.20")
	rnSetMoney(t, s, seedDeliveryAssignmentWithStatus(t, s, o2, nil, "CREATED"), "1.15", "0.92")
	var restaurantID uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT restaurant_id FROM food.orders WHERE id = $1`, o1).Scan(&restaurantID); err != nil {
		t.Fatal(err)
	}
	batch, err := s.CreateBatch(ctx, restaurantID, []uuid.UUID{o1, o2})
	if err != nil {
		t.Fatal(err)
	}
	batchOffer, err := s.CreateDeliveryOfferForBatch(ctx, batch.ID, o1, partnerID, time.Now().Add(time.Minute), nil)
	if err != nil {
		t.Fatal(err)
	}
	contexts, err := s.DeliveryOfferContexts(ctx, []uuid.UUID{batchOffer.ID})
	if err != nil {
		t.Fatal(err)
	}
	if got := contexts[batchOffer.ID].PayoutPaise; got != 2412 {
		t.Fatalf("batch offer payout_paise = %d, want 2412 (2320 + 92)", got)
	}
}
