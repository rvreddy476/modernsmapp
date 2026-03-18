package http

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc         *service.Service
	internalKey string
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

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Apply internal service key enforcement to all /v1 routes.
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
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
		v1.GET("/debug", h.DebugFeed)

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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
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
	platform := c.DefaultQuery("platform", "") // "postbook" | "posttube" | ""

	feedItems, err := h.svc.GetHomeFeed(c.Request.Context(), userID, limit, feedMode, excludeSelf, circleOnly)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	// Hydrate with full post details from post-service
	hydrated, err := h.svc.HydratePosts(c.Request.Context(), feedItems, userID)
	if err != nil {
		// Log but don't fail — return raw feed items as fallback
		log.Printf("Warning: post hydration failed: %v", err)
		c.Writer.Header().Set("X-Feed-Mode", feedMode)
		api.JSON(c.Writer, http.StatusOK, feedItems, nil)
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
	api.JSON(c.Writer, http.StatusOK, hydrated, nil)
}

func (h *Handler) GetReelFeed(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
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
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
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
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
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
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
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

	feedItems, err := h.svc.GetVideoFeed(c.Request.Context(), userID, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req preferenceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	if req.FeedMode != "ranked" && req.FeedMode != "chronological" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "feed_mode must be 'ranked' or 'chronological'", nil, nil)
		return
	}

	if err := h.svc.SetUserFeedMode(c.Request.Context(), userID, req.FeedMode); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req signalRequest
	if err := json.NewDecoder(c.Request.Body).Decode(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	if req.Signal != "see_less" && req.Signal != "see_more" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "signal must be 'see_less' or 'see_more'", nil, nil)
		return
	}

	postID, err := uuid.Parse(req.PostID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}

	if err := h.svc.RecordSignal(c.Request.Context(), userID, postID, req.Signal); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "recorded"}, nil)
}

func (h *Handler) DebugFeed(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	debug, err := h.svc.DebugFeed(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, debug, nil)
}

func (h *Handler) HidePost(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	if err := h.svc.HidePost(c.Request.Context(), userID, postID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) UnhidePost(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil, nil)
		return
	}
	if err := h.svc.UnhidePost(c.Request.Context(), userID, postID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) MuteTarget(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req struct {
		TargetType string     `json:"target_type" binding:"required"`
		TargetID   string     `json:"target_id" binding:"required"`
		ExpiresAt  *time.Time `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	if err := h.svc.MuteTarget(c.Request.Context(), userID, req.TargetType, req.TargetID, req.ExpiresAt); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "MUTE_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) UnmuteTarget(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	var req struct {
		TargetType string `json:"target_type" binding:"required"`
		TargetID   string `json:"target_id" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	if err := h.svc.UnmuteTarget(c.Request.Context(), userID, req.TargetType, req.TargetID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) GetMutedTargets(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	mutes, err := h.svc.GetMutedTargets(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if mutes == nil {
		mutes = []postgres.FeedMute{}
	}
	api.JSON(c.Writer, http.StatusOK, mutes, nil)
}
