package service

// Going-live eligibility (2 Oct 2026): "Almost there".
//
// LIVE_ACCESS_MODE decides who may go live:
//
//	pilot (default)  only LIVE_PILOT_USER_IDS, exactly as before
//	                 (403 LIVE_NOT_ENABLED for everyone else).
//	open             the requirements below decide; a pilot-list user is
//	                 always eligible.
//
// Requirements, each one about trust and none about popularity:
//
//	phone_verified  identity says the phone is verified (LIVE_ELIG_REQUIRE_PHONE)
//	adult           18 or over by the identity date of birth (always on)
//	account_age     the account is at least LIVE_ELIG_MIN_ACCOUNT_AGE old
//	activity        LIVE_ELIG_MIN_POSTS posts OR LIVE_ELIG_MIN_FOLLOWERS followers
//	good_standing   no platform live ban, and the account is active
//
// Every requirement is true, false or UNKNOWN (the fact could not be read
// right now, or no service exposes it). In open mode a false one refuses
// with 403 LIVE_NOT_ELIGIBLE and the list of what is missing; with nothing
// false but something unknown the answer is 503 AUTHORITY_UNAVAILABLE: an
// unreadable fact never lets anyone through.
//
// The new-streamer viewer cap applies in both modes: a creator with fewer
// than LIVE_NEW_STREAMER_STREAMS completed streams has their streams limited
// to LIVE_NEW_STREAMER_VIEWER_CAP concurrent viewers (viewercap below).

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// Access modes (LIVE_ACCESS_MODE).
const (
	AccessModePilot = "pilot"
	AccessModeOpen  = "open"
)

// Requirement keys, in the order they are listed.
const (
	ReqPhoneVerified = "phone_verified"
	ReqAdult         = "adult"
	ReqAccountAge    = "account_age"
	ReqActivity      = "activity"
	ReqGoodStanding  = "good_standing"
)

// Defaults of the eligibility settings.
const (
	DefaultEligRequirePhone     = true
	DefaultEligMinAccountAge    = 168 * time.Hour
	DefaultEligMinPosts         = 3
	DefaultEligMinFollowers     = 10
	DefaultNewStreamerStreams   = 3
	DefaultNewStreamerViewerCap = 200

	// EligibilityAdultAge is the age going live requires.
	EligibilityAdultAge = 18

	// eligFactTTL is how long a fact read from another service is reused
	// for the same user. A failed read is never kept.
	eligFactTTL     = 60 * time.Second
	eligFactTimeout = 2 * time.Second
	eligCacheMax    = 20000
)

var (
	// ErrLiveNotEligible is what a *NotEligibleError matches with errors.Is.
	ErrLiveNotEligible = errors.New("you do not meet the requirements to go live yet")
	// ErrStreamFull: the new-streamer viewer cap is reached.
	ErrStreamFull = errors.New("this stream is full right now")

	// errEligibilityUnknown is a requirement that could not be checked. It
	// is an ErrAuthorityUnavailable (503) for every caller.
	errEligibilityUnknown = fmt.Errorf("%w: a going-live requirement could not be checked", ErrAuthorityUnavailable)
)

// NotEligibleError is the open-mode refusal: Requirements holds every
// requirement that is not met (false, or unknown alongside a false one).
type NotEligibleError struct {
	Requirements []Requirement
}

func (e *NotEligibleError) Error() string { return ErrLiveNotEligible.Error() }

// Is makes errors.Is(err, ErrLiveNotEligible) true.
func (e *NotEligibleError) Is(target error) bool { return target == ErrLiveNotEligible }

// ParseAccessMode reads LIVE_ACCESS_MODE: empty is pilot, "pilot" and "open"
// are themselves (any case, padded). Anything else is an error and the
// caller refuses to start: a typo must not open going live to everyone, nor
// quietly close it.
func ParseAccessMode(raw string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", AccessModePilot:
		return AccessModePilot, nil
	case AccessModeOpen:
		return AccessModeOpen, nil
	default:
		return "", fmt.Errorf("LIVE_ACCESS_MODE must be %q or %q", AccessModePilot, AccessModeOpen)
	}
}

