package service

// Going-live eligibility (eligibility.go): the access mode, every
// requirement as met / not met / unknown, the OR of activity, the pilot list
// in open mode, what create, start and ingress answer, the fact cache, and
// the new-streamer viewer cap.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// fakeFacts is every fact source at once. The zero value answers "not
// verified, no date of birth, no account, nothing posted, nobody follows".
type fakeFacts struct {
	mu sync.Mutex

	email    bool
	emailErr error

	phone    bool
	phoneErr error

	dob           *time.Time
	identityFound bool
	identityErr   error

	account    AccountInfo
	accountErr error

	posts        int
	postsErr     error
	followers    int
	followersErr error

	calls map[string]int
}

func (f *fakeFacts) hit(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[name]++
}

func (f *fakeFacts) total() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		n += c
	}
	return n
}

func (f *fakeFacts) EmailVerified(context.Context, uuid.UUID) (bool, error) {
	f.hit("email")
	return f.email, f.emailErr
}

func (f *fakeFacts) PhoneVerified(context.Context, uuid.UUID) (bool, error) {
	f.hit("phone")
	return f.phone, f.phoneErr
}

func (f *fakeFacts) BirthDate(context.Context, uuid.UUID) (*time.Time, bool, error) {
	f.hit("identity")
	return f.dob, f.identityFound, f.identityErr
}

func (f *fakeFacts) Account(context.Context, uuid.UUID) (AccountInfo, error) {
	f.hit("account")
	return f.account, f.accountErr
}

func (f *fakeFacts) PostCount(context.Context, uuid.UUID) (int, error) {
	f.hit("posts")
	return f.posts, f.postsErr
}

func (f *fakeFacts) FollowerCount(context.Context, uuid.UUID) (int, error) {
	f.hit("followers")
	return f.followers, f.followersErr
}

var errFactDown = errors.New("the service is down")

// eligRig is the rig in `mode` with the default requirements (email on,
// phone off) and one user's facts, all of them good: a verified email, a
// verified phone, born in 1990, an active account made 30 days ago, 5 posts
// and no followers.
type eligRig struct {
	*rig
	facts *fakeFacts
}

func newEligRig(mode string, pilot ...uuid.UUID) *eligRig {
	r := newRig(pilot...)
	r.svc.now = r.clock.Now
	dob := time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC)
	f := &fakeFacts{
		email: true, phone: true, dob: &dob, identityFound: true,
		account: AccountInfo{Found: true, Active: true, CreatedAt: r.clock.Now().Add(-30 * 24 * time.Hour)},
		posts:   5,
	}
	r.svc.elig = EligibilityConfig{
		Mode: mode, RequireEmail: DefaultEligRequireEmail, RequirePhone: DefaultEligRequirePhone,
		MinAccountAge: DefaultEligMinAccountAge,
		MinPosts:      DefaultEligMinPosts, MinFollowers: DefaultEligMinFollowers,
		Emails: f, Phones: f, BirthDates: f, Accounts: f, Posts: f, Followers: f,
	}
	return &eligRig{rig: r, facts: f}
}

// expire steps past the fact cache.
func (e *eligRig) expire() { e.clock.Advance(eligFactTTL + time.Second) }

func reqByKey(t *testing.T, reqs []Requirement, key string) Requirement {
	t.Helper()
	for _, r := range reqs {
		if r.Key == key {
			return r
		}
	}
	t.Fatalf("no %q requirement in %+v", key, reqs)
	return Requirement{}
}

func metText(b *bool) string {
	switch {
	case b == nil:
		return "null"
	case *b:
		return "true"
	}
	return "false"
}

func keysOf(reqs []Requirement) string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Key+"="+metText(r.Met))
	}
	return strings.Join(out, ",")
}

