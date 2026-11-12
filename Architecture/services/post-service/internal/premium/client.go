// Package premium answers "does this user hold a live Momentum Premium
// role right now?" for Echo (docs/designs/echo-contract.md: a top-level
// note, a thread and a quote need a live `premium` role).
//
// The answer is a live read of identity auth-service's internal role list,
//
//	GET {AUTH_SERVICE_URL}/v1/auth/internal/roles/:userId
//	   -> {"data":{"user_id":"<uuid>","roles":["premium", ...]}}
//
// with X-Internal-Service-Key. auth-service already filters expired timed
// roles out of every role read, so a lapsed pass reads as "no premium" here
// with no clock of our own. The token's scopes are NOT used: they lag a
// grant or a revoke by up to an access-token lifetime.
//
// Answers are cached per user for CacheTTL (60 s). Only real answers are
// cached; a failure is never cached and never read as "no" or "yes": it is
// an error, and the caller fails closed (503 PREMIUM_CHECK_UNAVAILABLE).
package premium

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

	"github.com/google/uuid"
)

// RoleName is the role a live pass grants (auth-service roles.Timed()).
const RoleName = "premium"

// CacheTTL is how long one answer is reused.
const CacheTTL = 60 * time.Second

const (
	requestTimeout = 2 * time.Second
	maxBody        = 64 << 10
	callerService  = "post-service"
	// maxEntries bounds the cache; past it, expired entries are swept and,
	// if still full, the cache is cleared rather than grown.
	maxEntries = 50000
)

// ErrUnavailable wraps every failure to learn the answer.
var ErrUnavailable = errors.New("premium check unavailable")

// Checker answers whether a user holds a live premium role.
type Checker interface {
	IsPremium(ctx context.Context, userID uuid.UUID) (bool, error)
}

type entry struct {
	premium bool
	expires time.Time
}

// Client is Checker over auth-service, with the per-user cache.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
	now         func() time.Time

	mu    sync.Mutex
	cache map[uuid.UUID]entry
}

// New returns a Client for baseURL (the auth-service origin). An empty
// baseURL returns nil: the caller then has no checker and fails closed.
func New(baseURL, internalKey string) *Client {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return nil
	}
	return &Client{
		baseURL:     base,
		internalKey: internalKey,
		http: &http.Client{
			Timeout:       requestTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:   time.Now,
		cache: map[uuid.UUID]entry{},
	}
}

// WithClock swaps the clock (tests).
func (c *Client) WithClock(now func() time.Time) *Client {
	c.now = now
	return c
}

// IsPremium answers from the cache when fresh, else from auth-service.
func (c *Client) IsPremium(ctx context.Context, userID uuid.UUID) (bool, error) {
	if c == nil {
		return false, fmt.Errorf("%w: no auth-service configured", ErrUnavailable)
	}
	now := c.now()
	c.mu.Lock()
	if e, ok := c.cache[userID]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.premium, nil
	}
	c.mu.Unlock()

	premium, err := c.fetch(ctx, userID)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	if len(c.cache) >= maxEntries {
		for id, e := range c.cache {
			if !now.Before(e.expires) {
				delete(c.cache, id)
			}
		}
		if len(c.cache) >= maxEntries {
			c.cache = map[uuid.UUID]entry{}
		}
	}
	c.cache[userID] = entry{premium: premium, expires: now.Add(CacheTTL)}
	c.mu.Unlock()
	return premium, nil
}

func (c *Client) fetch(ctx context.Context, userID uuid.UUID) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v1/auth/internal/roles/"+userID.String(), nil)
	if err != nil {
		return false, fmt.Errorf("%w: build request", ErrUnavailable)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Caller-Service", callerService)
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("%w: request failed", ErrUnavailable)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return false, fmt.Errorf("%w: auth-service answered %d", ErrUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return false, fmt.Errorf("%w: read body", ErrUnavailable)
	}
	var w struct {
		Data *struct {
			UserID string    `json:"user_id"`
			Roles  *[]string `json:"roles"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &w); err != nil || w.Data == nil || w.Data.Roles == nil {
		return false, fmt.Errorf("%w: malformed body", ErrUnavailable)
	}
	if got, err := uuid.Parse(w.Data.UserID); err != nil || got != userID {
		return false, fmt.Errorf("%w: body is for a different user", ErrUnavailable)
	}
	for _, r := range *w.Data.Roles {
		if r == RoleName {
			return true, nil
		}
	}
	return false, nil
}
