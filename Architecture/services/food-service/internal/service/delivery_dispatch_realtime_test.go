package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/realtime"
	"github.com/google/uuid"
)

type dispatchFakeStore struct {
	Store
	ready   []postgres.ReadyOrderForBatching
	cands   []postgres.DispatchCandidate
	queries []postgres.DispatchQuery
	offers  []postgres.DeliveryOffer
	// offerCtx is what DeliveryOfferContexts answers for every offer id; nil
	// answers none, as for an offer whose order has gone.
	offerCtx *postgres.DeliveryOfferContext
}

func (f *dispatchFakeStore) DeliveryOfferContexts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.DeliveryOfferContext, error) {
	out := map[uuid.UUID]postgres.DeliveryOfferContext{}
	if f.offerCtx == nil {
		return out, nil
	}
	for _, id := range ids {
		c := *f.offerCtx
		c.OfferID = id
		out[id] = c
	}
	return out, nil
}

func (f *dispatchFakeStore) ListUnbatchedReadyOrders(context.Context, int) ([]postgres.ReadyOrderForBatching, error) {
	return f.ready, nil
}

func (f *dispatchFakeStore) ListDispatchCandidates(_ context.Context, q postgres.DispatchQuery) ([]postgres.DispatchCandidate, error) {
	f.queries = append(f.queries, q)
	return f.cands, nil
}

func (f *dispatchFakeStore) CreateDeliveryOffer(_ context.Context, orderID, partnerID uuid.UUID, expiresAt time.Time, distanceKM *float64) (*postgres.DeliveryOffer, error) {
	o := postgres.DeliveryOffer{ID: uuid.New(), OrderID: orderID, DeliveryPartnerID: partnerID, DistanceKM: distanceKM, Status: "pending"}
	f.offers = append(f.offers, o)
	return &o, nil
}

func (f *dispatchFakeStore) ListOrders(context.Context, uuid.UUID) ([]postgres.Order, error) {
	return nil, nil
}

func (f *dispatchFakeStore) ListPartnerRestaurants(context.Context, uuid.UUID) ([]postgres.PartnerRestaurant, error) {
	return nil, nil
}

// GetDeliveryPartner returns the candidate's row: its id is the
// delivery_partners.id, deliberately different from the user id.
func (f *dispatchFakeStore) GetDeliveryPartner(_ context.Context, userID uuid.UUID) (*postgres.DeliveryPartner, error) {
	for _, c := range f.cands {
		if c.UserID == userID {
			return &postgres.DeliveryPartner{ID: c.PartnerID, UserID: c.UserID}, nil
		}
	}
	return nil, nil
}

type capturePublisher struct{ topics []string }

func (c *capturePublisher) Publish(_ context.Context, topic, _ string, _ any) error {
	c.topics = append(c.topics, topic)
	return nil
}

func ptr(v float64) *float64 { return &v }

// The offer is published on a topic the partner's own realtime token grants.
// The token can only be keyed by user id, so the publish must be too.
func TestDispatchPublishesOnTheTopicTheTokenGrants(t *testing.T) {
	partnerUser, partnerRow := uuid.New(), uuid.New()
	fake := &dispatchFakeStore{
		ready: []postgres.ReadyOrderForBatching{{
			OrderID: uuid.New(), RestaurantID: uuid.New(), PlacedAt: time.Now(),
			RestaurantLat: ptr(12.9), RestaurantLng: ptr(77.6),
		}},
		cands: []postgres.DispatchCandidate{{PartnerID: partnerRow, UserID: partnerUser, DistanceKM: 1.25}},
	}
	pub := &capturePublisher{}
	svc := New(fake).WithDispatchConfig(4, 90*time.Second)
	svc.rtPublisher = pub
	svc.rtSigner = realtime.NewTokenSigner([]byte("test-only-secret"))

	if err := svc.dispatchPendingOrders(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(pub.topics) != 1 {
		t.Fatalf("want one publish, got %v", pub.topics)
	}
	tok, err := svc.IssueRealtimeToken(context.Background(), partnerUser, RealtimeScopeDelivery, nil)
	if err != nil {
		t.Fatalf("token: %v", err)
	}
	granted := tok.Topics
	if len(granted) != 1 {
		t.Fatalf("delivery token grants %v, want exactly the partner's assignments topic", granted)
	}
	found := false
	for _, g := range granted {
		if g == pub.topics[0] {
			found = true
		}
	}
	if !found {
		t.Fatalf("published on %q, but the partner's token grants %v", pub.topics[0], granted)
	}

	if len(fake.offers) != 1 || fake.offers[0].DeliveryPartnerID != partnerRow {
		t.Fatalf("offer must target delivery_partners.id: %+v", fake.offers)
	}
	if fake.offers[0].DistanceKM == nil || *fake.offers[0].DistanceKM != 1.25 {
		t.Fatalf("offer distance_km not filled: %+v", fake.offers[0])
	}
	if len(fake.queries) != 1 {
		t.Fatalf("want one candidate query, got %d", len(fake.queries))
	}
	q := fake.queries[0]
	if q.Lat != 12.9 || q.Lng != 77.6 || q.RadiusKM != 4 || q.MaxLocationAge != 90*time.Second || q.Limit != offersPerOrder {
		t.Fatalf("candidate query = %+v", q)
	}
}

func TestDispatchSkipsRestaurantWithoutLocation(t *testing.T) {
	fake := &dispatchFakeStore{
		ready: []postgres.ReadyOrderForBatching{{OrderID: uuid.New(), RestaurantID: uuid.New(), PlacedAt: time.Now()}},
		cands: []postgres.DispatchCandidate{{PartnerID: uuid.New(), UserID: uuid.New()}},
	}
	svc := New(fake)
	if err := svc.dispatchPendingOrders(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(fake.queries) != 0 || len(fake.offers) != 0 {
		t.Fatalf("dispatched without a pickup location: queries=%d offers=%d", len(fake.queries), len(fake.offers))
	}
}

func TestDispatchConfigDefaults(t *testing.T) {
	t.Setenv("FOOD_DISPATCH_RADIUS_KM", "")
	t.Setenv("FOOD_DISPATCH_LOCATION_MAX_AGE_SECONDS", "")
	svc := New(nil)
	if svc.dispatchRadiusKM != 5 || svc.dispatchLocationMaxAge != 120*time.Second {
		t.Fatalf("defaults = %v km, %v", svc.dispatchRadiusKM, svc.dispatchLocationMaxAge)
	}
	t.Setenv("FOOD_DISPATCH_RADIUS_KM", "3.5")
	t.Setenv("FOOD_DISPATCH_LOCATION_MAX_AGE_SECONDS", "45")
	svc = New(nil)
	if svc.dispatchRadiusKM != 3.5 || svc.dispatchLocationMaxAge != 45*time.Second {
		t.Fatalf("env = %v km, %v", svc.dispatchRadiusKM, svc.dispatchLocationMaxAge)
	}
}
