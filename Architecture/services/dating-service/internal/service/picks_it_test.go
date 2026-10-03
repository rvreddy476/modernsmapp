package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// Mechanic M7 with the deck cache: daily picks never repeat someone in the
// deck's current batch. Needs REDIS_ADDR (DB 15) as well as TEST_PG_DSN.
func TestDailyPicks_KeepApartFromTheDeckBatch(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	if svc.rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping deck-cache test")
	}
	svc.SetMechanicsConfig(MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50, Picks: true})
	ctx := context.Background()
	viewer, pool := m1CachePool(t, st, 6)
	t.Cleanup(func() { svc.InvalidatePulseCache(context.Background(), viewer) })

	deck, err := svc.GetPulseToday(ctx, viewer)
	if err != nil || len(deck.Data) == 0 || len(deck.Data) == len(pool) {
		t.Fatalf("deck batch = %d cards of a pool of %d (err %v); want a partial batch", len(deck.Data), len(pool), err)
	}
	inDeck := map[uuid.UUID]bool{}
	for _, c := range deck.Data {
		inDeck[c.CandidateID] = true
	}
	picks, err := svc.GetDailyPicks(ctx, viewer, "UTC")
	if err != nil {
		t.Fatal(err)
	}
	if len(picks.Data) == 0 {
		t.Fatalf("no picks although %d people are outside the deck batch", len(pool)-len(inDeck))
	}
	for _, p := range picks.Data {
		if inDeck[p.CandidateID] {
			t.Fatalf("pick %s is also in the deck's current batch", p.CandidateID)
		}
	}
}
