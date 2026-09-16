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
	intent, gender, city, first := "casual", "woman", "Hyderabad", "Asha"
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
	interested := "everyone"
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

// identityAnswer is a found identity with a birth date from source
// (registration | profile).
func identityAnswer(name string, dob time.Time, source string) *IdentityBasics {
	return &IdentityBasics{Found: true, FirstName: name, BirthDate: &dob, DOBSource: source}
}

// identityNoDOB is a found identity with dob_source=none.
func identityNoDOB(name string) *IdentityBasics {
	return &IdentityBasics{Found: true, FirstName: name, DOBSource: IdentityDOBSourceNone}
}

// TestUpsertProfile_IdentityBasicsAreAuthoritative: identity's registration
// birth date and first name are stored (identity_registration / identity) and
// a client birth_date or first_name has no effect.
func TestUpsertProfile_IdentityBasicsAreAuthoritative(t *testing.T) {
	svc, _, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	idDOB := time.Date(1990, 5, 5, 0, 0, 0, 0, time.UTC)
	svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: identityAnswer(" Meera ", idDOB, IdentityDOBSourceRegistration)})

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
		if !sameDay(p.BirthDate, idDOB) || deref(p.DOBSource) != store.BasicsSourceIdentityRegistration {
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
		svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: identityAnswer("Kid", idDOB, IdentityDOBSourceRegistration)})
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

// TestUpsertProfile_IdentityProfileDOBAcceptedAndLocked: with no registration
// birth date, identity's profile birth date is accepted (identity_profile)
// and neither a later client value nor a later "none" answer replaces it.
func TestUpsertProfile_IdentityProfileDOBAcceptedAndLocked(t *testing.T) {
	svc, _, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	idDOB := time.Date(1991, 3, 3, 0, 0, 0, 0, time.UTC)
	stub := &stubIdentityBasics{basics: identityAnswer("", idDOB, IdentityDOBSourceProfile)}
	svc.SetIdentityBasicsClient(stub)

	user := uuid.New()
	clientDOB := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	p, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &clientDOB})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if !sameDay(p.BirthDate, idDOB) || deref(p.DOBSource) != store.BasicsSourceIdentityProfile {
		t.Fatalf("birth_date=%v source=%s, want identity_profile 1991-03-03", p.BirthDate, deref(p.DOBSource))
	}

	later := time.Date(1985, 6, 6, 0, 0, 0, 0, time.UTC)
	for name, answer := range map[string]*stubIdentityBasics{
		"none":        {basics: identityNoDOB("")},
		"404":         {basics: &IdentityBasics{Found: false}},
		"unavailable": {err: ErrIdentityTransient},
	} {
		svc.SetIdentityBasicsClient(answer)
		if p, err = svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &later}); err != nil {
			t.Fatalf("%s: later upsert: %v", name, err)
		}
		if !sameDay(p.BirthDate, idDOB) || deref(p.DOBSource) != store.BasicsSourceIdentityProfile {
			t.Fatalf("%s: birth_date=%v source=%s, want the locked identity_profile 1991-03-03", name, p.BirthDate, deref(p.DOBSource))
		}
	}
}

// TestUpsertProfile_IdentityWithoutDOBUsesInterimClientRule: identity
// dob_source=none and identity 404 both fall back to the client lock-once rule.
func TestUpsertProfile_IdentityWithoutDOBUsesInterimClientRule(t *testing.T) {
	svc, _, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	for name, answer := range map[string]*IdentityBasics{
		"dob_source none": identityNoDOB(""),
		"404":             {Found: false},
	} {
		svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: answer})
		user := uuid.New()
		first := time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC)
		p, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &first})
		if err != nil {
			t.Fatalf("%s: first upsert: %v", name, err)
		}
		if !sameDay(p.BirthDate, first) || deref(p.DOBSource) != store.BasicsSourceClient {
			t.Fatalf("%s: birth_date=%v source=%s, want client 1995-01-01", name, p.BirthDate, deref(p.DOBSource))
		}
		changed := time.Date(1985, 6, 6, 0, 0, 0, 0, time.UTC)
		if p, err = svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &changed}); err != nil {
			t.Fatalf("%s: second upsert: %v", name, err)
		}
		if !sameDay(p.BirthDate, first) {
			t.Fatalf("%s: client birth date changed to %v; it must stay locked", name, p.BirthDate)
		}
		young := time.Now().AddDate(-17, 0, 0)
		if _, err := svc.UpsertProfile(ctx, uuid.New(), store.UpsertProfileParams{BirthDate: &young}); !errors.Is(err, ErrUnderage) {
			t.Fatalf("%s: under-18 client birth date: err=%v, want ErrUnderage", name, err)
		}
	}
}