// EligibilityConfig carries the eligibility settings and where the facts are
// read from. The zero value is pilot mode with every optional requirement
// off and no viewer cap; EligibilityConfigFromEnv applies the real defaults.
type EligibilityConfig struct {
	// Mode is AccessModePilot ("" too) or AccessModeOpen.
	Mode string
	// RequirePhone lists and enforces phone_verified.
	RequirePhone bool
	// MinAccountAge: zero or less drops the account_age requirement.
	MinAccountAge time.Duration
	// MinPosts / MinFollowers: activity is met by EITHER. Zero or less takes
	// that alternative away; both at zero drop the requirement.
	MinPosts     int
	MinFollowers int

	// NewStreamerStreams is how many completed streams lift the viewer cap;
	// NewStreamerViewerCap is the cap. Either at zero or less: no cap.
	NewStreamerStreams   int
	NewStreamerViewerCap int
	// CompletedMinLive is how long a stream must have been on air to count
	// as completed; zero or less means postgres.DefaultFoundingMinLive.
	CompletedMinLive time.Duration

	// The fact sources. A nil source makes its facts unknown.
	Phones     PhoneSource
	BirthDates BirthDateSource
	Accounts   AccountSource
	Posts      PostCountSource
	Followers  FollowerCountSource
}

// EligibilityConfigFromEnv reads the settings (not the sources). A value
// that does not parse is an error, and the caller refuses to start.
func EligibilityConfigFromEnv(getenv func(string) string) (EligibilityConfig, error) {
	cfg := EligibilityConfig{
		RequirePhone:         DefaultEligRequirePhone,
		MinAccountAge:        DefaultEligMinAccountAge,
		MinPosts:             DefaultEligMinPosts,
		MinFollowers:         DefaultEligMinFollowers,
		NewStreamerStreams:   DefaultNewStreamerStreams,
		NewStreamerViewerCap: DefaultNewStreamerViewerCap,
	}
	mode, err := ParseAccessMode(getenv("LIVE_ACCESS_MODE"))
	if err != nil {
		return EligibilityConfig{}, err
	}
	cfg.Mode = mode
	if v := strings.TrimSpace(getenv("LIVE_ELIG_REQUIRE_PHONE")); v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return EligibilityConfig{}, errors.New("LIVE_ELIG_REQUIRE_PHONE must be true or false")
		}
		cfg.RequirePhone = b
	}
	if v := strings.TrimSpace(getenv("LIVE_ELIG_MIN_ACCOUNT_AGE")); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return EligibilityConfig{}, errors.New("LIVE_ELIG_MIN_ACCOUNT_AGE must be a duration of zero or more (for example 168h)")
		}
		cfg.MinAccountAge = d
	}
	for _, f := range []struct {
		key string
		dst *int
	}{
		{"LIVE_ELIG_MIN_POSTS", &cfg.MinPosts},
		{"LIVE_ELIG_MIN_FOLLOWERS", &cfg.MinFollowers},
		{"LIVE_NEW_STREAMER_STREAMS", &cfg.NewStreamerStreams},
		{"LIVE_NEW_STREAMER_VIEWER_CAP", &cfg.NewStreamerViewerCap},
	} {
		v := strings.TrimSpace(getenv(f.key))
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return EligibilityConfig{}, fmt.Errorf("%s must be a whole number of zero or more", f.key)
		}
		*f.dst = n
	}
	return cfg, nil
}

// --- the facts and where they come from ---

// PhoneSource answers whether identity holds a verified phone for the user.
type PhoneSource interface {
	PhoneVerified(ctx context.Context, userID uuid.UUID) (bool, error)
}

// BirthDateSource answers the user's date of birth. found=false: identity
// has no such account, or one its owner shut or hid. A nil date with
// found=true: the account has no date of birth anyone stands behind.
type BirthDateSource interface {
	BirthDate(ctx context.Context, userID uuid.UUID) (dob *time.Time, found bool, err error)
}

// AccountInfo is the account record: when it was made and whether it is
// active.
type AccountInfo struct {
	Found     bool
	Active    bool
	CreatedAt time.Time
}

// AccountSource reads the account record.
type AccountSource interface {
	Account(ctx context.Context, userID uuid.UUID) (AccountInfo, error)
}

// PostCountSource counts the user's posts and videos.
type PostCountSource interface {
	PostCount(ctx context.Context, userID uuid.UUID) (int, error)
}

// FollowerCountSource counts the user's followers.
type FollowerCountSource interface {
	FollowerCount(ctx context.Context, userID uuid.UUID) (int, error)
}

type identityFact struct {
	dob   *time.Time
	found bool
}

