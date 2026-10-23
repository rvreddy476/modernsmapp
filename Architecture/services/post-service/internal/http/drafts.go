package http

import (
	"encoding/json"
	"errors"
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
		switch {
		// Hashtags follow the create route's rule, so they answer with its
		// codes (INVALID_HASHTAG, TOO_MANY_HASHTAGS).
		case writeScheduleError(c, err):
		// Video credits answer with the create route's codes
		// (CREDITS_NOT_ALLOWED, INVALID_CREDIT, INVALID_GENRE, ...).
		case writeCreditFieldError(c, err):
		// tier_required_id is a tier id, or null / "" to clear it:
		// 400 INVALID_TIER otherwise.
		case writeTierGateError(c, err):
		// audio_track_id is a sound id, or "" to remove the sound;
		// content_type is flick or long_video.
		case errors.Is(err, service.ErrInvalidDraftSound) || errors.Is(err, service.ErrInvalidLocationPlaceID) ||
			errors.Is(err, service.ErrInvalidDraftContentType):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
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
	// TierRequiredID, when present, is stored on the draft first (exactly as
	// PATCH /v1/drafts/:id would: a tier id sets, null / "" clears), so the
	// post — published now, or later by the schedule worker — is gated from
	// birth. CreatePost checks it against the author's active tiers.
	TierRequiredID json.RawMessage `json:"tier_required_id"`
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

	if len(req.TierRequiredID) > 0 {
		if _, err := h.svc.UpdateDraft(c.Request.Context(), draftID, authorID, &service.UpdateDraftInput{TierRequiredID: req.TierRequiredID}); err != nil {
			if !writeTierGateError(c, err) {
				writeReelDraftPublishError(c, err)
			}
			return
		}
	}

	post, err := h.svc.PublishDraft(c.Request.Context(), draftID, authorID, req.ScheduleAt)
	if err != nil {
		writeReelDraftPublishError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, post, nil)
}

// writeReelDraftPublishError answers a refused reel-draft publish. The draft
// publishes through CreatePost, so its refusals of the request are the
// create route's 4xx, as for composer drafts (writePostDraftError). Since
// a draft keeps its kind (migration 069), a long video reaches the
// long-video guards here too: CHANNEL_REQUIRED and TITLE_REQUIRED used to
// come back as 500 INTERNAL_ERROR, which tells the client to retry.
func writeReelDraftPublishError(c *gin.Context, err error) {
	switch {
	case writeStandingError(c, err):
	case writeCoverMediaError(c, err):
	case writeDistributionError(c, err):
	case writeCreateGuardError(c, err):
	case writeScheduleError(c, err):
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}
