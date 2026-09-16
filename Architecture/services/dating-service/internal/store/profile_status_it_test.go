// Lane D2 store integration tests: the guarded writer against a real
// database, the single-statement profile upsert, and the birth-date lock.
// Skipped unless TEST_PG_DSN is set; refuses a database not named *_test.
package store

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	storeSchemaOnce sync.Once
	storeSchemaErr  error
)

// ensureStoreSchemaForTest applies setup.sql once per test binary on a
// *_test database, so the lane D2 columns and dating_onboarding_step exist.
func ensureStoreSchemaForTest(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	if !strings.HasSuffix(pool.Config().ConnConfig.Database, "_test") {
		return
	}
	storeSchemaOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		storeSchemaErr = database.BootstrapSchema(ctx, pool)
	})
	if storeSchemaErr != nil {
		t.Fatalf("bootstrap schema: %v", storeSchemaErr)
	}
}

func statusITStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping profile status store integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	ensureStoreSchemaForTest(t, pool)
	return New(pool), func() { pool.Close() }
}

// seedOnboardingEvidence writes every piece of evidence the writer checks
// (basics, approved primary photo, passed selfie) without moving status.
func seedOnboardingEvidence(t *testing.T, s *Store, id uuid.UUID, gender string) {
	t.Helper()
	ctx := context.Background()
	ensureProfileForTest(t, s, id)
	if _, err := s.db.Exec(ctx, `
        UPDATE dating_profiles
        SET deleted_at = NULL, visible_to_public = true, birth_date = $2,
            gender = $3, city = 'Hyderabad', trust_tier = 'selfie', first_name = 'tester'
        WHERE user_id = $1`, id, time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC), gender); err != nil {
		t.Fatalf("seed basics: %v", err)
	}
	interested := "female"
	if gender == "female" {
		interested = "everyone"
	}
	if _, err := s.UpsertPreferences(ctx, id, UpsertPreferencesParams{InterestedInGender: &interested}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
	if _, err := s.db.Exec(ctx, `
        INSERT INTO dating_photos (user_id, media_id, sort_order, is_primary, visibility, moderation_status)
        VALUES ($1, $2, 0, true, 'public', 'approved')`, id, uuid.New()); err != nil {
		t.Fatalf("seed photo: %v", err)
	}
	if err := s.RecordSelfieAttempt(ctx, id, 0.99, "passed"); err != nil {
		t.Fatalf("seed selfie: %v", err)
	}
}

// driveTo moves a profile carrying full evidence (seedOnboardingEvidence)
// from draft to target through TransitionProfileStatus only.
func driveTo(t *testing.T, s *Store, id uuid.UUID, target string) *Profile {
	t.Helper()
	ctx := context.Background()
	steps := map[string][]ProfileEvent{
		ProfileStatusDraft:         nil,
		ProfileStatusPendingPhoto:  {ProfileEventBasicsComplete},
		ProfileStatusPendingSelfie: {ProfileEventBasicsComplete, ProfileEventPhotoApproved},
		ProfileStatusActive:        {ProfileEventBasicsComplete, ProfileEventPhotoApproved, ProfileEventSelfiePassed},
	}
	after := map[string]struct {
		ev    ProfileEvent
		actor ProfileActor
	}{
		ProfileStatusPaused:        {ProfileEventPause, ProfileActorUser},
		ProfileStatusPendingReview: {ProfileEventReview, ProfileActorAdmin},
		ProfileStatusRestricted:    {ProfileEventRestrict, ProfileActorAdmin},
		ProfileStatusSuspended:     {ProfileEventSuspend, ProfileActorAdmin},
		ProfileStatusDeleted:       {ProfileEventDelete, ProfileActorUser},
	}
	events, onboarding := steps[target]
	if !onboarding {
		events = steps[ProfileStatusActive]
	}
	var p *Profile
	var err error
	for _, ev := range events {
		if p, err = s.TransitionProfileStatus(ctx, id, ev, ProfileActorSystem); err != nil {
			t.Fatalf("drive %s: %s: %v", target, ev, err)
		}
	}
	if a, ok := after[target]; ok {
		if p, err = s.TransitionProfileStatus(ctx, id, a.ev, a.actor); err != nil {
			t.Fatalf("drive %s: %s: %v", target, a.ev, err)
		}
	}
	if p == nil {
		if p, err = s.GetProfile(ctx, id); err != nil {
			t.Fatalf("drive %s: get: %v", target, err)
		}
	}
	if p.ProfileStatus != target {
		t.Fatalf("drive %s: landed on %s", target, p.ProfileStatus)
	}
	return p
}

