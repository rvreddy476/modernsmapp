// flick_series_handler.go holds the flick series routes (create, get,
// episodes, add an episode, follow / unfollow, a creator's series) and the
// remix token route. Moved out of handler.go unchanged.

package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ── Flick Series ─────────────────────────────────────────────

func (h *Handler) CreateFlickSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return
	}
	var req struct {
		Title       string `json:"title" binding:"required"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	fs, err := h.svc.CreateFlickSeries(c.Request.Context(), userID, req.Title, req.Description)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "CREATE_SERIES_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, fs, nil)
}

func (h *Handler) GetFlickSeries(c *gin.Context) {
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil)
		return
	}
	fs, err := h.svc.GetFlickSeries(c.Request.Context(), seriesID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, fs, nil)
}

func (h *Handler) GetSeriesEpisodes(c *gin.Context) {
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil)
		return
	}
	// The series owner gets every episode; everyone else loses the ones
	// they may not open (service/series_visibility.go). 404 for no series.
	items, err := h.svc.GetSeriesEpisodes(c.Request.Context(), seriesID, optionalCallerID(c))
	if err != nil {
		if errors.Is(err, service.ErrFlickSeriesNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "series not found", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if items == nil {
		items = []postgres.FlickSeriesItem{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items}, nil)
}

func (h *Handler) AddEpisodeToSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return
	}
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil)
		return
	}
	var req struct {
		PostID     uuid.UUID `json:"post_id" binding:"required"`
		EpisodeNum int       `json:"episode_num" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	item, err := h.svc.AddEpisodeToSeries(c.Request.Context(), userID, seriesID, req.PostID, req.EpisodeNum)
	if err != nil {
		if strings.Contains(err.Error(), "forbidden") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "ADD_EPISODE_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, item, nil)
}

func (h *Handler) FollowSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return
	}
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil)
		return
	}
	if err := h.svc.FollowSeries(c.Request.Context(), userID, seriesID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) UnfollowSeries(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return
	}
	seriesID, err := uuid.Parse(c.Param("seriesId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid series id", nil)
		return
	}
	if err := h.svc.UnfollowSeries(c.Request.Context(), userID, seriesID); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

func (h *Handler) ListCreatorSeries(c *gin.Context) {
	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid creator id", nil)
		return
	}
	series, err := h.svc.ListFlickSeriesByCreator(c.Request.Context(), creatorID, 20, 0)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if series == nil {
		series = []postgres.FlickSeries{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"series": series}, nil)
}

func (h *Handler) GetRemixToken(c *gin.Context) {
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid post id", nil)
		return
	}
	// The post detail's read gate (read_gate.go, 2026-09-29).
	if !h.requirePostReadable(c, postID, optionalCallerID(c)) {
		return
	}
	result, err := h.svc.GetRemixToken(c.Request.Context(), postID)
	if err != nil {
		if strings.Contains(err.Error(), "does not allow") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "REMIX_NOT_ALLOWED", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}
