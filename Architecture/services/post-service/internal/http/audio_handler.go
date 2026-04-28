package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterAudioRoutes registers audio/music layer endpoints.
func (h *Handler) RegisterAudioRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/audio/tracks")
	{
		v1.POST("", h.CreateAudioTrack)
		v1.GET("/trending", h.GetTrendingAudio)
		v1.GET("/search", h.SearchAudioTracks)
		v1.GET("/:trackId", h.GetAudioTrackByID)
	}
}

type CreateAudioTrackRequest struct {
	Title          string     `json:"title" binding:"required"`
	Artist         string     `json:"artist"`
	DurationMs     int        `json:"duration_ms"`
	MediaID        string     `json:"media_id" binding:"required"`
	OriginalPostID *string    `json:"original_post_id"`
	Genre          string     `json:"genre"`
	IsOriginal     *bool      `json:"is_original"`
}

func (h *Handler) CreateAudioTrack(c *gin.Context) {
	var req CreateAudioTrackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	mediaID, err := uuid.Parse(req.MediaID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media_id", nil)
		return
	}

	track := &postgres.AudioTrack{
		Title:      req.Title,
		Artist:     req.Artist,
		DurationMs: req.DurationMs,
		MediaID:    mediaID,
		Genre:      req.Genre,
		IsOriginal: true,
	}

	if req.IsOriginal != nil {
		track.IsOriginal = *req.IsOriginal
	}

	if req.OriginalPostID != nil {
		pid, err := uuid.Parse(*req.OriginalPostID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid original_post_id", nil)
			return
		}
		track.OriginalPostID = &pid
	}

	result, err := h.svc.CreateAudioTrack(c.Request.Context(), track)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, result, nil)
}

func (h *Handler) GetTrendingAudio(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	tracks, err := h.svc.GetTrendingAudio(c.Request.Context(), limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if tracks == nil {
		tracks = []postgres.AudioTrack{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": tracks}, nil)
}

func (h *Handler) GetAudioTrackByID(c *gin.Context) {
	trackID, err := uuid.Parse(c.Param("trackId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid track ID", nil)
		return
	}

	track, err := h.svc.GetAudioTrack(c.Request.Context(), trackID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Audio track not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, track, nil)
}

func (h *Handler) SearchAudioTracks(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Query parameter 'q' is required", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))

	tracks, err := h.svc.SearchAudio(c.Request.Context(), query, limit)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if tracks == nil {
		tracks = []postgres.AudioTrack{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": tracks}, nil)
}