func TestParseAccessMode(t *testing.T) {
	for raw, want := range map[string]string{"": "pilot", "pilot": "pilot", " Pilot ": "pilot", "open": "open", " OPEN": "open"} {
		got, err := ParseAccessMode(raw)
		if err != nil || got != want {
			t.Fatalf("ParseAccessMode(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	// A value that is neither refuses to boot: it must not fall back to
	// either mode.
	for _, raw := range []string{"everyone", "opne", "true", "pilot,open", "0"} {
		if got, err := ParseAccessMode(raw); err == nil {
			t.Fatalf("ParseAccessMode(%q) = %q; want an error", raw, got)
		}
	}
}

func TestEligibilityConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	// Nothing set: pilot mode and the pinned defaults.
	cfg, err := EligibilityConfigFromEnv(env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != AccessModePilot || !cfg.RequireEmail || cfg.RequirePhone || cfg.MinAccountAge != 168*time.Hour ||
		cfg.MinPosts != 3 || cfg.MinFollowers != 10 || cfg.NewStreamerStreams != 3 || cfg.NewStreamerViewerCap != 200 {
		t.Fatalf("defaults: %+v", cfg)
	}

	cfg, err = EligibilityConfigFromEnv(env(map[string]string{
		"LIVE_ACCESS_MODE": "open", "LIVE_ELIG_REQUIRE_EMAIL": "false", "LIVE_ELIG_REQUIRE_PHONE": "true",
		"LIVE_ELIG_MIN_ACCOUNT_AGE": "36h", "LIVE_ELIG_MIN_POSTS": "1", "LIVE_ELIG_MIN_FOLLOWERS": "0",
		"LIVE_NEW_STREAMER_STREAMS": "5", "LIVE_NEW_STREAMER_VIEWER_CAP": "0",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Mode != AccessModeOpen || cfg.RequireEmail || !cfg.RequirePhone || cfg.MinAccountAge != 36*time.Hour ||
		cfg.MinPosts != 1 || cfg.MinFollowers != 0 || cfg.NewStreamerStreams != 5 || cfg.NewStreamerViewerCap != 0 {
		t.Fatalf("overrides: %+v", cfg)
	}

	// Anything unreadable refuses to boot rather than taking a default.
	for _, bad := range []map[string]string{
		{"LIVE_ACCESS_MODE": "public"},
		{"LIVE_ELIG_REQUIRE_EMAIL": "maybe"},
		{"LIVE_ELIG_REQUIRE_EMAIL": "2"},
		{"LIVE_ELIG_REQUIRE_PHONE": "maybe"},
		{"LIVE_ELIG_MIN_ACCOUNT_AGE": "7 days"},
		{"LIVE_ELIG_MIN_ACCOUNT_AGE": "-1h"},
		{"LIVE_ELIG_MIN_POSTS": "three"},
		{"LIVE_ELIG_MIN_FOLLOWERS": "-1"},
		{"LIVE_NEW_STREAMER_STREAMS": "x"},
		{"LIVE_NEW_STREAMER_VIEWER_CAP": "2.5"},
	} {
		if got, err := EligibilityConfigFromEnv(env(bad)); err == nil {
			t.Fatalf("%v parsed as %+v; want an error", bad, got)
		}
	}
}

// TestModeDefaultsToPilot: a service built without eligibility settings is
// in pilot mode, with no viewer cap.
func TestModeDefaultsToPilot(t *testing.T) {
	pilot, stranger := uuid.New(), uuid.New()
	r := newRig(pilot)
	got, err := r.svc.Eligibility(ctx, stranger)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != AccessModePilot || got.Eligible || !got.PilotOnly || got.ViewerCap != nil {
		t.Fatalf("stranger: %+v", got)
	}
	if _, err := r.svc.CreateStream(ctx, stranger, CreateStreamParams{Title: "x"}); !errors.Is(err, ErrLiveNotEnabled) {
		t.Fatalf("create by a stranger: %v", err)
	}
	got, _ = r.svc.Eligibility(ctx, pilot)
	if !got.Eligible || got.PilotOnly {
		t.Fatalf("pilot: %+v", got)
	}
}

// TestRequirementStates: each requirement true, false and unknown, what the
// eligibility read shows and what create answers.
func TestRequirementStates(t *testing.T) {
	yes, no := true, false
	var unknown *bool
	type want struct {
		key      string
		met      *bool
		eligible bool
		// createErr: nil = created; otherwise errors.Is target.
		createErr error
	}
	at := newFakeClock().Now() // 2026-10-01 12:00 UTC
	day := func(y int, m time.Month, d int) *time.Time {
		v := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
		return &v
	}
	cases := []struct {
		name string
		mut  func(e *eligRig)
		want want
	}{
		{"everything met", func(*eligRig) {}, want{ReqGoodStanding, &yes, true, nil}},

		{"email verified", func(*eligRig) {}, want{ReqEmailVerified, &yes, true, nil}},
		// "No email on file" is this same answer from the source: false, not
		// unknown (TestHTTPEmails).
		{"email not verified", func(e *eligRig) { e.facts.email = false }, want{ReqEmailVerified, &no, false, ErrLiveNotEligible}},
		{"email lookup fails", func(e *eligRig) { e.facts.emailErr = errFactDown }, want{ReqEmailVerified, unknown, false, ErrAuthorityUnavailable}},
		{"email has no source", func(e *eligRig) { e.svc.elig.Emails = nil }, want{ReqEmailVerified, unknown, false, ErrAuthorityUnavailable}},

		// phone_verified is off by default; switched on it works as before.
		{"phone verified", func(e *eligRig) { e.svc.elig.RequirePhone = true }, want{ReqPhoneVerified, &yes, true, nil}},
		{"phone not verified", func(e *eligRig) { e.svc.elig.RequirePhone = true; e.facts.phone = false }, want{ReqPhoneVerified, &no, false, ErrLiveNotEligible}},
		{"phone lookup fails", func(e *eligRig) { e.svc.elig.RequirePhone = true; e.facts.phoneErr = errFactDown }, want{ReqPhoneVerified, unknown, false, ErrAuthorityUnavailable}},
		{"phone has no source", func(e *eligRig) { e.svc.elig.RequirePhone = true; e.svc.elig.Phones = nil }, want{ReqPhoneVerified, unknown, false, ErrAuthorityUnavailable}},

		{"adult", func(*eligRig) {}, want{ReqAdult, &yes, true, nil}},
		{"18 today", func(e *eligRig) { e.facts.dob = day(at.Year()-18, at.Month(), at.Day()) }, want{ReqAdult, &yes, true, nil}},
		{"18 tomorrow", func(e *eligRig) { e.facts.dob = day(at.Year()-18, at.Month(), at.Day()+1) }, want{ReqAdult, &no, false, ErrLiveNotEligible}},
		{"no date of birth", func(e *eligRig) { e.facts.dob = nil }, want{ReqAdult, &no, false, ErrLiveNotEligible}},
		{"identity lookup fails", func(e *eligRig) { e.facts.identityErr = errFactDown }, want{ReqAdult, unknown, false, ErrAuthorityUnavailable}},
		{"identity has no source", func(e *eligRig) { e.svc.elig.BirthDates = nil }, want{ReqAdult, unknown, false, ErrAuthorityUnavailable}},

		{"account old enough", func(*eligRig) {}, want{ReqAccountAge, &yes, true, nil}},
		{"account exactly 7 days", func(e *eligRig) { e.facts.account.CreatedAt = at.Add(-168 * time.Hour) }, want{ReqAccountAge, &yes, true, nil}},
		{"account a second short", func(e *eligRig) { e.facts.account.CreatedAt = at.Add(-168*time.Hour + time.Second) }, want{ReqAccountAge, &no, false, ErrLiveNotEligible}},
		{"account lookup fails", func(e *eligRig) { e.facts.accountErr = errFactDown }, want{ReqAccountAge, unknown, false, ErrAuthorityUnavailable}},
		{"account has no source", func(e *eligRig) { e.svc.elig.Accounts = nil }, want{ReqAccountAge, unknown, false, ErrAuthorityUnavailable}},

		{"active by posts", func(*eligRig) {}, want{ReqActivity, &yes, true, nil}},
		{"not active", func(e *eligRig) { e.facts.posts = 2 }, want{ReqActivity, &no, false, ErrLiveNotEligible}},
		{"activity lookups fail", func(e *eligRig) { e.facts.postsErr, e.facts.followersErr = errFactDown, errFactDown }, want{ReqActivity, unknown, false, ErrAuthorityUnavailable}},

		{"good standing", func(*eligRig) {}, want{ReqGoodStanding, &yes, true, nil}},
		{"account not active", func(e *eligRig) { e.facts.account.Active = false }, want{ReqGoodStanding, &no, false, ErrLiveNotEligible}},
		{"no such account", func(e *eligRig) { e.facts.account = AccountInfo{} }, want{ReqGoodStanding, &no, false, ErrLiveNotEligible}},
		{"identity says the account is shut", func(e *eligRig) { e.facts.identityFound, e.facts.dob = false, nil }, want{ReqGoodStanding, &no, false, ErrLiveNotEligible}},
		{"standing unknown without the account", func(e *eligRig) { e.facts.accountErr = errFactDown }, want{ReqGoodStanding, unknown, false, ErrAuthorityUnavailable}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEligRig(AccessModeOpen)
			user := uuid.New()
			tc.mut(e)
			got, err := e.svc.Eligibility(ctx, user)
			if err != nil {
				t.Fatal(err)
			}
			if got.Mode != AccessModeOpen || got.PilotOnly {
				t.Fatalf("mode: %+v", got)
			}
			wantRows := 5
			if e.svc.elig.RequirePhone {
				wantRows = 6
			}
			if len(got.Requirements) != wantRows {
				t.Fatalf("requirements: %s", keysOf(got.Requirements))
			}
			if r := reqByKey(t, got.Requirements, tc.want.key); metText(r.Met) != metText(tc.want.met) {
				t.Fatalf("%s met = %s, want %s (%s)", tc.want.key, metText(r.Met), metText(tc.want.met), keysOf(got.Requirements))
			}
			if got.Eligible != tc.want.eligible {
				t.Fatalf("eligible = %v, want %v (%s)", got.Eligible, tc.want.eligible, keysOf(got.Requirements))
			}
			_, err = e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x"})
			switch {
			case tc.want.createErr == nil && err != nil:
				t.Fatalf("create: %v", err)
			case tc.want.createErr != nil && !errors.Is(err, tc.want.createErr):
				t.Fatalf("create: %v, want %v", err, tc.want.createErr)
			}
			if tc.want.createErr != nil && len(e.store.Streams) != 0 {
				t.Fatalf("a refused create stored a stream")
			}
		})
	}
}

// TestRequirementShapes: what each row carries beside key and met.
func TestRequirementShapes(t *testing.T) {
	e := newEligRig(AccessModeOpen)
	user := uuid.New()
	e.facts.account.CreatedAt = e.clock.Now().Add(-(2*24*time.Hour + 5*time.Hour))
	e.facts.posts, e.facts.followers = 1, 4
	got, _ := e.svc.Eligibility(ctx, user)

	if keys := keysOf(got.Requirements); keys != "email_verified=true,adult=true,account_age=false,activity=false,good_standing=true" {
		t.Fatalf("order and states: %s", keys)
	}
	age := reqByKey(t, got.Requirements, ReqAccountAge)
	if age.Current == nil || *age.Current != 2 || age.Needed == nil || *age.Needed != 7 || age.Unit != "days" {
		t.Fatalf("account_age: %+v", age)
	}
	act := reqByKey(t, got.Requirements, ReqActivity)
	if act.Posts == nil || act.Posts.Current == nil || *act.Posts.Current != 1 || act.Posts.Needed != 3 ||
		act.Followers == nil || act.Followers.Current == nil || *act.Followers.Current != 4 || act.Followers.Needed != 10 {
		t.Fatalf("activity: posts %+v followers %+v", act.Posts, act.Followers)
	}
	for _, key := range []string{ReqEmailVerified, ReqAdult, ReqGoodStanding} {
		r := reqByKey(t, got.Requirements, key)
		if r.Current != nil || r.Needed != nil || r.Unit != "" || r.Posts != nil || r.Followers != nil {
			t.Fatalf("%s carries progress: %+v", key, r)
		}
	}

	// Unknown: the target stays, the current value is left off.
	e.expire()
	e.facts.accountErr, e.facts.postsErr = errFactDown, errFactDown
	got, _ = e.svc.Eligibility(ctx, user)
	age = reqByKey(t, got.Requirements, ReqAccountAge)
	if age.Met != nil || age.Current != nil || age.Needed == nil || *age.Needed != 7 {
		t.Fatalf("unknown account_age: %+v", age)
	}
	act = reqByKey(t, got.Requirements, ReqActivity)
	if act.Met != nil || act.Posts.Current != nil || act.Posts.Needed != 3 || *act.Followers.Current != 4 {
		t.Fatalf("activity with posts unknown: met=%s posts %+v followers %+v", metText(act.Met), act.Posts, act.Followers)
	}

	// A required age that is not whole days is counted in hours.
	e2 := newEligRig(AccessModeOpen)
	e2.svc.elig.MinAccountAge = 36 * time.Hour
	e2.facts.account.CreatedAt = e2.clock.Now().Add(-10*time.Hour - 30*time.Minute)
	got, _ = e2.svc.Eligibility(ctx, user)
	age = reqByKey(t, got.Requirements, ReqAccountAge)
	if *age.Current != 10 || *age.Needed != 36 || age.Unit != "hours" || *age.Met {
		t.Fatalf("hours: %+v", age)
	}
}

// TestRequirementsSwitchedOff: a requirement that is configured away is not
// listed and not read.
func TestRequirementsSwitchedOff(t *testing.T) {
	e := newEligRig(AccessModeOpen)
	user := uuid.New()
	e.svc.elig.RequireEmail, e.svc.elig.RequirePhone = false, false
	e.svc.elig.MinAccountAge = 0
	e.svc.elig.MinPosts, e.svc.elig.MinFollowers = 0, 0
	e.facts.email, e.facts.phone, e.facts.posts = false, false, 0
	e.facts.account.CreatedAt = e.clock.Now()
	got, _ := e.svc.Eligibility(ctx, user)
	if keys := keysOf(got.Requirements); keys != "adult=true,good_standing=true" || !got.Eligible {
		t.Fatalf("with the optional requirements off: %s eligible=%v", keys, got.Eligible)
	}
	if e.facts.calls["email"]+e.facts.calls["phone"]+e.facts.calls["posts"]+e.facts.calls["followers"] != 0 {
		t.Fatalf("facts nobody needs were read: %v", e.facts.calls)
	}
	// adult cannot be switched off.
	e.expire()
	e.facts.dob = nil
	if _, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x"}); !errors.Is(err, ErrLiveNotEligible) {
		t.Fatalf("create without a date of birth: %v", err)
	}

	// One alternative off: only the other is offered, and it alone decides.
	e2 := newEligRig(AccessModeOpen)
	e2.svc.elig.MinFollowers = 0
	e2.facts.posts, e2.facts.followers = 2, 5000
	got, _ = e2.svc.Eligibility(ctx, user)
	act := reqByKey(t, got.Requirements, ReqActivity)
	if act.Followers != nil || act.Posts == nil || act.Met == nil || *act.Met {
		t.Fatalf("followers switched off: %+v", act)
	}
}

// TestPhoneIsOffByDefault: with the settings as shipped phone_verified is not
// listed, not read and decides nothing, and email_verified is first.
// Switched on, phone_verified sits right after email_verified and gates as
// it did. Both modes.
func TestPhoneIsOffByDefault(t *testing.T) {
	cfg, err := EligibilityConfigFromEnv(func(string) string { return "" })
	if err != nil || !cfg.RequireEmail || cfg.RequirePhone {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	state := func(reqs []Requirement, key string) string {
		for _, r := range reqs {
			if r.Key == key {
				return metText(r.Met)
			}
		}
		return "absent"
	}
	for _, mode := range []string{AccessModeOpen, AccessModePilot} {
		e := newEligRig(mode)
		e.svc.elig.RequireEmail, e.svc.elig.RequirePhone = cfg.RequireEmail, cfg.RequirePhone
		user := uuid.New()
		open := mode == AccessModeOpen
		// Nobody has a verified phone, and there is no source for one.
		e.facts.phone, e.svc.elig.Phones = false, nil
		got, _ := e.svc.Eligibility(ctx, user)
		if keys := keysOf(got.Requirements); keys != "email_verified=true,adult=true,account_age=true,activity=true,good_standing=true" {
			t.Fatalf("%s, defaults: %s", mode, keys)
		}
		if got.Eligible != open {
			t.Fatalf("%s, defaults: eligible=%v", mode, got.Eligible)
		}
		if e.facts.calls["phone"] != 0 || e.facts.calls["email"] != 1 {
			t.Fatalf("%s, defaults: reads %v", mode, e.facts.calls)
		}

		// Phone on: listed second; without a source it is unknown.
		e.svc.elig.RequirePhone = true
		got, _ = e.svc.Eligibility(ctx, user)
		if keys := keysOf(got.Requirements); keys != "email_verified=true,phone_verified=null,adult=true,account_age=true,activity=true,good_standing=true" {
			t.Fatalf("%s, phone on without a source: %s", mode, keys)
		}
		if got.Eligible {
			t.Fatalf("%s: eligible with the phone unknown", mode)
		}
		// With a source it is read and it decides (in open mode).
		e.svc.elig.Phones = e.facts
		got, _ = e.svc.Eligibility(ctx, user)
		if state(got.Requirements, ReqPhoneVerified) != "false" || got.Eligible {
			t.Fatalf("%s, phone on and not verified: %s eligible=%v", mode, keysOf(got.Requirements), got.Eligible)
		}
		e.expire()
		e.facts.phone = true
		got, _ = e.svc.Eligibility(ctx, user)
		if state(got.Requirements, ReqPhoneVerified) != "true" || got.Eligible != open {
			t.Fatalf("%s, phone on and verified: %s eligible=%v", mode, keysOf(got.Requirements), got.Eligible)
		}
		// An unverified email shows as false in both modes.
		e.expire()
		e.facts.email = false
		got, _ = e.svc.Eligibility(ctx, user)
		if state(got.Requirements, ReqEmailVerified) != "false" || got.Eligible {
			t.Fatalf("%s, email not verified: %s eligible=%v", mode, keysOf(got.Requirements), got.Eligible)
		}
	}

	// The gate, open mode, defaults: an unverified email refuses with its
	// row; an unverified phone does not matter.
	e := newEligRig(AccessModeOpen)
	user := uuid.New()
	e.facts.phone, e.facts.email = false, false
	_, err = e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x"})
	var ne *NotEligibleError
	if !errors.As(err, &ne) || keysOf(ne.Requirements) != "email_verified=false" {
		t.Fatalf("create with an unverified email: %v", err)
	}
	e.expire()
	e.facts.email = true
	if _, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x"}); err != nil {
		t.Fatalf("create with a verified email and no verified phone: %v", err)
	}
	// Both asked for and both short: both rows, email first.
	e.expire()
	e.svc.elig.RequirePhone = true
	e.facts.email = false
	_, err = e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "y"})
	ne = nil
	if !errors.As(err, &ne) || keysOf(ne.Requirements) != "email_verified=false,phone_verified=false" {
		t.Fatalf("create with neither verified: %v", err)
	}
	// Email switched off: not listed, not read, and it gates nothing.
	e2 := newEligRig(AccessModeOpen)
	e2.svc.elig.RequireEmail = false
	e2.facts.email, e2.svc.elig.Emails = false, nil
	got, _ := e2.svc.Eligibility(ctx, user)
	if keys := keysOf(got.Requirements); keys != "adult=true,account_age=true,activity=true,good_standing=true" || !got.Eligible {
		t.Fatalf("email off: %s eligible=%v", keys, got.Eligible)
	}
	if e2.facts.calls["email"] != 0 {
		t.Fatalf("the email fact was read with the requirement off")
	}
}

// TestActivityIsPostsOrFollowers: either count is enough; "not met" needs
// both known; one unknown count cannot hide the other being enough.
func TestActivityIsPostsOrFollowers(t *testing.T) {
	yes, no := "true", "false"
	cases := []struct {
		posts, followers       int
		postsErr, followersErr bool
		want                   string
	}{
		{3, 0, false, false, yes},
		{0, 10, false, false, yes},
		{3, 10, false, false, yes},
		{2, 9, false, false, no},
		{0, 0, false, false, no},
		{0, 10, true, false, yes},   // posts unknown, followers enough
		{3, 0, false, true, yes},    // followers unknown, posts enough
		{0, 9, true, false, "null"}, // posts unknown, followers short
		{2, 0, false, true, "null"}, // followers unknown, posts short
		{0, 0, true, true, "null"},
	}
	for _, tc := range cases {
		e := newEligRig(AccessModeOpen)
		user := uuid.New()
		e.facts.posts, e.facts.followers = tc.posts, tc.followers
		if tc.postsErr {
			e.facts.postsErr = errFactDown
		}
		if tc.followersErr {
			e.facts.followersErr = errFactDown
		}
		got, _ := e.svc.Eligibility(ctx, user)
		act := reqByKey(t, got.Requirements, ReqActivity)
		if metText(act.Met) != tc.want {
			t.Fatalf("%+v: activity met = %s", tc, metText(act.Met))
		}
		_, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x"})
		switch tc.want {
		case yes:
			if err != nil {
				t.Fatalf("%+v: create: %v", tc, err)
			}
		case no:
			if !errors.Is(err, ErrLiveNotEligible) {
				t.Fatalf("%+v: create: %v", tc, err)
			}
		default:
			if !errors.Is(err, ErrAuthorityUnavailable) {
				t.Fatalf("%+v: create: %v", tc, err)
			}
		}
	}
}

// TestNotEligibleListsWhatIsMissing: the refusal carries every requirement
// that is not met, in order, and a false one wins over an unknown one.
func TestNotEligibleListsWhatIsMissing(t *testing.T) {
	e := newEligRig(AccessModeOpen)
	user := uuid.New()
	e.facts.email = false
	e.facts.posts = 1
	e.facts.accountErr = errFactDown // account_age and good_standing unknown
	_, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x"})
	var ne *NotEligibleError
	if !errors.As(err, &ne) {
		t.Fatalf("create: %v", err)
	}
	if errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("a definite refusal reads as unavailable")
	}
	if keys := keysOf(ne.Requirements); keys != "email_verified=false,account_age=null,activity=false,good_standing=null" {
		t.Fatalf("unmet: %s", keys)
	}
	if act := reqByKey(t, ne.Requirements, ReqActivity); *act.Posts.Current != 1 || act.Posts.Needed != 3 {
		t.Fatalf("the refusal lost the progress: %+v", act.Posts)
	}
}

// TestPilotUsersAlwaysEligibleInOpenMode: the pilot list passes whatever
// the requirements say and whatever the other services are doing; the live
// ban still applies.
func TestPilotUsersAlwaysEligibleInOpenMode(t *testing.T) {
	pilot := uuid.New()
	e := newEligRig(AccessModeOpen, pilot)
	e.facts.email, e.facts.dob, e.facts.posts = false, nil, 0
	e.facts.accountErr = errFactDown

	got, _ := e.svc.Eligibility(ctx, pilot)
	if !got.Eligible || got.PilotOnly {
		t.Fatalf("pilot in open mode: %+v", got)
	}
	if keys := keysOf(got.Requirements); keys != "email_verified=false,adult=false,account_age=null,activity=false,good_standing=null" {
		t.Fatalf("the requirements are still shown: %s", keys)
	}
	before := e.facts.total()
	st, err := e.svc.CreateStream(ctx, pilot, CreateStreamParams{Title: "x", Source: SourceEncoder})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := e.svc.StartStream(ctx, st.ID, pilot); err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, err := e.svc.CreateIngress(ctx, st.ID, pilot); err != nil {
		t.Fatalf("ingress: %v", err)
	}
	if e.facts.total() != before {
		t.Fatalf("the gate read facts for a pilot user")
	}

	e.store.PlatformBans[pilot] = postgres.PlatformBan{UserID: pilot}
	if _, err := e.svc.CreateStream(ctx, pilot, CreateStreamParams{Title: "x"}); !errors.Is(err, ErrLiveBanned) {
		t.Fatalf("banned pilot: %v", err)
	}
	if got, _ := e.svc.Eligibility(ctx, pilot); got.Eligible || *reqByKey(t, got.Requirements, ReqGoodStanding).Met {
		t.Fatalf("banned pilot reads eligible: %+v", got)
	}
}

// TestPilotModeIsUnchanged: in pilot mode only the list decides; the
// requirements are computed for show and gate nothing.
func TestPilotModeIsUnchanged(t *testing.T) {
	pilot, stranger := uuid.New(), uuid.New()
	e := newEligRig(AccessModePilot, pilot)

	// A stranger who meets everything is still refused, without a single
	// fact being read by the gate.
	for name, call := range map[string]func() error{
		"create": func() error { _, err := e.svc.CreateStream(ctx, stranger, CreateStreamParams{Title: "x"}); return err },
		"start":  func() error { _, err := e.svc.StartStream(ctx, uuid.New(), stranger); return err },
		"ingress": func() error {
			_, err := e.svc.CreateIngress(ctx, uuid.New(), stranger)
			return err
		},
	} {
		if err := call(); !errors.Is(err, ErrLiveNotEnabled) {
			t.Fatalf("%s by a stranger in pilot mode: %v", name, err)
		}
	}
	if e.facts.total() != 0 {
		t.Fatalf("the pilot gate read facts: %v", e.facts.calls)
	}
	got, _ := e.svc.Eligibility(ctx, stranger)
	if got.Mode != AccessModePilot || got.Eligible || !got.PilotOnly {
		t.Fatalf("stranger: %+v", got)
	}
	if keys := keysOf(got.Requirements); keys != "email_verified=true,adult=true,account_age=true,activity=true,good_standing=true" {
		t.Fatalf("requirements in pilot mode: %s", keys)
	}

	// A pilot user who meets nothing still goes live.
	e.expire()
	e.facts.email, e.facts.dob, e.facts.posts = false, nil, 0
	got, _ = e.svc.Eligibility(ctx, pilot)
	if !got.Eligible || got.PilotOnly {
		t.Fatalf("pilot: %+v", got)
	}
	if reqByKey(t, got.Requirements, ReqAdult).Met == nil || *reqByKey(t, got.Requirements, ReqAdult).Met {
		t.Fatalf("progress is not shown in pilot mode: %s", keysOf(got.Requirements))
	}
	st, err := e.svc.CreateStream(ctx, pilot, CreateStreamParams{Title: "x"})
	if err != nil {
		t.Fatalf("pilot create: %v", err)
	}
	if _, err := e.svc.StartStream(ctx, st.ID, pilot); err != nil {
		t.Fatalf("pilot start: %v", err)
	}
	e.store.PlatformBans[pilot] = postgres.PlatformBan{UserID: pilot}
	if _, err := e.svc.CreateStream(ctx, pilot, CreateStreamParams{Title: "x"}); !errors.Is(err, ErrLiveBanned) {
		t.Fatalf("banned pilot: %v", err)
	}
	if got, _ := e.svc.Eligibility(ctx, pilot); got.Eligible || got.PilotOnly {
		t.Fatalf("a banned pilot reads eligible in pilot mode: %+v", got)
	}
}

// TestOpenModeEnforcement: create, start and ingress each refuse with
// LIVE_NOT_ELIGIBLE, AUTHORITY_UNAVAILABLE or LIVE_BANNED.
func TestOpenModeEnforcement(t *testing.T) {
	type step struct {
		name string
		call func(e *eligRig, user uuid.UUID, st *postgres.LiveStream) error
	}
	steps := []step{
		{"create", func(e *eligRig, user uuid.UUID, _ *postgres.LiveStream) error {
			_, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "y"})
			return err
		}},
		{"start", func(e *eligRig, user uuid.UUID, st *postgres.LiveStream) error {
			_, err := e.svc.StartStream(ctx, st.ID, user)
			return err
		}},
		{"ingress", func(e *eligRig, user uuid.UUID, st *postgres.LiveStream) error {
			_, err := e.svc.CreateIngress(ctx, st.ID, user)
			return err
		}},
	}
	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			e := newEligRig(AccessModeOpen)
			user := uuid.New()
			// Eligible: an encoder stream, scheduled.
			st, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x", Source: SourceEncoder})
			if err != nil {
				t.Fatalf("an eligible user could not create: %v", err)
			}
			if err := s.call(e, user, st); err != nil {
				t.Fatalf("eligible %s: %v", s.name, err)
			}
			// The stream goes back to scheduled so start is a real start.
			e.store.Streams[st.ID].Status = stScheduled

			e.expire()
			e.facts.posts = 0
			err = s.call(e, user, st)
			var ne *NotEligibleError
			if !errors.As(err, &ne) || keysOf(ne.Requirements) != "activity=false" {
				t.Fatalf("not eligible %s: %v", s.name, err)
			}

			e.expire()
			e.facts.posts = 5
			e.facts.identityErr = errFactDown
			if err := s.call(e, user, st); !errors.Is(err, ErrAuthorityUnavailable) || errors.Is(err, ErrLiveNotEligible) {
				t.Fatalf("unknown %s: %v", s.name, err)
			}

			e.expire()
			e.facts.identityErr = nil
			e.store.PlatformBans[user] = postgres.PlatformBan{UserID: user}
			if err := s.call(e, user, st); !errors.Is(err, ErrLiveBanned) {
				t.Fatalf("banned %s: %v", s.name, err)
			}
			delete(e.store.PlatformBans, user)
			if err := s.call(e, user, st); err != nil {
				t.Fatalf("eligible again %s: %v", s.name, err)
			}
		})
	}
}

