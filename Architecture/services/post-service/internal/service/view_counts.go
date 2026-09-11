package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The visible view count on a post (plan 5B, issue M-13).
//
// Until 2026-09-11 getViewCount read a Redis hash, post:views:{id}, that
// only analytics-service's Kafka VideoViewConsumer wrote — and that
// consumer subscribed to event types no service produced. Every view
// count post-service showed was structurally zero, while
// analytics.content_daily_summary held the real, capped, self-view-
// excluded number the creator is paid on. This file reads that number
// through analytics-service's internal batch route, the same pattern
// feed-service uses for channel cards.
//
// Fail-open, always: analytics being down, slow or wrong must never fail
// a post read. The count is 0 and a warning says why. A 60 s in-process
// cache keeps a hot post from asking analytics on every detail read; the
// figure behind it is at most ~5 min behind ingest (see the route's
// `freshness` block).

const (
	viewCountCacheTTL = 60 * time.Second
	// viewCountCacheMaxEntries bounds the cache: past it, expired entries
	// are dropped, and if that is not enough the cache is reset. A
	// process-wide map of every post ever read is not a cache.
	viewCountCacheMaxEntries = 10_000
	// contentViewsBatchMax is analytics-service's per-call limit.
	contentViewsBatchMax = 200
	viewCountTimeout     = 1500 * time.Millisecond
)

type viewCountEntry struct {
	views   int64
	expires time.Time
}

// SetAnalyticsServiceURL configures the analytics-service base URL the
// view count is read from. Empty disables the read (every count is 0).
func (s *Service) SetAnalyticsServiceURL(url string) {
	s.analyticsServiceURL = url
}

// getViewCount is the display view count for one post. Never fails:
// 0 on any error, with a warning.
func (s *Service) getViewCount(ctx context.Context, postID uuid.UUID) int64 {
	return s.fetchViewCounts(ctx, []uuid.UUID{postID})[postID]
}

// fetchViewCounts answers every id, from cache where fresh and from
// analytics-service otherwise. Ids analytics could not answer are 0.
func (s *Service) fetchViewCounts(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]int64 {
	out := make(map[uuid.UUID]int64, len(ids))
	if len(ids) == 0 {
		return out
	}
	now := time.Now()
	var missing []uuid.UUID
	s.viewCountMu.Lock()
	for _, id := range ids {
		if e, ok := s.viewCountCache[id]; ok && e.expires.After(now) {
			out[id] = e.views
			continue
		}
		out[id] = 0
		missing = append(missing, id)
	}
	s.viewCountMu.Unlock()
	if len(missing) == 0 {
		return out
	}
	if s.analyticsServiceURL == "" || s.httpClient == nil {
		slog.WarnContext(ctx, "view count unavailable: analytics-service not configured; serving 0", "posts", len(missing))
		return out
	}
	for start := 0; start < len(missing); start += contentViewsBatchMax {
		end := min(start+contentViewsBatchMax, len(missing))
		chunk := missing[start:end]
		fetched, err := s.requestContentViews(ctx, chunk)
		if err != nil {
			// Fail-open: the post still renders, the number is 0, and
			// the reason is in the log. Nothing is cached, so the next
			// read tries again.
			slog.WarnContext(ctx, "view count unavailable; serving 0", "posts", len(chunk), "err", err)
			continue
		}
		s.viewCountMu.Lock()
		if s.viewCountCache == nil {
			s.viewCountCache = make(map[uuid.UUID]viewCountEntry)
		}
		if len(s.viewCountCache)+len(chunk) > viewCountCacheMaxEntries {
			for id, e := range s.viewCountCache {
				if !e.expires.After(now) {
					delete(s.viewCountCache, id)
				}
			}
			if len(s.viewCountCache)+len(chunk) > viewCountCacheMaxEntries {
				s.viewCountCache = make(map[uuid.UUID]viewCountEntry)
			}
		}
		for _, id := range chunk {
			views := fetched[id] // absent from the answer means 0
			s.viewCountCache[id] = viewCountEntry{views: views, expires: now.Add(viewCountCacheTTL)}
			out[id] = views
		}
		s.viewCountMu.Unlock()
	}
	return out
}

// requestContentViews is one call to
// POST {ANALYTICS_SERVICE_URL}/v1/analytics/internal/content-views.
func (s *Service) requestContentViews(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]int64, error) {
	ctx, cancel := context.WithTimeout(ctx, viewCountTimeout)
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
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
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
