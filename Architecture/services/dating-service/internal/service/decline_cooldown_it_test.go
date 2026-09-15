// Decline cooldown: within it the declined sender cannot spark the decliner
// (the ordinary CANDIDATE_UNAVAILABLE), does not see them in the deck, and no
// match forms from their sparks; the decliner is unaffected; after it all is
// as before. Skipped without TEST_PG_DSN; cached-deck checks need REDIS_ADDR.
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ageDecline(t *testing.T, pool *pgxpool.Pool, sparkID uuid.UUID, ago time.Duration) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE dating_sparks SET declined_at = now() - make_interval(secs => $2) WHERE id = $1`,
		sparkID, ago.Seconds()); err != nil {
		t.Fatalf("age decline: %v", err)
	}
}

func deckHas(resp *PulseResponse, id uuid.UUID) bool {
	for _, c := range resp.Data {
		if c.CandidateID == id {
			return true
		}
	}
	return false
}

func TestDeclineCooldown_SenderRefusedAndOffDeckThenRestored(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	pool := itPool(t)
	ctx := context.Background()
	gender := "dc-" + uuid.NewString()[:8]
	sender, decliner, other := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{sender, decliner, other} {
		seedActiveProfile(t, st, id)
	}
	for _, id := range []uuid.UUID{decliner, other} {
		if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Gender: &gender}); err != nil {
			t.Fatalf("set gender: %v", err)
		}
	}
	if _, err := st.UpsertPreferences(ctx, sender, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatalf("set preference: %v", err)
	}
	deck := func() *PulseResponse {
		resp, err := svc.computePulseToday(ctx, sender)
		if err != nil {
			t.Fatalf("deck: %v", err)
		}
		return resp
	}

	sp, _, err := svc.CreateSpark(ctx, sender, decliner, "photo", "0", "")
	if err != nil {
		t.Fatalf("spark: %v", err)
	}
	first := deck()
	if !deckHas(first, decliner) || !deckHas(first, other) {
		t.Fatalf("seeded candidates missing from the sender's deck before the decline")
	}
	if svc.rdb != nil {
		svc.writePulseCache(ctx, sender, first)
	}
	if _, err := svc.DeclineSpark(ctx, sp.ID, decliner); err != nil {
		t.Fatalf("decline: %v", err)
	}
	ageDecline(t, pool, sp.ID, 24*time.Hour)

	for _, ref := range []string{"p1", "0"} {
		kind := "prompt"
		if ref == "0" {
			kind = "photo"
		}
		if _, _, err := svc.CreateSpark(ctx, sender, decliner, kind, ref, ""); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("spark %s/%s one day after a decline: err=%v, want ErrCandidateUnavailable", kind, ref, err)
		}
	}
	next := deck()
	if deckHas(next, decliner) {
		t.Fatalf("decliner is in the sender's deck within the cooldown")
	}
	if !deckHas(next, other) {
		t.Fatalf("the decline removed an unrelated candidate")
	}
	if svc.rdb != nil {
		if cached := svc.readPulseCache(ctx, sender); cached == nil || deckHas(cached, decliner) || !deckHas(cached, other) {
			t.Fatalf("sender's cached deck after the decline = %+v; want the decliner removed", cached)
		}
	}

	// One-directional: the decliner can still spark the sender, and no match
	// forms from the declined sender's interest.
	_, mid, err := svc.CreateSpark(ctx, decliner, sender, "prompt", "back", "")
	if err != nil {
		t.Fatalf("the decliner could not spark the sender: %v", err)
	}
	if mid != nil || openMatchesBetween(t, st, sender, decliner) != 0 {
		t.Fatalf("a match formed within the cooldown (match %v)", mid)
	}

	// After the cooldown everything behaves as before.
	ageDecline(t, pool, sp.ID, 31*24*time.Hour)
	if !deckHas(deck(), decliner) {
		t.Fatalf("decliner still off the sender's deck 31 days after the decline")
	}
	_, mid, err = svc.CreateSpark(ctx, sender, decliner, "prompt", "p2", "")
	if err != nil {
		t.Fatalf("spark 31 days after the decline: %v", err)
	}
	if mid == nil {
		t.Fatalf("after the cooldown, the sender's spark back to the decliner's spark did not form a match")
	}
}

func TestDeclineCooldown_NoMatchFromDeclinedSenderWithinCooldown(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)

	photo, _, err := svc.CreateSpark(ctx, a, b, "photo", "0", "")
	if err != nil {
		t.Fatalf("spark photo: %v", err)
	}
	if _, _, err := svc.CreateSpark(ctx, a, b, "prompt", "p1", ""); err != nil {
		t.Fatalf("spark prompt: %v", err)
	}
	if _, err := svc.DeclineSpark(ctx, photo.ID, b); err != nil {
		t.Fatalf("decline: %v", err)
	}
	// b declined one of a's sparks; a's other, undeclined spark must not let
	// b's spark form a match during the cooldown.
	_, mid, err := svc.CreateSpark(ctx, b, a, "photo", "0", "")
	if err != nil {
		t.Fatalf("decliner spark: %v", err)
	}
	if mid != nil {
		t.Fatalf("match %s formed from a declined sender's sparks within the cooldown", mid)
	}
	if n := openMatchesBetween(t, st, a, b); n != 0 {
		t.Fatalf("%d open matches within the cooldown", n)
	}
}

func TestDeclineCooldown_OverrideHonoured(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	pool := itPool(t)
	ctx := context.Background()
	st.SetDeclineCooldown(3 * 24 * time.Hour)

	check := func(ago time.Duration, wantRefused bool) {
		t.Helper()
		sender, decliner := uuid.New(), uuid.New()
		seedActiveProfile(t, st, sender)
		seedActiveProfile(t, st, decliner)
		sp, _, err := svc.CreateSpark(ctx, sender, decliner, "photo", "0", "")
		if err != nil {
			t.Fatalf("spark: %v", err)
		}
		if _, err := svc.DeclineSpark(ctx, sp.ID, decliner); err != nil {
			t.Fatalf("decline: %v", err)
		}
		ageDecline(t, pool, sp.ID, ago)
		_, _, err = svc.CreateSpark(ctx, sender, decliner, "prompt", "again", "")
		if refused := errors.Is(err, ErrCandidateUnavailable); refused != wantRefused || (!refused && err != nil) {
			t.Fatalf("3-day cooldown, declined %s ago: err=%v, want refused=%v", ago, err, wantRefused)
		}
	}
	check(2*24*time.Hour, true)
	check(5*24*time.Hour, false)
}
