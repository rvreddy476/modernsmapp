package service

// What live-service-v2 reads from other services for the live surfaces
// (2 Oct 2026). Every call carries X-Internal-Service-Key and reuses a route
// another service already calls; nothing was added to the services read.
//
//   - Profiles: identity-profile POST /v1/profiles/batch {"user_ids":[...]}
//     -> {"<uuid>":{username, display_name, avatar_url, ...}} (a raw map, the
//     route feed-service and post-service hydrate authors from). No X-User-Id
//     is sent, so the answer does not depend on the viewer and can be cached.
//   - Categories: post-service GET /v1/posts/categories ->
//     {"data":[{id, slug, label, kind}]}.
//   - Following: graph-service GET /v1/graph/:userId/following-ids?limit=500
//     -> {"data":{"items":[...]}} and post-service
//     GET /internal/users/:userId/subscribed-owner-ids?after&limit ->
//     {"data":{"owner_ids":[...], "next_after", "has_more"}}.
//   - Relationships, many creators at once: graph-service
//     POST /v1/graph/relationships/batch (the route Relationship already
//     calls with one target).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Profile is the part of a public profile a user card shows.
type Profile struct {
	Name      string
	Handle    string
	AvatarURL string
}

// ProfileSource reads public profiles. A user missing from the answer has
// no visible profile (deleted or hidden account).
type ProfileSource interface {
	Profiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error)
}

// Category is one entry of post-service's video taxonomy.
type Category struct {
	Slug  string
	Label string
}

// CategorySource reads the taxonomy.
type CategorySource interface {
	Categories(ctx context.Context) ([]Category, error)
}

// FollowingSource answers who a viewer follows or subscribes to.
type FollowingSource interface {
	// FollowedCreatorIDs returns the users the viewer follows together with
	// the owners of the channels the viewer subscribes to. An error means
	// the list is unknown, never "nobody".
	FollowedCreatorIDs(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error)
}

// batchGraph is implemented by a GraphClient that can answer many creators
// in one call.
type batchGraph interface {
	Relationships(ctx context.Context, viewerID uuid.UUID, creatorIDs []uuid.UUID) (map[uuid.UUID]Relationship, error)
}

const directoryTimeout = 3 * time.Second

