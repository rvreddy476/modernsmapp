// Package standing is post-service's client for the ONE question it may ask
// trust-safety before publishing on a user's behalf: may this author
// publish right now? (Copyright Match plan section 6.4, defect P-5.)
//
//	GET {TRUST_SAFETY_SERVICE_URL}/v1/internal/standing/{userId}
//
// It replaces the composer-draft check that called the admin-only strikes
// route with the author's own X-User-Id, expected the wrong body, matched
// severities that never existed and pointed at the wrong port — every
// answer was "unknown", so a scheduled draft was retried every minute
// forever (plan section 4, P-5 a–e).
//
// # Credentials
//
// Two headers, both required by trust-safety:
//
//   - X-Internal-Service-Key: the cluster key, which trust-safety's router
//     checks first but which proves nothing on its own (the gateway stamps
//     it on edge traffic);
//   - X-Service-Authorization: Bearer <token>, an Ed25519 service token
//     (shared/servicetoken) minted per request with iss=post-service,
//     aud=trust_safety, scope [trust_safety:standing.read], a two-minute
//     TTL and no "act" claim. trust-safety admits ONLY the post-service
//     issuer on this route, whatever the scope.
//
// The signing key is post-service's own (POST_SERVICE_TOKEN_KEY, seed or
// expanded Ed25519 key, base64; POST_SERVICE_TOKEN_KID names it). Only
// its public half is registered on trust-safety. Without both the client
// is not configured (FromEnv returns ErrNotConfigured) and the service
// fails closed — never open — on every publication path.
//
// # The answer, and the cache
//
//	{"data":{"standing":"ok"|"restricted"|"suspended","policy_version":"…",
//	         "suspended_until":RFC3339|null,"active_strikes":[…]}}
//
// trust-safety alone computes standing; this client never re-derives a
// verdict from severities. "ok" allows. "restricted" and "suspended" deny,
// with the suspension end on the typed error. Anything else — a transport
// failure, a timeout, a 4xx or 5xx, a body outside the contract, a value
// outside the enum — is ErrUnknown, and every caller fails closed on it
// (founder default F-7).
//
// Answers are cached per user for CacheTTL (60 s, the server's
// Cache-Control), allow AND deny alike, so a burst of publications costs
// one call. A stale entry is revalidated with If-None-Match; a 304 renews
// it without a body. Unknown is never cached: the next call asks again
// (callers on background paths back off per item; see the service).
package standing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const (
	// Issuer is the token issuer trust-safety registers for this client.
	Issuer = "post-service"
	// Audience is the "aud" trust-safety verifies (its AudienceTrustSafety).
	Audience = "trust_safety"
	// OpStandingRead is the one operation the standing route requires.
	OpStandingRead = "trust_safety:standing.read"
	// Path is the route, with the user id appended.
	Path = "/v1/internal/standing/"

	// EnvURL, EnvTokenKey and EnvTokenKID are the environment names
	// post-service reads. DefaultURL is trust-safety's compose name and
	// port (8091 — the old default named 8118, which nothing listens on).
	EnvURL      = "TRUST_SAFETY_SERVICE_URL"
	EnvTokenKey = "POST_SERVICE_TOKEN_KEY"
	EnvTokenKID = "POST_SERVICE_TOKEN_KID"
	DefaultURL  = "http://trust-safety-service:8091"

	// HeaderServiceAuthorization carries the service token.
	HeaderServiceAuthorization = "X-Service-Authorization"
	// HeaderInternalServiceKey carries the cluster key.
	HeaderInternalServiceKey = "X-Internal-Service-Key"

	// CacheTTL is how long an answer (allow or deny) is reused. It equals
	// the server's Cache-Control max-age.
	CacheTTL = 60 * time.Second
	// RequestTimeout bounds one attempt; there is one retry (plan 6.4).
	RequestTimeout = 800 * time.Millisecond
	// TokenTTL is the lifetime of each minted token (≤ servicetoken.MaxTTL).
	TokenTTL = 2 * time.Minute
	// Attempts is the total number of tries per Check when the transport
	// fails or the server answers 5xx.
	Attempts = 2

	// maxBody caps how much of an answer is read.
	maxBody = 64 << 10
)

