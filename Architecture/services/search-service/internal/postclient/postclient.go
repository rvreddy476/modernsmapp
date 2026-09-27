// Package postclient walks post-service's internal Tube catalogue listing
// (GET /internal/tube/channels, GET /internal/tube/playlists) for the tube
// reindex.
//
// It is a walk and not an event feed because post-service publishes no
// channel or playlist lifecycle events: there is nothing to subscribe to.
// The listing is keyset-paged over the surrogate id, so a walk sees every
// row exactly once regardless of how many are added while it runs.
//
// FAILURES ARE ERRORS. A page that cannot be read leaves the reindex not
// knowing what is in the catalogue; returning an empty page would end the
// walk early and report a clean run over a partial index.
package postclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured means POST_SERVICE_URL was never set.
var ErrNotConfigured = errors.New("postclient: POST_SERVICE_URL not configured")

// TubeChannel mirrors post-service's tubeChannelListRow. Unknown fields
// are ignored, so post-service may add one without a coordinated deploy.
type TubeChannel struct {
	ID            string    `json:"id"`
	OwnerID       string    `json:"owner_id"`
	Name          string    `json:"name"`
	Handle        string    `json:"handle"`
	About         string    `json:"about"`
	AvatarMediaID *string   `json:"avatar_media_id"`
	FollowerCount int       `json:"follower_count"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TubePlaylist mirrors post-service's tubePlaylistListRow. For a
// non-public playlist only ID and Visibility are populated.
type TubePlaylist struct {
	ID           string     `json:"id"`
	OwnerID      *string    `json:"owner_id"`
	Title        string     `json:"title"`
	Description  string     `json:"description"`
	Visibility   string     `json:"visibility"`
	ItemCount    int        `json:"item_count"`
	CoverMediaID *string    `json:"cover_media_id"`
	CoverURL     *string    `json:"cover_url"`
	CreatedAt    *time.Time `json:"created_at"`
	UpdatedAt    *time.Time `json:"updated_at"`
}

// Page is one keyset page of either listing.
type Page[T any] struct {
	Items     []T
	NextAfter string
	HasMore   bool
}

// Client calls post-service.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// New returns a client for post-service at baseURL; internalKey is
// forwarded as X-Internal-Service-Key.
func New(baseURL, internalKey string) *Client {
	return &Client{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		http:        &http.Client{Timeout: 15 * time.Second},
	}
}

// Configured reports whether a base URL was given.
func (c *Client) Configured() bool { return c != nil && c.baseURL != "" }

// ListTubeChannels reads one page of channels with id > after.
func (c *Client) ListTubeChannels(ctx context.Context, after string, limit int) (Page[TubeChannel], error) {
	return listPage[TubeChannel](ctx, c, "/internal/tube/channels", after, limit)
}

// ListTubePlaylists reads one page of playlists (every visibility) with
// id > after.
func (c *Client) ListTubePlaylists(ctx context.Context, after string, limit int) (Page[TubePlaylist], error) {
	return listPage[TubePlaylist](ctx, c, "/internal/tube/playlists", after, limit)
}

func listPage[T any](ctx context.Context, c *Client, path, after string, limit int) (Page[T], error) {
	var page Page[T]
	if !c.Configured() {
		return page, ErrNotConfigured
	}
	q := url.Values{}
	q.Set("limit", strconv.Itoa(limit))
	if after != "" {
		q.Set("after", after)
	}
	endpoint := c.baseURL + path + "?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return page, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return page, fmt.Errorf("postclient: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return page, fmt.Errorf("postclient: read %s: %w", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		return page, fmt.Errorf("postclient: %s returned %d: %s", path, resp.StatusCode, truncate(body))
	}
	var parsed struct {
		Data struct {
			Items     []T    `json:"items"`
			NextAfter string `json:"next_after"`
			HasMore   bool   `json:"has_more"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return page, fmt.Errorf("postclient: decode %s: %w", path, err)
	}
	page.Items = parsed.Data.Items
	page.NextAfter = parsed.Data.NextAfter
	page.HasMore = parsed.Data.HasMore
	return page, nil
}

func truncate(b []byte) string {
	const max = 300
	if len(b) <= max {
		return string(b)
	}
	return string(b[:max]) + "…(" + strconv.Itoa(len(b)) + " bytes)"
}