func profileState(p *Profile) ProfileStatusState {
	st := ProfileStatusState{Status: p.ProfileStatus, Paused: p.Paused}
	if p.PriorStatus != nil {
		st.Prior = *p.PriorStatus
	}
	return st
}

func TestTransitionProfileStatus_DB(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()

	user := uuid.New()
	seedOnboardingEvidence(t, s, user, "female")
	p := driveTo(t, s, user, ProfileStatusActive)

	walk := []struct {
		ev    ProfileEvent
		actor ProfileActor
		want  string
	}{
		{ProfileEventPause, ProfileActorUser, "paused<active+p"},
		{ProfileEventSuspend, ProfileActorAdmin, "suspended<active+p"},
		{ProfileEventUnpause, ProfileActorUser, "suspended<active"},
		{ProfileEventRestrict, ProfileActorAdmin, "restricted<active"},
		{ProfileEventReinstate, ProfileActorAdmin, "active"},
	}
	for _, step := range walk {
		if p, _ = s.TransitionProfileStatus(ctx, user, step.ev, step.actor); p == nil || profileState(p) != parseState(step.want) {
			t.Fatalf("%s by %s: got %v, want %s", step.ev, step.actor, p, step.want)
		}
	}

	// Refused edges leave the row exactly as it was.
	before := profileState(p)
	for _, bad := range []struct {
		ev    ProfileEvent
		actor ProfileActor
		want  error
	}{
		{ProfileEventReinstate, ProfileActorAdmin, ErrProfileTransitionNotAllowed},
		{ProfileEventSelfiePassed, ProfileActorSystem, ErrProfileTransitionNotAllowed},
		{ProfileEventSuspend, ProfileActorUser, ErrProfileTransitionActor},
	} {
		if _, err := s.TransitionProfileStatus(ctx, user, bad.ev, bad.actor); !errors.Is(err, bad.want) {
			t.Fatalf("%s by %s: err=%v, want %v", bad.ev, bad.actor, err, bad.want)
		}
		got, err := s.GetProfile(ctx, user)
		if err != nil || profileState(got) != before {
			t.Fatalf("%s refused but row changed to %v (err %v)", bad.ev, got, err)
		}
	}

	// Onboarding needs evidence: a bare draft cannot complete basics.
	bare := uuid.New()
	ensureProfileForTest(t, s, bare)
	if _, err := s.TransitionProfileStatus(ctx, bare, ProfileEventBasicsComplete, ProfileActorSystem); !errors.Is(err, ErrOnboardingIncomplete) {
		t.Fatalf("basics_complete on a bare profile: err=%v, want ErrOnboardingIncomplete", err)
	}
	if _, err := s.TransitionProfileStatus(ctx, uuid.New(), ProfileEventPause, ProfileActorUser); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("missing profile: err=%v, want ErrProfileNotFound", err)
	}

	// Soft delete goes through the writer, stamps deleted_at, is idempotent.
	for i := 0; i < 2; i++ {
		if err := s.SoftDeleteProfile(ctx, user); err != nil {
			t.Fatalf("soft delete #%d: %v", i+1, err)
		}
	}
	if _, err := s.GetProfile(ctx, user); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("soft-deleted profile still readable: %v", err)
	}
	if _, err := s.TransitionProfileStatus(ctx, user, ProfileEventUnpause, ProfileActorUser); !errors.Is(err, ErrProfileTransitionNotAllowed) {
		t.Fatalf("user unpause of deleted: err=%v, want refusal", err)
	}
}

