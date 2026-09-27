package http

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube captions list for creators (2026-09-27).
//
//	GET   /v1/subtitles/mine?status=all|draft|published&cursor=&limit=
//	      200 {"data":{"items":[{media_id, post_id: null,
//	                             languages:[{language, source, published, updated_at}],
//	                             modified_at}]},
//	           "meta":{"next_cursor"}}          (next_cursor only when more)
//	PATCH /v1/subtitles/:mediaId/:language  {"published": bool}   owner
//	      200 {"data": <media_subtitles row>}
//	      403 FORBIDDEN, 404 NOT_FOUND (no such track), 400 BAD_REQUEST
//
// Both authenticated: the list is the caller's own, the patch is a write.

// captionsMineService is the slice of the service these routes use; an
// interface so the handler tests run without PostgreSQL.
type captionsMineService interface {
	ListMyCaptions(ctx context.Context, callerID uuid.UUID, status, cursor string, limit int) (*service.CaptionListPage, error)
	SetCaptionPublished(ctx context.Context, callerID, mediaID uuid.UUID, language string, published bool) (*postgres.MediaSubtitle, error)
}

func (h *Handler) registerCaptionsMineRoutes(subtitles *gin.RouterGroup, authMW gin.HandlerFunc) {
	subtitles.GET("/mine", authMW, h.ListMyCaptions)
	subtitles.PATCH("/:mediaId/:language", authMW, h.SetCaptionPublished)
}

func (h *Handler) captionsMineSvc() captionsMineService {
	if h.captionsMine != nil {
		return h.captionsMine
	}
	return h.svc
}

// ListMyCaptions — GET /v1/subtitles/mine
func (h *Handler) ListMyCaptions(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	limit := 20
	if raw := c.Query("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "limit must be a positive integer", nil)
			return
		}
		limit = n
	}
	page, err := h.captionsMineSvc().ListMyCaptions(c.Request.Context(), userID, c.Query("status"), c.Query("cursor"), limit)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrInvalidCaptionFilter), errors.Is(err, service.ErrInvalidCaptionCursor):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	var meta *api.Meta
	if page.NextCursor != "" {
		meta = &api.Meta{NextCursor: page.NextCursor}
	}
	items := page.Items
	if items == nil {
		items = []service.CaptionMediaItem{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, meta)
}

// SetCaptionPublished — PATCH /v1/subtitles/:mediaId/:language
func (h *Handler) SetCaptionPublished(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	var body struct {
		Published *bool `json:"published"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Published == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "body must be {\"published\": true|false}", nil)
		return
	}
	sub, err := h.captionsMineSvc().SetCaptionPublished(c.Request.Context(), userID, mediaID, c.Param("language"), *body.Published)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrNotMediaOwner):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this media", nil)
		case errors.Is(err, service.ErrCaptionTrackNotFound):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "No caption track for that language", nil)
		case errors.Is(err, service.ErrInvalidCaption):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, sub, nil)
}
