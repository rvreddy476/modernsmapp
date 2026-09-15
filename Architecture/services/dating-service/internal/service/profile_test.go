// Lane D2 profile lifecycle tests: the guarded status machine end to end,
// unpause, account-lifecycle hide, and identity-sourced basics.
//
// Skipped when TEST_PG_DSN is unset so `go test ./...` stays green on
// developer laptops without a Postgres at hand. The deck-cache test also
// needs REDIS_ADDR.
package service

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/purge"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type stubIdentityBasics struct {
	basics *IdentityBasics
	err    error
}

func (s *stubIdentityBasics) GetIdentityBasics(_ context.Context, _ uuid.UUID) (*IdentityBasics, error) {
	return s.basics, s.err
}

func mustGetProfile(t *testing.T, st *store.Store, id uuid.UUID) *store.Profile {
	t.Helper()
	p, err := st.GetProfile(context.Background(), id)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	return p
}

func sameDay(a *time.Time, b time.Time) bool {
	return a != nil && a.Format("2006-01-02") == b.Format("2006-01-02")
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

// TestProfileLifecycle_OnboardingToActive walks draft → pending_photo →
// pending_selfie → active through the service entry points and proves each
// step needs its evidence and cannot be skipped.
func TestProfileLifecycle_OnboardingToActive(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	user := uuid.New()

	if _, err := st.UpsertProfile(ctx, user, store.UpsertProfileParams{}); err != nil {
		t.Fatalf("seed bare profile: %v", err)
	}
	if p := mustGetProfile(t, st, user); p.ProfileStatus != store.ProfileStatusDraft {
		t.Fatalf("bare profile status = %s, want draft", p.ProfileStatus)
	}

	// Basics without interested_in: still draft, and the writer refuses.
	intent, gender, city, first := "casual", "female", "Hyderabad", "Asha"
	dob := time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC)
	p, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{
		Intent: &intent, Gender: &gender, City: &city, BirthDate: &dob, FirstName: &first,
	})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p.ProfileStatus != store.ProfileStatusDraft {
		t.Fatalf("status without interested_in = %s, want draft", p.ProfileStatus)
	}
	if _, err := st.TransitionProfileStatus(ctx, user, store.ProfileEventBasicsComplete, store.ProfileActorSystem); !errors.Is(err, store.ErrOnboardingIncomplete) {
		t.Fatalf("basics_complete without interested_in: err=%v, want ErrOnboardingIncomplete", err)
	}

	// interested_in completes the basics.
	interested := "male"
	if _, err := svc.UpsertPreferences(ctx, user, store.UpsertPreferencesParams{InterestedInGender: &interested}); err != nil {
		t.Fatalf("preferences: %v", err)
	}
	if p := mustGetProfile(t, st, user); p.ProfileStatus != store.ProfileStatusPendingPhoto {
		t.Fatalf("status after basics = %s, want pending_photo", p.ProfileStatus)
	}

	// The selfie step cannot be skipped to.
	if _, err := st.TransitionProfileStatus(ctx, user, store.ProfileEventSelfiePassed, store.ProfileActorSystem); !errors.Is(err, store.ErrProfileTransitionNotAllowed) {
		t.Fatalf("selfie_passed from pending_photo: err=%v, want ErrProfileTransitionNotAllowed", err)
	}

	// A pending photo is not evidence; a moderator approval is.
	photo, err := st.CreatePhoto(ctx, user, store.CreatePhotoParams{MediaID: uuid.New(), IsPrimary: true, Visibility: "public"})
	if err != nil {
		t.Fatalf("create photo: %v", err)
	}
	if _, err := st.TransitionProfileStatus(ctx, user, store.ProfileEventPhotoApproved, store.ProfileActorSystem); !errors.Is(err, store.ErrOnboardingIncomplete) {
		t.Fatalf("photo_approved with a pending photo: err=%v, want ErrOnboardingIncomplete", err)
	}
	if _, err := svc.SetPhotoModerationStatus(ctx, uuid.New(), photo.ID, "approved", ""); err != nil {
		t.Fatalf("approve photo: %v", err)
	}
	if p := mustGetProfile(t, st, user); p.ProfileStatus != store.ProfileStatusPendingSelfie {
		t.Fatalf("status after photo approval = %s, want pending_selfie", p.ProfileStatus)
	}

	if err := st.RecordSelfieAttempt(ctx, user, 0.99, "passed"); err != nil {
		t.Fatalf("record selfie: %v", err)
	}
	if p, err = svc.advanceOnboarding(ctx, user); err != nil {
		t.Fatalf("advance: %v", err)
	}
	if p.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("status after selfie = %s, want active", p.ProfileStatus)
	}

	// Moderator review blocks outbound actions; reinstate restores active.
	driveProfile(t, st, user, store.ProfileEventReview, store.ProfileActorAdmin)
	if err := svc.requireInteractiveProfile(ctx, user); !errors.Is(err, ErrProfilePendingReview) {
		t.Fatalf("expected pending_review gate, got %v", err)
	}
	if p := driveProfile(t, st, user, store.ProfileEventReinstate, store.ProfileActorAdmin); p.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("reinstate = %s, want active", p.ProfileStatus)
	}
}

