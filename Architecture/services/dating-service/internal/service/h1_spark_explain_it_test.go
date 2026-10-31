// Audit H1 follow-up (docs/harness/audit-2026-10-30-dating-backend.md): a
// spark or a Super Spark, like a pass, acts only on someone the sender was
// shown — a dealt card still visible, a daily pick, an open match, or someone
// whose spark they may see — and explain answers only for a dealt card, from
// the durable dealt record, never from a recomputed deck. Service flows on
// the test database (CI only; skipped without TEST_PG_DSN). The rule's
// branches without a database: actionable_rule_test.go.
package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

func h1SparkUsage(t *testing.T, st *store.Store, id uuid.UUID) (sparks, supers, balance int) {
	t.Helper()
	ctx := context.Background()
	sparks, _, err := st.SparkUsage(ctx, id)
	if err != nil {
		t.Fatalf("spark usage: %v", err)
	}
	supers, _, balance, err = st.SuperSparkUsage(ctx, id)
	if err != nil {
		t.Fatalf("super spark usage: %v", err)
	}
	return sparks, supers, balance
}

// A spark or a Super Spark to someone never dealt to the sender — active,
// adult, unblocked and deck-eligible — is the one 404, writes no spark and
// spends neither allowance nor a purchased Super Spark. Dealt, both go
// through, and a repeat stays free.
func TestH1Spark_OnlyToADealtCardAndARefusalSpendsNothing(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	svc.SetMechanicsConfig(MechanicsConfig{SuperSpark: true, SuperSparkDailyLimitFree: 1, SuperSparkDailyLimitPass: 1})
	ctx := context.Background()
	sender, stranger, dealt := uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{sender, stranger, dealt} {
		seedActiveProfile(t, st, id)
	}
	d8Exec(t, st, `INSERT INTO dating_super_spark_balances (user_id, balance) VALUES ($1, 2)
        ON CONFLICT (user_id) DO UPDATE SET balance = EXCLUDED.balance`, sender)

	if _, _, err := svc.CreateSpark(ctx, sender, stranger, "prompt", "h1", ""); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("spark to a never-dealt stranger: err=%v, want ErrCandidateUnavailable", err)
	}
	if _, _, err := svc.CreateSuperSpark(ctx, sender, stranger, "prompt", "h1", ""); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("Super Spark to a never-dealt stranger: err=%v, want ErrCandidateUnavailable", err)
	}
	if _, _, err := svc.CreateSparkFrom(ctx, "profile", false, sender, stranger, "prompt", "h1", ""); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("spark from the profile surface to a never-dealt stranger: err=%v, want ErrCandidateUnavailable", err)
	}
	if n := sparksBetween(t, st, sender, stranger); n != 0 {
		t.Fatalf("a refused spark left %d spark(s)", n)
	}
	if sparks, supers, balance := h1SparkUsage(t, st, sender); sparks != 0 || supers != 0 || balance != 2 {
		t.Fatalf("refused sparks spent: sparks=%d supers=%d balance=%d, want 0, 0, 2", sparks, supers, balance)
	}

	dealCards(t, svc, st, sender, dealt)
	if _, _, err := svc.CreateSpark(ctx, sender, dealt, "prompt", "h1", ""); err != nil {
		t.Fatalf("spark to a dealt card: %v", err)
	}
	if _, _, err := svc.CreateSpark(ctx, sender, dealt, "prompt", "h1", ""); err != nil {
		t.Fatalf("repeating the spark: %v", err)
	}
	if _, _, err := svc.CreateSuperSpark(ctx, sender, dealt, "photo", "0", ""); err != nil {
		t.Fatalf("Super Spark to a dealt card: %v", err)
	}
	if sparks, supers, balance := h1SparkUsage(t, st, sender); sparks != 2 || supers != 1 || balance != 2 {
		t.Fatalf("after two sparks and a repeat: sparks=%d supers=%d balance=%d, want 2, 1, 2", sparks, supers, balance)
	}
}

// A dealt card stops being sparkable when the person hides (visible_to_public
// off) or is held from discovery, or when the sender passed them — the shared
// visibility rule, as for a pass. The profile is still active and adult, so
// only the H1 rule refuses.
func TestH1Spark_DealtCardHiddenOrPassedSinceIsRefused(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	sender, hidden, held, passed := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	for _, id := range []uuid.UUID{sender, hidden, held, passed} {
		seedActiveProfile(t, st, id)
	}
	dealCards(t, svc, st, sender, hidden, held, passed)

	off := false
	if _, err := st.UpsertProfile(ctx, hidden, store.UpsertProfileParams{VisibleToPublic: &off}); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if err := st.UpsertAccountRisk(ctx, &store.AccountRisk{UserID: held, RiskLevel: store.RiskLevelHideFromDiscovery, Signals: map[string]any{}}); err != nil {
		t.Fatalf("hold from discovery: %v", err)
	}
	if _, err := svc.PassCandidate(ctx, sender, passed, ""); err != nil {
		t.Fatalf("pass: %v", err)
	}
	for name, id := range map[string]uuid.UUID{"hidden": hidden, "held from discovery": held, "passed": passed} {
		if _, _, err := svc.CreateSpark(ctx, sender, id, "prompt", "h1", ""); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("spark to a dealt card %s since: err=%v, want ErrCandidateUnavailable", name, err)
		}
	}
	if sparks, _, _ := h1SparkUsage(t, st, sender); sparks != 0 {
		t.Fatalf("refused sparks spent %d of the allowance", sparks)
	}
}

