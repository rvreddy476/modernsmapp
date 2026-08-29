package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// CallPolicy gates "can user A call user B" against graph-service's
// permission matrix — the SAME single source of truth message-service uses
// for who_can_message (GET /v1/permissions/check).
//
// History: per the realtime audit C2, CreateCall used to skip social gating
// entirely. The first fix queried the raw relationship endpoint and
// re-implemented a circle heuristic here — which silently IGNORED the
// callee's `who_can_call` privacy setting (stored, editable, and rendered in
// clients, but never enforced). Delegating to /v1/permissions/check closes
// that: graph resolves block-in-either-direction AND who_can_call in one
// decision, and a future policy change lands in exactly one service.
//
// Fails CLOSED on transport errors (the audit specifically flagged fail-open
// as the bug path): if graph-service is unreachable, direct calls are
// rejected with a transient error until it recovers.
type CallPolicy struct {
	graphServiceURL string
	internalKey     string
	httpClient      *http.Client
}

var (
	// ErrCallNotAllowed is the ONE refusal for every policy denial —
	// block, privacy, circle. Deliberately generic: distinguishing
	// "blocked you" from "privacy settings" would leak the callee's
	// block state to the caller. Surface as 403 with generic copy.
	ErrCallNotAllowed = errors.New("calling this user is not permitted")

	// ErrGraphUnavailable wraps any permission-lookup transport error.
	// Surface as 503 so the client knows to retry; "graph down" must not
	// be misrepresented as "you can't call this person".
	ErrGraphUnavailable = errors.New("call-permission lookup temporarily unavailable")
)

func NewCallPolicy(graphServiceURL, internalKey string) *CallPolicy {
	return &CallPolicy{
		graphServiceURL: graphServiceURL,
		internalKey:     internalKey,
		httpClient:      &http.Client{Timeout: 5 * time.Second},
	}
}

// CanCall returns nil when the call is allowed.
//
// Bypassed entirely when graphServiceURL is empty — used in unit tests +
// bootstrap configs that don't run graph-service. Anything that wants a hard
// gate must validate the URL at startup (config does, when CALLS_ENABLED).
func (p *CallPolicy) CanCall(ctx context.Context, callerID, targetID uuid.UUID) error {
	if p.graphServiceURL == "" {
		return nil
	}
	if callerID == targetID {
		return nil
	}

	url := fmt.Sprintf(
		"%s/v1/permissions/check?target_user_id=%s&actions=call",
		p.graphServiceURL, targetID,
	)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.Warn("call_policy: build request failed", "err", err)
		return ErrGraphUnavailable
	}
	// The permission check is actor-scoped: the caller is the actor.
	req.Header.Set("X-User-Id", callerID.String())
	// graph-service gates /v1/* behind the internal service key — without it
	// the call 401s and CanCall fails closed on every call.
	if p.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", p.internalKey)
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

	var envelope struct {
		Data struct {
			Decisions map[string]struct {
				Allowed bool   `json:"allowed"`
				Reason  string `json:"reason,omitempty"`
			} `json:"decisions"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		slog.Warn("call_policy: permission response decode failed", "err", err)
		return ErrGraphUnavailable
	}
	decision, ok := envelope.Data.Decisions["call"]
	if !ok {
		// The authority answered but did not decide the action — fail
		// closed as unavailable, not as a policy denial.
		slog.Warn("call_policy: no call decision in response",
			"caller", callerID, "target", targetID)
		return ErrGraphUnavailable
	}
	if !decision.Allowed {
		// The reason (blocked / privacy_no_one / privacy_connections_only)
		// is logged for operators but NEVER differentiated to the caller.
		slog.Info("call rejected by permission matrix",
			"caller", callerID, "target", targetID, "reason", decision.Reason)
		return ErrCallNotAllowed
	}
	return nil
}
