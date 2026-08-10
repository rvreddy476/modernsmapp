package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 1 P0-5 — server-side drafts + scheduling for the unified
// composer (text / photo / carousel / poll / article). Reel drafts keep
// their legacy /v1/reels/drafts endpoints untouched.

// RegisterPostDraftRoutes adds /v1/posts/drafts endpoints.
func (h *Handler) RegisterPostDraftRoutes(r *gin.Engine) {
	drafts := r.Group("/v1/posts/drafts")
	{
		drafts.POST("", h.CreatePostDraft)
		drafts.GET("", h.ListPostDrafts)
		drafts.GET("/:draftId", h.GetPostDraft)
		drafts.PATCH("/:draftId", h.UpdatePostDraft)
		drafts.DELETE("/:draftId", h.DeletePostDraft)
		drafts.POST("/:draftId/publish", h.PublishPostDraft)
	}
}

type postDraftRequest struct {
	PostType string          `json:"post_type"`
	Payload  json.RawMessage `json:"payload" binding:"required"`
	// ScheduleAt is RFC3339; clients send UTC (the mobile composer
	// converts from local display time before sending — P0-2).
	ScheduleAt *string `json:"schedule_at"`
}

func parseScheduleAt(raw *string) (*time.Time, bool) {
	if raw == nil || *raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, *raw)
	if err != nil {
		return nil, false
	}
	utc := t.UTC()
	return &utc, true
}

func (h *Handler) writePostDraftError(c *gin.Context, err error) {
	switch {
	case writeDistributionError(c, err):
	case errors.Is(err, service.ErrInvalidDraft):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_DRAFT", err.Error(), nil)
	case errors.Is(err, service.ErrDraftNotFound):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "draft not found", nil)
	case errors.Is(err, service.ErrDraftNotEditable):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "DRAFT_NOT_EDITABLE", err.Error(), nil)
	case errors.Is(err, postgres.ErrDraftQuota):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "DRAFT_QUOTA", "too many drafts; delete some first", nil)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}

func (h *Handler) CreatePostDraft(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	var req postDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	scheduleAt, ok := parseScheduleAt(req.ScheduleAt)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "schedule_at must be RFC3339", nil)
		return
	}
	d, err := h.svc.CreatePostDraft(c.Request.Context(), authorID, req.PostType, req.Payload, scheduleAt)
	if err != nil {
		h.writePostDraftError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, d, nil)
}

func (h *Handler) ListPostDrafts(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	drafts, err := h.svc.ListPostDrafts(c.Request.Context(), authorID, limit)
	if err != nil {
		h.writePostDraftError(c, err)
		return
	}
	if drafts == nil {
		drafts = []postgres.PostDraft{}
	}
	api.JSON(c.Writer, http.StatusOK, drafts, nil)
}

func (h *Handler) GetPostDraft(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	draftID, err := uuid.Parse(c.Param("draftId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid draft ID", nil)
		return
	}
	d, err := h.svc.GetPostDraft(c.Request.Context(), draftID, authorID)
	if err != nil {
		h.writePostDraftError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, d, nil)
}

func (h *Handler) UpdatePostDraft(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	draftID, err := uuid.Parse(c.Param("draftId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid draft ID", nil)
		return
	}
	var req postDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	scheduleAt, ok := parseScheduleAt(req.ScheduleAt)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "schedule_at must be RFC3339", nil)
		return
	}
	d, err := h.svc.UpdatePostDraft(c.Request.Context(), draftID, authorID, req.PostType, req.Payload, scheduleAt)
	if err != nil {
		h.writePostDraftError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, d, nil)
}

func (h *Handler) DeletePostDraft(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	draftID, err := uuid.Parse(c.Param("draftId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid draft ID", nil)
		return
	}
	if err := h.svc.DeletePostDraft(c.Request.Context(), draftID, authorID); err != nil {
		h.writePostDraftError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]bool{"deleted": true}, nil)
}

type publishPostDraftRequest struct {
	// ScheduleAt: RFC3339 → schedule instead of publishing immediately.
	ScheduleAt *string `json:"schedule_at"`
}

func (h *Handler) PublishPostDraft(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	draftID, err := uuid.Parse(c.Param("draftId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid draft ID", nil)
		return
	}
	var req publishPostDraftRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
			return
		}
	}
	scheduleAt, ok := parseScheduleAt(req.ScheduleAt)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "schedule_at must be RFC3339", nil)
		return
	}

	post, draft, err := h.svc.PublishPostDraft(c.Request.Context(), draftID, authorID, scheduleAt)
	if err != nil {
		h.writePostDraftError(c, err)
		return
	}
	if draft != nil {
		// Scheduled, not yet published.
		api.JSON(c.Writer, http.StatusOK, draft, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, post, nil)
}
