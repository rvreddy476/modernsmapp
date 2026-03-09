package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterReelEngagementRoutes adds reel-specific engagement endpoints.
func (h *Handler) RegisterReelEngagementRoutes(r *gin.Engine) {
	reels := r.Group("/v1/reels")
	{
		reels.POST("/:reelId/react", h.ReactToReel)
		reels.DELETE("/:reelId/react", h.UnreactToReel)
		reels.GET("/:reelId/react/me", h.GetReelReaction)
		reels.POST("/:reelId/comments", h.AddReelComment)
		reels.GET("/:reelId/comments", h.ListReelComments)
		reels.POST("/:reelId/share", h.ShareReel)
		reels.POST("/:reelId/save", h.SaveReel)
		reels.DELETE("/:reelId/save", h.UnsaveReel)
		reels.GET("/:reelId/saved", h.IsReelSaved)
		reels.POST("/:reelId/view", h.RecordReelView)
		reels.GET("/:reelId/counts", h.GetReelCounts)
		reels.POST("/batch/counts", h.BatchGetReelCounts)
		reels.GET("/saved", h.ListSavedReels)
		reels.GET("/liked", h.GetUserReelLikes)
	}
}

// ─── Request types ──────────────────────────────────────────────────

type reactToReelRequest struct {
	Reaction string `json:"reaction" binding:"required"`
}

type addReelCommentRequest struct {
	Text string `json:"text" binding:"required"`
}

type shareReelRequest struct {
	ShareType string `json:"share_type"`
}

type recordReelViewRequest struct {
	SessionID string `json:"session_id"`
	WatchedMs int64  `json:"watched_ms"`
	Surface   string `json:"surface"`
}

type batchReelCountsRequest struct {
	ReelIDs []string `json:"reel_ids" binding:"required"`
}

// ─── Handlers ───────────────────────────────────────────────────────

func (h *Handler) ReactToReel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	var req reactToReelRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.ReactToReel(c.Request.Context(), reelID, userID, req.Reaction); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

func (h *Handler) UnreactToReel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	if err := h.svc.UnreactToReel(c.Request.Context(), reelID, userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

func (h *Handler) GetReelReaction(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	reaction, err := h.svc.GetReelReaction(c.Request.Context(), reelID, userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"reaction": reaction}, nil)
}

func (h *Handler) AddReelComment(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	var req addReelCommentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	commentID, err := h.svc.AddReelComment(c.Request.Context(), reelID, userID, req.Text)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, map[string]string{"comment_id": commentID.String()}, nil)
}

func (h *Handler) ListReelComments(c *gin.Context) {
	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	comments, err := h.svc.ListReelComments(c.Request.Context(), reelID, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if comments == nil {
		api.JSON(c.Writer, http.StatusOK, []struct{}{}, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, comments, nil)
}

func (h *Handler) ShareReel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	var req shareReelRequest
	// Body is optional; ignore bind errors for empty body.
	_ = c.ShouldBindJSON(&req)
	if req.ShareType == "" {
		req.ShareType = "direct"
	}

	if err := h.svc.ShareReel(c.Request.Context(), reelID, userID, req.ShareType); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

func (h *Handler) SaveReel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	if err := h.svc.SaveReel(c.Request.Context(), reelID, userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

func (h *Handler) UnsaveReel(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	if err := h.svc.UnsaveReel(c.Request.Context(), reelID, userID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

func (h *Handler) IsReelSaved(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	saved, err := h.svc.IsReelSaved(c.Request.Context(), reelID, userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]bool{"saved": saved}, nil)
}

func (h *Handler) RecordReelView(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	var req recordReelViewRequest
	_ = c.ShouldBindJSON(&req)

	if err := h.svc.RecordReelView(c.Request.Context(), reelID, userID, req.SessionID, req.WatchedMs, req.Surface); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

func (h *Handler) GetReelCounts(c *gin.Context) {
	reelID, err := uuid.Parse(c.Param("reelId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID", nil, nil)
		return
	}

	counts, err := h.svc.GetReelCounts(c.Request.Context(), reelID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

func (h *Handler) BatchGetReelCounts(c *gin.Context) {
	var req batchReelCountsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	ids := make([]uuid.UUID, 0, len(req.ReelIDs))
	for _, raw := range req.ReelIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid reel ID: "+raw, nil, nil)
			return
		}
		ids = append(ids, id)
	}

	counts, err := h.svc.BatchGetReelCounts(c.Request.Context(), ids)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, counts, nil)
}

func (h *Handler) ListSavedReels(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	reelIDs, err := h.svc.ListSavedReels(c.Request.Context(), userID, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if reelIDs == nil {
		reelIDs = []string{}
	}

	api.JSON(c.Writer, http.StatusOK, reelIDs, nil)
}

func (h *Handler) GetUserReelLikes(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "20")); err == nil && l > 0 {
		limit = l
	}

	reelIDs, err := h.svc.GetUserReelLikes(c.Request.Context(), userID, limit)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	if reelIDs == nil {
		reelIDs = []string{}
	}

	api.JSON(c.Writer, http.StatusOK, reelIDs, nil)
}
