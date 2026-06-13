// Package identityclient is a thin HTTP client to the identity-platform
// profile-service. user-service uses it to repair its local app.users
// projection from the identity source of truth when a UserRegistered Kafka
// event was missed — both on-demand (read-through) and in the background
// reconcile job.
package identityclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
)

// ErrNotConfigured is returned by every call when the client has no base URL
// (PROFILE_SERVICE_URL unset). Callers treat it as "cannot repair right now".
var ErrNotConfigured = errors.New("identity client not configured")

// Profile is the subset of the profile-service record that the user-service
// projection (app.users) needs. Fields mirror profile-service's JSON.
type Profile struct {
	UserID        uuid.UUID  `json:"user_id"`
	Username      *string    `json:"username"`
	DisplayName   string     `json:"display_name"`
	FirstName     *string    `json:"first_name"`
	LastName      *string    `json:"last_name"`
	Bio           string     `json:"bio"`
	Category      string     `json:"category"`
	Profession    string     `json:"profession"`
	Website       string     `json:"website"`
	Location      string     `json:"location"`
	BadgeFlags    int        `json:"badge_flags"`
	IsVerified    bool       `json:"is_verified"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	CoverMediaID  *uuid.UUID `json:"cover_media_id"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// Client talks to profile-service over HTTP.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// New builds a client. baseURL is e.g. http://identity-profile:8098. An empty
// baseURL yields a client whose calls all return ErrNotConfigured, so callers
// degrade gracefully when the dependency is not wired.
func New(baseURL, internalKey string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		http:        httpclient.NewWithBreaker(5*time.Second, "user->profile"),
	}
}

// GetProfile fetches one profile. Returns (nil, nil) when profile-service has
// no such user (404) — letting callers distinguish "not in identity at all"
// from a transport error.
func (c *Client) GetProfile(ctx context.Context, id uuid.UUID) (*Profile, error) {
	if c == nil || c.baseURL == "" {
		return nil, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/v1/profiles/%s", c.baseURL, id), nil)
	if err != nil {
		return nil, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var env struct {
			Data Profile `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
			return nil, err
		}
		return &env.Data, nil
	case http.StatusNotFound:
		return nil, nil
	default:
		return nil, fmt.Errorf("profile-service GET profile %s: status %d", id, resp.StatusCode)
	}
}

// ChangesPage is one page of the profile-changes feed.
type ChangesPage struct {
	Items     []Profile
	NextSince time.Time
	Count     int
}

// ListChangedProfiles fetches profiles updated at or after `since`,
// oldest-change-first — the feed the reconcile job pages through. A zero
// `since` requests a full snapshot.
func (c *Client) ListChangedProfiles(ctx context.Context, since time.Time, limit int) (*ChangesPage, error) {
	if c == nil || c.baseURL == "" {
		return nil, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/v1/profiles/changes", c.baseURL), nil)
	if err != nil {
		return nil, err
	}
	q := req.URL.Query()
	if !since.IsZero() {
		q.Set("since", since.UTC().Format(time.RFC3339Nano))
	}
	q.Set("limit", fmt.Sprintf("%d", limit))
	req.URL.RawQuery = q.Encode()
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("profile-service GET changes: status %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			Items     []Profile `json:"items"`
			Count     int       `json:"count"`
			NextSince time.Time `json:"next_since"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	return &ChangesPage{Items: env.Data.Items, NextSince: env.Data.NextSince, Count: env.Data.Count}, nil
}

// CountProfiles returns the total number of profiles in the identity source —
// the master count the projection health check compares the local count to.
func (c *Client) CountProfiles(ctx context.Context) (int64, error) {
	if c == nil || c.baseURL == "" {
		return 0, ErrNotConfigured
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/v1/profiles/discover?limit=1&offset=0", c.baseURL), nil)
	if err != nil {
		return 0, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("profile-service GET discover: status %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			Meta struct {
				Total int64 `json:"total"`
			} `json:"meta"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return 0, err
	}
	return env.Data.Meta.Total, nil
}