// eligFacts is what was read about one user; a nil field is unknown.
type eligFacts struct {
	phone     *bool
	identity  *identityFact
	account   *AccountInfo
	posts     *int
	followers *int
}

type cachedFact[T any] struct {
	v  T
	at time.Time
	ok bool
}

func (c cachedFact[T]) fresh(now time.Time) (*T, bool) {
	if !c.ok || now.Sub(c.at) >= eligFactTTL {
		return nil, false
	}
	v := c.v
	return &v, true
}

type eligEntry struct {
	phone     cachedFact[bool]
	identity  cachedFact[identityFact]
	account   cachedFact[AccountInfo]
	posts     cachedFact[int]
	followers cachedFact[int]
}

// eligCache keeps each fact per user for eligFactTTL.
type eligCache struct {
	mu sync.Mutex
	m  map[uuid.UUID]*eligEntry
}

func (c *eligCache) entry(userID uuid.UUID) *eligEntry {
	if c.m == nil || len(c.m) > eligCacheMax {
		c.m = map[uuid.UUID]*eligEntry{}
	}
	e := c.m[userID]
	if e == nil {
		e = &eligEntry{}
		c.m[userID] = e
	}
	return e
}

// readFact returns the cached fact, or reads it and keeps a successful
// answer. An error (or a nil source, signalled by read == nil) is unknown.
func readFact[T any](ctx context.Context, s *Service, userID uuid.UUID, name string,
	slot func(*eligEntry) *cachedFact[T], read func(context.Context) (T, error)) *T {
	now := s.clock()
	s.eligFacts.mu.Lock()
	v, ok := slot(s.eligFacts.entry(userID)).fresh(now)
	s.eligFacts.mu.Unlock()
	if ok {
		return v
	}
	if read == nil {
		return nil
	}
	fctx, cancel := context.WithTimeout(ctx, eligFactTimeout)
	defer cancel()
	got, err := read(fctx)
	if err != nil {
		// The user id only: an error from the identity read must never carry
		// a date of birth into a log line, and these do not.
		slog.WarnContext(ctx, "live-v2: eligibility fact not read; the requirement is unknown", "fact", name, "user_id", userID, "err", err)
		return nil
	}
	s.eligFacts.mu.Lock()
	*slot(s.eligFacts.entry(userID)) = cachedFact[T]{v: got, at: now, ok: true}
	s.eligFacts.mu.Unlock()
	return &got
}

// gatherFacts reads what the configured requirements need, the lookups side
// by side so one slow service costs one timeout, not four.
func (s *Service) gatherFacts(ctx context.Context, userID uuid.UUID) eligFacts {
	cfg := s.elig
	var f eligFacts
	var wg sync.WaitGroup
	run := func(fn func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			fn()
		}()
	}
	if cfg.RequirePhone {
		run(func() {
			var read func(context.Context) (bool, error)
			if cfg.Phones != nil {
				read = func(c context.Context) (bool, error) { return cfg.Phones.PhoneVerified(c, userID) }
			}
			f.phone = readFact(ctx, s, userID, ReqPhoneVerified, func(e *eligEntry) *cachedFact[bool] { return &e.phone }, read)
		})
	}
	run(func() {
		var read func(context.Context) (identityFact, error)
		if cfg.BirthDates != nil {
			read = func(c context.Context) (identityFact, error) {
				dob, found, err := cfg.BirthDates.BirthDate(c, userID)
				return identityFact{dob: dob, found: found}, err
			}
		}
		f.identity = readFact(ctx, s, userID, ReqAdult, func(e *eligEntry) *cachedFact[identityFact] { return &e.identity }, read)
	})
	run(func() {
		var read func(context.Context) (AccountInfo, error)
		if cfg.Accounts != nil {
			read = func(c context.Context) (AccountInfo, error) { return cfg.Accounts.Account(c, userID) }
		}
		f.account = readFact(ctx, s, userID, "account", func(e *eligEntry) *cachedFact[AccountInfo] { return &e.account }, read)
	})
	if cfg.MinPosts > 0 {
		run(func() {
			var read func(context.Context) (int, error)
			if cfg.Posts != nil {
				read = func(c context.Context) (int, error) { return cfg.Posts.PostCount(c, userID) }
			}
			f.posts = readFact(ctx, s, userID, "posts", func(e *eligEntry) *cachedFact[int] { return &e.posts }, read)
		})
	}
	if cfg.MinFollowers > 0 {
		run(func() {
			var read func(context.Context) (int, error)
			if cfg.Followers != nil {
				read = func(c context.Context) (int, error) { return cfg.Followers.FollowerCount(c, userID) }
			}
			f.followers = readFact(ctx, s, userID, "followers", func(e *eligEntry) *cachedFact[int] { return &e.followers }, read)
		})
	}
	wg.Wait()
	return f
}

