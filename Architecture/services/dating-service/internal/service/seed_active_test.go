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