func getJSON(ctx context.Context, client *http.Client, method, rawURL, internalKey string, body io.Reader, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", internalKey)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("%s %s: status %d", method, req.URL.Path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// --- profiles ---

// HTTPProfiles is ProfileSource over identity-profile.
type HTTPProfiles struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// NewHTTPProfiles returns nil when baseURL is empty (cards then carry the
// user id only).
func NewHTTPProfiles(baseURL, internalKey string) *HTTPProfiles {
	if strings.TrimSpace(baseURL) == "" {
		return nil
	}
	return &HTTPProfiles{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, http: &http.Client{Timeout: directoryTimeout}}
}

// profileBatchMax bounds one batch call.
const profileBatchMax = 100

func (c *HTTPProfiles) Profiles(ctx context.Context, userIDs []uuid.UUID) (map[uuid.UUID]Profile, error) {
	out := make(map[uuid.UUID]Profile, len(userIDs))
	for start := 0; start < len(userIDs); start += profileBatchMax {
		end := start + profileBatchMax
		if end > len(userIDs) {
			end = len(userIDs)
		}
		ids := make([]string, 0, end-start)
		for _, id := range userIDs[start:end] {
			ids = append(ids, id.String())
		}
		body, err := json.Marshal(map[string]any{"user_ids": ids})
		if err != nil {
			return nil, err
		}
		var page map[uuid.UUID]*struct {
			Username    *string `json:"username"`
			DisplayName string  `json:"display_name"`
			AvatarURL   *string `json:"avatar_url"`
		}
		if err := getJSON(ctx, c.http, http.MethodPost, c.baseURL+"/v1/profiles/batch", c.internalKey, jsonReader(body), &page); err != nil {
			return nil, fmt.Errorf("profiles batch: %w", err)
		}
		for id, p := range page {
			if p == nil {
				continue
			}
			prof := Profile{Name: p.DisplayName}
			if p.Username != nil {
				prof.Handle = *p.Username
			}
			if p.AvatarURL != nil {
				prof.AvatarURL = *p.AvatarURL
			}
			out[id] = prof
		}
	}
	return out, nil
}

// --- categories ---

// HTTPCategories is CategorySource over post-service.
type HTTPCategories struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// NewHTTPCategories returns nil when baseURL is empty.
func NewHTTPCategories(baseURL, internalKey string) *HTTPCategories {
	if strings.TrimSpace(baseURL) == "" {
		return nil
	}
	return &HTTPCategories{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, http: &http.Client{Timeout: directoryTimeout}}
}

func (c *HTTPCategories) Categories(ctx context.Context) ([]Category, error) {
	var env struct {
		Data []struct {
			ID    string `json:"id"`
			Slug  string `json:"slug"`
			Label string `json:"label"`
		} `json:"data"`
	}
	if err := getJSON(ctx, c.http, http.MethodGet, c.baseURL+"/v1/posts/categories", c.internalKey, nil, &env); err != nil {
		return nil, fmt.Errorf("post-service categories: %w", err)
	}
	out := make([]Category, 0, len(env.Data))
	for _, d := range env.Data {
		slug := d.Slug
		if slug == "" {
			slug = d.ID // the list was {id, label} before `slug` was added
		}
		if slug == "" {
			continue
		}
		out = append(out, Category{Slug: slug, Label: d.Label})
	}
	if len(out) == 0 {
		// An empty taxonomy is not an answer: treating it as one would
		// refuse every category for ten minutes.
		return nil, errors.New("post-service categories: empty list")
	}
	return out, nil
}

// --- following ---

// HTTPFollowing is FollowingSource over graph-service (follows) and
// post-service (channel subscriptions).
type HTTPFollowing struct {
	graphURL    string
	postURL     string
	internalKey string
	http        *http.Client
}

// NewHTTPFollowing returns nil unless BOTH base URLs are set: half a list
// would silently hide streams, so without both the Following filter answers
// an empty list.
func NewHTTPFollowing(graphURL, postURL, internalKey string) *HTTPFollowing {
	if strings.TrimSpace(graphURL) == "" || strings.TrimSpace(postURL) == "" {
		return nil
	}
	return &HTTPFollowing{
		graphURL: strings.TrimRight(graphURL, "/"), postURL: strings.TrimRight(postURL, "/"),
		internalKey: internalKey, http: &http.Client{Timeout: directoryTimeout},
	}
}

const (
	// followingIDsMax is graph-service's own cap on following-ids.
	followingIDsMax = 500
	// subscribedOwnersPage / subscribedOwnersMaxPages bound the subscription
	// walk (5,000 channels).
	subscribedOwnersPage     = 1000
	subscribedOwnersMaxPages = 5
)

func (c *HTTPFollowing) FollowedCreatorIDs(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	add := func(raw string) error {
		id, err := uuid.Parse(raw)
		if err != nil {
			return fmt.Errorf("invalid id in answer")
		}
		if id != uuid.Nil && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
		return nil
	}

	var follows struct {
		Data struct {
			Items []string `json:"items"`
		} `json:"data"`
	}
	u := fmt.Sprintf("%s/v1/graph/%s/following-ids?limit=%d", c.graphURL, viewerID, followingIDsMax)
	if err := getJSON(ctx, c.http, http.MethodGet, u, c.internalKey, nil, &follows); err != nil {
		return nil, fmt.Errorf("graph following-ids: %w", err)
	}
	for _, raw := range follows.Data.Items {
		if err := add(raw); err != nil {
			return nil, fmt.Errorf("graph following-ids: %w", err)
		}
	}

	after := ""
	for page := 0; page < subscribedOwnersMaxPages; page++ {
		q := url.Values{"limit": {fmt.Sprint(subscribedOwnersPage)}}
		if after != "" {
			q.Set("after", after)
		}
		var subs struct {
			Data struct {
				OwnerIDs  []string `json:"owner_ids"`
				NextAfter string   `json:"next_after"`
				HasMore   bool     `json:"has_more"`
			} `json:"data"`
		}
		u := fmt.Sprintf("%s/internal/users/%s/subscribed-owner-ids?%s", c.postURL, viewerID, q.Encode())
		if err := getJSON(ctx, c.http, http.MethodGet, u, c.internalKey, nil, &subs); err != nil {
			return nil, fmt.Errorf("post-service subscribed-owner-ids: %w", err)
		}
		for _, raw := range subs.Data.OwnerIDs {
			if err := add(raw); err != nil {
				return nil, fmt.Errorf("post-service subscribed-owner-ids: %w", err)
			}
		}
		if !subs.Data.HasMore || subs.Data.NextAfter == "" {
			break
		}
		after = subs.Data.NextAfter
	}
	return out, nil
}

// --- relationships, many at once ---

// relationshipBatchMax is graph-service's MaxRelationshipBatch.
const relationshipBatchMax = 100

// Relationships is Relationship for many creators. Like Relationship it is
// all-or-error: a creator missing from the answer fails the call, because a
// missing entry cannot rule out a block.
func (c *HTTPGraphClient) Relationships(ctx context.Context, viewerID uuid.UUID, creatorIDs []uuid.UUID) (map[uuid.UUID]Relationship, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("graph client not configured")
	}
	out := make(map[uuid.UUID]Relationship, len(creatorIDs))
	for start := 0; start < len(creatorIDs); start += relationshipBatchMax {
		end := start + relationshipBatchMax
		if end > len(creatorIDs) {
			end = len(creatorIDs)
		}
		ids := make([]string, 0, end-start)
		for _, id := range creatorIDs[start:end] {
			ids = append(ids, id.String())
		}
		body, err := json.Marshal(map[string]any{"viewer_id": viewerID.String(), "target_ids": ids})
		if err != nil {
			return nil, err
		}
		var page map[string]struct {
			Follows   bool `json:"follows"`
			Blocked   bool `json:"blocked"`
			BlockedBy bool `json:"blocked_by"`
		}
		if err := getJSON(ctx, c.http, http.MethodPost, c.baseURL+"/v1/graph/relationships/batch", c.internalKey, jsonReader(body), &page); err != nil {
			return nil, fmt.Errorf("graph relationships/batch: %w", err)
		}
		for _, id := range creatorIDs[start:end] {
			rel, ok := page[id.String()]
			if !ok {
				return nil, fmt.Errorf("graph relationships/batch: no entry for a creator")
			}
			out[id] = Relationship{Follows: rel.Follows, Blocked: rel.Blocked || rel.BlockedBy}
		}
	}
	return out, nil
}