// TestSetPaused_UnpauseRestoresStepAndKeepsHolds: unpausing a suspended or
// restricted profile keeps the hold; unpausing a draft/pending profile
// returns to that step.
func TestSetPaused_UnpauseRestoresStepAndKeepsHolds(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()

	holds := map[store.ProfileEvent]string{
		store.ProfileEventSuspend:  store.ProfileStatusSuspended,
		store.ProfileEventRestrict: store.ProfileStatusRestricted,
	}
	for ev, held := range holds {
		user := uuid.New()
		seedActiveProfile(t, st, user)
		driveProfile(t, st, user, ev, store.ProfileActorAdmin)
		for _, paused := range []bool{false, true, false} {
			p, err := svc.SetPaused(ctx, user, paused)
			if err != nil {
				t.Fatalf("%s: SetPaused(%v): %v", held, paused, err)
			}
			if p.ProfileStatus != held || p.Paused != paused {
				t.Fatalf("%s: after SetPaused(%v) status=%s paused=%v, want %s paused=%v", held, paused, p.ProfileStatus, p.Paused, held, paused)
			}
		}
		if err := svc.requireInteractiveProfile(ctx, user); err == nil {
			t.Fatalf("%s: unpause must not re-enable outbound actions", held)
		}
	}

	// draft
	draft := uuid.New()
	if _, err := st.UpsertProfile(ctx, draft, store.UpsertProfileParams{}); err != nil {
		t.Fatalf("seed draft: %v", err)
	}
	// pending_photo
	pendingPhoto := uuid.New()
	seedBasicsProfile(t, st, pendingPhoto)
	driveProfile(t, st, pendingPhoto, store.ProfileEventBasicsComplete, store.ProfileActorSystem)
	// active
	active := uuid.New()
	seedActiveProfile(t, st, active)

	for user, step := range map[uuid.UUID]string{
		draft:        store.ProfileStatusDraft,
		pendingPhoto: store.ProfileStatusPendingPhoto,
		active:       store.ProfileStatusActive,
	} {
		p, err := svc.SetPaused(ctx, user, true)
		if err != nil {
			t.Fatalf("%s: pause: %v", step, err)
		}
		if p.ProfileStatus != store.ProfileStatusPaused || deref(p.PriorStatus) != step || !p.Paused {
			t.Fatalf("%s: paused state = %s<%s paused=%v", step, p.ProfileStatus, deref(p.PriorStatus), p.Paused)
		}
		gateErr := svc.requireInteractiveProfile(ctx, user)
		if step == store.ProfileStatusActive && gateErr != nil {
			t.Fatalf("paused active profile may still spark: %v", gateErr)
		}
		if step != store.ProfileStatusActive && gateErr == nil {
			t.Fatalf("paused %s profile must not spark", step)
		}
		p, err = svc.SetPaused(ctx, user, false)
		if err != nil {
			t.Fatalf("%s: unpause: %v", step, err)
		}
		if p.ProfileStatus != step || p.Paused || p.PriorStatus != nil {
			t.Fatalf("%s: unpaused state = %s<%s paused=%v, want %s", step, p.ProfileStatus, deref(p.PriorStatus), p.Paused, step)
		}
	}
}

