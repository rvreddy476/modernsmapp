package store

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sequentialPartner(t *testing.T, s *Store) (uuid.UUID, uuid.UUID) {
	t.Helper()
	p, r := makePartnerWithRide(t, s)
	if _, err := s.db.Exec(context.Background(), `UPDATE rider_partners SET status='approved',kyc_status='approved',is_online=true WHERE id=$1`, p); err != nil {
		t.Fatal(err)
	}
	return p, r
}

func TestSequentialRideOfferConcurrentWorkersAndDurableEvent(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	p, r := sequentialPartner(t, s)
	p2, _ := sequentialPartner(t, s)
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make([]*RideOffer, 12)
	errs := make([]error, 12)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			pid := p
			if i%2 == 0 {
				pid = p2
			}
			results[i], errs[i] = s.CreateSequentialRideOffer(ctx, CreateOfferInput{RideID: r, PartnerID: pid, ExpiresAt: time.Now().Add(time.Hour)})
		}(i)
	}
	wg.Wait()
	created := 0
	for i, o := range results {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if o != nil {
			created++
			if d := time.Until(o.ExpiresAt); d > 20*time.Second || d < 15*time.Second {
				t.Fatalf("wrong expiry: %v", d)
			}
		}
	}
	if created != 1 {
		t.Fatalf("created %d offers", created)
	}
	var count int
	var body []byte
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM rider.outbox_events WHERE event_type='rider.ride.offered' AND payload->>'ride_id'=$1`, r.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("want one durable event, got %d", count)
	}
	if err := s.db.QueryRow(ctx, `SELECT payload FROM rider.outbox_events WHERE payload->>'ride_id'=$1`, r.String()).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	if event["partner_user_id"] == nil || event["expires_at"] == nil {
		t.Fatalf("missing authoritative routing or deadline: %s", body)
	}
}

func TestSequentialRideOfferExpiryMovesToNextPartnerWithoutReplay(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, r := sequentialPartner(t, s)
	p2, _ := sequentialPartner(t, s)
	a, err := s.CreateSequentialRideOffer(ctx, CreateOfferInput{RideID: r, PartnerID: p})
	if err != nil || a == nil {
		t.Fatalf("first: %v", err)
	}
	if _, err = s.db.Exec(ctx, `UPDATE rider_ride_offers SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1`, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ExpireStaleOffers(ctx); err != nil {
		t.Fatal(err)
	}
	replay, err := s.CreateSequentialRideOffer(ctx, CreateOfferInput{RideID: r, PartnerID: p})
	if err != nil || replay != nil {
		t.Fatalf("old offer replayed: %v %v", replay, err)
	}
	next, err := s.CreateSequentialRideOffer(ctx, CreateOfferInput{RideID: r, PartnerID: p2})
	if err != nil || next == nil {
		t.Fatalf("next: %v", err)
	}
	var user uuid.UUID
	if err = s.db.QueryRow(ctx, `SELECT user_id FROM rider_partners WHERE id=$1`, p).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.AcceptOfferAndAssignRideTx(ctx, AcceptOfferAndAssignInput{OfferID: a.ID, PartnerID: p, PartnerUserID: user}); !errors.Is(err, ErrOfferAlreadyDecided) {
		t.Fatal("expired offer accepted")
	}
}

func TestSequentialRideOfferPartnerCanCompareTwoRides(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, r := sequentialPartner(t, s)
	_, r2 := sequentialPartner(t, s)
	var wg sync.WaitGroup
	offers := make([]*RideOffer, 2)
	errs := make([]error, 2)
	for i, id := range []uuid.UUID{r, r2} {
		wg.Add(1)
		go func(i int, id uuid.UUID) {
			defer wg.Done()
			offers[i], errs[i] = s.CreateSequentialRideOffer(ctx, CreateOfferInput{RideID: id, PartnerID: p})
		}(i, id)
	}
	wg.Wait()
	n := 0
	for i, o := range offers {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		if o != nil {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("offers=%d", n)
	}
	if _, err := s.db.Exec(ctx, `UPDATE rider_rides SET pickup_address='Uppal',drop_address='Peerzadiguda',estimated_fare=123.45 WHERE id=$1`, r); err != nil {
		t.Fatal(err)
	}
	visible, err := s.ListPendingOffersForPartner(ctx, p)
	if err != nil || len(visible) != 2 {
		t.Fatalf("readback=%v error=%v", visible, err)
	}
	for _, offer := range visible {
		if offer.RideID == r && (offer.PickupAddress != "Uppal" || offer.DropAddress != "Peerzadiguda" || offer.EstimatedFarePaise == nil || *offer.EstimatedFarePaise != 12345) {
			t.Fatalf("missing comparison details: %+v", offer)
		}
	}
}

func TestSequentialRideOfferOutboxFailureRollsBackOffer(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, r := sequentialPartner(t, s)
	// Test-only trigger rejects this event, leaving other test events untouched.
	_, err := s.db.Exec(ctx, `CREATE OR REPLACE FUNCTION rider.reject_test_offer() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test outbox unavailable'; END $$;
	CREATE TRIGGER reject_test_offer BEFORE INSERT ON rider.outbox_events FOR EACH ROW EXECUTE FUNCTION rider.reject_test_offer()`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if _, err := s.db.Exec(ctx, `DROP TRIGGER reject_test_offer ON rider.outbox_events; DROP FUNCTION rider.reject_test_offer()`); err != nil {
			t.Errorf("drop test trigger: %v", err)
		}
	}()
	if _, err = s.CreateSequentialRideOffer(ctx, CreateOfferInput{RideID: r, PartnerID: p}); err == nil {
		t.Fatal("outbox failure ignored")
	}
	var n int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM rider_ride_offers WHERE ride_id=$1`, r).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("orphan offer committed")
	}
}

