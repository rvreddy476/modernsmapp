package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// makePartnerWithRide is a tiny helper that drops a partner + a ride into the
// schema so offer tests can attach to real foreign keys.
func makePartnerWithRide(t *testing.T, s *Store) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	cs, _ := s.ListActiveCities(ctx)
	if len(cs) == 0 {
		t.Skip("no seeded cities")
	}
	p, err := s.CreatePartner(ctx, CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "Offer Test", Phone: "+919800000000",
		CityID: &cs[0].ID,
	})
	if err != nil {
		t.Fatalf("create partner: %v", err)
	}
	r, err := s.CreateRide(ctx, CreateRideInput{
		CustomerUserID: uuid.New(), CityID: &cs[0].ID,
		VehicleType: "auto",
		PickupAddress: "P", PickupLat: 12.97, PickupLng: 77.59,
		DropAddress: "D", DropLat: 12.93, DropLng: 77.62,
	})
	if err != nil {
		t.Fatalf("create ride: %v", err)
	}
	return p.ID, r.ID
}

func TestCreateRideOffer_RoundTrip(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	dist := 2.5
	o, err := s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid, Score: 1234.5, DistanceKM: &dist,
		ExpiresAt: time.Now().Add(15 * time.Second),
	})
	if err != nil {
		t.Fatalf("create offer: %v", err)
	}
	if o.Status != "sent" {
		t.Fatalf("status: %s", o.Status)
	}
	got, err := s.GetOffer(context.Background(), o.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Score != 1234.5 {
		t.Fatalf("score round-trip: %v", got.Score)
	}
}

func TestCreateRideOffer_DuplicateIsIdempotent(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	exp := time.Now().Add(15 * time.Second)
	a, err := s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid, Score: 100, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	b, err := s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid, Score: 200, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if a.ID != b.ID {
		t.Fatalf("conflict path should return same offer id")
	}
}

func TestAcceptOfferTx_FirstWinsSecondConflicts(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	exp := time.Now().Add(30 * time.Second)
	o, err := s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid, Score: 100, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	// First accept wins.
	if _, err := s.AcceptOfferTx(context.Background(), o.ID, pid); err != nil {
		t.Fatalf("first accept: %v", err)
	}
	// Second accept must conflict (already decided).
	_, err = s.AcceptOfferTx(context.Background(), o.ID, pid)
	if !errors.Is(err, ErrOfferAlreadyDecided) {
		t.Fatalf("expected already-decided; got %v", err)
	}
}

func TestAcceptOfferTx_SupersedesSiblings(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid1, rid := makePartnerWithRide(t, s)
	// Add a second partner (and its offer) on the same ride.
	cs, _ := s.ListActiveCities(context.Background())
	p2, _ := s.CreatePartner(context.Background(), CreatePartnerInput{
		UserID: uuid.New(), PartnerType: "individual_driver",
		FullName: "P2", Phone: "+919800000001", CityID: &cs[0].ID,
	})
	exp := time.Now().Add(30 * time.Second)
	o1, err := s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid1, Score: 100, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("o1: %v", err)
	}
	o2, err := s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: p2.ID, Score: 90, ExpiresAt: exp,
	})
	if err != nil {
		t.Fatalf("o2: %v", err)
	}
	if _, err := s.AcceptOfferTx(context.Background(), o1.ID, pid1); err != nil {
		t.Fatalf("accept o1: %v", err)
	}
	got, err := s.GetOffer(context.Background(), o2.ID)
	if err != nil {
		t.Fatalf("re-get o2: %v", err)
	}
	if got.Status != "superseded" {
		t.Fatalf("o2 should be superseded; got %s", got.Status)
	}
}

func TestRejectOffer_FlipsStatus(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	o, _ := s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid, Score: 50,
		ExpiresAt: time.Now().Add(30 * time.Second),
	})
	if err := s.RejectOffer(context.Background(), o.ID, pid); err != nil {
		t.Fatalf("reject: %v", err)
	}
	got, _ := s.GetOffer(context.Background(), o.ID)
	if got.Status != "rejected" {
		t.Fatalf("status: %s", got.Status)
	}
	// Replay: should report already-decided.
	if err := s.RejectOffer(context.Background(), o.ID, pid); !errors.Is(err, ErrOfferAlreadyDecided) {
		t.Fatalf("expected already-decided on replay; got %v", err)
	}
}

func TestExpireStaleOffers_FlipsToExpired(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	// Insert an offer with a past expiry so the sweeper picks it up.
	_, err := s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid, Score: 0,
		ExpiresAt: time.Now().Add(-1 * time.Second),
	})
	if err != nil {
		t.Fatalf("create stale offer: %v", err)
	}
	n, err := s.ExpireStaleOffers(context.Background())
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if n < 1 {
		t.Fatalf("expected ≥1 expired; got %d", n)
	}
}

func TestCountOffersForRide(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	_, _ = s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid, Score: 0,
		ExpiresAt: time.Now().Add(30 * time.Second),
	})
	counts, err := s.CountOffersForRide(context.Background(), rid)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if counts["sent"] != 1 {
		t.Fatalf("expected sent=1; got %v", counts)
	}
}

func TestListPendingOffersForPartner(t *testing.T) {
	s, cleanup := riderTestStore(t)
	defer cleanup()
	pid, rid := makePartnerWithRide(t, s)
	_, _ = s.CreateRideOffer(context.Background(), CreateOfferInput{
		RideID: rid, PartnerID: pid, Score: 0,
		ExpiresAt: time.Now().Add(30 * time.Second),
	})
	out, err := s.ListPendingOffersForPartner(context.Background(), pid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 pending; got %d", len(out))
	}
}
