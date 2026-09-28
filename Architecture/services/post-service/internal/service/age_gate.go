package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Age-restricted posts (Creator Hub, 2026-09-28; migration 052).

	A post with age_restricted = true opens only to its owner and to signed-in
	viewers whose date of birth says 18 or over:

	  anonymous viewer                 -> 401 AGE_RESTRICTED_SIGN_IN (ErrAgeSignIn)
	  date of birth says under 18      -> 403 AGE_RESTRICTED         (ErrAgeRestricted)
	  no date of birth, or the lookup
	  failed                           -> 403 AGE_UNVERIFIED         (ErrAgeUnverified)

	The gate runs AFTER the visibility gate, so a viewer who may not see the
	post at all still gets the plain 404 and learns nothing about its age
	setting. Reads that list posts drop an age-restricted post for the same
	viewers instead of refusing the page (ageAllowance, applied by
	attachMediaStateToDetails, GetPostsByIDs, the reel feed and media access).

	Date of birth: identity-profile's service-only identity read (the one
	dating-service uses), keyed by the caller's user id:

	  GET {PROFILE_SERVICE_URL}/internal/v1/profiles/users/:userId/identity
	  X-Internal-Service-Key, X-Caller-Service: post-service, NO user headers
	  -> {"data":{"user_id","first_name","dob":"YYYY-MM-DD"|null,"dob_source"}}

	A 404 (unknown / shut account) is "no date of birth". Anything else that
	is not a 200 with a well-formed body is an error, and an error fails
	closed. The date of birth never appears in a log line or an error.
*/

var (
	ErrAgeSignIn     = errors.New("sign in to confirm your age to watch this")
	ErrAgeRestricted = errors.New("this content is age-restricted")
	ErrAgeUnverified = errors.New("confirm your date of birth to watch age-restricted content")
)

// AdultAge is the age an age-restricted post requires.
const AdultAge = 18

// birthDateSource answers a user's date of birth; (nil, nil) is "not known".
type birthDateSource interface {
	BirthDate(ctx context.Context, userID uuid.UUID) (*time.Time, error)
}

// SetBirthDateSource wires the date-of-birth lookup (main.go).
func (s *Service) SetBirthDateSource(src birthDateSource) { s.birthDates = src }

func (s *Service) clock() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

// isAdultOn reports whether someone born on dob is AdultAge or older on day
// now (calendar arithmetic in UTC: the 18th birthday itself counts).
func isAdultOn(dob, now time.Time) bool {
	now = now.UTC()
	birthday := time.Date(dob.Year()+AdultAge, dob.Month(), dob.Day(), 0, 0, 0, 0, time.UTC)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	return !birthday.After(today)
}

// viewerAgeVerdict is the per-viewer answer, independent of any post: nil
// for an adult, else the refusal an age-restricted post returns.
func (s *Service) viewerAgeVerdict(ctx context.Context, viewerID *uuid.UUID) error {
	if viewerID == nil || *viewerID == uuid.Nil {
		return ErrAgeSignIn
	}
	if s.birthDates == nil {
		return ErrAgeUnverified
	}
	dob, err := s.birthDates.BirthDate(ctx, *viewerID)
	if err != nil {
		slog.WarnContext(ctx, "age gate: date of birth lookup failed; refusing", "viewer_id", *viewerID, "err", err)
		return ErrAgeUnverified
	}
	if dob == nil {
		return ErrAgeUnverified
	}
	if !isAdultOn(*dob, s.clock()) {
		return ErrAgeRestricted
	}
	return nil
}

// checkAgeGate is the single-post gate: nil when p is not age-restricted,
// when the viewer is its owner, or when the viewer is an adult.
func (s *Service) checkAgeGate(ctx context.Context, p *postgres.Post, viewerID *uuid.UUID) error {
	if p == nil || !p.AgeRestricted {
		return nil
	}
	if viewerID != nil && *viewerID == p.AuthorID {
		return nil
	}
	return s.viewerAgeVerdict(ctx, viewerID)
}

