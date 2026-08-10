package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Module 1 fixes-v1 — Codex P1-8: one eligibility decision, applied per
// recipient immediately before the inbox write.
//
// Enqueue-time checks are not sufficient: a fan-out job can sit in the
// queue (or be retried hours later) while the post is deleted, moderated,
// or its author blocks a subscriber. Everything time-sensitive is
// therefore re-evaluated here.
//
// Checks, in cost order:
//  1. post still visible/approved  (cached per job, one call per batch)
//  2. viewer is not blocked by / has not blocked the author
//  3. followers-only posts require an actual follow relationship, so a
//     subscriber never receives a deep link they cannot open
//
// Failure policy: a check that cannot be evaluated returns an error, which
// counts the recipient as FAILED (retried) rather than silently
// delivering or silently dropping.

// eligibilityDeps is the small surface the checks need. Nil clients make
// the corresponding check a no-op (dev / partial deployments).
type eligibilityDeps struct {
	graphURL    string
	postURL     string
	internalKey string
	http        *http.Client
}

// SetEligibilityDeps wires the per-recipient eligibility checks.
func (f *SubscriberFanout) SetEligibilityDeps(graphURL, postURL, internalKey string) {
	f.elig = &eligibilityDeps{
		graphURL:    graphURL,
		postURL:     postURL,
		internalKey: internalKey,
		http:        &http.Client{Timeout: 5 * time.Second},
	}
}

// postState is the cached per-job view of the post's current state.
type postState struct {
	exists     bool
	visibility string
	approved   bool
}

// eligible decides whether one recipient should receive this upload
// notification right now.
func (f *SubscriberFanout) eligible(ctx context.Context, job *postgres.FanoutJob, userID uuid.UUID) (bool, error) {
	if f.elig == nil {
		// No dependencies wired: fall back to the enqueue-time decision.
		return true, nil
	}

	state, err := f.postStateFor(ctx, job)
	if err != nil {
		return false, err
	}
	// Deleted or no longer approved before delivery ⇒ terminal skip.
	if !state.exists || !state.approved {
		return false, nil
	}
	// Visibility may have narrowed after enqueue.
	if state.visibility == "private" || state.visibility == "unlisted" {
		return false, nil
	}

	// One graph round-trip yields both the block state and the follow
	// state we need below.
	rel, err := f.relationship(ctx, userID, job.AuthorID)
	if err != nil {
		return false, err
	}
	if rel == nil {
		// No graph client wired: fall back to the enqueue-time decision.
		return true, nil
	}
	// fixes-v2 / Codex P1-6/P1-7: BOTH block directions. v1 checked only
	// `blocked` (author→viewer); a recipient who had blocked the author
	// still received their upload notifications.
	if rel.Blocked || rel.BlockedBy {
		return false, nil
	}

	// Audience checks: never send a deep link the recipient cannot open.
	switch state.visibility {
	case "followers":
		if !rel.Follows {
			return false, nil
		}
	case "trusted", "close_friends":
		// EXACT membership — v1 treated `trusted` as ordinary following
		// and did not handle `close_friends` at all.
		if !rel.IsCloseFriend {
			return false, nil
		}
	}

	return true, nil
}

// graphRelationship mirrors graph-service's /v1/graph/relationship
// response (service.Relationship). Field names must stay in sync.
type graphRelationship struct {
	Follows    bool `json:"follows"`
	FollowedBy bool `json:"followed_by"`
	// Blocked: author blocked viewer. BlockedBy: viewer blocked author.
	// Both suppress delivery (fixes-v2 / Codex P1-6).
	Blocked      bool `json:"blocked"`
	BlockedBy    bool `json:"blocked_by"`
	IsMuted      bool `json:"is_muted"`
	IsConnection bool `json:"is_connection"`
	// IsCloseFriend is the exact trusted/close-friends audience.
	IsCloseFriend bool `json:"is_close_friend"`
}

// relationship fetches viewer→author relationship state. Returns
// (nil, nil) when no graph client is configured.
func (f *SubscriberFanout) relationship(ctx context.Context, viewerID, authorID uuid.UUID) (*graphRelationship, error) {
	if f.elig.graphURL == "" {
		return nil, nil
	}
	// Contract: user_id = the actor whose perspective we evaluate,
	// other_id = the counterparty.
	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s",
		f.elig.graphURL, viewerID, authorID)
	body, status, err := f.elig.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("relationship: %w", err)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("graph-service returned %d", status)
	}
	var env struct {
		Data graphRelationship `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("relationship decode: %w", err)
	}
	return &env.Data, nil
}

// postStateFor fetches and caches the post's current state for the life
// of one job pass (one call per batch rather than per recipient).
func (f *SubscriberFanout) postStateFor(ctx context.Context, job *postgres.FanoutJob) (*postState, error) {
	f.stateMu.Lock()
	if f.stateCache != nil && f.stateCachePost == job.PostID &&
		time.Since(f.stateCacheAt) < 30*time.Second {
		st := f.stateCache
		f.stateMu.Unlock()
		return st, nil
	}
	f.stateMu.Unlock()

	url := fmt.Sprintf("%s/v1/posts/%s", f.elig.postURL, job.PostID)
	body, status, err := f.elig.get(ctx, url)
	if err != nil {
		return nil, fmt.Errorf("post state: %w", err)
	}
	st := &postState{}
	switch status {
	case http.StatusNotFound:
		st.exists = false
	case http.StatusOK:
		var env struct {
			Data struct {
				Visibility   string  `json:"visibility"`
				ReviewStatus string  `json:"review_status"`
				DeletedAt    *string `json:"deleted_at"`
			} `json:"data"`
		}
		if err := json.Unmarshal(body, &env); err != nil {
			return nil, fmt.Errorf("post state decode: %w", err)
		}
		st.exists = env.Data.DeletedAt == nil
		st.visibility = env.Data.Visibility
		st.approved = env.Data.ReviewStatus == "approved" || env.Data.ReviewStatus == ""
	default:
		return nil, fmt.Errorf("post-service returned %d", status)
	}

	f.stateMu.Lock()
	f.stateCache, f.stateCachePost, f.stateCacheAt = st, job.PostID, time.Now()
	f.stateMu.Unlock()
	return st, nil
}

func (d *eligibilityDeps) get(ctx context.Context, url string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	if d.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", d.internalKey)
	}
	resp, err := d.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return body, resp.StatusCode, err
}