// TestHostOfAStreamOnAirIsNotAskedAgain: the requirements gate starting to
// go live. A host coming back to a stream that is already on air gets their
// token (and reads their stream key) even if a requirement slipped or cannot
// be read; a ban still stops them, and a new stream is still refused.
func TestHostOfAStreamOnAirIsNotAskedAgain(t *testing.T) {
	e := newEligRig(AccessModeOpen)
	user := uuid.New()
	st, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x", Source: SourceEncoder})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartStream(ctx, st.ID, user); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CreateIngress(ctx, st.ID, user); err != nil {
		t.Fatal(err)
	}
	mustStatus(t, e.rig, st.ID, stStarting, "")

	for _, c := range []struct {
		name string
		slip func()
	}{
		{"not eligible any more", func() { e.facts.posts = 0 }},
		{"facts unreadable", func() { e.facts.posts = 5; e.facts.accountErr = errFactDown }},
	} {
		name := c.name
		e.expire()
		c.slip()
		if _, err := e.svc.StartStream(ctx, st.ID, user); err != nil {
			t.Fatalf("%s: the host could not come back to a stream on air: %v", name, err)
		}
		if _, err := e.svc.CreateIngress(ctx, st.ID, user); err != nil {
			t.Fatalf("%s: the host could not read the stream key of a stream on air: %v", name, err)
		}
		if _, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "another"}); err == nil {
			t.Fatalf("%s: a NEW stream was allowed", name)
		}
	}

	// Somebody else's stream on air is no pass.
	other := uuid.New()
	if _, err := e.svc.StartStream(ctx, st.ID, other); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("a stranger on somebody's live stream: %v", err)
	}
	// A stream that is not on air is a real start.
	e.store.Streams[st.ID].Status = stScheduled
	if _, err := e.svc.StartStream(ctx, st.ID, user); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("start of a scheduled stream with unreadable facts: %v", err)
	}
	e.store.Streams[st.ID].Status = stLive
	// The ban is never waived.
	e.store.PlatformBans[user] = postgres.PlatformBan{UserID: user}
	if _, err := e.svc.StartStream(ctx, st.ID, user); !errors.Is(err, ErrLiveBanned) {
		t.Fatalf("a banned host on air: %v", err)
	}
}

