package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterDraftRoutes adds /v1/reels/drafts endpoints to the router.
func (h *Handler) RegisterDraftRoutes(r *gin.Engine) {
	drafts := r.Group("/v1/reels/drafts")
	{
		drafts.POST("", h.CreateDraft)
		drafts.GET("", h.ListDrafts)
		drafts.GET("/:draftId", h.GetDraft)
		drafts.PATCH("/:draftId", h.UpdateDraft)
		drafts.DELETE("/:draftId", h.DeleteDraft)
		drafts.POST("/:draftId/publish", h.PublishDraft)
	}
}

type createDraftRequest struct {
	MediaID    *string `json:"media_id"`
	Visibility string  `json:"visibility"`
	Caption    string  `json:"caption"`
}

func (h *Handler) CreateDraft(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req createDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	var mediaID *uuid.UUID
	if req.MediaID != nil {
		id, err := uuid.Parse(*req.MediaID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid media_id", nil)
			return
		}
		mediaID = &id
	}

	draft, err := h.svc.CreateDraft(c.Request.Context(), &service.CreateDraftInput{
		AuthorID:   authorID,
		MediaID:    mediaID,
		Visibility: req.Visibility,
		Caption:    req.Caption,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, draft, nil)
}

func (h *Handler) GetDraft(c *gin.Context) {
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

	draft, err := h.svc.GetDraft(c.Request.Context(), draftID, authorID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Draft not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, draft, nil)
}

func (h *Handler) UpdateDraft(c *gin.Context) {
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

	var input service.UpdateDraftInput
	if err := c.ShouldBindJSON(&input); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}

	draft, err := h.svc.UpdateDraft(c.Request.Context(), draftID, authorID, &input)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, draft, nil)
}

func (h *Handler) ListDrafts(c *gin.Context) {
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	limit := 20
	if l, err := strconv.Atoi(c.Query("limit")); err == nil && l > 0 && l <= 50 {
		limit = l
	}

	var cursor *time.Time
	if cs := c.Query("cursor"); cs != "" {
		if t, err := time.Parse(time.RFC3339Nano, cs); err == nil {
			cursor = &t
		}
	}

	drafts, nextCursor, err := h.svc.ListDrafts(c.Request.Context(), authorID, limit, cursor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	var meta *api.Meta
	if nextCursor != nil {
		meta = &api.Meta{NextCursor: nextCursor.Format(time.RFC3339Nano)}
	}

	api.JSON(c.Writer, http.StatusOK, drafts, meta)
}

func (h *Handler) DeleteDraft(c *gin.Context) {
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

	if err := h.svc.DeleteDraft(c.Request.Context(), draftID, authorID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	c.Writer.WriteHeader(http.StatusNoContent)
}

type publishDraftRequest struct {
	ScheduleAt *string `json:"schedule_at"`
}

func (h *Handler) PublishDraft(c *gin.Context) {
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

	var req publishDraftRequest
	_ = c.ShouldBindJSON(&req) // Body is optional

	post, err := h.svc.PublishDraft(c.Request.Context(), draftID, authorID, req.ScheduleAt)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, post, nil)
}