func TestSequentialRideAssignmentRoutesAuthoritativeRecipients(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	ctx := context.Background()
	p, r := sequentialPartner(t, s)
	var user uuid.UUID
	if err := s.db.QueryRow(ctx, `SELECT user_id FROM rider_partners WHERE id=$1`, p).Scan(&user); err != nil {
		t.Fatal(err)
	}
	o, err := s.CreateSequentialRideOffer(ctx, CreateOfferInput{RideID: r, PartnerID: p})
	if err != nil {
		t.Fatal(err)
	}
	_, assigned, err := s.AcceptOfferAndAssignRideTx(ctx, AcceptOfferAndAssignInput{OfferID: o.ID, PartnerID: p, PartnerUserID: user, OutboxEventType: "rider.ride.assigned", OutboxPayload: []byte(`{"customer_user_id":"wrong","otp":"do-not-publish"}`)})
	if err != nil {
		t.Fatal(err)
	}
	var body []byte
	if err = s.db.QueryRow(ctx, `SELECT payload FROM rider.outbox_events WHERE event_type='rider.ride.assigned' AND payload->>'ride_id'=$1`, r.String()).Scan(&body); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err = json.Unmarshal(body, &event); err != nil {
		t.Fatal(err)
	}
	if event["customer_user_id"] != assigned.CustomerUserID.String() || event["partner_user_id"] != user.String() {
		t.Fatalf("wrong routing: %s", body)
	}
	if _, ok := event["otp"]; ok {
		t.Fatal("secret propagated")
	}
	if _, err = s.TransitionRideAtomic(ctx, TransitionRideAtomicInput{RideID: r, ExpectedRevision: assigned.Revision, FromStatus: "partner_assigned", ToStatus: "cancelled_by_customer", ActorKind: "customer", OutboxEventType: "rider.ride.cancelled", OutboxPayload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.db.QueryRow(ctx, `SELECT count(*) FROM rider.outbox_events WHERE event_type='rider.ride.cancelled' AND payload->>'ride_id'=$1 AND payload->>'partner_user_id'=$2`, r.String(), user.String()).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatal("partner cancellation not queued")
	}
}