// TestFactsCachedSixtySecondsPerUser: a fact is read once a minute per user;
// a failed read is not kept.
func TestFactsCachedSixtySecondsPerUser(t *testing.T) {
	e := newEligRig(AccessModeOpen)
	a, b := uuid.New(), uuid.New()
	for i := 0; i < 3; i++ {
		if _, err := e.svc.Eligibility(ctx, a); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.CreateStream(ctx, a, CreateStreamParams{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"email", "identity", "account", "posts", "followers"} {
		if e.facts.calls[name] != 1 {
			t.Fatalf("%s read %d times in one minute", name, e.facts.calls[name])
		}
	}
	if e.facts.calls["phone"] != 0 {
		t.Fatalf("the phone fact was read with the requirement off")
	}
	// Per user.
	_, _ = e.svc.Eligibility(ctx, b)
	if e.facts.calls["posts"] != 2 {
		t.Fatalf("another user shared the cache: %v", e.facts.calls)
	}
	// 59s: still cached. 60s: read again, and the new value counts.
	e.clock.Advance(59 * time.Second)
	e.facts.posts = 0
	if got, _ := e.svc.Eligibility(ctx, a); !got.Eligible {
		t.Fatalf("the cache did not hold for 59s")
	}
	e.clock.Advance(time.Second)
	if got, _ := e.svc.Eligibility(ctx, a); got.Eligible {
		t.Fatalf("a fact outlived 60s")
	}

	// The email fact is held for the same minute.
	e.expire()
	e.facts.posts = 5
	if got, _ := e.svc.Eligibility(ctx, a); !got.Eligible {
		t.Fatalf("not eligible again: %s", keysOf(got.Requirements))
	}
	e.clock.Advance(59 * time.Second)
	e.facts.email = false
	if got, _ := e.svc.Eligibility(ctx, a); !got.Eligible {
		t.Fatalf("the email fact did not hold for 59s")
	}
	e.clock.Advance(time.Second)
	if got, _ := e.svc.Eligibility(ctx, a); got.Eligible || metText(reqByKey(t, got.Requirements, ReqEmailVerified).Met) != "false" {
		t.Fatalf("the email fact outlived 60s: %s", keysOf(got.Requirements))
	}
	// A failed email read is unknown and is not kept: the next read asks
	// again, and its answer counts at once.
	e.expire()
	e.facts.email, e.facts.emailErr = true, errFactDown
	if got, _ := e.svc.Eligibility(ctx, a); reqByKey(t, got.Requirements, ReqEmailVerified).Met != nil || got.Eligible {
		t.Fatalf("a failed email read produced an answer")
	}
	e.facts.emailErr = nil
	if got, _ := e.svc.Eligibility(ctx, a); !got.Eligible {
		t.Fatalf("the failed email read was cached: %s", keysOf(got.Requirements))
	}

	// A failure is not cached: the next read asks again.
	e.expire()
	e.facts.posts, e.facts.postsErr = 5, errFactDown
	e.facts.followersErr = errFactDown
	if got, _ := e.svc.Eligibility(ctx, a); reqByKey(t, got.Requirements, ReqActivity).Met != nil {
		t.Fatalf("a failed read produced an answer")
	}
	e.facts.postsErr, e.facts.followersErr = nil, nil
	if got, _ := e.svc.Eligibility(ctx, a); !got.Eligible {
		t.Fatalf("the failure was cached: %s", keysOf(got.Requirements))
	}
}

// --- the new-streamer viewer cap ---

// capRig: cap 3 viewers until 2 completed streams.
func newCapRig(mode string, pilot ...uuid.UUID) *eligRig {
	e := newEligRig(mode, pilot...)
	e.svc.elig.NewStreamerStreams, e.svc.elig.NewStreamerViewerCap = 2, 3
	return e
}

func (e *eligRig) join(st *postgres.LiveStream, users ...uuid.UUID) {
	for _, u := range users {
		_ = e.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "participant_joined", Room: st.LiveKitRoom, ParticipantIdentity: u.String()})
	}
}