// TestAccountDeactivate_PausesAndClearsDeckCaches drives the account
// lifecycle adapter main.go wires (purge.NewEraser(svc, store)).
func TestAccountDeactivate_PausesAndClearsDeckCaches(t *testing.T) {
	_, st, cleanup := newSvcForTest(t)
	defer cleanup()
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		t.Skip("REDIS_ADDR not set; skipping deck-cache invalidation test")
	}
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer rdb.Close()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	svc := New(st, rdb)
	eraser := purge.NewEraser(svc, st)

	candidate, viewer := uuid.New(), uuid.New()
	seedActiveProfile(t, st, candidate)
	deckKey, memberKey := svc.cacheKey(viewer), deckMembershipKey(candidate)
	defer rdb.Del(ctx, deckKey, memberKey, svc.cacheKey(candidate))
	if err := rdb.Set(ctx, deckKey, `{"data":[]}`, time.Minute).Err(); err != nil {
		t.Fatalf("seed deck: %v", err)
	}
	if err := rdb.SAdd(ctx, memberKey, viewer.String()).Err(); err != nil {
		t.Fatalf("seed membership: %v", err)
	}

	for i := 0; i < 2; i++ { // second delivery is idempotent
		if err := eraser.SetUserHidden(ctx, candidate, true, purge.EventUserDeactivated); err != nil {
			t.Fatalf("deactivate #%d: %v", i+1, err)
		}
		p := mustGetProfile(t, st, candidate)
		if p.ProfileStatus != store.ProfileStatusPaused || deref(p.PriorStatus) != store.ProfileStatusActive || !p.Paused {
			t.Fatalf("deactivate #%d: state %s<%s paused=%v, want paused<active", i+1, p.ProfileStatus, deref(p.PriorStatus), p.Paused)
		}
	}
	if n := rdb.Exists(ctx, deckKey, memberKey).Val(); n != 0 {
		t.Fatalf("deactivate left %d deck cache keys; want the viewer deck and membership set gone", n)
	}

	if err := eraser.SetUserHidden(ctx, candidate, false, purge.EventUserReactivated); err != nil {
		t.Fatalf("reactivate: %v", err)
	}
	if p := mustGetProfile(t, st, candidate); p.ProfileStatus != store.ProfileStatusActive || p.Paused {
		t.Fatalf("reactivate: status=%s paused=%v, want active", p.ProfileStatus, p.Paused)
	}

	// Deactivate + reactivate never lifts a suspension.
	suspended := uuid.New()
	seedActiveProfile(t, st, suspended)
	driveProfile(t, st, suspended, store.ProfileEventSuspend, store.ProfileActorAdmin)
	if err := eraser.SetUserHidden(ctx, suspended, true, purge.EventUserDeactivated); err != nil {
		t.Fatalf("deactivate suspended: %v", err)
	}
	if err := eraser.SetUserHidden(ctx, suspended, false, purge.EventUserReactivated); err != nil {
		t.Fatalf("reactivate suspended: %v", err)
	}
	if p := mustGetProfile(t, st, suspended); p.ProfileStatus != store.ProfileStatusSuspended {
		t.Fatalf("reactivated suspended profile status = %s, want suspended", p.ProfileStatus)
	}

	// A user who never made a dating profile is a no-op.
	if err := eraser.SetUserHidden(ctx, uuid.New(), true, purge.EventUserDeactivated); err != nil {
		t.Fatalf("deactivate without profile: %v", err)
	}
}