// Sparking back needs no deck: someone who sparked the sender may be sparked
// back (the match forms) — unless the liked-you gate keeps their spark from
// the sender, who then has to meet them in the deck. A daily pick is
// sparkable too.
func TestH1Spark_SparkBackAndPicks(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	seedActiveProfile(t, st, a)
	seedActiveProfile(t, st, b)
	dealCards(t, svc, st, a, b)
	if _, _, err := svc.CreateSpark(ctx, a, b, "prompt", "h1", ""); err != nil {
		t.Fatalf("a sparks b: %v", err)
	}
	if was, err := st.WasDealt(ctx, b, a); err != nil || was {
		t.Fatalf("b was dealt a (%v, %v); the spark back must not need it", was, err)
	}
	if _, mid, err := svc.CreateSpark(ctx, b, a, "prompt", "h1", ""); err != nil || mid == nil {
		t.Fatalf("b sparks back: match=%v err=%v, want a match", mid, err)
	}

	// Liked-you gate on: a locked recipient cannot spark back off the
	// incoming spark alone, and the refusal spends nothing.
	svc.SetMechanicsConfig(MechanicsConfig{LikedYouGate: true})
	c, d := uuid.New(), uuid.New()
	seedActiveProfile(t, st, c)
	seedActiveProfile(t, st, d)
	dealCards(t, svc, st, c, d)
	if _, _, err := svc.CreateSpark(ctx, c, d, "prompt", "h1", ""); err != nil {
		t.Fatalf("c sparks d: %v", err)
	}
	if _, _, err := svc.CreateSpark(ctx, d, c, "prompt", "h1", ""); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("locked d sparks back c, never dealt: err=%v, want ErrCandidateUnavailable", err)
	}
	if sparks, _, _ := h1SparkUsage(t, st, d); sparks != 0 {
		t.Fatalf("the refused spark back spent %d of the allowance", sparks)
	}
	dealCards(t, svc, st, d, c)
	if _, mid, err := svc.CreateSpark(ctx, d, c, "prompt", "h1", ""); err != nil || mid == nil {
		t.Fatalf("d sparks c from the deck: match=%v err=%v, want a match", mid, err)
	}

	// A daily pick (mechanic M7) is a card shown to the viewer.
	svc.SetMechanicsConfig(MechanicsConfig{Picks: true})
	viewer, pick := uuid.New(), uuid.New()
	seedActiveProfile(t, st, viewer)
	seedActiveProfile(t, st, pick)
	if err := st.SaveDailyPicks(ctx, viewer, time.Now().UTC(), []uuid.UUID{pick}); err != nil {
		t.Fatalf("save picks: %v", err)
	}
	if _, _, err := svc.CreateSparkFrom(ctx, "picks", false, viewer, pick, "prompt", "h1", ""); err != nil {
		t.Fatalf("spark a pick: %v", err)
	}
}

// Explain reads the dealt record: a deck-eligible card never dealt is
// refused (and explaining it does not deal it); a dealt card explains even
// when a recomputed deck would no longer hold it; a dealt card hidden since,
// or a viewer the deck now gates, is refused.
func TestH1Explain_DecidedByTheDealtRecord(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	viewer, ids, _ := d7Deck(t, st, 2)
	first, second := ids[0], ids[1]

	if _, err := svc.ExplainCandidate(ctx, viewer, first); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("explain a deck-eligible card never dealt: err=%v, want ErrCandidateUnavailable", err)
	}
	if was, err := st.WasDealt(ctx, viewer, first); err != nil || was {
		t.Fatalf("explaining dealt the card (%v, %v)", was, err)
	}
	if _, err := svc.PassCandidate(ctx, viewer, first, ""); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("pass after an explain only: err=%v, want ErrCandidateUnavailable", err)
	}

	deck, err := svc.GetPulseToday(ctx, viewer)
	if err != nil || d7Card(deck, first) == nil || d7Card(deck, second) == nil {
		t.Fatalf("deal: both seeded candidates want dealing (err %v)", err)
	}
	if _, err := svc.ExplainCandidate(ctx, viewer, first); err != nil {
		t.Fatalf("explain a dealt card: %v", err)
	}

	// The viewer narrows their age window: a recomputed deck drops both,
	// but the cards were dealt and stay explainable.
	maxAge := 20
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{MaxAge: &maxAge}); err != nil {
		t.Fatalf("narrow the age window: %v", err)
	}
	svc.InvalidatePulseCache(ctx, viewer)
	if fresh, err := svc.computePulseToday(ctx, viewer); err != nil || d7Card(fresh, first) != nil {
		t.Fatalf("the recomputed deck still holds the card (err %v); the test would prove nothing", err)
	}
	if _, err := svc.ExplainCandidate(ctx, viewer, first); err != nil {
		t.Fatalf("explain a dealt card a recomputed deck no longer holds: %v", err)
	}

	off := false
	if _, err := st.UpsertProfile(ctx, second, store.UpsertProfileParams{VisibleToPublic: &off}); err != nil {
		t.Fatalf("hide: %v", err)
	}
	if _, err := svc.ExplainCandidate(ctx, viewer, second); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("explain a dealt card hidden since: err=%v, want ErrCandidateUnavailable", err)
	}

	if err := st.UpsertAccountRisk(ctx, &store.AccountRisk{UserID: viewer, RiskLevel: store.RiskLevelHideFromDiscovery, Signals: map[string]any{}}); err != nil {
		t.Fatalf("gate the viewer: %v", err)
	}
	if _, err := svc.ExplainCandidate(ctx, viewer, first); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("explain by a viewer the deck gates: err=%v, want ErrCandidateUnavailable", err)
	}
}
