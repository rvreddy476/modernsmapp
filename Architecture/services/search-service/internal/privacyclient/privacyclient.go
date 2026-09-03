// Package privacyclient reads a user's account_visibility from the identity
// user-service (GET /v1/users/{id}/settings, internal-key gated) and caches
// the answer for 60s.
//
// The index needs the author's visibility at WRITE time — a search query
// cannot join to the settings table — so every path that (re)indexes a user
// or a post asks here. The settings-changed consumer keeps the index fresh
// afterwards and invalidates this cache, so the 60s window only ever covers
// the reindex/auto-heal paths that walk profiles in bulk.
package privacyclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

const cacheTTL = 60 * time.Second

// Lookup is what the indexer and the reindexer consume.
type Lookup interface {
	// IsPrivate reports whether the account is private. An unknown user
	// (404) is public — a brand-new registration whose settings row has not
	// been written yet defaults to public, and the settings-changed event
	// corrects it the moment that changes.
	IsPrivate(ctx context.Context, userID string) (bool, error)
	// Invalidate drops the cached answer for one user.
	Invalidate(userID string)
}

type entry struct {
	private bool
	expires time.Time
}

// Client is the HTTP implementation of Lookup.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client

	mu    sync.Mutex
	cache map[string]entry
	now   func() time.Time
}

// New returns a client for the identity user-service at baseURL. An empty
// baseURL yields a client whose lookups fail with an explicit error — the
// caller decides what that means for its path.
func New(baseURL, internalKey string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		http:        &http.Client{Timeout: 3 * time.Second},
		cache:       map[string]entry{},
		now:         time.Now,
	}
}

// Configured reports whether a base URL was provided.
func (c *Client) Configured() bool { return c != nil && c.baseURL != "" }

func (c *Client) IsPrivate(ctx context.Context, userID string) (bool, error) {
	if c == nil || c.baseURL == "" {
		return false, fmt.Errorf("identity user-service URL not configured")
	}
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return false, fmt.Errorf("empty user id")
	}
	now := c.now()
	c.mu.Lock()
	if e, ok := c.cache[userID]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.private, nil
	}
	c.mu.Unlock()

	private, err := c.fetch(ctx, userID)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	c.cache[userID] = entry{private: private, expires: now.Add(cacheTTL)}
	if len(c.cache) > 100000 {
		for k, e := range c.cache {
			if now.After(e.expires) {
				delete(c.cache, k)
			}
		}
	}
	c.mu.Unlock()
	return private, nil
}

func (c *Client) Invalidate(userID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	delete(c.cache, userID)
	c.mu.Unlock()
}

// Prime records a known answer (from a settings-changed event) so the next
// index write does not round-trip for a value we were just told.
func (c *Client) Prime(userID string, private bool) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.cache[userID] = entry{private: private, expires: c.now().Add(cacheTTL)}
	c.mu.Unlock()
}

func (c *Client) fetch(ctx context.Context, userID string) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/users/"+userID+"/settings", nil)
	if err != nil {
		return false, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("identity settings lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return false, fmt.Errorf("identity settings lookup returned %d: %s", resp.StatusCode, string(b))
	}
	var body struct {
		Data struct {
			AccountVisibility string `json:"account_visibility"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return false, fmt.Errorf("decode identity settings: %w", err)
	}
	return strings.EqualFold(body.Data.AccountVisibility, "private"), nil
}
