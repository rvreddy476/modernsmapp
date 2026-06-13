// Package graphclient calls graph-service's GET /v1/graph/:userId/following-ids
// to fetch the slice of the follow graph used for author-affinity boosts
// in function_score ranking. Results are cached in Redis for 60s under
// search:follows:{viewerID} so a typical typeahead burst (one keystroke
// per ~200ms) doesn't hammer graph-service.
package graphclient

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const cacheTTL = 60 * time.Second

// Client wraps an HTTP client + Redis cache. baseURL is graph-service's
// internal address; internalKey is forwarded as X-Internal-Service-Key.
// rdb may be nil — the lookup just bypasses cache in that case.
type Client struct {
	baseURL     string
	internalKey string
	httpClient  *http.Client
	rdb         *redis.Client
}

func New(baseURL, internalKey string, rdb *redis.Client) *Client {
	return &Client{
		baseURL:     baseURL,
		internalKey: internalKey,
		httpClient:  &http.Client{Timeout: 3 * time.Second},
		rdb:         rdb,
	}
}

// FollowingIDs returns up to `limit` user IDs that viewerID follows.
// Returns nil + nil error when:
//   - viewerID is uuid.Nil (anonymous viewer)
//   - graph-service is unreachable (we degrade to no-affinity ranking
//     rather than failing the search)
//   - baseURL was never configured (dev mode without graph-service)
//
// Callers should treat the result as a best-effort hint, not a
// guarantee. Cached 60s in Redis if rdb is non-nil.
func (c *Client) FollowingIDs(ctx context.Context, viewerID uuid.UUID, limit int) []string {
	if c == nil || c.baseURL == "" {
		return nil
	}
	if viewerID == uuid.Nil {
		return nil
	}
	if limit <= 0 || limit > 500 {
		limit = 500
	}

	cacheKey := "search:follows:" + viewerID.String()
	if c.rdb != nil {
		if raw, err := c.rdb.Get(ctx, cacheKey).Result(); err == nil && raw != "" {
			var cached []string
			if err := json.Unmarshal([]byte(raw), &cached); err == nil {
				return cached
			}
		}
	}

	ids, err := c.fetch(ctx, viewerID, limit)
	if err != nil {
		// Degrade silently: an unhealthy graph-service should not
		// take search down. Log at WARN so ops sees it.
		slog.Warn("search: graphclient FollowingIDs failed; ranking without affinity", "viewer", viewerID, "err", err)
		return nil
	}

	if c.rdb != nil && len(ids) > 0 {
		if data, err := json.Marshal(ids); err == nil {
			_ = c.rdb.Set(ctx, cacheKey, data, cacheTTL).Err()
		}
	}
	return ids
}

func (c *Client) fetch(ctx context.Context, viewerID uuid.UUID, limit int) ([]string, error) {
	url := fmt.Sprintf("%s/v1/graph/%s/following-ids?limit=%d", c.baseURL, viewerID.String(), limit)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	req.Header.Set("X-User-Id", viewerID.String()) // owner-self view bypasses privacy gates

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph-service returned %d: %s", resp.StatusCode, string(body))
	}
	var parsed struct {
		Data struct {
			Items []string `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, err
	}
	return parsed.Data.Items, nil
}