// Standing values on the wire. The enum is the server's; anything else is
// unknown.
const (
	StandingOK         = "ok"
	StandingRestricted = "restricted"
	StandingSuspended  = "suspended"
)

var (
	// ErrUnknown: standing could not be established. Fail closed.
	ErrUnknown = errors.New("standing unknown: trust-safety unavailable or answered outside the contract")
	// ErrDenied is the errors.Is target for every *DeniedError.
	ErrDenied = errors.New("standing denies publication")
	// ErrNotConfigured: POST_SERVICE_TOKEN_KEY or POST_SERVICE_TOKEN_KID is
	// unset, so no token can be minted and no call can succeed.
	ErrNotConfigured = errors.New(EnvTokenKey + " and " + EnvTokenKID + " are required for the trust-safety standing check")
)

// DeniedError is a "restricted" or "suspended" answer.
type DeniedError struct {
	UserID         uuid.UUID
	Standing       string
	PolicyVersion  string
	SuspendedUntil *time.Time
}

func (e *DeniedError) Error() string {
	if e.SuspendedUntil != nil {
		return fmt.Sprintf("standing %s until %s", e.Standing, e.SuspendedUntil.UTC().Format(time.RFC3339))
	}
	return "standing " + e.Standing
}

// Is makes errors.Is(err, ErrDenied) true for every DeniedError.
func (e *DeniedError) Is(target error) bool { return target == ErrDenied }

// Strike is one active strike as trust-safety reports it.
type Strike struct {
	ID        string  `json:"id"`
	Severity  string  `json:"severity"`
	Reason    string  `json:"reason"`
	CaseID    *string `json:"case_id"`
	IssuedAt  string  `json:"issued_at"`
	ExpiresAt string  `json:"expires_at"`
}

// Result is a decoded answer.
type Result struct {
	Standing       string     `json:"standing"`
	PolicyVersion  string     `json:"policy_version"`
	SuspendedUntil *time.Time `json:"suspended_until"`
	ActiveStrikes  []Strike   `json:"active_strikes"`
}

// Config builds a Client.
type Config struct {
	// BaseURL is trust-safety's origin (DefaultURL when empty).
	BaseURL string
	// InternalKey is the cluster key; sent when non-empty.
	InternalKey string
	// Signer mints the per-request token. Required.
	Signer *servicetoken.Signer
	// HTTPClient is optional; the default has RequestTimeout.
	HTTPClient *http.Client
	// Now is the clock (tests).
	Now func() time.Time
	// CacheTTL overrides the answer lifetime (tests); CacheTTL when zero.
	CacheTTL time.Duration
}

// Client asks trust-safety and remembers the answer.
type Client struct {
	baseURL     string
	internalKey string
	signer      *servicetoken.Signer
	http        *http.Client
	now         func() time.Time
	ttl         time.Duration

	mu    sync.Mutex
	cache map[uuid.UUID]*entry
}

type entry struct {
	result  *Result
	etag    string
	expires time.Time
}

// New builds a client. Signer is required.
func New(cfg Config) (*Client, error) {
	if cfg.Signer == nil {
		return nil, ErrNotConfigured
	}
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = DefaultURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: RequestTimeout}
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = CacheTTL
	}
	return &Client{
		baseURL:     base,
		internalKey: cfg.InternalKey,
		signer:      cfg.Signer,
		http:        hc,
		now:         now,
		ttl:         ttl,
		cache:       map[uuid.UUID]*entry{},
	}, nil
}

// FromEnv builds the client from TRUST_SAFETY_SERVICE_URL,
// POST_SERVICE_TOKEN_KEY and POST_SERVICE_TOKEN_KID. It returns
// ErrNotConfigured when the key or kid is blank and any other error for a
// key that does not load. internalKey is INTERNAL_SERVICE_KEY, which the
// caller already holds.
func FromEnv(getenv func(string) string, internalKey string) (*Client, error) {
	key := strings.TrimSpace(getenv(EnvTokenKey))
	kid := strings.TrimSpace(getenv(EnvTokenKID))
	if key == "" || kid == "" {
		return nil, ErrNotConfigured
	}
	signer, err := servicetoken.NewSignerFromBase64(Issuer, kid, key)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", EnvTokenKey, err)
	}
	return New(Config{
		BaseURL:     getenv(EnvURL),
		InternalKey: internalKey,
		Signer:      signer,
	})
}

