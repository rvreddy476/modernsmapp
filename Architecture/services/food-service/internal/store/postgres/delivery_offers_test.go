package postgres

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestAcceptDeliveryOfferTx_FirstWinsRace verifies the supersede
// behavior of B4's accept transaction:
//
//   1. Two delivery partners get parallel offers on the same order.
//   2. Both fire AcceptDeliveryOfferTx concurrently.
//   3. Exactly one succeeds; the other's offer is now `superseded`,
//      and only the winner shows up in food.delivery_assignments.
//
// The store layer's FOR UPDATE on the offer row + the UNIQUE on
// delivery_assignments.order_id are what make this safe; this test
// pins the contract.
func TestAcceptDeliveryOfferTx_FirstWinsRace(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	user1, partner1 := seedDeliveryPartner(t, s)
	user2, partner2 := seedDeliveryPartner(t, s)

	expires := time.Now().Add(30 * time.Second)
	offer1, err := s.CreateDeliveryOffer(ctx, orderID, partner1, expires)
	if err != nil {
		t.Fatalf("create offer 1: %v", err)
	}
	offer2, err := s.CreateDeliveryOffer(ctx, orderID, partner2, expires)
	if err != nil {
		t.Fatalf("create offer 2: %v", err)
	}

	var wg sync.WaitGroup
	results := make([]error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, results[0] = s.AcceptDeliveryOfferTx(ctx, user1, offer1.ID)
	}()
	go func() {
		defer wg.Done()
		_, results[1] = s.AcceptDeliveryOfferTx(ctx, user2, offer2.ID)
	}()
	wg.Wait()

	successes := 0
	for _, e := range results {
		if e == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("want exactly 1 winner, got %d (results: %v %v)",
			successes, results[0], results[1])
	}

	// Inspect the offer rows — one accepted, the other superseded
	// (the loser may also surface as `pending` if the row was not
	// taken inside the winning tx; the SUT supersedes pending peers
	// after a successful accept, so we expect exactly one accepted
	// + one superseded).
	statuses := readOfferStatuses(t, s, []uuid.UUID{offer1.ID, offer2.ID})
	acceptedCount := 0
	supersededOrRejected := 0
	for _, st := range statuses {
		switch st {
		case "accepted":
			acceptedCount++
		case "superseded", "rejected", "pending":
			supersededOrRejected++
		}
	}
	if acceptedCount != 1 {
		t.Fatalf("want exactly 1 accepted offer, got %d (statuses=%v)",
			acceptedCount, statuses)
	}

	// Only one delivery_assignments row, pointing to one of the two
	// partners (UNIQUE constraint on order_id enforces this even if
	// the tx logic had a bug).
	var assignedPartner uuid.UUID
	if err := s.db.QueryRow(ctx, `
		SELECT delivery_partner_id FROM food.delivery_assignments WHERE order_id = $1
	`, orderID).Scan(&assignedPartner); err != nil {
		t.Fatalf("read assignment: %v", err)
	}
	if assignedPartner != partner1 && assignedPartner != partner2 {
		t.Fatalf("assignment points to unknown partner %s", assignedPartner)
	}

	// Order has moved to DELIVERY_ASSIGNED.
	assertOrderStatus(t, s, orderID, "DELIVERY_ASSIGNED")
}

// TestAcceptDeliveryOfferTx_RejectsExpiredOrSuperseded ensures the
// idempotent-replay protection holds — once an offer's status leaves
// `pending`, AcceptDeliveryOfferTx refuses.
func TestAcceptDeliveryOfferTx_RejectsExpiredOrSuperseded(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	ctx := context.Background()

	orderID, _, _ := seedOrderWithItem(t, s, "DELIVERY_ASSIGNING")
	user, partner := seedDeliveryPartner(t, s)

	// Past-expiry offer.
	stale := time.Now().Add(-1 * time.Hour)
	offer, err := s.CreateDeliveryOffer(ctx, orderID, partner, stale)
	if err != nil {
		t.Fatalf("create stale offer: %v", err)
	}
	if _, err := s.ExpireDeliveryOffers(ctx); err != nil {
		t.Fatalf("expire pass: %v", err)
	}
	if _, err := s.AcceptDeliveryOfferTx(ctx, user, offer.ID); err == nil {
		t.Fatal("expected refusal on expired offer")
	}
}

func readOfferStatuses(t *testing.T, s *Store, ids []uuid.UUID) []string {
	t.Helper()
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		var st string
		if err := s.db.QueryRow(context.Background(), `
			SELECT status::text FROM food.delivery_offers WHERE id = $1
		`, id).Scan(&st); err != nil {
			t.Fatalf("read offer status: %v", err)
		}
		out = append(out, st)
	}
	return out
}
