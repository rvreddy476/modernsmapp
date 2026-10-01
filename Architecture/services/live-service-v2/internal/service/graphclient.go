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
// one read — the viewer's relationship to the creator (follows, and a
// block in EITHER direction). Wrapping it in an interface lets the unit
// tests substitute a fake.
type GraphClient interface {
	Relationship(ctx context.Context, viewerID, creatorID uuid.UUID) (Relationship, error)
}

// Relationship is what the live gate needs to know about viewer -> creator.
type Relationship struct {
	Follows bool
	// Blocked: either side blocked the other (graph's blocked || blocked_by;
	// every block rule on the platform is symmetric).
	Blocked bool
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

// Relationship returns the viewer's relationship to the creator. An
// unconfigured client, a transport error, a non-2xx or an answer without the
// creator's entry is an ERROR: the caller cannot rule out a block, so it
// refuses (fail closed).
func (c *HTTPGraphClient) Relationship(ctx context.Context, viewerID, creatorID uuid.UUID) (Relationship, error) {
	if c == nil || c.baseURL == "" {
		return Relationship{}, fmt.Errorf("graph client not configured")
	}
	body := map[string]any{
		"viewer_id":  viewerID.String(),
		"target_ids": []string{creatorID.String()},
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return Relationship{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/graph/relationships/batch",
		jsonReader(buf))
	if err != nil {
		return Relationship{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Relationship{}, fmt.Errorf("graph relationships/batch: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return Relationship{}, fmt.Errorf("graph relationships/batch: status %d", resp.StatusCode)
	}
	// graph-service returns a raw map[uuid]Relationship at the top
	// level, not wrapped in the api envelope (see handler.go:1126).
	var out map[string]struct {
		Follows   bool `json:"follows"`
		Blocked   bool `json:"blocked"`
		BlockedBy bool `json:"blocked_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return Relationship{}, err
	}
	rel, ok := out[creatorID.String()]
	if !ok {
		return Relationship{}, fmt.Errorf("graph relationships/batch: no entry for the creator")
	}
	return Relationship{Follows: rel.Follows, Blocked: rel.Blocked || rel.BlockedBy}, nil
}
