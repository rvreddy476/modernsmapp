package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The visible view count on every hydrated post (plan 5B, issue M-13).
//
// enrichViewCounts used to pipeline HGET post:views:{id} display against
// shared Redis. That hash was written only by analytics-service's Kafka
// VideoViewConsumer, which subscribed to event types no service ever
// produced, so every view_count the feed served was structurally zero
// while analytics.content_daily_summary held the real, capped,
// self-view-excluded number the creator is paid on. This reads that
// number through analytics-service's internal batch route — the same
// shape as fetchChannels against post-service — one call per page.
//
// Fail-open, always: analytics being down, slow or wrong never fails a
// feed page. Every post renders, view_count is 0, and a warning names
// the reason. A 60 s in-process cache keeps the hot page from asking
// again on every scroll; the figure behind it is at most ~5 min behind
// ingest (the route's `freshness` block documents the windows).

const (
	viewCountCacheTTL = 60 * time.Second
	// viewCountCacheMaxEntries bounds the cache: past it, expired entries
	// are dropped, and if that is not enough the cache is reset.
	viewCountCacheMaxEntries = 20_000
	// contentViewsBatchMax is analytics-service's per-call limit.
	contentViewsBatchMax = 200
)

type viewCountEntry struct {
	views   int64
	expires time.Time
}

// enrichViewCounts fills HydratedPost.ViewCount for a page. View counts
// are deliberately not part of the hydration cache blob, so this always
// reflects analytics within the cache TTL.
func (s *Service) enrichViewCounts(ctx context.Context, posts []HydratedPost) {
	if len(posts) == 0 {
		return
	}
	ids := make([]uuid.UUID, 0, len(posts))
	seen := make(map[uuid.UUID]bool, len(posts))
	for _, p := range posts {
		if !seen[p.ID] {
			seen[p.ID] = true
			ids = append(ids, p.ID)
		}
	}
	counts := s.fetchViewCounts(ctx, ids)
	for i := range posts {
		posts[i].ViewCount = counts[posts[i].ID]
	}
}

// fetchViewCounts answers every id, from cache where fresh and from
// analytics-service otherwise. Ids analytics could not answer are 0.
func (s *Service) fetchViewCounts(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]int64 {
	out := make(map[uuid.UUID]int64, len(ids))
	now := time.Now()
	var missing []uuid.UUID
	s.vcMu.Lock()
	for _, id := range ids {
		if e, ok := s.vcCache[id]; ok && e.expires.After(now) {
			out[id] = e.views
			continue
		}
		out[id] = 0
		missing = append(missing, id)
	}
	s.vcMu.Unlock()
	if len(missing) == 0 {
		return out
	}
	if s.analyticsServiceURL == "" || s.analyticsClient == nil {
		slog.WarnContext(ctx, "view count unavailable: analytics-service not configured; serving 0", "posts", len(missing))
		return out
	}
	for start := 0; start < len(missing); start += contentViewsBatchMax {
		end := min(start+contentViewsBatchMax, len(missing))
		chunk := missing[start:end]
		fetched, err := s.requestContentViews(ctx, chunk)
		if err != nil {
			// Fail-open: the page still renders, these posts show 0, the
			// reason is in the log, and nothing is cached so the next
			// page tries again.
			slog.WarnContext(ctx, "view count unavailable; serving 0", "posts", len(chunk), "err", err)
			continue
		}
		s.vcMu.Lock()
		if s.vcCache == nil {
			s.vcCache = make(map[uuid.UUID]viewCountEntry)
		}
		if len(s.vcCache)+len(chunk) > viewCountCacheMaxEntries {
			for id, e := range s.vcCache {
				if !e.expires.After(now) {
					delete(s.vcCache, id)
				}
			}
			if len(s.vcCache)+len(chunk) > viewCountCacheMaxEntries {
				s.vcCache = make(map[uuid.UUID]viewCountEntry)
			}
		}
		for _, id := range chunk {
			views := fetched[id] // absent from the answer means 0
			s.vcCache[id] = viewCountEntry{views: views, expires: now.Add(viewCountCacheTTL)}
			out[id] = views
		}
		s.vcMu.Unlock()
	}
	return out
}

// requestContentViews is one call to
// POST {ANALYTICS_SERVICE_URL}/v1/analytics/internal/content-views.
func (s *Service) requestContentViews(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
	defer cancel()
	raw := make([]string, len(ids))
	for i, id := range ids {
		raw[i] = id.String()
	}
	body, err := json.Marshal(map[string]any{"content_ids": raw})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(s.analyticsServiceURL, "/")+"/v1/analytics/internal/content-views", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.analyticsClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("analytics-service content-views returned %d", resp.StatusCode)
	}
	var envelope struct {
		Data map[string]struct {
			ViewsDisplay int64 `json:"views_display"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode content-views: %w", err)
	}
	out := make(map[uuid.UUID]int64, len(envelope.Data))
	for idStr, entry := range envelope.Data {
		id, err := uuid.Parse(idStr)
		if err != nil {
			continue
		}
		out[id] = entry.ViewsDisplay
	}
	return out, nil
}