// Check answers whether userID may publish now. nil allows; a *DeniedError
// (errors.Is ErrDenied) refuses; ErrUnknown means fail closed.
func (c *Client) Check(ctx context.Context, userID uuid.UUID) (*Result, error) {
	if userID == uuid.Nil {
		return nil, ErrUnknown
	}
	now := c.now()

	c.mu.Lock()
	cached := c.cache[userID]
	c.mu.Unlock()
	if cached != nil && now.Before(cached.expires) {
		return cached.result, decide(userID, cached.result)
	}

	etag := ""
	if cached != nil {
		etag = cached.etag
	}
	res, newETag, notModified, err := c.fetch(ctx, userID, etag)
	if err != nil {
		return nil, err
	}
	if notModified {
		if cached == nil {
			// A 304 with nothing to revalidate is outside the contract.
			return nil, ErrUnknown
		}
		res, newETag = cached.result, cached.etag
	}
	if err := decide(userID, res); errors.Is(err, ErrUnknown) {
		return nil, err
	}
	c.mu.Lock()
	c.cache[userID] = &entry{result: res, etag: newETag, expires: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return res, decide(userID, res)
}

// Forget drops one user's cached answer (tests, and a future
// standing_changed consumer).
func (c *Client) Forget(userID uuid.UUID) {
	c.mu.Lock()
	delete(c.cache, userID)
	c.mu.Unlock()
}

// decide maps an answer to allow / deny / unknown.
func decide(userID uuid.UUID, r *Result) error {
	if r == nil {
		return ErrUnknown
	}
	switch r.Standing {
	case StandingOK:
		return nil
	case StandingRestricted, StandingSuspended:
		return &DeniedError{UserID: userID, Standing: r.Standing, PolicyVersion: r.PolicyVersion, SuspendedUntil: r.SuspendedUntil}
	default:
		return ErrUnknown
	}
}

// fetch performs the call with one retry on transport failure or 5xx.
// A 304 returns notModified. Any other status is ErrUnknown.
func (c *Client) fetch(ctx context.Context, userID uuid.UUID, ifNoneMatch string) (res *Result, etag string, notModified bool, err error) {
	for attempt := 0; attempt < Attempts; attempt++ {
		if ctx.Err() != nil {
			return nil, "", false, ErrUnknown
		}
		res, etag, notModified, retry, err := c.once(ctx, userID, ifNoneMatch)
		if err == nil {
			return res, etag, notModified, nil
		}
		if !retry {
			break
		}
	}
	// The reason is deliberately not carried: every caller treats unknown
	// the same way, and a 401/403 body could name our credential problem.
	return nil, "", false, ErrUnknown
}

func (c *Client) once(ctx context.Context, userID uuid.UUID, ifNoneMatch string) (res *Result, etag string, notModified, retry bool, err error) {
	tok, err := c.signer.Mint(Audience, Issuer, []string{OpStandingRead}, nil, TokenTTL)
	if err != nil {
		return nil, "", false, false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+Path+userID.String(), nil)
	if err != nil {
		return nil, "", false, false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(HeaderServiceAuthorization, "Bearer "+tok)
	if c.internalKey != "" {
		req.Header.Set(HeaderInternalServiceKey, c.internalKey)
	}
	if ifNoneMatch != "" {
		req.Header.Set("If-None-Match", ifNoneMatch)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", false, true, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, "", false, true, err
	}
	switch {
	case resp.StatusCode == http.StatusNotModified:
		return nil, resp.Header.Get("ETag"), true, false, nil
	case resp.StatusCode == http.StatusOK:
		var env struct {
			Data *Result `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil || env.Data == nil {
			return nil, "", false, false, ErrUnknown
		}
		if env.Data.ActiveStrikes == nil {
			env.Data.ActiveStrikes = []Strike{}
		}
		return env.Data, resp.Header.Get("ETag"), false, false, nil
	case resp.StatusCode >= 500:
		return nil, "", false, true, fmt.Errorf("trust-safety answered %d", resp.StatusCode)
	default:
		// 401/403 (a credential problem on our side), 404, 400: retrying
		// changes nothing.
		return nil, "", false, false, fmt.Errorf("trust-safety answered %d", resp.StatusCode)
	}
}
