package http

import (
	"io"
	"net/http"
	"strconv"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterAudioRoutes registers audio-related endpoints.
func (h *Handler) RegisterAudioRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	v1 := r.Group("/v1/audio")
	{
		v1.POST("/extract/:mediaId", authMW, h.ExtractAudio)
		v1.GET("/trending", h.GetTrendingAudio)
		v1.GET("/search", h.SearchAudio)
		v1.GET("/:audioId", h.GetAudioTrack)
		v1.GET("/:audioId/url", h.GetAudioTrackURL)
		v1.POST("/:audioId/use", authMW, h.UseAudioTrack)
		v1.POST("/voiceover", authMW, h.UploadVoiceover)
	}

	lib := r.Group("/v1/audio-library")
	{
		lib.GET("", h.GetAudioLibrary)
		lib.GET("/:audioId", h.GetAudioLibraryTrack)
	}
}

type ExtractAudioRequest struct {
	Title  string `json:"title"`
	Artist string `json:"artist"`
}

func (h *Handler) ExtractAudio(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil, nil)
		return
	}

	var req ExtractAudioRequest
	_ = c.ShouldBindJSON(&req)

	// Verify ownership
	media, err := h.svc.GetMedia(c.Request.Context(), mediaID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil, nil)
		return
	}
	if media.UploaderID != userID {
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this media", nil, nil)
		return
	}

	track, err := h.svc.ExtractAudioFromMedia(c.Request.Context(), mediaID, req.Title, req.Artist)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, track, nil)
}

func (h *Handler) GetAudioTrack(c *gin.Context) {
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil, nil)
		return
	}

	track, err := h.svc.GetAudioTrack(c.Request.Context(), audioID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Audio track not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, track, nil)
}

func (h *Handler) GetAudioTrackURL(c *gin.Context) {
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil, nil)
		return
	}

	url, err := h.svc.GetAudioTrackURL(c.Request.Context(), audioID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Audio track not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"url": url}, nil)
}

func (h *Handler) GetTrendingAudio(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	tracks, err := h.svc.GetTrendingAudio(c.Request.Context(), limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": tracks}, nil)
}

func (h *Handler) SearchAudio(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Query parameter 'q' is required", nil, nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	tracks, err := h.svc.SearchAudio(c.Request.Context(), query, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": tracks}, nil)
}

func (h *Handler) UseAudioTrack(c *gin.Context) {
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil, nil)
		return
	}

	if err := h.svc.UseAudioTrack(c.Request.Context(), audioID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

func (h *Handler) UploadVoiceover(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "audio file required (form field: audio)", nil, nil)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read file", nil, nil)
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "audio/webm"
	}

	asset, err := h.svc.RecordVoiceover(c.Request.Context(), userID, data, mimeType)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusCreated, asset, nil)
}

func (h *Handler) GetAudioLibrary(c *gin.Context) {
	genre := c.Query("genre")
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	var genrePtr *string
	if genre != "" {
		genrePtr = &genre
	}

	tracks, err := h.svc.GetTrendingAudioLibrary(c.Request.Context(), genrePtr, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if tracks == nil {
		tracks = []postgres.AudioLibraryTrack{}
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": tracks, "total": len(tracks)}, nil)
}

func (h *Handler) GetAudioLibraryTrack(c *gin.Context) {
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil, nil)
		return
	}

	track, err := h.svc.GetAudioLibraryTrackByID(c.Request.Context(), audioID)
	if err != nil || track == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Audio track not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, track, nil)
}