func (e *eligRig) leave(st *postgres.LiveStream, u uuid.UUID) {
	_ = e.svc.HandleWebhook(ctx, WebhookEvent{ID: uuid.NewString(), Event: "participant_left", Room: st.LiveKitRoom, ParticipantIdentity: u.String()})
}

// ended adds a stream of the creator that was on air for `onAir` and ended
// for `reason`.
func (e *eligRig) ended(creator uuid.UUID, onAir time.Duration, reason string) {
	st := e.store.AddStreamStatus(creator, stEnded)
	row := e.store.Streams[st.ID]
	end := e.clock.Now()
	start := end.Add(-onAir)
	row.StartedAt, row.EndedAt, row.EndedReason = &start, &end, &reason
}

func TestViewerCapBoundary(t *testing.T) {
	host := uuid.New()
	e := newCapRig(AccessModePilot, host)
	st := e.store.AddStream(host)
	v1, v2, v3, late, mod := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	if _, err := e.svc.SetModerators(ctx, st.ID, host, []uuid.UUID{mod}); err != nil {
		t.Fatal(err)
	}
	token := func(u uuid.UUID) error {
		_, err := e.svc.IssueViewerToken(ctx, st.ID, u)
		return err
	}

	// The host is not counted and need not be in the room to pass.
	e.join(st, v1, v2)
	// Two of three: a new viewer gets in.
	if err := token(v3); err != nil {
		t.Fatalf("below the cap: %v", err)
	}
	e.join(st, v3)
	if n := e.store.Stream(st.ID).ViewerCount; n != 3 {
		t.Fatalf("viewer_count = %d", n)
	}
	// Three of three: a NEW viewer is refused...
	if err := token(late); !errors.Is(err, ErrStreamFull) {
		t.Fatalf("at the cap: %v", err)
	}
	// ...the host, a moderator and the viewers already in the room are not.
	for name, u := range map[string]uuid.UUID{"host": host, "moderator": mod, "viewer in the room": v1} {
		if err := token(u); err != nil {
			t.Fatalf("%s at the cap: %v", name, err)
		}
	}
	// A viewer who left is a new viewer again; their seat is free for the
	// next one.
	e.leave(st, v2)
	if err := token(late); err != nil {
		t.Fatalf("after a viewer left: %v", err)
	}
	e.join(st, late)
	if err := token(v2); !errors.Is(err, ErrStreamFull) {
		t.Fatalf("the viewer who left came back to a full room: %v", err)
	}
}

