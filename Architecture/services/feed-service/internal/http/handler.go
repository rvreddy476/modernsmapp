package http

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Handler struct {
	svc         *service.Service
	internalKey string
	rdb         *redis.Client
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

// WithInternalKey sets the internal service key used to authenticate
// service-to-service requests via the X-Internal-Service-Key header.
func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

// WithRedis wires the Redis client used by the per-user feed rate
// limiter (MF5). Without it the limiter falls back to no-op.
func (h *Handler) WithRedis(rdb *redis.Client) *Handler {
	h.rdb = rdb
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Apply internal service key enforcement to all /v1 routes.
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	// MF5: per-user feed rate limit. 120 req/min per user (sustained
	// 2/sec) — generous for legitimate tab-switching + pull-to-refresh,
	// blocks scrape bursts. Internal callers (X-Internal-Service-Key)
	// bypass; the gateway is trusted not to abuse.
	if h.rdb != nil {
		r.Use(h.feedRateLimit())
	}

	v1 := r.Group("/v1/feed")
	{
		v1.GET("/delta", h.FeedDelta)
		v1.GET("/home", h.GetHomeFeed)
		v1.GET("/reels", h.GetReelFeed)
		v1.GET("/flicks", h.GetFlickFeed)
		v1.GET("/videos", h.GetLongVideoFeed)
		v1.GET("/watch", h.GetVideoFeed)
		v1.POST("/preference", h.SetPreference)
		v1.POST("/signal", h.PostSignal)
		// MF9 — /internal/debug requires an admin scope at the
		// gateway (requireAdminForInternalPaths). The legacy /debug
		// path is kept as a 410 Gone so any deployed client surfaces
		// the move loudly instead of silently 401'ing.
		v1.GET("/internal/debug", h.DebugFeed)
		v1.GET("/debug", h.legacyDebugGone)

		// Feed control
		v1.POST("/hide/:postId", h.HidePost)
		v1.DELETE("/hide/:postId", h.UnhidePost)
		v1.POST("/mute", h.MuteTarget)
		v1.DELETE("/mute", h.UnmuteTarget)
		v1.GET("/muted", h.GetMutedTargets)
	}
}

func (h *Handler) GetHomeFeed(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}

	feedMode := c.DefaultQuery("feed_mode", "")
	if feedMode == "" {
		// Check user's saved preference, default to chronological
		feedMode = h.svc.GetUserFeedMode(c.Request.Context(), userID)
	}
	if feedMode != "ranked" && feedMode != "shadow" {
		feedMode = "chronological"
	}

	excludeSelf := c.DefaultQuery("exclude_self", "") == "true"
	circleOnly := c.DefaultQuery("circle_only", "") == "true"
	followingOnly := c.DefaultQuery("following_only", "") == "true"
	platform := c.DefaultQuery("platform", "") // "postbook" | "posttube" | ""

	var cursor *time.Time
	if rawCursor := c.Query("cursor"); rawCursor != "" {
		parsed, err := time.Parse(time.RFC3339Nano, rawCursor)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil)
			return
		}
		cursor = &parsed
	}

	feedItems, err := h.svc.GetHomeFeed(c.Request.Context(), userID, limit, feedMode, excludeSelf, circleOnly, followingOnly, cursor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	// Hydrate with full post details from post-service.
	// On failure: fail loud (502) instead of silently returning the
	// bare FeedItem rows — those have no text/media so the client
	// would render every post as a blank "Shared a post" placeholder,
	// which masks the real problem (post-service unreachable, wrong
	// POST_SERVICE_URL, INTERNAL_SERVICE_KEY mismatch, etc).
	hydrated, err := h.svc.HydratePosts(c.Request.Context(), feedItems, userID)
	if err != nil {
		log.Printf("ERROR: post hydration failed: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway, "HYDRATION_FAILED", "Could not load post details — try again", nil)
		return
	}

	// Platform filter: Postbook sees only social posts (post/poll) plus PostTube
	// videos the author explicitly opted to share there. PostTube uses its own
	// dedicated /feed/flicks and /feed/videos endpoints instead.
	if platform == "postbook" {
		videoTypes := map[string]bool{"flick": true, "long_video": true, "video": true, "reel": true, "short": true}
		filtered := hydrated[:0]
		for _, p := range hydrated {
			if videoTypes[p.ContentType] {
				if p.ShareToPostbook {
					filtered = append(filtered, p)
				}
			} else {
				filtered = append(filtered, p)
			}
		}
		hydrated = filtered
	}

	c.Writer.Header().Set("X-Feed-Mode", feedMode)
	var meta *api.Meta
	if len(feedItems) >= limit {
		meta = &api.Meta{NextCursor: feedItems[len(feedItems)-1].CreatedAt.UTC().Format(time.RFC3339Nano)}
	}
	api.JSON(c.Writer, http.StatusOK, hydrated, meta)
}

func (h *Handler) GetReelFeed(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	feedItems, err := h.svc.GetReelFeed(c.Request.Context(), userID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	hydrated, err := h.svc.HydratePosts(c.Request.Context(), feedItems, userID)
	if err != nil {
		log.Printf("Warning: reel feed hydration failed: %v", err)
		c.Writer.Header().Set("X-Feed-Surface", "reels")
		api.JSON(c.Writer, http.StatusOK, feedItems, nil)
		return
	}

	c.Writer.Header().Set("X-Feed-Surface", "reels")
	api.JSON(c.Writer, http.StatusOK, hydrated, nil)
}

func (h *Handler) GetFlickFeed(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	feedItems, err := h.svc.GetFlickFeed(c.Request.Context(), userID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	hydrated, err := h.svc.HydratePosts(c.Request.Context(), feedItems, userID)
	if err != nil {
		log.Printf("Warning: flick feed hydration failed: %v", err)
		c.Writer.Header().Set("X-Feed-Surface", "flicks")
		api.JSON(c.Writer, http.StatusOK, feedItems, nil)
		return
	}

	c.Writer.Header().Set("X-Feed-Surface", "flicks")
	api.JSON(c.Writer, http.StatusOK, hydrated, nil)
}

func (h *Handler) GetLongVideoFeed(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	feedItems, err := h.svc.GetLongVideoFeed(c.Request.Context(), userID, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	hydrated, err := h.svc.HydratePosts(c.Request.Context(), feedItems, userID)
	if err != nil {
		log.Printf("Warning: long video feed hydration failed: %v", err)
		c.Writer.Header().Set("X-Feed-Surface", "videos")
		api.JSON(c.Writer, http.StatusOK, feedItems, nil)
		return
	}

	c.Writer.Header().Set("X-Feed-Surface", "videos")
	api.JSON(c.Writer, http.StatusOK, hydrated, nil)
}

func (h *Handler) GetVideoFeed(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limitStr := c.DefaultQuery("limit", "20")
	limit, err := strconv.Atoi(limitStr)
	if err != nil || limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}

	followingOnly := c.DefaultQuery("following_only", "") == "true"
	feedItems, err := h.svc.GetVideoFeed(c.Request.Context(), userID, limit, followingOnly)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	hydrated, err := h.svc.HydratePosts(c.Request.Context(), feedItems, userID)
	if err != nil {
		log.Printf("Warning: video feed hydration failed: %v", err)
		c.Writer.Header().Set("X-Feed-Surface", "watch")
		api.JSON(c.Writer, http.StatusOK, feedItems, nil)
		return
	}

	c.Writer.Header().Set("X-Feed-Surface", "watch")
	api.JSON(c.Writer, http.StatusOK, hydrated, nil)
}

type preferenceRequest struct {
	FeedMode string `json:"feed_mode" binding:"required"`
}

func (h *Handler) SetPreference(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req preferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if req.FeedMode != "ranked" && req.FeedMode != "chronological" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "feed_mode must be 'ranked' or 'chronological'", nil)
		return
	}

	if err := h.svc.SetUserFeedMode(c.Request.Context(), userID, req.FeedMode); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"feed_mode": req.FeedMode}, nil)
}

