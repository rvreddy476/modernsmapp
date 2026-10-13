package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/food-service/internal/payments"
	"github.com/google/uuid"
)

func TestDispatchCandidatesAllowOfferComparisonAndEnforceCap(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, partner := seedDeliveryPartner(t, s)
	if _, err := s.db.Exec(ctx, `INSERT INTO food.delivery_partner_locations(delivery_partner_id,latitude,longitude) VALUES($1,-31.721,148.333)`, partner); err != nil {
		t.Fatal(err)
	}
	eligible := func() bool {
		t.Helper()
		cs, err := s.ListDispatchCandidates(ctx, DispatchQuery{Lat: -31.721, Lng: 148.333, RadiusKM: 1, MaxLocationAge: time.Minute})
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range cs {
			if c.PartnerID == partner {
				return true
			}
		}
		return false
	}
	var offers []*DeliveryOffer
	for i := 0; i < MaxPendingDeliveryOffers; i++ {
		if !eligible() {
			t.Fatalf("excluded with only %d pending offers", i)
		}
		order, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
		o, err := s.CreateSequentialDeliveryOffer(ctx, order, partner, nil, 1)
		if err != nil || o == nil {
			t.Fatalf("offer %d: %v", i, err)
		}
		offers = append(offers, o)
	}
	if eligible() {
		t.Fatal("partner at offer cap remains eligible")
	}
	order, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	if o, err := s.CreateSequentialDeliveryOffer(ctx, order, partner, nil, 1); err != nil || o != nil {
		t.Fatalf("writer exceeded cap: %v %v", o, err)
	}
	if _, err := s.AcceptDeliveryOfferTx(ctx, user, offers[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptDeliveryOfferTx(ctx, user, offers[1].ID); err == nil {
		t.Fatal("accepted second active job")
	}
	if eligible() {
		t.Fatal("active partner remains eligible")
	}
	if pending, err := s.ListMyPendingDeliveryOffers(ctx, user); err != nil || len(pending) != 0 {
		t.Fatalf("active rider still sees offers: %v %v", pending, err)
	}
}

func TestReleasedDeliveryHistoryAndRetryArePreserved(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	order, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNED")
	user, partner := seedDeliveryPartner(t, s)
	aid := seedDeliveryAssignmentWithStatus(t, s, order, &partner, "ACCEPTED")
	if _, _, err := s.EnsureDeliveryCodes(ctx, order); err != nil {
		t.Fatal(err)
	}
	key := uuid.NewString()
	for i := 0; i < 2; i++ {
		a, err := s.DeliveryUpdateAssignment(ctx, user, aid, "REJECTED", key)
		if err != nil || a == nil || a.Status != "REJECTED" {
			t.Fatalf("release %d: %v %v", i, a, err)
		}
	}
	history, err := s.DeliveryHistory(ctx, user)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].ID != aid || history[0].PickupCode != "" || history[0].Drop != nil || history[0].Navigation != nil {
		t.Fatalf("missing or unsafe released history: %+v", history)
	}
	other, nextPartner := seedDeliveryPartner(t, s)
	offer, err := s.CreateSequentialDeliveryOffer(ctx, order, nextPartner, nil, 1)
	if err != nil || offer == nil {
		t.Fatalf("redispatch: %v", err)
	}
	if _, err := s.AcceptDeliveryOfferTx(ctx, other, offer.ID); err != nil {
		t.Fatal(err)
	}
	history, err = s.DeliveryHistory(ctx, user)
	if err != nil || len(history) != 1 || history[0].DeliveryPartnerID == nil || *history[0].DeliveryPartnerID != partner {
		t.Fatalf("reassignment overwrote history: %v %v", history, err)
	}
}

// Integrated store workflow uses an authoritative payment-event fixture, not a
// real provider transaction. It exercises actual SQL, locks, snapshots and
// lifecycle/outbox writers without contacting customers or payment providers.
func TestFoodFlowCheckoutPaymentDeliveryAndLedger(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()
	customer, restaurant, owner, _ := seedPlaceableCart(t, s, f64(12.9), f64(77.6))
	if _, err := s.GetRestaurant(ctx, restaurant, &GeoPoint{Lat: 12.901, Lng: 77.6}); err != nil {
		t.Fatal(err)
	}
	address := seedAddress(t, s, customer, f64(12.901), f64(77.6))
	order, err := s.PlaceOrder(ctx, customer, PlaceOrderInput{AddressID: address, PaymentMethod: "ONLINE", PaymentInstrument: "upi"}, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreatePaymentIntent(ctx, customer, order.ID, "ONLINE", "upi", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	intent := uuid.NewString()
	if err := s.AttachPaymentProviderReference(ctx, customer, order.ID, intent, "provider-order", nil); err != nil {
		t.Fatal(err)
	}
	event := succeeded(order.ID, customer, intent)
	if err := s.db.QueryRow(ctx, `SELECT final_amount_paise FROM food.orders WHERE id=$1`, order.ID).Scan(&event.AmountMinor); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		res, err := s.ApplyPaymentEvent(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && res.Decision.Outcome != payments.OutcomeDuplicate {
			t.Fatal("payment replay not deduped")
		}
	}
	for _, status := range []string{"PREPARING", "READY_FOR_PICKUP"} {
		if _, err := s.PartnerUpdateOrderStatus(ctx, owner, order.ID, status, "", uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	rider, partner := seedDeliveryPartner(t, s)
	offer, err := s.CreateSequentialDeliveryOffer(ctx, order.ID, partner, nil, 1)
	if err != nil || offer == nil {
		t.Fatalf("offer: %v", err)
	}
	if _, err := s.AcceptDeliveryOfferTx(ctx, rider, offer.ID); err != nil {
		t.Fatal(err)
	}
	aid, _, _ := readAssignment(t, s, order.ID)
	if _, err := s.DeliveryUpdateAssignment(ctx, rider, aid, "ACCEPTED", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	pickup, delivery, err := s.EnsureDeliveryCodes(ctx, order.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := s.VerifyPickupCode(ctx, owner, order.ID, pickup); err != nil {
			t.Fatal(err)
		}
	}
	assertOrderStatus(t, s, order.ID, "OUT_FOR_DELIVERY")
	if _, err := s.DeliveryUpdateAssignment(ctx, rider, aid, "ARRIVED_AT_CUSTOMER", uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := s.RiderVerifyDeliveryCode(ctx, rider, aid, delivery); err != nil {
			t.Fatal(err)
		}
	}
	assertOrderStatus(t, s, order.ID, "DELIVERED")
	if countHistoryTo(t, s, order.ID, "DELIVERED") != 1 {
		t.Fatal("duplicate completion")
	}
	earnings, err := s.DeliveryEarnings(ctx, rider)
	if err != nil {
		t.Fatal(err)
	}
	if earnings["total_deliveries"] != 1 {
		t.Fatalf("duplicate/missing earnings count: %v", earnings)
	}
	var expectedPaise int64
	if err := s.db.QueryRow(ctx, `SELECT ROUND(delivery_partner_payout*100)::bigint FROM food.delivery_assignments WHERE id=$1`, aid).Scan(&expectedPaise); err != nil {
		t.Fatal(err)
	}
	if earnings["total_earnings_paise"] != expectedPaise {
		t.Fatalf("earnings don't equal one completed delivery: %v", earnings)
	}
	for _, kind := range []string{"food.order.payment_succeeded", "food.order.restaurant_accepted", "food.order.ready_for_pickup", "food.delivery.assigned", "food.delivery.picked_up", "food.delivery.delivered"} {
		var count int
		if err := s.db.QueryRow(ctx, `SELECT count(*) FROM food.outbox_events WHERE event_type=$1 AND payload->>'order_id'=$2`, kind, order.ID.String()).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("%s events: %d", kind, count)
		}
	}
}
