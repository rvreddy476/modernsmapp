package http

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/atpost/media-service/internal/delivery"
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}

	var req ExtractAudioRequest
	_ = c.ShouldBindJSON(&req)

	// Verify ownership
	media, err := h.svc.GetMedia(c.Request.Context(), mediaID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil)
		return
	}
	if media.UploaderID != userID {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this media", nil)
		return
	}

	track, err := h.svc.ExtractAudioFromMedia(c.Request.Context(), mediaID, req.Title, req.Artist)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, track, nil)
}

func (h *Handler) GetAudioTrack(c *gin.Context) {
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return
	}

	// A sound is a piece of its source video: its record is answered to that
	// video's audience (service/audio_reads.go). It used to be answered to
	// anyone holding the id, storage key and source included.
	track, err := h.recordsSvc().AudioTrackForViewer(c.Request.Context(), deliveryViewer(c), audioID)
	if err != nil {
		writeDeliveryErrorAs(c, err, "Audio track not found")
		return
	}

	api.JSON(c.Writer, http.StatusOK, track, nil)
}

func (h *Handler) GetAudioTrackURL(c *gin.Context) {
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return
	}

	// The track is the audio of a source video: the URL is answered to that
	// video's audience through the delivery gate (service/record_read.go).
	// It used to take no viewer at all. A denial is the same body as a
	// missing track; an unresolved authority is a retryable 503.
	url, err := h.recordsSvc().AudioTrackURLForViewer(c.Request.Context(), deliveryViewer(c), audioID)
	if err != nil {
		writeDeliveryErrorAs(c, err, "Audio track not found")
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"url": url}, nil)
}

func (h *Handler) GetTrendingAudio(c *gin.Context) {
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	// Only the sounds this viewer may hear: a private video's sound is not
	// listed to a stranger. An unresolved authority is a retryable 503, never
	// a shorter list that looks complete.
	tracks, err := h.recordsSvc().TrendingAudioForViewer(c.Request.Context(), deliveryViewer(c), limit, offset)
	if err != nil {
		writeAudioListError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": tracks}, nil)
}

func (h *Handler) SearchAudio(c *gin.Context) {
	query := c.Query("q")
	if query == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Query parameter 'q' is required", nil)
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	tracks, err := h.recordsSvc().SearchAudioForViewer(c.Request.Context(), deliveryViewer(c), query, limit, offset)
	if err != nil {
		writeAudioListError(c, err)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": tracks}, nil)
}

// writeAudioListError answers a sound list that could not be built. There is
// no denial for a list (a sound the viewer may not hear is left out), so the
// only outcomes are "try again" and a fault whose text stays on the server.
func writeAudioListError(c *gin.Context, err error) {
	if errors.Is(err, delivery.ErrDeliveryUnresolved) {
		writeDeliveryErrorAs(c, err, "Audio track not found")
		return
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not list audio tracks", nil)
}

func (h *Handler) UseAudioTrack(c *gin.Context) {
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return
	}

	// Only a sound the caller may hear can be counted as used by them.
	if err := h.recordsSvc().UseAudioTrackAsViewer(c.Request.Context(), deliveryViewer(c), audioID); err != nil {
		if errors.Is(err, delivery.ErrDeliveryDenied) || errors.Is(err, delivery.ErrDeliveryUnresolved) {
			writeDeliveryErrorAs(c, err, "Audio track not found")
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not record the use", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

func (h *Handler) UploadVoiceover(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "audio file required (form field: audio)", nil)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "failed to read file", nil)
		return
	}

	mimeType := header.Header.Get("Content-Type")
	if mimeType == "" {
		mimeType = "audio/webm"
	}

	asset, err := h.svc.RecordVoiceover(c.Request.Context(), userID, data, mimeType)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
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
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return
	}

	track, err := h.svc.GetAudioLibraryTrackByID(c.Request.Context(), audioID)
	if err != nil || track == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Audio track not found", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, track, nil)
}