// --- requirements ---

// Progress is one countable part of a requirement. Current is left off
// when the count could not be read.
type Progress struct {
	Current *int `json:"current,omitempty"`
	Needed  int  `json:"needed"`
}

// Requirement is one going-live requirement as the clients show it. Met is
// true, false, or null when it could not be checked right now.
type Requirement struct {
	Key string `json:"key"`
	Met *bool  `json:"met"`
	// account_age: how old the account is and has to be, in Unit ("days", or
	// "hours" when the required age is not a whole number of days). Current
	// is left off when the account's age could not be read.
	Current *int   `json:"current,omitempty"`
	Needed  *int   `json:"needed,omitempty"`
	Unit    string `json:"unit,omitempty"`
	// activity: met by EITHER count. An alternative that is switched off is
	// left off.
	Posts     *Progress `json:"posts,omitempty"`
	Followers *Progress `json:"followers,omitempty"`
}

func metOf(b bool) *bool { return &b }
func countOf(n int) *int { return &n }

// isAdultOn reports whether someone born on dob is EligibilityAdultAge or
// older on day now (calendar arithmetic in UTC; the birthday itself counts).
func isAdultOn(dob, now time.Time) bool {
	now = now.UTC()
	birthday := time.Date(dob.Year()+EligibilityAdultAge, dob.Month(), dob.Day(), 0, 0, 0, 0, time.UTC)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return !birthday.After(today)
}

// buildRequirements turns the facts into the listed requirements. banned is
// the platform live ban (nil = the check failed).
func (s *Service) buildRequirements(f eligFacts, banned *bool) []Requirement {
	cfg := s.elig
	now := s.clock()
	out := make([]Requirement, 0, 5)

	if cfg.RequirePhone {
		out = append(out, Requirement{Key: ReqPhoneVerified, Met: f.phone})
	}

	adult := Requirement{Key: ReqAdult}
	if f.identity != nil {
		adult.Met = metOf(f.identity.found && f.identity.dob != nil && isAdultOn(*f.identity.dob, now))
	}
	out = append(out, adult)

	if cfg.MinAccountAge > 0 {
		unit, per := "days", 24*time.Hour
		if cfg.MinAccountAge%(24*time.Hour) != 0 {
			unit, per = "hours", time.Hour
		}
		needed := int((cfg.MinAccountAge + per - 1) / per)
		r := Requirement{Key: ReqAccountAge, Needed: countOf(needed), Unit: unit}
		if f.account != nil {
			age := time.Duration(0)
			if f.account.Found {
				if age = now.Sub(f.account.CreatedAt); age < 0 {
					age = 0
				}
			}
			r.Current = countOf(int(age / per))
			r.Met = metOf(f.account.Found && age >= cfg.MinAccountAge)
		}
		out = append(out, r)
	}

	if cfg.MinPosts > 0 || cfg.MinFollowers > 0 {
		r := Requirement{Key: ReqActivity}
		met, unknown := false, false
		if cfg.MinPosts > 0 {
			r.Posts = &Progress{Current: f.posts, Needed: cfg.MinPosts}
			if f.posts == nil {
				unknown = true
			} else if *f.posts >= cfg.MinPosts {
				met = true
			}
		}
		if cfg.MinFollowers > 0 {
			r.Followers = &Progress{Current: f.followers, Needed: cfg.MinFollowers}
			if f.followers == nil {
				unknown = true
			} else if *f.followers >= cfg.MinFollowers {
				met = true
			}
		}
		// Either is enough, so one count that is known and enough settles it
		// whatever the other is; "not met" needs every offered count known.
		switch {
		case met:
			r.Met = metOf(true)
		case !unknown:
			r.Met = metOf(false)
		}
		out = append(out, r)
	}

	standing := Requirement{Key: ReqGoodStanding}
	shut := (f.account != nil && (!f.account.Found || !f.account.Active)) ||
		(f.identity != nil && !f.identity.found)
	switch {
	case banned != nil && *banned, shut:
		standing.Met = metOf(false)
	case banned != nil && f.account != nil:
		standing.Met = metOf(true)
	}
	out = append(out, standing)
	return out
}

