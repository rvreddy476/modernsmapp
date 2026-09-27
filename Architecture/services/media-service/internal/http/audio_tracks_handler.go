package http

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Alternate audio tracks (2026-09-27).
//
//	GET    /v1/media/:id/audio-tracks           any viewer the serve gate admits
//	POST   /v1/media/:id/audio-tracks           owner; multipart file+language[+label]
//	POST   /v1/media/:id/audio-tracks/generate  owner; JSON {language, source_language?}
//	DELETE /v1/media/:id/audio-tracks/:trackId  owner
//
// Playback is the existing /serve/:variant route with variant dub_<lang>_<rung>.

// audioTracksService is the slice of the service these endpoints use; an
// interface so the handler tests drive every status path without PostgreSQL.
type audioTracksService interface {
	List(ctx context.Context, viewerID, mediaID uuid.UUID) ([]service.AudioTrackView, error)
	Upload(ctx context.Context, callerID, mediaID uuid.UUID, in service.AudioTrackUpload) (*service.AudioTrackView, error)
	Generate(ctx context.Context, callerID, mediaID uuid.UUID, language, sourceLanguage string) (*service.AudioTrackView, error)
	Delete(ctx context.Context, callerID, mediaID, trackID uuid.UUID) error
}

// registerAudioTrackRoutes adds the four routes to the /v1/media group.
func (h *Handler) registerAudioTrackRoutes(v1 *gin.RouterGroup, authMW gin.HandlerFunc) {
	v1.GET("/:mediaId/audio-tracks", h.ListAudioTracks)
	v1.POST("/:mediaId/audio-tracks", authMW, h.UploadAudioTrack)
	v1.POST("/:mediaId/audio-tracks/generate", authMW, h.GenerateAudioTrack)
	v1.DELETE("/:mediaId/audio-tracks/:trackId", authMW, h.DeleteAudioTrack)
}

func (h *Handler) audioTracksSvc() audioTracksService {
	if h.audioTracks != nil {
		return h.audioTracks
	}
	return h.svc.AudioTracks()
}

// ListAudioTracks — GET /v1/media/:mediaId/audio-tracks
func (h *Handler) ListAudioTracks(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	tracks, err := h.audioTracksSvc().List(c.Request.Context(), deliveryViewer(c), mediaID)
	if err != nil {
		writeAudioTrackError(c, err)
		return
	}
	if tracks == nil {
		tracks = []service.AudioTrackView{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": tracks}, nil)
}

// UploadAudioTrack — POST /v1/media/:mediaId/audio-tracks (multipart)
func (h *Handler) UploadAudioTrack(c *gin.Context) {
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

	// Bound the whole body before the multipart parser buffers it.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxAudioTrackUploadBytes+64*1024)
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "audio file exceeds 50 MB", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "audio file required (form field: file)", nil)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, service.MaxAudioTrackUploadBytes+1))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "failed to read file", nil)
		return
	}
	if int64(len(data)) > service.MaxAudioTrackUploadBytes {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", "audio file exceeds 50 MB", nil)
		return
	}
	mime := header.Header.Get("Content-Type")
	if mime == "" {
		mime = "application/octet-stream"
	}

	track, err := h.audioTracksSvc().Upload(c.Request.Context(), userID, mediaID, service.AudioTrackUpload{
		Data:     data,
		Mime:     mime,
		Language: c.PostForm("language"),
		Label:    c.PostForm("label"),
	})
	if err != nil {
		writeAudioTrackError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, track, nil)
}

// GenerateAudioTrack — POST /v1/media/:mediaId/audio-tracks/generate
func (h *Handler) GenerateAudioTrack(c *gin.Context) {
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
		Language       string `json:"language"`
		SourceLanguage string `json:"source_language"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}
	if strings.TrimSpace(body.Language) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "language is required", nil)
		return
	}
	track, err := h.audioTracksSvc().Generate(c.Request.Context(), userID, mediaID, body.Language, body.SourceLanguage)
	if err != nil {
		writeAudioTrackError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, track, nil)
}

// DeleteAudioTrack — DELETE /v1/media/:mediaId/audio-tracks/:trackId
func (h *Handler) DeleteAudioTrack(c *gin.Context) {
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
	trackID, err := uuid.Parse(c.Param("trackId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid track ID", nil)
		return
	}
	if err := h.audioTracksSvc().Delete(c.Request.Context(), userID, mediaID, trackID); err != nil {
		writeAudioTrackError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// writeAudioTrackError maps the feature's errors onto the contract.
func writeAudioTrackError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, delivery.ErrDeliveryDenied), errors.Is(err, delivery.ErrDeliveryUnresolved):
		writeDeliveryError(c, err)
	case errors.Is(err, service.ErrNotMediaOwner):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this media", nil)
	case errors.Is(err, service.ErrAudioTrackMediaNotFound), errors.Is(err, postgres.ErrAudioTrackNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Not found", nil)
	case errors.Is(err, service.ErrAudioTrackMediaNotReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "MEDIA_NOT_READY", "Video is not ready yet", nil)
	case errors.Is(err, service.ErrAudioDurationMismatch):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "AUDIO_DURATION_MISMATCH", err.Error(), nil)
	case errors.Is(err, service.ErrAudioTrackLimit):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "AUDIO_TRACK_LIMIT", "This video already has the maximum number of audio tracks", nil)
	case errors.Is(err, service.ErrDubbingUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "DUBBING_UNAVAILABLE", "AI dubbing is not available", nil)
	case errors.Is(err, service.ErrAudioTrackInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_AUDIO_TRACK", err.Error(), nil)
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}