// TestUpsertProfile_IdentityDown: with no birth date locked, an identity
// failure refuses the save (ErrIdentityUnavailable → 503) instead of trusting
// the client; once a birth date is locked, a later save proceeds without
// identity.
func TestUpsertProfile_IdentityDown(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	adult := time.Date(1990, 1, 1, 0, 0, 0, 0, time.UTC)

	for name, cause := range map[string]error{"transient": ErrIdentityTransient, "misconfigured": ErrIdentityMisconfigured} {
		svc.SetIdentityBasicsClient(&stubIdentityBasics{err: cause})
		user := uuid.New()
		if _, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &adult}); !errors.Is(err, ErrIdentityUnavailable) {
			t.Fatalf("%s: first create: err=%v, want ErrIdentityUnavailable", name, err)
		}
		if _, err := st.GetProfile(ctx, user); !errors.Is(err, store.ErrProfileNotFound) {
			t.Fatalf("%s: a refused first create must not write a profile (get err=%v)", name, err)
		}
	}

	// A row that exists but has no birth date yet is not locked: still 503.
	bare := uuid.New()
	if _, err := st.UpsertProfile(ctx, bare, store.UpsertProfileParams{}); err != nil {
		t.Fatalf("seed bare: %v", err)
	}
	svc.SetIdentityBasicsClient(&stubIdentityBasics{err: ErrIdentityTransient})
	if _, err := svc.UpsertProfile(ctx, bare, store.UpsertProfileParams{BirthDate: &adult}); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("bare row without a birth date: err=%v, want ErrIdentityUnavailable", err)
	}
	if p := mustGetProfile(t, st, bare); p.BirthDate != nil {
		t.Fatalf("bare row got a birth date while identity was down: %v", p.BirthDate)
	}

	// Locked profile: later update proceeds.
	user := uuid.New()
	svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: identityNoDOB("")})
	if _, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &adult}); err != nil {
		t.Fatalf("create: %v", err)
	}
	svc.SetIdentityBasicsClient(&stubIdentityBasics{err: ErrIdentityTransient})
	bio := "updated while identity is down"
	other := time.Date(1980, 1, 1, 0, 0, 0, 0, time.UTC)
	p, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{Bio: &bio, BirthDate: &other})
	if err != nil {
		t.Fatalf("update with identity down: %v", err)
	}
	if p.Bio != bio {
		t.Fatalf("bio=%q, want the update applied", p.Bio)
	}
	if !sameDay(p.BirthDate, adult) || deref(p.DOBSource) != store.BasicsSourceClient {
		t.Fatalf("birth_date=%v source=%s, want the locked client 1990-01-01", p.BirthDate, deref(p.DOBSource))
	}
}

// TestUpsertProfile_IdentityFirstNameWinsOverClient: identity's first name is
// stored even when identity has no birth date, and a later client first name
// never replaces it.
func TestUpsertProfile_IdentityFirstNameWinsOverClient(t *testing.T) {
	svc, _, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: identityNoDOB(" Priya ")})

	user := uuid.New()
	dob := time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC)
	fake := "Fake"
	p, err := svc.UpsertProfile(ctx, user, store.UpsertProfileParams{BirthDate: &dob, FirstName: &fake})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if deref(p.FirstName) != "Priya" || deref(p.FirstNameSource) != store.BasicsSourceIdentity {
		t.Fatalf("first_name=%q source=%s, want identity Priya", deref(p.FirstName), deref(p.FirstNameSource))
	}
	if deref(p.DOBSource) != store.BasicsSourceClient {
		t.Fatalf("dob_source=%s, want client (identity had no birth date)", deref(p.DOBSource))
	}

	svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: &IdentityBasics{Found: false}})
	other := "Other"
	if p, err = svc.UpsertProfile(ctx, user, store.UpsertProfileParams{FirstName: &other}); err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if deref(p.FirstName) != "Priya" || deref(p.FirstNameSource) != store.BasicsSourceIdentity {
		t.Fatalf("after client name: first_name=%q source=%s, want identity Priya kept", deref(p.FirstName), deref(p.FirstNameSource))
	}
}

// seedActiveClientDOBProfile builds an active profile whose 1995 birth date
// and first name were locked under the interim client rule.
func seedActiveClientDOBProfile(t *testing.T, st *store.Store, id uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	intent, gender, city := "casual", "female", "Hyderabad"
	if _, err := st.UpsertProfile(ctx, id, store.UpsertProfileParams{Intent: &intent, Gender: &gender, City: &city}); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	if _, err := st.SetProfileBirthDate(ctx, id, time.Date(1995, 1, 1, 0, 0, 0, 0, time.UTC), store.BasicsSourceClient); err != nil {
		t.Fatalf("seed birth date: %v", err)
	}
	if _, err := st.SetProfileFirstName(ctx, id, "Asha", store.BasicsSourceClient); err != nil {
		t.Fatalf("seed first name: %v", err)
	}
	interested := "everyone"
	if _, err := st.UpsertPreferences(ctx, id, store.UpsertPreferencesParams{InterestedInGender: &interested}); err != nil {
		t.Fatalf("seed preferences: %v", err)
	}
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
	for _, ev := range []store.ProfileEvent{store.ProfileEventBasicsComplete, store.ProfileEventPhotoApproved, store.ProfileEventSelfiePassed} {
		driveProfile(t, st, id, ev, store.ProfileActorSystem)
	}
	p := mustGetProfile(t, st, id)
	if p.ProfileStatus != store.ProfileStatusActive || deref(p.DOBSource) != store.BasicsSourceClient {
		t.Fatalf("seed: status=%s dob_source=%s, want active/client", p.ProfileStatus, deref(p.DOBSource))
	}
}

