package http

import (
	"net/http"
	"time"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// POST /v1/analytics/internal/content-views — the visible view count for
// a page of posts (plan 5B, issue M-13).
//
// post-service (post detail) and feed-service (every hydrated page) used
// to read a Redis hash, post:views:{id}, that only the Kafka
// VideoViewConsumer wrote — and that consumer subscribed to event types
// no service produced. The number a creator saw on their post was
// structurally zero while content_daily_summary held the real, capped,
// self-view-excluded figure they are paid on. This route serves that
// figure, batched, behind the internal key like every /v1 route here.
//
// Request:  {"content_ids": ["<uuid>", ...]}   at most 200
// Response: {"data": {"<uuid>": {"views_display": n, "as_of": "<RFC3339>"}}, "freshness": {...}}
//
// Every requested id is answered; unknown content is 0. See
// AggregateStore.ContentViewsBatch for what is summed and why.
//
// Freshness. `as_of` is when the aggregates were read. Behind that:
// today's hours are rebuilt every five minutes from finalised sessions
// (aggregation.aggregationInterval), settled days are re-rolled every
// fifteen minutes for 48 hours (aggregation.rollupInterval), and today's
// figure is uncapped until midnight's rollup folds it. Callers add a
// 60-second in-process cache on top. A reader comparing this number to a
// creator statement should expect it to move within those windows and
// never assume equality inside them.
const maxContentViewsBatch = 200

// contentViewsFreshness documents the windows above on the wire so a
// caller does not have to read this file to know how stale "now" is.
var contentViewsFreshness = gin.H{
	"window":               "today's hours rebuilt every 5m (uncapped until the day rolls up); settled days re-rolled every 15m for 48h; callers cache 60s",
	"hourly_lag_seconds":   300,
	"daily_lag_seconds":    900,
	"caller_cache_seconds": 60,
}

type contentViewsRequest struct {
	ContentIDs []string `json:"content_ids"`
}

// ContentViewsEntry is one content id's answer.
type ContentViewsEntry struct {
	ViewsDisplay int64     `json:"views_display"`
	AsOf         time.Time `json:"as_of"`
}

func (h *Handler) ContentViewsBatch(c *gin.Context) {
	if h.aggStore == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "aggregate store not configured", nil)
		return
	}
	var req contentViewsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if len(req.ContentIDs) > maxContentViewsBatch {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "at most 200 content_ids per call", nil)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.ContentIDs))
	for _, raw := range req.ContentIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "content_ids must be UUIDs", nil)
			return
		}
		ids = append(ids, id)
	}

	counts, asOf, err := h.aggStore.ContentViewsBatch(c.Request.Context(), ids)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "view count lookup failed", nil)
		return
	}
	data := make(map[string]ContentViewsEntry, len(counts))
	for id, views := range counts {
		data[id.String()] = ContentViewsEntry{ViewsDisplay: views, AsOf: asOf}
	}
	c.JSON(http.StatusOK, gin.H{"data": data, "freshness": contentViewsFreshness})
}