func TestViewerCapLiftsAfterCompletedStreams(t *testing.T) {
	host := uuid.New()
	e := newCapRig(AccessModeOpen)
	st := e.store.AddStream(host)
	e.join(st, uuid.New(), uuid.New(), uuid.New())
	late := uuid.New()
	full := func() bool {
		_, err := e.svc.IssueViewerToken(ctx, st.ID, late)
		if err != nil && !errors.Is(err, ErrStreamFull) {
			t.Fatalf("viewer token: %v", err)
		}
		return err != nil
	}
	hostCap := func() *int {
		row, err := e.svc.GetStream(ctx, st.ID, host)
		if err != nil {
			t.Fatal(err)
		}
		return row.ViewerCap
	}
	if !full() || hostCap() == nil || *hostCap() != 3 {
		t.Fatalf("a first-time streamer is not capped")
	}

	// Streams that do not count: too short, stopped by an admin, failed,
	// and somebody else's.
	e.ended(host, 5*time.Minute-time.Second, ReasonHostEnded)
	e.ended(host, time.Hour, ReasonAdminStopped)
	failed := e.store.AddStreamStatus(host, stFailed)
	_ = failed
	e.ended(uuid.New(), time.Hour, ReasonHostEnded)
	e.ended(uuid.New(), time.Hour, ReasonHostEnded)
	if !full() {
		t.Fatalf("streams that do not count lifted the cap")
	}
	// One that counts (exactly five minutes; ended because the host was lost
	// counts too): 1 of 2, still capped.
	e.ended(host, 5*time.Minute, ReasonHostLost)
	if !full() || hostCap() == nil {
		t.Fatalf("one completed stream lifted the cap of two")
	}
	// The second: the cap is gone, for the token and on the host's row.
	e.ended(host, 20*time.Minute, ReasonHostEnded)
	if full() {
		t.Fatalf("the cap did not lift after two completed streams")
	}
	if hostCap() != nil {
		t.Fatalf("viewer_cap stayed on the row: %d", *hostCap())
	}
	if got, _ := e.svc.Eligibility(ctx, host); got.ViewerCap != nil {
		t.Fatalf("eligibility still shows the cap")
	}
}

