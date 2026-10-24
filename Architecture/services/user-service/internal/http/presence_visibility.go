package http

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/google/uuid"
)

// presenceBackend is the slice of presence.Store the online-status routes
// use. *presence.Store in production; tests substitute an in-memory one.
type presenceBackend interface {
	SetOnline(ctx context.Context, userID string) error
	IsOnline(ctx context.Context, userID string) (bool, error)
	AreOnline(ctx context.Context, userIDs []string) (map[string]bool, error)
}

// graphPermissionBatch is graph-service's per-call target cap on
// POST /v1/permissions/check-batch (it silently truncates past it).
const graphPermissionBatch = 50

// actionSeeOnlineStatus is graph-service's online-status permission. It
// applies the target's who_can_see_online_status (everyone / the
// connections_only audience — a legacy connection, the target following the
// requester, or an accepted 1:1 chat — / no one), blocks and hidden accounts
// either way, the target's chat pause and the requester's own reciprocity.
const actionSeeOnlineStatus = "see_online_status"

// presenceVisibleTo reports, for each target, whether requester may see its
// online status. The requester always sees their own. Everything else is
// graph-service's see_online_status decision; a target is admitted only by an
// explicit allowed=true, so a transport error, a non-200, an undecodable body
// or a missing entry all leave it hidden (fail closed).
func (h *Handler) presenceVisibleTo(ctx context.Context, requester uuid.UUID, targets []string) map[string]bool {
	out := make(map[string]bool, len(targets))
	ask := make([]string, 0, len(targets))
	for _, t := range targets {
		if t == requester.String() {
			out[t] = true
			continue
		}
		ask = append(ask, t)
	}
	for start := 0; start < len(ask); start += graphPermissionBatch {
		end := start + graphPermissionBatch
		if end > len(ask) {
			end = len(ask)
		}
		for id, ok := range h.checkOnlinePermission(ctx, requester, ask[start:end]) {
			if ok {
				out[id] = true
			}
		}
	}
	return out
}

// checkOnlinePermission asks graph-service about one chunk (at most
// graphPermissionBatch targets). The requester is named in X-User-Id: this
// call carries the internal key, and graph resolves the permission for
// exactly that actor.
func (h *Handler) checkOnlinePermission(ctx context.Context, requester uuid.UUID, targets []string) map[string]bool {
	payload, err := json.Marshal(map[string]any{
		"target_user_ids": targets,
		"actions":         []string{actionSeeOnlineStatus},
	})
	if err != nil {
		return nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.graphURL+"/v1/permissions/check-batch", bytes.NewReader(payload))
	if err != nil {
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", requester.String())
	if h.internalRouteKey != "" {
		req.Header.Set("X-Internal-Service-Key", h.internalRouteKey)
	}
	resp, err := h.graphClient.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body struct {
		Data struct {
			Results map[string]map[string]struct {
				Allowed bool `json:"allowed"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return nil
	}
	out := make(map[string]bool, len(targets))
	for _, t := range targets {
		out[t] = body.Data.Results[t][actionSeeOnlineStatus].Allowed
	}
	return out
}