// requirementsFor reads the facts and builds the list.
func (s *Service) requirementsFor(ctx context.Context, userID uuid.UUID, banned *bool) []Requirement {
	return s.buildRequirements(s.gatherFacts(ctx, userID), banned)
}

// unmetOf splits the list: the requirements that are not met (false or
// unknown), and whether any is false / any is unknown.
func unmetOf(reqs []Requirement) (unmet []Requirement, anyFalse, anyUnknown bool) {
	for _, r := range reqs {
		switch {
		case r.Met == nil:
			anyUnknown = true
		case !*r.Met:
			anyFalse = true
		default:
			continue
		}
		unmet = append(unmet, r)
	}
	return unmet, anyFalse, anyUnknown
}

func (s *Service) accessMode() string {
	if s.elig.Mode == AccessModeOpen {
		return AccessModeOpen
	}
	return AccessModePilot
}

// Eligibility is GET /v1/livestream/eligibility.
type Eligibility struct {
	Mode         string        `json:"mode"`
	Eligible     bool          `json:"eligible"`
	Requirements []Requirement `json:"requirements"`
	// PilotOnly: pilot mode and the user is not on the pilot list.
	PilotOnly bool `json:"pilot_only,omitempty"`
	// ViewerCap is present only while the new-streamer cap applies.
	ViewerCap *int `json:"viewer_cap,omitempty"`
}

// Eligibility answers whether the user may go live and what is missing.
// The requirements are computed in both modes (in pilot mode they show
// progress and decide nothing). Eligible is exactly what create and start
// would say: the same gate decides both.
func (s *Service) Eligibility(ctx context.Context, userID uuid.UUID) (*Eligibility, error) {
	var banned *bool
	if b, err := s.store.IsPlatformBanned(ctx, userID); err != nil {
		slog.WarnContext(ctx, "live-v2: live ban not read; good standing is unknown", "user_id", userID, "err", err)
	} else {
		banned = &b
	}
	reqs := s.requirementsFor(ctx, userID, banned)
	out := &Eligibility{Mode: s.accessMode(), Requirements: reqs}
	listed := s.pilot[userID]
	notBanned := banned != nil && !*banned
	if out.Mode == AccessModeOpen {
		_, anyFalse, anyUnknown := unmetOf(reqs)
		out.Eligible = notBanned && (listed || (!anyFalse && !anyUnknown))
	} else {
		out.Eligible = listed && notBanned
		out.PilotOnly = !listed
	}
	if limit, err := s.viewerCapFor(ctx, userID); err != nil {
		slog.WarnContext(ctx, "live-v2: completed streams not counted; viewer_cap left off", "user_id", userID, "err", err)
	} else if limit > 0 {
		out.ViewerCap = &limit
	}
	return out, nil
}

// requireMayGoLive is the gate on create, start and ingress.
//
// Pilot mode: the pilot allowlist (empty = nobody) and the platform live
// ban, as it always was. Open mode: the live ban, then a pilot-list user
// passes, and everyone else needs every requirement met.
func (s *Service) requireMayGoLive(ctx context.Context, userID uuid.UUID) error {
	if userID == uuid.Nil {
		return ErrLiveNotEnabled
	}
	listed := s.pilot[userID]
	open := s.accessMode() == AccessModeOpen
	if !open && !listed {
		return ErrLiveNotEnabled
	}
	banned, err := s.store.IsPlatformBanned(ctx, userID)
	if err != nil {
		if open {
			slog.WarnContext(ctx, "live-v2: live ban not read; refusing", "user_id", userID, "err", err)
			return errEligibilityUnknown
		}
		return fmt.Errorf("check live ban: %w", err)
	}
	if banned {
		return ErrLiveBanned
	}
	if listed {
		return nil
	}
	notBanned := false
	unmet, anyFalse, anyUnknown := unmetOf(s.requirementsFor(ctx, userID, &notBanned))
	switch {
	case anyFalse:
		return &NotEligibleError{Requirements: unmet}
	case anyUnknown:
		return errEligibilityUnknown
	}
	return nil
}