// TestViewerCapCountsARealStream: the count comes from streams ended through
// the lifecycle, not only from rows a test wrote.
func TestViewerCapCountsARealStream(t *testing.T) {
	host := uuid.New()
	e := newCapRig(AccessModePilot, host)
	e.svc.elig.NewStreamerStreams = 1
	run := func(onAir time.Duration, end func(st *postgres.LiveStream)) {
		st, err := e.svc.CreateStream(ctx, host, CreateStreamParams{Title: "x"})
		if err != nil {
			t.Fatal(err)
		}
		if st.ViewerCap == nil || *st.ViewerCap != 3 {
			t.Fatalf("the created row has no viewer_cap")
		}
		res, err := e.svc.StartStream(ctx, st.ID, host)
		if err != nil {
			t.Fatal(err)
		}
		if res.Stream.ViewerCap == nil {
			t.Fatalf("the started row has no viewer_cap")
		}
		if err := e.svc.HandleWebhook(ctx, hostEvent(res.Stream, "track_published")); err != nil {
			t.Fatal(err)
		}
		e.clock.Advance(onAir)
		end(res.Stream)
	}
	hostEnd := func(st *postgres.LiveStream) {
		if _, err := e.svc.EndStream(ctx, st.ID, host); err != nil {
			t.Fatal(err)
		}
	}
	capped := func() bool {
		got, _ := e.svc.Eligibility(ctx, host)
		return got.ViewerCap != nil
	}
	run(4*time.Minute, hostEnd)
	if !capped() {
		t.Fatalf("a four-minute stream lifted the cap")
	}
	run(6*time.Minute, func(st *postgres.LiveStream) {
		if _, err := e.svc.AdminStopStream(ctx, uuid.New(), st.ID, "abuse"); err != nil {
			t.Fatal(err)
		}
	})
	if !capped() {
		t.Fatalf("an admin-stopped stream lifted the cap")
	}
	run(6*time.Minute, hostEnd)
	if capped() {
		t.Fatalf("a completed stream did not lift the cap")
	}
}

