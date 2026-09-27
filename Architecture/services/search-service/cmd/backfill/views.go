package main

// MTube posts backfill helpers (2026-09-27): the two optional filter
// columns, and the display view count from analytics-service.

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

	"github.com/jackc/pgx/v5/pgxpool"
)

// viewCountBatch is how many eligible posts are written per batch, and
// therefore how many ids one analytics call carries (feed-service asks
// the same endpoint for 100 at a time).
const viewCountBatch = 100

// videoFilterExprs returns the SELECT expressions for height and
// has_subtitles, each degraded to a constant when its source table is not
// on this database.
//
//	height        post-service video_metadata.height (migration 008)
//	has_subtitles media-service media_caption_jobs.status = 'completed' for
//	              any of the post's media (migration 012)
func videoFilterExprs(ctx context.Context, pool *pgxpool.Pool) (heightExpr, subtitlesExpr string) {
	heightExpr = "0::int"
	subtitlesExpr = "false"
	if tableExists(ctx, pool, "video_metadata") {
		heightExpr = `COALESCE((SELECT vm.height FROM video_metadata vm WHERE vm.post_id = p.id LIMIT 1), 0)::int`
	} else {
		slog.Warn("backfill posts: video_metadata not on this database; height indexed as 0 (hd/4k facets empty)")
	}
	if tableExists(ctx, pool, "media_caption_jobs") {
		subtitlesExpr = `EXISTS (SELECT 1 FROM post_media pm JOIN media_caption_jobs cj ON cj.media_id = pm.media_id
		                   WHERE pm.post_id = p.id AND cj.status = 'completed')`
	} else {
		slog.Warn("backfill posts: media_caption_jobs not on this database; has_subtitles indexed as false (cc facet empty)")
	}
	return heightExpr, subtitlesExpr
}

func tableExists(ctx context.Context, pool *pgxpool.Pool, name string) bool {
	var present bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&present); err != nil {
		return false
	}
	return present
}

// viewCountSource reads analytics-service's display view count
// (POST /v1/analytics/internal/content-views, the same batch feed-service
// hydrates from) — the number the creator is paid on. Best-effort: a
// failed batch is 0 for every id in it and a warning, never an aborted
// run, because a rebuild with stale view counts beats no rebuild.
type viewCountSource struct {
	baseURL     string
	internalKey string
	http        *http.Client
	warned      bool
}

func newViewCountSource(baseURL, internalKey string) *viewCountSource {
	return &viewCountSource{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		http:        &http.Client{Timeout: 10 * time.Second},
	}
}

func (v *viewCountSource) fetch(ctx context.Context, ids []string) map[string]int64 {
	out := make(map[string]int64, len(ids))
	if v.baseURL == "" {
		if !v.warned {
			slog.Warn("backfill posts: ANALYTICS_SERVICE_URL not set; view_count indexed as 0 (sort=views is by recency until it is)")
			v.warned = true
		}
		return out
	}
	body, err := json.Marshal(map[string]any{"content_ids": ids})
	if err != nil {
		return out
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, v.baseURL+"/v1/analytics/internal/content-views", bytes.NewReader(body))
	if err != nil {
		return out
	}
	req.Header.Set("Content-Type", "application/json")
	if v.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", v.internalKey)
	}
	resp, err := v.http.Do(req)
	if err != nil {
		slog.Warn("backfill posts: view counts unavailable for this batch; indexing 0", "posts", len(ids), "err", err)
		return out
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slog.Warn("backfill posts: view counts unavailable for this batch; indexing 0", "posts", len(ids), "err", fmt.Sprintf("analytics-service returned %d", resp.StatusCode))
		return out
	}
	var envelope struct {
		Data map[string]struct {
			ViewsDisplay int64 `json:"views_display"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&envelope); err != nil {
		slog.Warn("backfill posts: view counts undecodable for this batch; indexing 0", "posts", len(ids), "err", err)
		return out
	}
	for id, entry := range envelope.Data {
		out[id] = entry.ViewsDisplay
	}
	return out
}
