// Shared lane D2 seeds for the service integration tests: profiles reach
// 'active' only through store.TransitionProfileStatus, with the evidence the
// writer checks (basics incl. an 18+ birth date and first name, an approved
// primary photo, a passed selfie).
package service

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	schemaOnce sync.Once
	schemaErr  error
)

// ensureSchemaForTest applies setup.sql once per test binary so the lane D2
// columns and dating_onboarding_step exist. Only on a database whose name
// ends in _test: the schema's backfill rewrites rows.
func ensureSchemaForTest(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if !strings.HasSuffix(pool.Config().ConnConfig.Database, "_test") {
		return
	}
	schemaOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		schemaErr = database.BootstrapSchema(ctx, pool)
	})
	if schemaErr != nil {
		t.Fatalf("bootstrap schema: %v", schemaErr)
	}
}

// seedBasicsProfile writes the onboarding basics (intent, gender, city,
// identity-sourced 1995 birth date and first name, interested_in) but
// leaves the status at draft.
func seedBasicsProfile(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	intent, gender, city := "casual", "female", "Hyderabad"
	if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Intent: &intent, Gender: &gender, City: &city}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := st.SetProfileBirthDate(ctx, id, time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC), store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed birth date: %v", err)
	}
	if _, err := st.SetProfileFirstName(ctx, id, "Asha", store.BasicsSourceIdentity); err != nil {
		t.Fatalf("seed first name: %v", err)
	}
	interested := "everyone"
	if _, err := st.UpsertPreferences(ctx, id, store.UpsertPreferencesParams{InterestedInGender: &interested}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
}

// seedActiveProfile builds a valid active 18+ profile through the writer.
func seedActiveProfile(t *testing.T, st *store.Store, id uuid.UUID) *store.Profile {
	t.Helper()
	ctx := context.Background()
	seedBasicsProfile(t, st, id)
	photo, err := st.CreatePhoto(ctx, id, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true, Visibility: "public"})
	if err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	if _, err := st.SetPhotoModerationStatus(ctx, photo.ID, "approved", ""); err != nil {
		t.Fatalf("approve photo: %v", err)
	}
	if err := st.RecordSelfieAttempt(ctx, id, 0.99, "passed"); err != nil {
		t.Fatalf("seed selfie: %v", err)
	}
	var p *store.Profile
	for _, ev := range []store.ProfileEvent{store.ProfileEventBasicsComplete, store.ProfileEventPhotoApproved, store.ProfileEventSelfiePassed} {
		if p, err = st.TransitionProfileStatus(ctx, id, ev, store.ProfileActorSystem); err != nil {
			t.Fatalf("seed transition %s: %v", ev, err)
		}
	}
	if p.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("seeded profile status = %s, want active", p.ProfileStatus)
	}
	if p.BirthDate == nil || store.AgeOn(*p.BirthDate, time.Now()) < MinDatingAgeYears {
		t.Fatalf("seeded profile is not 18+: %v", p.BirthDate)
	}
	return p
}

// driveProfile applies one event through the writer or fails the test.
func driveProfile(t *testing.T, st *store.Store, id uuid.UUID, ev store.ProfileEvent, actor store.ProfileActor) *store.Profile {
	t.Helper()
	p, err := st.TransitionProfileStatus(context.Background(), id, ev, actor)
	if err != nil {
		t.Fatalf("transition %s by %s: %v", ev, actor, err)
	}
	return p
}

// dealCards deals each candidate to viewer the way the app does — the
// viewer's own deck fetch (GetPulseToday), which records the card as dealt —
// and fails unless the candidate was in that deck. Audit H1: a pass, a stash,
// a spark, a Super Spark and explain act only on a card the viewer was dealt
// (or a pick, a match, an incoming spark), so a test sparks someone it dealt.
//
// To make each candidate the one card in the deck it gives the candidate a
// gender nobody else holds and points the viewer's preference at it, one
// candidate at a time (the pilot deck keeps at most three cards of one
// intent), dropping any cached deck first. Afterwards both genders and the
// viewer's preference are put back (an unset preference as "everyone", which
// the gender rule treats the same) and the cached deck is dropped again, so
// the test continues from the state it built.
func dealCards(t *testing.T, svc *Service, st *store.Store, viewer uuid.UUID, candidates ...uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	prefs, err := st.GetPreferences(ctx, viewer)
	if err != nil {
		t.Fatalf("deal: viewer preferences: %v", err)
	}
	wants := InterestedInEveryone
	if prefs.InterestedInGender != nil {
		wants = *prefs.InterestedInGender
	}
	for _, c := range candidates {
		cp, err := st.GetProfile(ctx, c)
		if err != nil {
			t.Fatalf("deal: candidate %s: %v", c, err)
		}
		gender := "deal-" + uuid.NewString()[:8]
		if _, err := st.UpsertProfile(ctx, c, store.UpsertProfileParams{Gender: &gender}); err != nil {
			t.Fatalf("deal: candidate gender: %v", err)
		}
		if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
			t.Fatalf("deal: viewer preference: %v", err)
		}
		svc.InvalidatePulseCache(ctx, viewer)
		deck, err := svc.GetPulseToday(ctx, viewer)
		if err != nil {
			t.Fatalf("deal: deck: %v", err)
		}
		dealt := false
		for _, card := range deck.Data {
			dealt = dealt || card.CandidateID == c
		}
		if !dealt {
			t.Fatalf("deal: candidate %s was not in the viewer's deck (%d cards)", c, len(deck.Data))
		}
		if cp.Gender != nil {
			if _, err := st.UpsertProfile(ctx, c, store.UpsertProfileParams{Gender: cp.Gender}); err != nil {
				t.Fatalf("deal: restore candidate gender: %v", err)
			}
		}
	}
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &wants}); err != nil {
		t.Fatalf("deal: restore viewer preference: %v", err)
	}
	svc.InvalidatePulseCache(ctx, viewer)
}