// requireMayContinue is requireMayGoLive for an action on one stream (start,
// ingress). The requirements are a condition of STARTING to go live: once
// the stream is on air, its host coming back to it (a reconnect asks for a
// new publisher token, a reload reads the stream key again) is not asked for
// them again, so a follower count that dipped or an identity service that is
// down for a minute cannot end a broadcast. The live ban always applies.
func (s *Service) requireMayContinue(ctx context.Context, streamID, hostID uuid.UUID) error {
	err := s.requireMayGoLive(ctx, hostID)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ErrLiveNotEligible) && !errors.Is(err, errEligibilityUnknown) {
		return err
	}
	if banned, berr := s.store.IsPlatformBanned(ctx, hostID); berr != nil || banned {
		return err
	}
	st, gerr := s.store.GetByID(ctx, streamID)
	if gerr == nil && st.CreatorUserID == hostID && onAir(st.Status) {
		return nil
	}
	return err
}

// --- the new-streamer viewer cap ---

func (s *Service) completedMinLive() time.Duration {
	if s.elig.CompletedMinLive > 0 {
		return s.elig.CompletedMinLive
	}
	return postgres.DefaultFoundingMinLive
}

// viewerCapFor returns the cap on the creator's streams, or 0 when none
// applies: the cap is switched off, or the creator has completed enough
// streams (ended after at least CompletedMinLive on air, not admin-stopped).
func (s *Service) viewerCapFor(ctx context.Context, creatorID uuid.UUID) (int, error) {
	cfg := s.elig
	if cfg.NewStreamerViewerCap <= 0 || cfg.NewStreamerStreams <= 0 || creatorID == uuid.Nil {
		return 0, nil
	}
	n, err := s.store.CountCompletedStreams(ctx, creatorID, s.completedMinLive())
	if err != nil {
		return 0, err
	}
	if n >= cfg.NewStreamerStreams {
		return 0, nil
	}
	return cfg.NewStreamerViewerCap, nil
}

// checkViewerCap refuses a NEW viewer of a capped stream that is full. The
// host, the stream's moderators and anyone already in the room pass.
//
// The count is viewer_count, the people LiveKit reports in the room, so a
// burst of tokens handed out in the same second can overshoot by the size
// of the burst; the cap is a safety limit, not a seat count.
func (s *Service) checkViewerCap(ctx context.Context, st *postgres.LiveStream, viewerID uuid.UUID) error {
	limit := s.elig.NewStreamerViewerCap
	if limit <= 0 || s.elig.NewStreamerStreams <= 0 {
		return nil
	}
	if viewerID == st.CreatorUserID || st.ViewerCount < limit {
		return nil
	}
	limit, err := s.viewerCapFor(ctx, st.CreatorUserID)
	if err != nil {
		slog.WarnContext(ctx, "live-v2: completed streams not counted; refusing a new viewer of a full room", "stream_id", st.ID, "err", err)
		return ErrAuthorityUnavailable
	}
	if limit <= 0 || st.ViewerCount < limit {
		return nil
	}
	if mod, err := s.store.IsModerator(ctx, st.ID, viewerID); err != nil {
		return ErrAuthorityUnavailable
	} else if mod {
		return nil
	}
	if present, err := s.store.IsViewerPresent(ctx, st.ID, viewerID); err != nil {
		return ErrAuthorityUnavailable
	} else if present {
		return nil
	}
	return ErrStreamFull
}

// withViewerCap sets viewer_cap on the viewer's OWN streams that have not
// ended, while the cap applies to them. Rows are copied, never mutated. Best
// effort: a failed count leaves the field off.
func (s *Service) withViewerCap(ctx context.Context, viewerID uuid.UUID, rows []*postgres.LiveStream) []*postgres.LiveStream {
	if viewerID == uuid.Nil || s.elig.NewStreamerViewerCap <= 0 || s.elig.NewStreamerStreams <= 0 {
		return rows
	}
	asked, limit := false, 0
	var out []*postgres.LiveStream
	for i, st := range rows {
		if st.CreatorUserID != viewerID || postgres.IsTerminal(st.Status) {
			continue
		}
		if !asked {
			asked = true
			n, err := s.viewerCapFor(ctx, viewerID)
			if err != nil {
				slog.WarnContext(ctx, "live-v2: completed streams not counted; viewer_cap left off", "user_id", viewerID, "err", err)
				return rows
			}
			limit = n
		}
		if limit <= 0 {
			return rows
		}
		if out == nil {
			out = append([]*postgres.LiveStream{}, rows...)
		}
		cp := *st
		c := limit
		cp.ViewerCap = &c
		out[i] = &cp
	}
	if out == nil {
		return rows
	}
	return out
}