// TestUpsertProfile_LockedClientDOBUpgradedByIdentity: the next save of a
// profile with a locked client birth date takes identity's differing one; an
// under-18 identity birth date is recorded and the profile restricted through
// the status writer, without softening a suspension.
func TestUpsertProfile_LockedClientDOBUpgradedByIdentity(t *testing.T) {
	svc, st, cleanup := newSvcForTest(t)
	defer cleanup()
	ctx := context.Background()
	bio := "hello"

	// Adult, different: identity wins, status untouched.
	adult := uuid.New()
	seedActiveClientDOBProfile(t, st, adult)
	idDOB := time.Date(1993, 7, 7, 0, 0, 0, 0, time.UTC)
	svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: identityAnswer("", idDOB, IdentityDOBSourceRegistration)})
	p, err := svc.UpsertProfile(ctx, adult, store.UpsertProfileParams{Bio: &bio})
	if err != nil {
		t.Fatalf("adult upgrade: %v", err)
	}
	if !sameDay(p.BirthDate, idDOB) || deref(p.DOBSource) != store.BasicsSourceIdentityRegistration || p.ProfileStatus != store.ProfileStatusActive {
		t.Fatalf("adult upgrade: birth_date=%v source=%s status=%s, want identity_registration 1993-07-07 active",
			p.BirthDate, deref(p.DOBSource), p.ProfileStatus)
	}
	if deref(p.FirstName) != "Asha" || deref(p.FirstNameSource) != store.BasicsSourceClient {
		t.Fatalf("adult upgrade: an empty identity first name must keep the client one, got %q/%s", deref(p.FirstName), deref(p.FirstNameSource))
	}

	// Under 18: recorded, restricted, refused.
	minor := uuid.New()
	seedActiveClientDOBProfile(t, st, minor)
	minorDOB := time.Now().UTC().AddDate(-16, 0, 0)
	svc.SetIdentityBasicsClient(&stubIdentityBasics{basics: identityAnswer("Kid", minorDOB, IdentityDOBSourceRegistration)})
	for i := 1; i <= 2; i++ { // the second save is idempotent
		if _, err := svc.UpsertProfile(ctx, minor, store.UpsertProfileParams{Bio: &bio}); !errors.Is(err, ErrUnderage) {
			t.Fatalf("minor upgrade #%d: err=%v, want ErrUnderage", i, err)
		}
		p = mustGetProfile(t, st, minor)
		if p.ProfileStatus != store.ProfileStatusRestricted || deref(p.PriorStatus) != store.ProfileStatusActive {
			t.Fatalf("minor upgrade #%d: state %s<%s, want restricted<active", i, p.ProfileStatus, deref(p.PriorStatus))
		}
		if !sameDay(p.BirthDate, minorDOB) || deref(p.DOBSource) != store.BasicsSourceIdentityRegistration {
			t.Fatalf("minor upgrade #%d: birth_date=%v source=%s, want identity_registration minor date", i, p.BirthDate, deref(p.DOBSource))
		}
	}
	if err := svc.requireInteractiveProfile(ctx, minor); !errors.Is(err, ErrProfileRestricted) {
		t.Fatalf("restricted minor gate: err=%v, want ErrProfileRestricted", err)
	}
	if err := svc.requireAdult(ctx, minor); !errors.Is(err, ErrUnderage) {
		t.Fatalf("restricted minor adult gate: err=%v, want ErrUnderage", err)
	}

	// A suspension stays a suspension.
	suspended := uuid.New()
	seedActiveClientDOBProfile(t, st, suspended)
	driveProfile(t, st, suspended, store.ProfileEventSuspend, store.ProfileActorAdmin)
	if _, err := svc.UpsertProfile(ctx, suspended, store.UpsertProfileParams{Bio: &bio}); !errors.Is(err, ErrUnderage) {
		t.Fatalf("suspended minor: err=%v, want ErrUnderage", err)
	}
	if p := mustGetProfile(t, st, suspended); p.ProfileStatus != store.ProfileStatusSuspended {
		t.Fatalf("suspended minor status = %s, want suspended kept", p.ProfileStatus)
	}
}
