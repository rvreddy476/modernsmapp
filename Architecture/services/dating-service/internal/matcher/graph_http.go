package matcher

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/httpclient"
	"github.com/google/uuid"
)

// internalKeyHeader is the header shared/middleware.RequireInternalKey
// checks. graph-service and community-service both apply it to every route,
// so a keyless call is 401.
const internalKeyHeader = "X-Internal-Service-Key"

const (
	// followingIDsLimit is graph-service's cap on /v1/graph/:userId/following-ids.
	followingIDsLimit = 500
	// community-service pages /v1/communities/my at most 100 per request.
	communityPageSize = 100
	communityMaxPages = 5
)

// HTTPGraphProvider implements GraphProvider against the live graph-service
// and community-service over internal HTTP. It is resilient: any error or
// timeout falls back to 0 (do not fail the Pulse request).
type HTTPGraphProvider struct {
	graphBase     string
	communityBase string
	internalKey   string
	client        *http.Client
}

// NewHTTPGraphProvider returns a provider with sane defaults. Bases default
// to the docker-compose service names if empty. internalKey is sent to both.
func NewHTTPGraphProvider(graphBase, communityBase, internalKey string) *HTTPGraphProvider {
	if graphBase == "" {
		graphBase = "http://graph-service:8083"
	}
	if communityBase == "" {
		communityBase = "http://community-service:8107"
	}
	return &HTTPGraphProvider{
		graphBase:     strings.TrimRight(graphBase, "/"),
		communityBase: strings.TrimRight(communityBase, "/"),
		internalKey:   internalKey,
		client:        httpclient.New(2 * time.Second),
	}
}

// FollowsOverlap is the Jaccard overlap of the two users' following sets
// (graph-service /v1/graph/:userId/following-ids, at most 500 each).
// Errors collapse to 0.
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

// fetchFollows reads the ids userID follows. The route answers
// {"data":{"items":["<uuid>",...],"count":n}}.
func (h *HTTPGraphProvider) fetchFollows(ctx context.Context, userID uuid.UUID) ([]string, error) {
	url := fmt.Sprintf("%s/v1/graph/%s/following-ids?limit=%d", h.graphBase, userID.String(), followingIDsLimit)
	body, err := h.getJSON(ctx, url, "")
	if err != nil {
		return nil, err
	}
	var data struct {
		Items []string `json:"items"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("decode following-ids: %w", err)
	}
	return data.Items, nil
}

// fetchCommunities reads the communities userID is an active member of from
// community-service GET /v1/communities/my (X-User-Id names the user; banned
// and pending memberships are already excluded). The route pages at most 100
// per request and answers {"data":[{"id":...},...]}.
func (h *HTTPGraphProvider) fetchCommunities(ctx context.Context, userID uuid.UUID) ([]string, error) {
	var out []string
	for page := 0; page < communityMaxPages; page++ {
		url := fmt.Sprintf("%s/v1/communities/my?limit=%d&offset=%d", h.communityBase, communityPageSize, page*communityPageSize)
		body, err := h.getJSON(ctx, url, userID.String())
		if err != nil {
			return nil, err
		}
		var data []struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, fmt.Errorf("decode communities: %w", err)
		}
		for _, d := range data {
			out = append(out, d.ID)
		}
		if len(data) < communityPageSize {
			break
		}
	}
	return out, nil
}

// getJSON GETs an internal route with the internal key and returns the
// envelope's data. actingUser, when set, goes out as X-User-Id.
func (h *HTTPGraphProvider) getJSON(ctx context.Context, url, actingUser string) (json.RawMessage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if h.internalKey != "" {
		req.Header.Set(internalKeyHeader, h.internalKey)
	}
	if actingUser != "" {
		req.Header.Set("X-User-Id", actingUser)
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
