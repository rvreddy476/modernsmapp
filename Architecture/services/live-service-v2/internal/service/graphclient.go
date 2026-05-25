package service

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// GraphClient is the surface live-service-v2 needs from graph-service:
// a single read — does `viewer` follow `creator`. Wrapping it in an
// interface lets the unit tests substitute a fake.
type GraphClient interface {
	IsFollowing(ctx context.Context, viewerID, creatorID uuid.UUID) (bool, error)
}

// HTTPGraphClient calls graph-service's relationship-batch endpoint with
// X-Internal-Service-Key. See graph_service_internal_key memory note —
// callers MUST send this header or graph-service returns 401.
type HTTPGraphClient struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func NewHTTPGraphClient(baseURL, internalKey string) *HTTPGraphClient {
	return &HTTPGraphClient{
		baseURL:     baseURL,
		internalKey: internalKey,
		http:        &http.Client{Timeout: 4 * time.Second},
	}
}

// IsFollowing returns true iff viewer follows creator. On a nil receiver
// or unconfigured base URL, returns (false, nil) — the caller treats
// this as "deny" for followers-only streams.
func (c *HTTPGraphClient) IsFollowing(ctx context.Context, viewerID, creatorID uuid.UUID) (bool, error) {
	if c == nil || c.baseURL == "" {
		return false, nil
	}
	body := map[string]any{
		"viewer_id":  viewerID.String(),
		"target_ids": []string{creatorID.String()},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/graph/relationships/batch",
		jsonReader(buf))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, fmt.Errorf("graph relationships/batch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("graph relationships/batch: status %d", resp.StatusCode)
	}
	// graph-service returns a raw map[uuid]Relationship at the top
	// level, not wrapped in the api envelope (see handler.go:1126).
	var out map[string]struct {
		Follows bool `json:"follows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, err
	}
	rel, ok := out[creatorID.String()]
	if !ok {
		return false, nil
	}
	return rel.Follows, nil
}
