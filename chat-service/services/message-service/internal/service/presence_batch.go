package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

const (
	// actionSeeOnlineStatus is graph-service's online-status permission
	// (the target's who_can_see_online_status, blocks, hidden accounts,
	// chat pause and the viewer's own reciprocity).
	actionSeeOnlineStatus = "see_online_status"

	// graphPermissionBatchMax is graph-service's per-call target cap on
	// POST /v1/permissions/check-batch (it silently truncates past it), so
	// longer lists are sent in chunks of this size.
	graphPermissionBatchMax = 50

	// onlineVisibilityCacheTTL is how long one viewer→target decision is
	// reused (unchanged from the per-pair lookup it replaces).
	onlineVisibilityCacheTTL = 30 * time.Second
)

// onlineVisibilityBudget bounds the whole graph round trip of one disclosure
// lookup (audit L10). It is tighter than the HTTP client's 5 s so a slow graph
// costs a presence request at most this long; a lookup that runs out reports
// the affected targets offline. A var only so tests can shorten it.
var onlineVisibilityBudget = 3 * time.Second

func onlineVisibilityCacheKey(viewerID, targetID uuid.UUID) string {
	return "onlinevis:" + viewerID.String() + ":" + targetID.String()
}

// discloseOnlineToMany is the batched discloseOnlineTo (audit L10): for each
// target it reports whether viewerID may see that target's online status.
//
// Cached decisions (the same onlinevis:<viewer>:<target> keys, 30 s) are read
// in one MGET; the misses go to graph-service in ONE
// POST /v1/permissions/check-batch per 50 targets instead of one GET per
// target. The rule is unchanged and fails closed: a target is admitted only
// by an explicit allowed=true from graph (or a cached "1"); a transport
// error, timeout, non-200, undecodable body or missing entry leaves it
// hidden. Only decisions graph actually returned are cached, so a graph
// outage is not remembered as 30 s of denials.
//
// The viewer is never asked about themself: callers handle self before
// calling, and a self entry here is simply decided by graph like any other.
func (s *Service) discloseOnlineToMany(ctx context.Context, viewerID uuid.UUID, targets []uuid.UUID) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(targets))
	// Deduplicate: each distinct target costs at most one cache key and one
	// graph slot.
	uniq := make([]uuid.UUID, 0, len(targets))
	for _, t := range targets {
		if _, seen := out[t]; seen {
			continue
		}
		out[t] = false
		uniq = append(uniq, t)
	}
	if len(uniq) == 0 {
		return out
	}

	misses := uniq
	if s.rdb != nil {
		keys := make([]string, len(uniq))
		for i, t := range uniq {
			keys[i] = onlineVisibilityCacheKey(viewerID, t)
		}
		if cached, err := s.rdb.MGet(ctx, keys...).Result(); err == nil && len(cached) == len(uniq) {
			misses = make([]uuid.UUID, 0, len(uniq))
			for i, t := range uniq {
				v, ok := cached[i].(string)
				if !ok {
					misses = append(misses, t)
					continue
				}
				out[t] = v == "1"
			}
		}
	}
	if len(misses) == 0 {
		return out
	}

	decided := s.checkOnlineVisibilityBatch(ctx, viewerID, misses)
	if len(decided) == 0 {
		return out
	}
	if s.rdb != nil {
		p := s.rdb.Pipeline()
		for t, allowed := range decided {
			value := "0"
			if allowed {
				value = "1"
			}
			p.Set(ctx, onlineVisibilityCacheKey(viewerID, t), value, onlineVisibilityCacheTTL)
		}
		_, _ = p.Exec(ctx)
	}
	for t, allowed := range decided {
		out[t] = allowed
	}
	return out
}

// checkOnlineVisibilityBatch asks graph-service for see_online_status on
// targets, chunked at graphPermissionBatchMax, all within one
// onlineVisibilityBudget. It returns ONLY the targets graph decided (allowed
// or denied); anything undecided is absent, and the caller treats absence as
// hidden. The actor is named in X-User-Id with the internal key — the same
// auth as the single /v1/permissions/check call.
func (s *Service) checkOnlineVisibilityBatch(ctx context.Context, viewerID uuid.UUID, targets []uuid.UUID) map[uuid.UUID]bool {
	if s.graphServiceURL == "" || s.httpClient == nil || len(targets) == 0 {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, onlineVisibilityBudget)
	defer cancel()

	decided := make(map[uuid.UUID]bool, len(targets))
	for start := 0; start < len(targets); start += graphPermissionBatchMax {
		end := start + graphPermissionBatchMax
		if end > len(targets) {
			end = len(targets)
		}
		chunk := targets[start:end]
		got, ok := s.checkOnlineVisibilityChunk(ctx, viewerID, chunk)
		if !ok {
			// The budget is shared: a failed chunk (most often the deadline)
			// ends the lookup rather than spending more of it.
			break
		}
		for t, allowed := range got {
			decided[t] = allowed
		}
	}
	return decided
}

// checkOnlineVisibilityChunk is one check-batch call. ok is false when the
// call failed as a whole (nothing in it may be cached or trusted).
func (s *Service) checkOnlineVisibilityChunk(ctx context.Context, viewerID uuid.UUID, chunk []uuid.UUID) (map[uuid.UUID]bool, bool) {
	ids := make([]string, len(chunk))
	for i, t := range chunk {
		ids[i] = t.String()
	}
	payload, err := json.Marshal(map[string]any{
		"target_user_ids": ids,
		"actions":         []string{actionSeeOnlineStatus},
	})
	if err != nil {
		return nil, false
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.graphServiceURL+"/v1/permissions/check-batch", bytes.NewReader(payload))
	if err != nil {
		return nil, false
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", viewerID.String())
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, false
	}
	var envelope struct {
		Data struct {
			Results map[string]map[string]struct {
				Allowed bool `json:"allowed"`
			} `json:"results"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, false
	}
	got := make(map[uuid.UUID]bool, len(chunk))
	for i, t := range chunk {
		decisions, present := envelope.Data.Results[ids[i]]
		if !present {
			continue // undecided: hidden, and not cached
		}
		d, present := decisions[actionSeeOnlineStatus]
		if !present {
			continue
		}
		got[t] = d.Allowed
	}
	return got, true
}