// TestUpsertProfile_FailingFieldReturnsError: a field the database rejects
// fails the call, and no other field from the same call lands.
func TestUpsertProfile_FailingFieldReturnsError(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	user := uuid.New()
	before := "before"
	if _, err := s.UpsertProfile(ctx, user, UpsertProfileParams{Bio: &before}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	after := "after"
	tooTall := 1 << 40 // does not fit height_cm INT
	_, err := s.UpsertProfile(ctx, user, UpsertProfileParams{Bio: &after, HeightCm: &tooTall})
	if err == nil {
		t.Fatalf("expected an error for an out-of-range height_cm")
	}
	if errors.Is(err, ErrProfileNotFound) || !strings.Contains(err.Error(), "update dating profile") {
		t.Fatalf("want the database error from the update surfaced, got %v", err)
	}
	p, err := s.GetProfile(ctx, user)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if p.Bio != "before" || p.HeightCm != nil {
		t.Fatalf("failed upsert half-landed: bio=%q height=%v", p.Bio, p.HeightCm)
	}
	// UpsertProfile never writes birth_date, even when the client sends one.
	dob := time.Date(1999, 9, 9, 0, 0, 0, 0, time.UTC)
	if p, err = s.UpsertProfile(ctx, user, UpsertProfileParams{BirthDate: &dob}); err != nil || p.BirthDate != nil {
		t.Fatalf("UpsertProfile wrote birth_date=%v (err %v)", p.BirthDate, err)
	}
}

func TestSetProfileBirthDate_ClientLocksIdentityWins(t *testing.T) {
	s, cleanup := statusITStore(t)
	defer cleanup()
	ctx := context.Background()
	user := uuid.New()
	ensureProfileForTest(t, s, user)
	d := func(y int) time.Time { return time.Date(y, 1, 1, 0, 0, 0, 0, time.UTC) }

	check := func(label string, wantYear int, wantSource string) {
		t.Helper()
		p, err := s.GetProfile(ctx, user)
		if err != nil {
			t.Fatalf("%s: get: %v", label, err)
		}
		if p.BirthDate == nil || p.BirthDate.Year() != wantYear || p.DOBSource == nil || *p.DOBSource != wantSource {
			t.Fatalf("%s: birth_date=%v source=%v, want %d/%s", label, p.BirthDate, p.DOBSource, wantYear, wantSource)
		}
	}
	steps := []struct {
		label      string
		year       int
		source     string
		wantChange bool
		wantYear   int
		wantSource string
	}{
		{"first client value", 1995, BasicsSourceClient, true, 1995, BasicsSourceClient},
		{"second client value is ignored", 1990, BasicsSourceClient, false, 1995, BasicsSourceClient},
		{"identity replaces client", 1992, BasicsSourceIdentity, true, 1992, BasicsSourceIdentity},
		{"client never replaces identity", 1980, BasicsSourceClient, false, 1992, BasicsSourceIdentity},
		{"identity_profile replaces identity", 1993, BasicsSourceIdentityProfile, true, 1993, BasicsSourceIdentityProfile},
		{"client never replaces identity_profile", 1970, BasicsSourceClient, false, 1993, BasicsSourceIdentityProfile},
		{"identity_registration replaces identity_profile", 1994, BasicsSourceIdentityRegistration, true, 1994, BasicsSourceIdentityRegistration},
		{"same identity_registration value is a no-op", 1994, BasicsSourceIdentityRegistration, false, 1994, BasicsSourceIdentityRegistration},
		{"client never replaces identity_registration", 1971, BasicsSourceClient, false, 1994, BasicsSourceIdentityRegistration},
	}
	for _, st := range steps {
		changed, err := s.SetProfileBirthDate(ctx, user, d(st.year), st.source)
		if err != nil {
			t.Fatalf("%s: %v", st.label, err)
		}
		if changed != st.wantChange {
			t.Fatalf("%s: changed=%v, want %v", st.label, changed, st.wantChange)
		}
		check(st.label, st.wantYear, st.wantSource)
	}
}
