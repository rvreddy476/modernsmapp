package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/foodevents"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/realtime"
	"github.com/google/uuid"
)

// rnOfferContext is the store's view of an offered job: exact points (the
// drop is rounded before anything leaves the service) and the stored payout.
func rnOfferContext() postgres.DeliveryOfferContext {
	lat, lng, dLat, dLng := 12.9716, 77.5946, 12.978449, 77.640812
	return postgres.DeliveryOfferContext{
		RestaurantID: uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0002"), RestaurantName: "Test Kitchen",
		RestaurantLat: &lat, RestaurantLng: &lng, RestaurantAddressLine1: "1 Test Lane", RestaurantCity: "Bengaluru",
		DropLat: &dLat, DropLng: &dLng, DropCity: "Bengaluru", PayoutPaise: 2320,
	}
}

// rnAssertOfferPrivate: an offer payload (list item, realtime frame or
// food.delivery.offered event) names the restaurant, a ~500 m drop area and
// the payout, and nothing that identifies or pins the customer.
func rnAssertOfferPrivate(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	body := string(raw)
	for _, banned := range []string{"42 Lake View Road", "Flat 3B", "Opp. City Park", "560038", "Asha", "Rao", "9812345678",
		"12.978449", "77.640812", "address_line2", "landmark", "postal_code", "receiver_name", "phone", "customer", "delivery_instructions"} {
		if strings.Contains(body, banned) {
			t.Fatalf("offer payload carries %q: %s", banned, body)
		}
	}
	var p map[string]any
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	offer := p
	if inner, ok := p["offer"].(map[string]any); ok {
		offer = inner
	}
	area, _ := offer["drop_area"].(map[string]any)
	if area == nil || area["latitude"] != 12.98 || area["longitude"] != 77.64 || area["locality"] != "Bengaluru" {
		t.Fatalf("drop_area = %v in %s", offer["drop_area"], body)
	}
	restaurant, _ := offer["restaurant"].(map[string]any)
	if restaurant == nil || restaurant["name"] != "Test Kitchen" || restaurant["latitude"] != 12.9716 || restaurant["address_line1"] != "1 Test Lane" {
		t.Fatalf("restaurant = %v in %s", offer["restaurant"], body)
	}
	if offer["payout_paise"] != float64(2320) || offer["currency"] != "INR" {
		t.Fatalf("payout = %v %v in %s", offer["payout_paise"], offer["currency"], body)
	}
	if trip, _ := offer["trip_distance_meters"].(float64); trip <= 0 {
		t.Fatalf("trip_distance_meters = %v in %s", offer["trip_distance_meters"], body)
	}
	return offer
}

type rnFramePublisher struct{ frames []any }

func (p *rnFramePublisher) Publish(_ context.Context, _, _ string, data any) error {
	p.frames = append(p.frames, data)
	return nil
}