func TestViewerCapOffAndWhoSeesIt(t *testing.T) {
	host, viewer := uuid.New(), uuid.New()

	// 0 disables it, either setting.
	for _, set := range []func(*EligibilityConfig){
		func(c *EligibilityConfig) { c.NewStreamerViewerCap = 0 },
		func(c *EligibilityConfig) { c.NewStreamerStreams = 0 },
	} {
		e := newCapRig(AccessModePilot, host)
		set(&e.svc.elig)
		st := e.store.AddStream(host)
		e.join(st, uuid.New(), uuid.New(), uuid.New(), uuid.New())
		if _, err := e.svc.IssueViewerToken(ctx, st.ID, viewer); err != nil {
			t.Fatalf("the cap is switched off: %v", err)
		}
		if row, _ := e.svc.GetStream(ctx, st.ID, host); row.ViewerCap != nil {
			t.Fatalf("viewer_cap with the cap off")
		}
		if got, _ := e.svc.Eligibility(ctx, host); got.ViewerCap != nil {
			t.Fatalf("eligibility viewer_cap with the cap off")
		}
	}

	// Only the host reads viewer_cap, on streams that have not ended, in
	// lists as in the single read.
	e := newCapRig(AccessModePilot, host)
	st := e.store.AddStream(host)
	e.ended(host, time.Hour, ReasonHostEnded) // 1 of 2: still capped
	if row, _ := e.svc.GetStream(ctx, st.ID, viewer); row.ViewerCap != nil {
		t.Fatalf("a viewer reads viewer_cap")
	}
	if row, _ := e.svc.GetStream(ctx, st.ID, uuid.Nil); row.ViewerCap != nil {
		t.Fatalf("a signed-out reader reads viewer_cap")
	}
	mine, err := e.svc.UserStreams(ctx, host, host, UserStreamsLive, 10, "")
	if err != nil || len(mine.Streams) != 1 || mine.Streams[0].ViewerCap == nil || *mine.Streams[0].ViewerCap != 3 {
		t.Fatalf("the host's own live list: %+v %v", mine, err)
	}
	theirs, _ := e.svc.UserStreams(ctx, viewer, host, UserStreamsLive, 10, "")
	if len(theirs.Streams) != 1 || theirs.Streams[0].ViewerCap != nil {
		t.Fatalf("a viewer's read of the host's list carries viewer_cap")
	}
	past, _ := e.svc.UserStreams(ctx, host, host, UserStreamsPast, 10, "")
	if len(past.Streams) != 1 || past.Streams[0].ViewerCap != nil {
		t.Fatalf("an ended stream carries viewer_cap")
	}
	// The stored row was not touched.
	if e.store.Streams[st.ID].ViewerCap != nil {
		t.Fatalf("decorating wrote viewer_cap into the store's row")
	}
	// Eligibility shows it in pilot mode too, to anyone it applies to.
	if got, _ := e.svc.Eligibility(ctx, viewer); got.ViewerCap == nil || *got.ViewerCap != 3 {
		t.Fatalf("eligibility of a user with no streams: %+v", got)
	}
}

// banErrStore fails the live-ban read.
type banErrStore struct {
	Store
	err error
}

func (b *banErrStore) IsPlatformBanned(ctx context.Context, userID uuid.UUID) (bool, error) {
	if b.err != nil {
		return false, b.err
	}
	return b.Store.IsPlatformBanned(ctx, userID)
}

// TestUnreadableBanNeverPasses: when the live ban cannot be read, nobody is
// let through in open mode — not an eligible user (503), and not the host
// of a stream already on air, whose pass covers the requirements only.
func TestUnreadableBanNeverPasses(t *testing.T) {
	e := newEligRig(AccessModeOpen)
	user := uuid.New()
	st, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartStream(ctx, st.ID, user); err != nil {
		t.Fatal(err)
	}
	broken := &banErrStore{Store: e.svc.store, err: errors.New("database is down")}
	e.svc.store = broken

	if _, err := e.svc.CreateStream(ctx, user, CreateStreamParams{Title: "y"}); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("create with the ban unreadable: %v", err)
	}
	if _, err := e.svc.StartStream(ctx, st.ID, user); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("the host of a stream on air passed with the ban unreadable: %v", err)
	}
	got, err := e.svc.Eligibility(ctx, user)
	if err != nil || got.Eligible || reqByKey(t, got.Requirements, ReqGoodStanding).Met != nil {
		t.Fatalf("eligibility with the ban unreadable: %+v %v", got, err)
	}

	// Pilot mode keeps its own answer for this: an error, not a pass.
	pilot := uuid.New()
	p := newEligRig(AccessModePilot, pilot)
	p.svc.store = &banErrStore{Store: p.svc.store, err: errors.New("database is down")}
	if _, err := p.svc.CreateStream(ctx, pilot, CreateStreamParams{Title: "x"}); err == nil || errors.Is(err, ErrLiveNotEnabled) {
		t.Fatalf("pilot create with the ban unreadable: %v", err)
	}
	if got, _ := p.svc.Eligibility(ctx, pilot); got.Eligible {
		t.Fatalf("pilot reads eligible with the ban unreadable")
	}
}