type signalRequest struct {
	PostID string `json:"post_id" binding:"required"`
	Signal string `json:"signal" binding:"required"`
}

func (h *Handler) PostSignal(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req signalRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if req.Signal != "see_less" && req.Signal != "see_more" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "signal must be 'see_less' or 'see_more'", nil)
		return
	}

	postID, err := uuid.Parse(req.PostID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}

	if err := h.svc.RecordSignal(c.Request.Context(), userID, postID, req.Signal); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "recorded"}, nil)
}

// feedRateLimit enforces a per-user 120 req/min cap on the feed
// surface. Redis sliding-window via INCR+EXPIRE (same pattern as
// notification autocomplete, see HS3). Skips when X-User-Id is empty
// (anonymous browsing) or when the request carries the trusted
// internal-service key.
func (h *Handler) feedRateLimit() gin.HandlerFunc {
	const limit int64 = 120
	const window = time.Minute
	return func(c *gin.Context) {
		// Skip if anonymous / unauthenticated paths.
		userID := c.GetHeader("X-User-Id")
		if userID == "" {
			c.Next()
			return
		}
		// Skip if the request is from an internal caller.
		if h.internalKey != "" && c.GetHeader("X-Internal-Service-Key") == h.internalKey {
			// Trusted callsite — let it through.
			c.Next()
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 200*time.Millisecond)
		defer cancel()
		key := fmt.Sprintf("feed_rl:%s", userID)
		pipe := h.rdb.Pipeline()
		incr := pipe.Incr(ctx, key)
		pipe.Expire(ctx, key, window)
		if _, err := pipe.Exec(ctx); err != nil {
			// Fail-OPEN on Redis blip: feed is not security-critical
			// (no money, no auth), and dropping every request during a
			// Redis hiccup is worse than serving a few extra.
			slog.Warn("feed rate limit: redis error — failing open", "err", err)
			c.Next()
			return
		}
		if incr.Val() > limit {
			c.Header("Retry-After", "60")
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED",
				"feed request rate exceeded", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

// legacyDebugGone returns 410 on the pre-MF9 /v1/feed/debug path so
// any deployed client immediately sees the move to /internal/debug
// instead of silently failing with a 401 once the gateway admin gate
// is in place.
func (h *Handler) legacyDebugGone(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "MOVED",
		"GET /v1/feed/debug is removed — use /v1/feed/internal/debug (admin only).",
		map[string]any{"new_path": "/v1/feed/internal/debug"})
}

func (h *Handler) DebugFeed(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	debug, err := h.svc.DebugFeed(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, debug, nil)
}

func (h *Handler) HidePost(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	if err := h.svc.HidePost(c.Request.Context(), userID, postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) UnhidePost(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	if err := h.svc.UnhidePost(c.Request.Context(), userID, postID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) MuteTarget(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req struct {
		TargetType string     `json:"target_type" binding:"required"`
		TargetID   string     `json:"target_id" binding:"required"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if err := h.svc.MuteTarget(c.Request.Context(), userID, req.TargetType, req.TargetID, req.ExpiresAt); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "MUTE_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) UnmuteTarget(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req struct {
		TargetType string `json:"target_type" binding:"required"`
		TargetID   string `json:"target_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if err := h.svc.UnmuteTarget(c.Request.Context(), userID, req.TargetType, req.TargetID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) GetMutedTargets(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	mutes, err := h.svc.GetMutedTargets(c.Request.Context(), userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if mutes == nil {
		mutes = []postgres.FeedMute{}
	}
	api.JSON(c.Writer, http.StatusOK, mutes, nil)
}
