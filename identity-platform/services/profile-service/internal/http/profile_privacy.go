package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Private accounts on the profile surfaces.
//
// A public profile now carries two viewer-facing facts the client needs to
// render the TikTok-style private-account state:
//
//	is_private     — the target's account_visibility (identity user-service)
//	follow_status  — "none" | "requested" | "following", the VIEWER's edge
//	                 toward the target (graph-service), only when a viewer is
//	                 present and is not the profile owner
//
// Neither is a gate. The profile itself stays reachable (a private profile
// is findable and shows its counts); what is gated is the POST surface,
// which post-service owns (GET /v1/posts/user/:authorId denies through the
// graph permission matrix). So both facts FAIL OPEN to their zero values —
// false / omitted — with a log line: a settings blip must not blank every
// profile card in a feed, and a wrong "not private" badge is a rendering
// defect, not a leak, because the posts stay locked regardless.

// ProfilePrivacyResolver resolves the two display facts.
type ProfilePrivacyResolver interface {
	// IsPrivate reports the target's account_visibility == private.
	IsPrivate(ctx context.Context, userID uuid.UUID) (bool, error)
	// FollowStatus resolves the viewer→target edge: "none", "requested"
	// (a pending follow request) or "following".
	FollowStatus(ctx context.Context, viewerID, targetID uuid.UUID) (string, error)
}

const (
	FollowStatusNone      = "none"
	FollowStatusRequested = "requested"
	FollowStatusFollowing = "following"
)

const profilePrivacyCacheTTL = 60 * time.Second

type profilePrivacyEntry struct {
	private bool
	expires time.Time
}

type profilePrivacyClient struct {
	graphURL    string
	userURL     string
	internalKey string
	client      *http.Client

	mu    sync.Mutex
	cache map[uuid.UUID]profilePrivacyEntry
	now   func() time.Time
}

// NewProfilePrivacyResolver builds the HTTP resolver. userURL is the
// identity user-service (settings authority), graphURL is graph-service.
func NewProfilePrivacyResolver(graphURL, userURL, internalKey string) ProfilePrivacyResolver {
	return &profilePrivacyClient{
		graphURL:    strings.TrimRight(graphURL, "/"),
		userURL:     strings.TrimRight(userURL, "/"),
		internalKey: internalKey,
		client:      &http.Client{Timeout: 3 * time.Second},
		cache:       map[uuid.UUID]profilePrivacyEntry{},
		now:         time.Now,
	}
}

func (p *profilePrivacyClient) authorize(req *http.Request) {
	if p.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", p.internalKey)
	}
}

// IsPrivate reads account_visibility, cached 60s per user. The
// settings-changed event does not reach profile-service, so the TTL is the
// staleness bound for the badge (the posts themselves lock immediately).
func (p *profilePrivacyClient) IsPrivate(ctx context.Context, userID uuid.UUID) (bool, error) {
	if p.userURL == "" {
		return false, fmt.Errorf("identity user-service is not configured")
	}
	now := p.now()
	p.mu.Lock()
	if e, ok := p.cache[userID]; ok && now.Before(e.expires) {
		p.mu.Unlock()
		return e.private, nil
	}
	p.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/v1/users/%s/settings", p.userURL, userID), nil)
	if err != nil {
		return false, err
	}
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("read account visibility: %w", err)
	}
	defer resp.Body.Close()
	var private bool
	switch resp.StatusCode {
	case http.StatusOK:
		var body struct {
			Data struct {
				AccountVisibility string `json:"account_visibility"`
			} `json:"data"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			return false, fmt.Errorf("decode account visibility: %w", err)
		}
		private = strings.EqualFold(body.Data.AccountVisibility, "private")
	case http.StatusNotFound:
		private = false // no settings row yet: the default is public
	default:
		return false, fmt.Errorf("account visibility returned %d", resp.StatusCode)
	}

	p.mu.Lock()
	p.cache[userID] = profilePrivacyEntry{private: private, expires: now.Add(profilePrivacyCacheTTL)}
	if len(p.cache) > 100000 {
		for k, e := range p.cache {
			if now.After(e.expires) {
				delete(p.cache, k)
			}
		}
	}
	p.mu.Unlock()
	return private, nil
}

// FollowStatus asks graph-service's relationship endpoint. Not cached: a
// follow/accept must render on the very next profile read.
func (p *profilePrivacyClient) FollowStatus(ctx context.Context, viewerID, targetID uuid.UUID) (string, error) {
	if p.graphURL == "" {
		return "", fmt.Errorf("graph-service is not configured")
	}
	endpoint, err := url.Parse(p.graphURL + "/v1/graph/relationship")
	if err != nil {
		return "", err
	}
	q := endpoint.Query()
	q.Set("user_id", viewerID.String())
	q.Set("other_id", targetID.String())
	endpoint.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return "", err
	}
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("read relationship: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("relationship returned %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Follows             bool   `json:"follows"`
			FollowRequestStatus string `json:"follow_request_status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", fmt.Errorf("decode relationship: %w", err)
	}
	return followStatusFrom(body.Data.Follows, body.Data.FollowRequestStatus), nil
}

// followStatusFrom maps the graph facts onto the client's three states.
func followStatusFrom(follows bool, requestStatus string) string {
	if follows {
		return FollowStatusFollowing
	}
	if requestStatus == "pending_sent" {
		return FollowStatusRequested
	}
	return FollowStatusNone
}

// WithProfilePrivacy wires the private-account facts onto the public
// profile surfaces.
func (h *Handler) WithProfilePrivacy(r ProfilePrivacyResolver) *Handler {
	h.privacy = r
	return h
}

// applyProfilePrivacy stamps is_private and, for a signed-in non-owner
// viewer, follow_status. Fail-open to the zero values (see file comment).
func (h *Handler) applyProfilePrivacy(c *gin.Context, profile *PublicProfile) *PublicProfile {
	if profile == nil || h.privacy == nil {
		return profile
	}
	ctx := c.Request.Context()
	if private, err := h.privacy.IsPrivate(ctx, profile.UserID); err != nil {
		h.log.Warn("profile privacy: account visibility unresolved; rendering as public badge", "err", err, "user_id", profile.UserID)
	} else {
		profile.IsPrivate = private
	}
	viewerID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || viewerID == uuid.Nil || viewerID == profile.UserID {
		return profile
	}
	status, err := h.privacy.FollowStatus(ctx, viewerID, profile.UserID)
	if err != nil {
		h.log.Warn("profile privacy: follow status unresolved; omitting", "err", err, "viewer_id", viewerID, "user_id", profile.UserID)
		return profile
	}
	profile.FollowStatus = status
	return profile
}

// applyProfilePrivacyMap stamps is_private on a batch page. follow_status
// is a per-viewer edge and is deliberately NOT resolved here — the batch
// surface hydrates feed cards, which render the badge but not the button;
// the single-profile read is where the button lives.
func (h *Handler) applyProfilePrivacyMap(c *gin.Context, profiles map[uuid.UUID]*PublicProfile) map[uuid.UUID]*PublicProfile {
	if h.privacy == nil {
		return profiles
	}
	ctx := c.Request.Context()
	for id, profile := range profiles {
		if profile == nil {
			continue
		}
		private, err := h.privacy.IsPrivate(ctx, id)
		if err != nil {
			h.log.Warn("profile privacy: account visibility unresolved in batch; rendering as public badge", "err", err, "user_id", id)
			continue
		}
		profile.IsPrivate = private
	}
	return profiles
}
