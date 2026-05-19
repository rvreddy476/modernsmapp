package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/google/uuid"
)

// CallPolicy gates "can user A call user B" against the social graph.
//
// Per the realtime audit C2, CreateCall used to skip this entirely —
// rate limit + anti-spam was the only barrier between any authed user
// and ringing arbitrary strangers. Mirrors the DM policy already used
// by message-service (Architecture/services/message-service/internal/
// policy/dm_policy.go), with two deliberate differences:
//
//  1. Returns ErrBlockedByTarget / ErrNotInCircle separately so the
//     handler can present a more accurate message than "denied".
//  2. Fails closed on transport errors (matches DM policy) — the
//     audit specifically flagged fail-open as the bug path. If
//     graph-service is unreachable, every direct call is rejected
//     with a transient error until graph recovers; the alternative
//     is a known abuse window.
type CallPolicy struct {
	graphServiceURL string
	httpClient      *http.Client
}

var (
	// ErrBlockedByTarget signals the callee has blocked the caller.
	// Surface as 403; client should hide the call button entirely.
	ErrBlockedByTarget = errors.New("target has blocked the caller")

	// ErrNotInCircle signals neither friend nor mutual-follow.
	// Surface as 403; clients can show "you can only call people
	// you follow / who follow you back".
	ErrNotInCircle = errors.New("caller and target are not in each other's circle")

	// ErrGraphUnavailable wraps any graph-service transport error.
	// Caller should return 503/Service-Unavailable to the client so
	// they know to retry; we don't want to misrepresent "graph down"
	// as "you can't call this person".
	ErrGraphUnavailable = errors.New("call-relationship lookup temporarily unavailable")
)

func NewCallPolicy(graphServiceURL string) *CallPolicy {
	return &CallPolicy{
		graphServiceURL: graphServiceURL,
		httpClient:      &http.Client{Timeout: 5 * time.Second},
	}
}

// CanCall checks the social graph and returns nil when the call is
// allowed. The error sentinels above let the handler decide HTTP
// status + user-facing copy.
//
// Bypassed entirely when graphServiceURL is empty — used in unit
// tests + bootstrap configs that don't run graph-service. Anything
// that wants a hard gate must validate the URL at startup.
func (p *CallPolicy) CanCall(ctx context.Context, callerID, targetID uuid.UUID) error {
	if p.graphServiceURL == "" {
		return nil
	}
	if callerID == targetID {
		return nil
	}

	url := fmt.Sprintf(
		"%s/v1/graph/relationship?user_id=%s&other_id=%s",
		p.graphServiceURL, callerID, targetID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.Warn("call_policy: build request failed", "err", err)
		return ErrGraphUnavailable
	}
	// graph-service gates /v1/graph/* behind the internal service key —
	// without it the call 401s and CanCall fails closed on every call.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		slog.Warn("call_policy: graph unreachable; failing closed", "err", err)
		return ErrGraphUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("call_policy: graph non-200; failing closed",
			"status", resp.StatusCode, "caller", callerID, "target", targetID)
		return ErrGraphUnavailable
	}

	var body struct {
		Data struct {
			IsFriend   bool `json:"is_friend"`
			Follows    bool `json:"follows"`
			FollowedBy bool `json:"followed_by"`
			Blocked    bool `json:"blocked"`
		} `json:"data"`
		// Legacy un-wrapped shape (some endpoints return the
		// relationship at the top level) — tolerated.
		IsFriend   bool `json:"is_friend"`
		Follows    bool `json:"follows"`
		FollowedBy bool `json:"followed_by"`
		Blocked    bool `json:"blocked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		slog.Warn("call_policy: graph response decode failed", "err", err)
		return ErrGraphUnavailable
	}
	rel := body.Data
	if !rel.IsFriend && !rel.Follows && !rel.FollowedBy && !rel.Blocked {
		// Fall back to the un-wrapped shape.
		rel.IsFriend = body.IsFriend
		rel.Follows = body.Follows
		rel.FollowedBy = body.FollowedBy
		rel.Blocked = body.Blocked
	}

	// `Blocked=true` in the graph response means: from `callerID`'s
	// perspective, `targetID` has blocked them. Reject the call so
	// the target's device doesn't ring.
	if rel.Blocked {
		return ErrBlockedByTarget
	}
	// Circle gate: same as DM policy. Friend OR mutual-follow.
	if rel.IsFriend {
		return nil
	}
	if rel.Follows && rel.FollowedBy {
		return nil
	}
	return ErrNotInCircle
}
