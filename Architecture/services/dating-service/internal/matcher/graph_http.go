package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
)

// HTTPGraphProvider implements GraphProvider against the live graph-service
// and community-service over internal HTTP. It is resilient: any error or
// timeout falls back to 0 (do not fail the Pulse request).
type HTTPGraphProvider struct {
	graphBase     string
	communityBase string
	client        *http.Client
}

// NewHTTPGraphProvider returns a provider with sane defaults. Bases default
// to the docker-compose service names if empty.
func NewHTTPGraphProvider(graphBase, communityBase string) *HTTPGraphProvider {
	if graphBase == "" {
		graphBase = "http://graph-service:8108"
	}
	if communityBase == "" {
		communityBase = "http://community-service:8109"
	}
	return &HTTPGraphProvider{
		graphBase:     graphBase,
		communityBase: communityBase,
		client:        httpclient.New(2 * time.Second),
	}
}

// FollowsOverlap is the Jaccard overlap of /v1/graph/follows/:userId for the
// two users. Errors collapse to 0.
func (h *HTTPGraphProvider) FollowsOverlap(ctx context.Context, viewer, candidate uuid.UUID) float64 {
	a, errA := h.fetchFollows(ctx, viewer)
	if errA != nil {
		slog.Warn("matcher: follows fetch failed", "user_id", viewer, "error", errA)
		return 0
	}
	b, errB := h.fetchFollows(ctx, candidate)
	if errB != nil {
		slog.Warn("matcher: follows fetch failed", "user_id", candidate, "error", errB)
		return 0
	}
	return jaccard(a, b)
}

// CommunitiesOverlap is the Jaccard overlap over the candidate set of
// community memberships.
func (h *HTTPGraphProvider) CommunitiesOverlap(ctx context.Context, viewer, candidate uuid.UUID) float64 {
	a, errA := h.fetchCommunities(ctx, viewer)
	if errA != nil {
		slog.Warn("matcher: communities fetch failed", "user_id", viewer, "error", errA)
		return 0
	}
	b, errB := h.fetchCommunities(ctx, candidate)
	if errB != nil {
		slog.Warn("matcher: communities fetch failed", "user_id", candidate, "error", errB)
		return 0
	}
	return jaccard(a, b)
}

func (h *HTTPGraphProvider) fetchFollows(ctx context.Context, userID uuid.UUID) ([]string, error) {
	url := fmt.Sprintf("%s/v1/graph/follows/%s", h.graphBase, userID.String())
	body, err := h.getJSON(ctx, url)
	if err != nil {
		return nil, err
	}
	return extractIDs(body, "user_id", "id"), nil
}

func (h *HTTPGraphProvider) fetchCommunities(ctx context.Context, userID uuid.UUID) ([]string, error) {
	// community-service exposes /v1/communities/me — when called s2s we pass
	// the candidate identity via X-User-ID so we can read either side.
	url := fmt.Sprintf("%s/v1/communities/me", h.communityBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-User-ID", userID.String())
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("community-service status %d", resp.StatusCode)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	return extractIDs(envelope.Data, "id", "community_id", "slug"), nil
}

func (h *HTTPGraphProvider) getJSON(ctx context.Context, url string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	return envelope.Data, nil
}

// extractIDs walks a JSON array of objects and pulls out the first string
// value matching any of `keys`. Returns nil on shape mismatch.
func extractIDs(raw json.RawMessage, keys ...string) []string {
	if len(raw) == 0 {
		return nil
	}
	var arr []map[string]any
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil
	}
	out := make([]string, 0, len(arr))
	for _, m := range arr {
		for _, k := range keys {
			if v, ok := m[k].(string); ok && v != "" {
				out = append(out, v)
				break
			}
		}
	}
	return out
}

// staticGraphProvider is a zero-value GraphProvider that always returns 0 —
// used when graph/community services aren't configured (e.g. local dev).
type staticGraphProvider struct{}

// NewStaticGraphProvider returns a no-op provider that yields zero overlap.
func NewStaticGraphProvider() GraphProvider { return staticGraphProvider{} }

func (staticGraphProvider) FollowsOverlap(_ context.Context, _, _ uuid.UUID) float64 {
	return 0
}
func (staticGraphProvider) CommunitiesOverlap(_ context.Context, _, _ uuid.UUID) float64 {
	return 0
}
