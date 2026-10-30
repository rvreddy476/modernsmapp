package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterReelEngagementRoutes adds the reel routes that remain, and the
// retired ones (410 Gone).
func (h *Handler) RegisterReelEngagementRoutes(r *gin.Engine) {
	reels := r.Group("/v1/reels")
	{
		for _, g := range retiredReelRoutes {
			reels.Handle(g.method, g.path, retiredReelRoute(g))
		}
		// Retired 2026-09-11 (plan 5B, issue M-13): answers 410 Gone. See
		// ReelViewGone.
		reels.POST("/:reelId/view", h.ReelViewGone)
		reels.POST("/batch/counts", h.BatchGetReelCounts)
		reels.GET("/saved", h.ListSavedReels)
		reels.GET("/liked", h.GetUserReelLikes)
	}
}

// retiredReelRoutes are the legacy per-reel engagement routes (retired
// 2026-10-30, audit MEDIUM 6). They kept a second engagement store in
// Scylla beside the canonical /v1/posts one: comments with no rate limit,
// idempotency, moderation, notification, delete or report, listed for the
// current month only; read-then-write counters that double-counted a
// double tap and could go negative; a like the post's own count never saw;
// shares with no rate limit or dedup. No first-party client calls them
// (Android uses /v1/posts/:id/*). Each answers 410 with its replacement
// named, as POST /:reelId/view does, so an old client gets a deliberate
// answer rather than a 404 it might retry.
var retiredReelRoutes = []retiredReelRouteSpec{
	{http.MethodPost, "/:reelId/react", "POST /v1/posts/{postId}/react"},
	{http.MethodDelete, "/:reelId/react", "POST /v1/posts/{postId}/react"},
	{http.MethodGet, "/:reelId/react/me", "GET /v1/posts/{postId} (viewer_reaction)"},
	{http.MethodPost, "/:reelId/comments", "POST /v1/posts/{postId}/comments"},
	{http.MethodGet, "/:reelId/comments", "GET /v1/posts/{postId}/comments"},
	{http.MethodPost, "/:reelId/share", "POST /v1/posts/{postId}/share"},
	{http.MethodPost, "/:reelId/save", "POST /v1/posts/{postId}/bookmark"},
	{http.MethodDelete, "/:reelId/save", "DELETE /v1/posts/{postId}/bookmark"},
	{http.MethodGet, "/:reelId/saved", "GET /v1/posts/{postId} (viewer state)"},
	{http.MethodGet, "/:reelId/counts", "GET /v1/posts/{postId} (counts)"},
}

type retiredReelRouteSpec struct {
	method, path, replacement string
}

// retiredReelRoute answers a retired route: 410 ROUTE_RETIRED with the
// replacement, before anything is read or written.
func retiredReelRoute(g retiredReelRouteSpec) gin.HandlerFunc {
	msg := g.method + " /v1/reels" + strings.ReplaceAll(g.path, ":reelId", "{reelId}") +
		" has been retired; use " + g.replacement
	return func(c *gin.Context) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "ROUTE_RETIRED", msg,
			map[string]string{"replacement": g.replacement})
	}
}

type batchReelCountsRequest struct {
	ReelIDs []string `json:"reel_ids" binding:"required"`
}

// ReelViewGone answers the retired POST /v1/reels/:reelId/view.
//
// The route was a bare `view_count = view_count + 1` in Scylla with no
// dedup, no watch-time rule and no self-view exclusion: any caller could
// spin a reel's counter as fast as it could POST (plan 5B, issue M-13).
// A view is now what analytics-service decides from a play_start /
// watch_heartbeat / play_end session on POST /v1/analytics/events, and
// the number on the post comes from that. The route stays registered so
// an old client gets a deliberate 410 with the replacement named, not a
// 404 it might retry.
func (h *Handler) ReelViewGone(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "ROUTE_RETIRED",
		"POST /v1/reels/{reelId}/view has been retired; views are measured from play_start/watch_heartbeat/play_end events sent to POST /v1/analytics/events",
		map[string]string{"replacement": "POST /v1/analytics/events"})
}

func (h *Handler) BatchGetReelCounts(c *gin.Context) {
	var req batchReelCountsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	// Each id now runs the post detail's read gate, so the batch is bounded.
	if len(req.ReelIDs) > maxReelCountsBatch {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"at most 100 reel_ids per request", nil)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.ReelIDs))
	for _, raw := range req.ReelIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID: "+raw, nil)
			return
		}
		ids = append(ids, id)
	}

	// A reel the caller may not open is left out of the map, as if unknown
	// (2026-09-29): its counts were answered for any id.
	ids = h.svc.ViewablePostIDs(c.Request.Context(), ids, optionalCallerID(c))
	counts, err := h.svc.BatchGetReelCounts(c.Request.Context(), ids)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

func (h *Handler) ListSavedReels(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	reelIDs, err := h.svc.ListSavedReels(c.Request.Context(), userID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	if reelIDs == nil {
		reelIDs = []string{}
	}

	api.JSON(c.Writer, http.StatusOK, reelIDs, nil)
}

// GetUserReelLikes is GET /v1/reels/liked?limit=&cursor=: the caller's
// liked short-form posts they may still open, newest like first.
//
//	→ {data: ["<post id>", …], meta: {next_cursor}}
//
// data stays the bare id array clients already parse; meta.next_cursor
// (absent on the last page) is the keyset on (liked-at desc, id) to pass
// back as ?cursor=. limit defaults to 50, at most 100. A page can be
// shorter than limit and still carry a cursor (service/liked_reels.go).
// An unreadable cursor is 400 INVALID_CURSOR, never the first page.
func (h *Handler) GetUserReelLikes(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limit := service.NormalizeLikedReelsLimit(c.Query("limit"))
	reelIDs, next, err := h.svc.ListLikedReels(c.Request.Context(), userID, limit, c.Query("cursor"))
	if err != nil {
		if errors.Is(err, service.ErrInvalidLikedCursor) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "cursor is not one this list returned", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "could not load liked reels", nil)
		return
	}

	if reelIDs == nil {
		reelIDs = []string{}
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, reelIDs, meta)
}

// maxReelCountsBatch bounds POST /v1/reels/batch/counts: every id runs the
// post detail's read gate.
const maxReelCountsBatch = 100