// TestUpsertProfile_IdentityBasicsAreAuthoritative: with an identity client
// wired, identity's birth date and first name are stored and a client
// birth_date has no effect.
func TestUpsertProfile_IdentityBasicsAreAuthoritative(t *testing.T) {
	svc, _, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	idDOB := time.Date(1990, 5, 5, 0, 0, 0, 0, time.UTC)
	svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: &IdentityBasics{FirstName: " Meera ", BirthDate: idDOB}})

	user := uuid.New()
	intent := "serious"
	clientDOB := time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC)
	fake := "Fake"
	p, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{Intent: &intent, BirthDate: &clientDOB, FirstName: &fake})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	for i, other := range []time.Time{clientDOB, time.Date(1980, 2, 2, 0, 0, 0, 0, time.UTC)} {
		if i > 0 {
			if p, err = svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &other, FirstName: &fake}); err != nil {
				t.Fatalf("second upsert: %v", err)
			}
		}
		if !sameDay(p.BirthDate, idDOB) || deref(p.DOBSource) != store.BasicsSourceIdentity {
			t.Fatalf("upsert #%d: birth_date=%v source=%s, want identity %s", i+1, p.BirthDate, deref(p.DOBSource), idDOB.Format("2006-01-02"))
		}
		if deref(p.FirstName) != "Meera" || deref(p.FirstNameSource) != store.BasicsSourceIdentity {
			t.Fatalf("upsert #%d: first_name=%q source=%s, want identity Meera", i+1, deref(p.FirstName), deref(p.FirstNameSource))
		}
	}
}

// TestUpsertProfile_UnderageIdentityRefused: identity's DOB is the one that
// gates, even when the client sends an adult one; nothing is written.
func TestUpsertProfile_UnderageIdentityRefused(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	adultClient := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)
	now := time.Now()
	for name, idDOB := range map[string]time.Time{
		"seventeen":                  now.AddDate(-17, 0, 0),
		"eighteenth birthday tomorrow": time.Date(now.Year()-18, now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).AddDate(0, 0, 1),
	} {
		svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: &IdentityBasics{FirstName: "Kid", BirthDate: idDOB}})
		user := uuid.New()
		_, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &adultClient})
		if !errors.Is(err, ErrUnderage) {
			t.Fatalf("%s: err=%v, want ErrUnderage", name, err)
		}
		if _, err := st.GetProfile(ctx, user); !errors.Is(err, store.ErrProfileNotFound) {
			t.Fatalf("%s: a refused profile must not be written (get err=%v)", name, err)
		}
	}
}

// TestUpsertProfile_InterimClientDOBLocksAfterFirstSet covers the interim
// rule while no identity route exposes DOB.
func TestUpsertProfile_InterimClientDOBLocksAfterFirstSet(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()

	user := uuid.New()
	first := time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC)
	name := "Asha"
	p, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &first, FirstName: &name})
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if !sameDay(p.BirthDate, first) || deref(p.DOBSource) != store.BasicsSourceClient {
		t.Fatalf("first upsert: birth_date=%v source=%s, want client 1995-01-01", p.BirthDate, deref(p.DOBSource))
	}
	if deref(p.FirstName) != "Asha" || deref(p.FirstNameSource) != store.BasicsSourceClient {
		t.Fatalf("first upsert: first_name=%q source=%s", deref(p.FirstName), deref(p.FirstNameSource))
	}
	changed := time.Date(1985, 6, 6, 0, 0, 0, 0, time.UTC)
	if p, err = svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &changed}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if !sameDay(p.BirthDate, first) {
		t.Fatalf("client birth date changed to %v; it must stay locked at 1995-01-01", p.BirthDate)
	}

	minor := uuid.New()
	young := time.Now().AddDate(-17, 0, 0)
	if _, err := svc.UpsertProfile(ctx, minor, store.UpsertProfileParams{BirthDate: &young}); !errors.Is(err, ErrUnderage) {
		t.Fatalf("under-18 client birth date: err=%v, want ErrUnderage", err)
	}
	if _, err := st.GetProfile(ctx, minor); !errors.Is(err, store.ErrProfileNotFound) {
		t.Fatalf("refused minor profile must not be written (get err=%v)", err)
	}
}