// The dispatch worker's realtime frame and its food.delivery.offered event
// carry the job detail, and neither carries the drop-off address.
func TestDispatchedOfferNamesTheJobButNotTheCustomer(t *testing.T) {
	now := time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC)
	recorded := now.Add(-15 * time.Second)
	riderLat, riderLng := 12.9616, 77.5846
	c := rnOfferContext()
	c.RiderLat, c.RiderLng, c.RiderRecordedAt = &riderLat, &riderLng, &recorded

	partnerUser, partnerRow := uuid.New(), uuid.New()
	fake := &dispatchFakeStore{
		ready: []postgres.ReadyOrderForBatching{{
			OrderID: uuid.New(), RestaurantID: uuid.New(), PlacedAt: now,
			RestaurantLat: ptr(12.9716), RestaurantLng: ptr(77.5946),
		}},
		cands:    []postgres.DispatchCandidate{{PartnerID: partnerRow, UserID: partnerUser, DistanceKM: 1.25}},
		offerCtx: &c,
	}
	pub := &rnFramePublisher{}
	var events [][]byte
	svc := New(fake).WithDispatchConfig(4, 90*time.Second).WithRealtimeClock(func() time.Time { return now })
	svc.rtPublisher = pub
	svc.rtSigner = realtime.NewTokenSigner([]byte("test-only-secret"))
	svc.outboxSink = func(_ context.Context, eventType, _ string, body []byte) error {
		if eventType == foodevents.DeliveryOffered {
			events = append(events, body)
		}
		return nil
	}

	if err := svc.dispatchPendingOrders(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(pub.frames) != 1 || len(events) != 1 {
		t.Fatalf("frames = %d, events = %d, want one each", len(pub.frames), len(events))
	}
	frame, err := json.Marshal(pub.frames[0])
	if err != nil {
		t.Fatal(err)
	}
	for name, raw := range map[string][]byte{"realtime frame": frame, "food.delivery.offered": events[0]} {
		offer := rnAssertOfferPrivate(t, raw)
		if d, _ := offer["distance_to_restaurant_meters"].(float64); d <= 0 {
			t.Fatalf("%s: distance_to_restaurant_meters = %v", name, offer["distance_to_restaurant_meters"])
		}
		if offer["id"] != fake.offers[0].ID.String() || offer["delivery_partner_user_id"] != partnerUser.String() {
			t.Fatalf("%s: offer id / rider = %v / %v", name, offer["id"], offer["delivery_partner_user_id"])
		}
	}
}

type rnListStore struct {
	Store
	offers []postgres.DeliveryOffer
	ctx    postgres.DeliveryOfferContext
}

func (f *rnListStore) ListMyPendingDeliveryOffers(context.Context, uuid.UUID) ([]postgres.DeliveryOffer, error) {
	return f.offers, nil
}

func (f *rnListStore) DeliveryOfferContexts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.DeliveryOfferContext, error) {
	out := map[uuid.UUID]postgres.DeliveryOfferContext{}
	for _, id := range ids {
		c := f.ctx
		c.OfferID = id
		out[id] = c
	}
	return out, nil
}

// The inbox list carries the same detail; the distance to the restaurant only
// while the rider's last ping is fresher than the dispatch location max age.
func TestListMyPendingDeliveryOffersCarriesTheJobDetail(t *testing.T) {
	now := time.Date(2026, 9, 13, 6, 30, 0, 0, time.UTC)
	riderLat, riderLng := 12.9616, 77.5846
	for _, tc := range []struct {
		name     string
		pingAge  time.Duration
		distance bool
	}{
		{"fresh ping", 30 * time.Second, true},
		{"stale ping", 91 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorded := now.Add(-tc.pingAge)
			c := rnOfferContext()
			c.RiderLat, c.RiderLng, c.RiderRecordedAt = &riderLat, &riderLng, &recorded
			st := &rnListStore{offers: []postgres.DeliveryOffer{{ID: uuid.New(), OrderID: uuid.New(), DeliveryPartnerID: uuid.New(),
				Status: "pending", ExpiresAt: "2026-09-13 06:30:25+00", CreatedAt: "2026-09-13 06:30:00+00"}}, ctx: c}
			svc := New(st).WithDispatchConfig(4, 90*time.Second).WithRealtimeClock(func() time.Time { return now })
			items, err := svc.ListMyPendingDeliveryOffers(context.Background(), uuid.New())
			if err != nil || len(items) != 1 {
				t.Fatalf("list = %v, %v", items, err)
			}
			raw, err := json.Marshal(items[0])
			if err != nil {
				t.Fatal(err)
			}
			offer := rnAssertOfferPrivate(t, raw)
			if _, has := offer["distance_to_restaurant_meters"]; has != tc.distance {
				t.Fatalf("distance_to_restaurant_meters present = %v, want %v: %s", has, tc.distance, raw)
			}
			if offer["expires_at"] != "2026-09-13 06:30:25+00" {
				t.Fatalf("expires_at = %v", offer["expires_at"])
			}
		})
	}
}
