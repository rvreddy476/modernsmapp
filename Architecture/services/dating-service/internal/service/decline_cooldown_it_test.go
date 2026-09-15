// Decline cooldown: within it the declined sender cannot spark the decliner
// (the ordinary CANDIDATE_UNAVAILABLE), does not see them in the deck, and no
// match forms from their sparks; the decliner is unaffected; after it all is
// as before. The decliner sparking the sender after the decline lifts it; the
// declined spark itself never counts toward a match. Skipped without
// TEST_PG_DSN; cached-deck checks need REDIS_ADDR.
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

	// One-directional: the decliner can still spark the sender (which lifts
	// the cooldown), and the declined spark alone forms no match.
	_, mid, err := svc.CreateSpark(ctx, decliner, sender, "prompt", "back", "")
	if err != nil {
		t.Fatalf("the decliner could not spark the sender: %v", err)
	}
	if mid != nil || openMatchesBetween(t, st, sender, decliner) != 0 {
		t.Fatalf("a match formed from a declined spark (match %v)", mid)
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

// While a decline is unlifted, the declined sender's other, undeclined sparks
// do not count toward a match. Without lifting, the decliner reaches the
// mutual check only by repeating a spark made before the decline. A new spark
// lifts it, and then the sender's undeclined spark forms the match.
func TestDeclineCooldown_NoMatchUntilLiftedThenUndeclinedSparkCounts(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	pool := itPool(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New() // a sparks, b declines
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)

	// Seeded in the store so no match forms while setting up.
	early, err := st.CreateSpark(ctx, b, a, "photo", "0", "")
	if err != nil {
		t.Fatalf("early b spark: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE dating_sparks SET created_at = now() - INTERVAL '1 hour' WHERE id = $1`, early.ID); err != nil {
		t.Fatalf("age early spark: %v", err)
	}
	photo, err := st.CreateSpark(ctx, a, b, "photo", "0", "")
	if err != nil {
		t.Fatalf("spark photo: %v", err)
	}
	if _, err := st.CreateSpark(ctx, a, b, "prompt", "p1", ""); err != nil {
		t.Fatalf("spark prompt: %v", err)
	}
	if _, err := svc.DeclineSpark(ctx, photo.ID, b); err != nil {
		t.Fatalf("decline: %v", err)
	}
	_, mid, err := svc.CreateSpark(ctx, b, a, "photo", "0", "")
	if err != nil {
		t.Fatalf("decliner repeat spark: %v", err)
	}
	if mid != nil || openMatchesBetween(t, st, a, b) != 0 {
		t.Fatalf("match %v formed from a declined sender's sparks while the decline is unlifted", mid)
	}

	_, mid, err = svc.CreateSpark(ctx, b, a, "prompt", "new", "")
	if err != nil {
		t.Fatalf("decliner new spark: %v", err)
	}
	if mid == nil || openMatchesBetween(t, st, a, b) != 1 {
		t.Fatalf("after the lift, a's undeclined spark and b's new spark formed no match (match %v)", mid)
	}
}

// A declines B, B is refused; A sparks B (any item) and the cooldown lifts:
// A is back in B's fresh and cached deck, B's declined spark alone forms no
// match, and B's next spark succeeds and forms one.
func TestDeclineCooldown_LiftedWhenDeclinerSparksBack(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	gender := "dl-" + uuid.NewString()[:8]
	sender, decliner := uuid.New(), uuid.New()
	seedActiveProfile(t, st, sender)
	seedActiveProfile(t, st, decliner)
	if _, err := st.UpsertProfile(ctx, decliner, store.UpsertProfileParams{Gender: &gender}); err != nil {
		t.Fatalf("set gender: %v", err)
	}
	if _, err := st.UpsertPreferences(ctx, sender, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatalf("set preference: %v", err)
	}
	deck := func() *PulseResponse {
		t.Helper()
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
	if _, err := svc.DeclineSpark(ctx, sp.ID, decliner); err != nil {
		t.Fatalf("decline: %v", err)
	}
	if _, _, err := svc.CreateSpark(ctx, sender, decliner, "prompt", "p1", ""); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("sender spark after the decline: err=%v, want ErrCandidateUnavailable", err)
	}
	within := deck()
	if deckHas(within, decliner) {
		t.Fatalf("decliner in the sender's deck within the cooldown")
	}
	if svc.rdb != nil {
		svc.writePulseCache(ctx, sender, within)
	}

	_, mid, err := svc.CreateSpark(ctx, decliner, sender, "prompt", "hello", "")
	if err != nil {
		t.Fatalf("decliner spark: %v", err)
	}
	if mid != nil || openMatchesBetween(t, st, sender, decliner) != 0 {
		t.Fatalf("the sender's declined spark alone formed a match (match %v)", mid)
	}
	if svc.rdb != nil {
		if cached := svc.readPulseCache(ctx, sender); cached != nil {
			t.Fatalf("sender's cached deck survived the lift (%d cards)", len(cached.Data))
		}
	}
	if !deckHas(deck(), decliner) {
		t.Fatalf("decliner still off the sender's deck after the lift")
	}

	_, mid, err = svc.CreateSpark(ctx, sender, decliner, "prompt", "p1", "")
	if err != nil {
		t.Fatalf("sender spark after the lift: %v", err)
	}
	if mid == nil || openMatchesBetween(t, st, sender, decliner) != 1 {
		t.Fatalf("sender's spark after the lift formed no match (match %v)", mid)
	}
}

// A block after the lift still blocks everything, whichever side blocks.
func TestDeclineCooldown_BlockAfterLiftStillBlocks(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	for _, senderBlocks := range []bool{false, true} {
		gender := "dl-" + uuid.NewString()[:8]
		sender, decliner := uuid.New(), uuid.New()
		seedActiveProfile(t, st, sender)
		seedActiveProfile(t, st, decliner)
		if _, err := st.UpsertProfile(ctx, decliner, store.UpsertProfileParams{Gender: &gender}); err != nil {
			t.Fatalf("set gender: %v", err)
		}
		if _, err := st.UpsertPreferences(ctx, sender, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
			t.Fatalf("set preference: %v", err)
		}
		sp, _, err := svc.CreateSpark(ctx, sender, decliner, "photo", "0", "")
		if err != nil {
			t.Fatalf("spark: %v", err)
		}
		if _, err := svc.DeclineSpark(ctx, sp.ID, decliner); err != nil {
			t.Fatalf("decline: %v", err)
		}
		if _, _, err := svc.CreateSpark(ctx, decliner, sender, "prompt", "hello", ""); err != nil {
			t.Fatalf("lifting spark: %v", err)
		}
		if resp, err := svc.computePulseToday(ctx, sender); err != nil || !deckHas(resp, decliner) {
			t.Fatalf("lift did not restore the deck (err %v)", err)
		}

		blocker, blocked := decliner, sender
		if senderBlocks {
			blocker, blocked = sender, decliner
		}
		if err := svc.Block(ctx, blocker, blocked); err != nil {
			t.Fatalf("block: %v", err)
		}
		if _, _, err := svc.CreateSpark(ctx, sender, decliner, "prompt", "p1", ""); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("senderBlocks=%v: sender spark after block: err=%v, want ErrCandidateUnavailable", senderBlocks, err)
		}
		if _, _, err := svc.CreateSpark(ctx, decliner, sender, "prompt", "again", ""); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("senderBlocks=%v: decliner spark after block: err=%v, want ErrCandidateUnavailable", senderBlocks, err)
		}
		if resp, err := svc.computePulseToday(ctx, sender); err != nil || deckHas(resp, decliner) {
			t.Fatalf("senderBlocks=%v: decliner in the sender's deck after a block (err %v)", senderBlocks, err)
		}
		if n := openMatchesBetween(t, st, sender, decliner); n != 0 {
			t.Fatalf("senderBlocks=%v: %d open matches after a block", senderBlocks, n)
		}
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