// ageAllowance answers "may this viewer see age-restricted posts" at most
// once per page: the lookup runs only when the page holds one.
func (s *Service) ageAllowance(ctx context.Context, viewerID *uuid.UUID) func(p *postgres.Post) bool {
	checked, adult := false, false
	return func(p *postgres.Post) bool {
		if p == nil || !p.AgeRestricted {
			return true
		}
		if viewerID != nil && *viewerID == p.AuthorID {
			return true
		}
		if !checked {
			checked = true
			adult = s.viewerAgeVerdict(ctx, viewerID) == nil
		}
		return adult
	}
}

// ── identity-profile client ────────────────────────────────────────────────

const (
	birthDateTimeout   = 2 * time.Second
	birthDateKnownTTL  = 10 * time.Minute
	birthDateAbsentTTL = 2 * time.Minute
	birthDateCacheMax  = 50000
	maxIdentityBody    = 64 << 10
)

type birthDateEntry struct {
	dob     *time.Time
	expires time.Time
}

// IdentityBirthDates reads dates of birth from identity-profile, caching
// each answer per viewer (a known date for 10 minutes, "not known" for 2 so
// someone who just added one is not kept out long). Errors are not cached.
type IdentityBirthDates struct {
	baseURL string
	key     string
	http    *http.Client
	now     func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]birthDateEntry
}

// NewIdentityBirthDates builds the client. baseURL is identity-profile's
// origin (PROFILE_SERVICE_URL); key is INTERNAL_SERVICE_KEY. An empty base
// URL answers every lookup with an error, so the gate fails closed.
func NewIdentityBirthDates(baseURL, key string) *IdentityBirthDates {
	return &IdentityBirthDates{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		key:     key,
		http: &http.Client{
			Timeout: birthDateTimeout,
			// Never follow a redirect: it would resend the internal key.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:   time.Now,
		cache: map[uuid.UUID]birthDateEntry{},
	}
}

// BirthDate implements birthDateSource.
func (c *IdentityBirthDates) BirthDate(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	now := c.now()
	c.mu.Lock()
	if e, ok := c.cache[userID]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.dob, nil
	}
	c.mu.Unlock()

	dob, err := c.fetch(ctx, userID)
	if err != nil {
		return nil, err
	}
	ttl := birthDateAbsentTTL
	if dob != nil {
		ttl = birthDateKnownTTL
	}
	c.mu.Lock()
	if len(c.cache) >= birthDateCacheMax {
		c.cache = map[uuid.UUID]birthDateEntry{}
	}
	c.cache[userID] = birthDateEntry{dob: dob, expires: now.Add(ttl)}
	c.mu.Unlock()
	return dob, nil
}

func (c *IdentityBirthDates) fetch(ctx context.Context, userID uuid.UUID) (*time.Time, error) {
	if c.baseURL == "" {
		return nil, errors.New("identity-profile URL not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/internal/v1/profiles/users/"+userID.String()+"/identity", nil)
	if err != nil {
		return nil, fmt.Errorf("build identity request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", "post-service")
	if c.key != "" {
		req.Header.Set("X-Internal-Service-Key", c.key)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity request: %w", err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("identity-profile returned %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxIdentityBody))
	if err != nil {
		return nil, errors.New("read identity body")
	}
	return decodeBirthDate(body, userID)
}

// decodeBirthDate maps the 200 body. Error messages are fixed text: a parse
// error could quote the date.
func decodeBirthDate(body []byte, userID uuid.UUID) (*time.Time, error) {
	var w struct {
		Data *struct {
			UserID    string  `json:"user_id"`
			DoB       *string `json:"dob"`
			DoBSource string  `json:"dob_source"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil {
		return nil, errors.New("malformed identity body")
	}
	if got, err := uuid.Parse(w.Data.UserID); err != nil || got != userID {
		return nil, errors.New("identity body is for a different user")
	}
	if w.Data.DoB == nil || strings.TrimSpace(*w.Data.DoB) == "" {
		return nil, nil
	}
	switch w.Data.DoBSource {
	case "registration", "profile":
	default:
		// A date nobody stands behind is not a verified age.
		return nil, nil
	}
	dob, err := time.Parse("2006-01-02", strings.TrimSpace(*w.Data.DoB))
	if err != nil {
		return nil, errors.New("identity dob is not YYYY-MM-DD")
	}
	return &dob, nil
}
