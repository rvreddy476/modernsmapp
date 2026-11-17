package http

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
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
		// Trending sounds (2026-10-24): the stored ranking, signed in
		// (trending_sounds_handler.go).
		v1.GET("/trending", authMW, h.GetTrendingSounds)
		v1.GET("/search", h.SearchAudio)
		v1.GET("/:audioId", h.GetAudioTrack)
		v1.GET("/:audioId/url", h.GetAudioTrackURL)
		// The bytes of a sound, for an <audio> element: the /url decision,
		// answered as a redirect (sound_handler.go).
		v1.GET("/:audioId/serve", h.ServeAudioTrack)
		v1.HEAD("/:audioId/serve", h.HeadAudioTrack)
		v1.POST("/:audioId/use", authMW, h.UseAudioTrack)
		v1.POST("/voiceover", authMW, h.UploadVoiceover)
	}

	lib := r.Group("/v1/audio-library")
	{
		lib.GET("", h.GetAudioLibrary)
		lib.GET("/:audioId", h.GetAudioLibraryTrack)
		// The library row's audio, for a preview before it is used: the
		// same redirect as /v1/audio/:id/serve (library_sound_handler.go).
		lib.GET("/:audioId/serve", h.ServeAudioLibraryTrack)
		lib.HEAD("/:audioId/serve", h.HeadAudioLibraryTrack)
		// Use a library sound: its audio_tracks row, made on first use. The
		// answer's audio_track_id is what a post's audio_track_id takes.
		lib.POST("/:audioId/use", authMW, h.UseAudioLibraryTrack)
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

	// Owner-only, and the same ensure path as the internal route
	// (service/sounds.go): a second call answers the row the first one made.
	track, err := h.soundsSvc().EnsureForOwner(c.Request.Context(), userID, service.EnsureSoundInput{
		MediaID: mediaID, Title: req.Title, Artist: req.Artist,
	})
	if err != nil {
		writeSoundError(c, err)
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
	viaPost, ok := soundCarrierPost(c)
	if !ok {
		return
	}
	track, err := h.recordsSvc().AudioTrackForViewerVia(c.Request.Context(), deliveryViewer(c), audioID, viaPost)
	if err != nil {
		writeDeliveryErrorAs(c, err, "Audio track not found")
		return
	}

	// The sound page's badge (trending sounds, 2026-10-24): its place in
	// the stored ranking, or null.
	api.JSON(c.Writer, http.StatusOK, soundWithTrending{
		track:    track,
		trending: h.trendingBadge(c.Request.Context(), track.ID),
	}, nil)
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
	viaPost, ok := soundCarrierPost(c)
	if !ok {
		return
	}
	url, err := h.recordsSvc().AudioTrackURLForViewerVia(c.Request.Context(), deliveryViewer(c), audioID, viaPost)
	if err != nil {
		writeDeliveryErrorAs(c, err, "Audio track not found")
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"url": url}, nil)
}

// soundCarrierParam names the reel a viewer is playing a sound on
// (GET /v1/audio/:id, /url, /serve and HEAD /serve). Optional; a RETIRED
// sound (its source video deleted by its author) is heard only through a
// live reel that plays it, so a client playing a post's sound always sends
// it. When the request does not name one it is uuid.Nil.
const soundCarrierParam = "post_id"

// soundCarrierPost reads ?post_id=; a value that is not a uuid is answered
// 400 here (false).
func soundCarrierPost(c *gin.Context) (uuid.UUID, bool) {
	raw := c.Query(soundCarrierParam)
	if raw == "" {
		return uuid.Nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid post_id", nil)
		return uuid.Nil, false
	}
	return id, true
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
		if errors.Is(err, service.ErrSoundUnavailableForNewPosts) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, "SOUND_UNAVAILABLE",
				"This sound can no longer be added to new posts", nil)
			return
		}
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

	// reels-16: bound the whole body before the multipart parser buffers it
	// (service.MaxVoiceoverUploadBytes plus room for the multipart framing),
	// then the file part itself. It used to be io.ReadAll with no cap.
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, service.MaxVoiceoverUploadBytes+multipartOverheadBytes)
	file, header, err := c.Request.FormFile("audio")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeVoiceoverTooLarge(c)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "audio file required (form field: audio)", nil)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(io.LimitReader(file, service.MaxVoiceoverUploadBytes+1))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "failed to read file", nil)
		return
	}
	if int64(len(data)) > service.MaxVoiceoverUploadBytes {
		writeVoiceoverTooLarge(c)
		return
	}

	// Informational only: the demuxer is chosen from the bytes.
	mimeType := header.Header.Get("Content-Type")

	asset, err := h.svc.RecordVoiceover(c.Request.Context(), userID, data, mimeType)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrVoiceoverTooLarge):
			writeVoiceoverTooLarge(c)
		case errors.Is(err, service.ErrVoiceoverUnsupported):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
				"voiceover must be MP4/M4A, WebM, Ogg, WAV, FLAC, MP3, AAC or AMR audio", nil)
		case errors.Is(err, service.ErrVoiceoverUnreadable):
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "AUDIO_UNREADABLE",
				"the audio could not be decoded", nil)
		default:
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "could not store the voiceover", nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusCreated, asset, nil)
}

// multipartOverheadBytes is the room a multipart body gets above its file
// part's cap: boundaries, part headers and small form fields.
const multipartOverheadBytes = 64 * 1024

func writeVoiceoverTooLarge(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
		"voiceover exceeds 50 MB", map[string]any{"max_bytes": service.MaxVoiceoverUploadBytes})
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
	out := make([]postgres.AudioLibraryTrack, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, service.PresentLibraryTrack(t))
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"tracks": out, "total": len(out)}, nil)
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

	api.JSON(c.Writer, http.StatusOK, service.PresentLibraryTrack(*track), nil)
}
