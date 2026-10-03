package service

import (
	"context"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Mechanic M1 with the deck cache: the batch is cached, each acted-on card
// leaves it, and a used-up batch refills on the very next fetch. Needs
// REDIS_ADDR (DB 15) as well as TEST_PG_DSN.

// m1CachePool seeds a viewer and n candidates only the viewer can see (a
// gender nobody else in the database has, which the viewer alone looks for).
func m1CachePool(t *testing.T, st *store.Store, n int) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	gender := "m1c-" + uuid.NewString()[:8]
	viewer := uuid.New()
	seedActiveProfile(t, st, viewer)
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatal(err)
	}
	pool := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		id := uuid.New()
		seedActiveProfile(t, st, id)
		if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Gender: &gender}); err != nil {
			t.Fatal(err)
		}
		pool = append(pool, id)
	}
	return viewer, pool
}

func TestDeckRefill_UsedUpCachedBatchRefillsAtOnce(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	if svc.rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping deck-cache test")
	}
	svc.SetMechanicsConfig(MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50})
	ctx := context.Background()
	// More candidates than one batch holds, so a second batch exists.
	viewer, pool := m1CachePool(t, st, 10)
	t.Cleanup(func() { svc.InvalidatePulseCache(context.Background(), viewer) })

	first, err := svc.GetPulseToday(ctx, viewer)
	if err != nil || len(first.Data) == 0 {
		t.Fatalf("first batch = %v err=%v", first, err)
	}
	if svc.readPulseCache(ctx, viewer) == nil {
		t.Fatalf("the batch was not cached")
	}
	seen := map[uuid.UUID]bool{}
	for _, card := range first.Data {
		seen[card.CandidateID] = true
		if _, err := svc.PassCandidate(ctx, viewer, card.CandidateID, ""); err != nil {
			t.Fatalf("pass: %v", err)
		}
	}

	next, err := svc.GetPulseToday(ctx, viewer)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Data) == 0 {
		t.Fatalf("the used-up batch did not refill (%d candidates were left in the pool of %d)", len(pool)-len(seen), len(pool))
	}
	for _, card := range next.Data {
		if seen[card.CandidateID] {
			t.Fatalf("the refill repeats a passed card %s", card.CandidateID)
		}
	}
	if next.Meta.RemainingToday != 50-len(first.Data) {
		t.Fatalf("remaining_today = %d, want %d", next.Meta.RemainingToday, 50-len(first.Data))
	}
}

// A batch that is genuinely empty (nobody to show) is trusted for a while
// instead of being recomputed on every fetch.
func TestDeckRefill_GenuinelyEmptyBatchIsNotRecomputedEachFetch(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	if svc.rdb == nil {
		t.Skip("REDIS_ADDR not set; skipping deck-cache test")
	}
	svc.SetMechanicsConfig(MechanicsConfig{DeckRefill: true, DeckDailyLimitFree: 50, DeckDailyLimitPass: 50})
	ctx := context.Background()
	viewer, _ := m1CachePool(t, st, 0)
	t.Cleanup(func() { svc.InvalidatePulseCache(context.Background(), viewer) })
	if resp, err := svc.GetPulseToday(ctx, viewer); err != nil || len(resp.Data) != 0 {
		t.Fatalf("empty pool deck = %v err=%v", resp, err)
	}
	cached := svc.readPulseCache(ctx, viewer)
	if cached == nil || len(cached.Data) != 0 {
		t.Fatalf("an empty batch was not cached: %v", cached)
	}
}
