package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Original sounds on reels (2026-09-29).
//
//	POST /v1/media/internal/:mediaId/sound   internal key only
//	  body {"title","artist","source_post_id","creator_user_id"}, all optional
//	  200  {data: Sound} — the one sound of the video, extracted on first use
//	  400  BAD_REQUEST       bad id or body
//	  404  NOT_FOUND         no such asset
//	  422  NOT_A_VIDEO | TOO_LONG | NOT_READY | NO_AUDIO
//	  503  SOUND_UNAVAILABLE storage, ffmpeg or store fault; retry
//
//	GET  /v1/audio/:audioId/serve
//	  307  Location: the presigned audio; Cache-Control / Vary as the media
//	       serve route writes them
//	HEAD /v1/audio/:audioId/serve
//	  200  no body, no Location; Content-Type audio/mp4, Accept-Ranges bytes.
//	       No Content-Length: the row records no size, and the object is
//	       never opened to learn one.
//	  404  NOT_FOUND "Audio track not found" — denied or missing, one answer
//	  503  DEPENDENCY_UNAVAILABLE — the audience could not be determined
//
// The decisions live in service/sounds.go and service/record_read.go; this
// file is the wiring the handler tests pin.

// soundService is the slice of the service the two ensure routes use.
type soundService interface {
	Ensure(ctx context.Context, in service.EnsureSoundInput) (*postgres.AudioTrack, error)
	EnsureForOwner(ctx context.Context, ownerID uuid.UUID, in service.EnsureSoundInput) (*postgres.AudioTrack, error)
}

func (h *Handler) soundsSvc() soundService {
	if h.sounds != nil {
		return h.sounds
	}
	return h.svc.Sounds()
}

// EnsureSoundRequest names the sound. source_post_id is the reel it is
// taken from and creator_user_id that reel's author.
type EnsureSoundRequest struct {
	Title         string     `json:"title"`
	Artist        string     `json:"artist"`
	SourcePostID  *uuid.UUID `json:"source_post_id"`
	CreatorUserID *uuid.UUID `json:"creator_user_id"`
}

// EnsureSound — POST /v1/media/internal/:mediaId/sound
func (h *Handler) EnsureSound(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	var body EnsureSoundRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST",
				"body must be {\"title\",\"artist\",\"source_post_id\":\"<uuid>\",\"creator_user_id\":\"<uuid>\"} or empty", nil)
			return
		}
	}
	track, err := h.soundsSvc().Ensure(c.Request.Context(), service.EnsureSoundInput{
		MediaID:       mediaID,
		Title:         body.Title,
		Artist:        body.Artist,
		SourcePostID:  body.SourcePostID,
		CreatorUserID: body.CreatorUserID,
	})
	if err != nil {
		writeSoundError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, track, nil)
}

// writeSoundError answers an ensure that did not yield a sound. A fault's
// text stays on the server: it names object keys and ffmpeg's output.
func writeSoundError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrAssetNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil)
	case errors.Is(err, service.ErrNotMediaOwner):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "You do not own this media", nil)
	case errors.Is(err, service.ErrSoundNotAVideo):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "NOT_A_VIDEO", "Only a video has a sound", nil)
	case errors.Is(err, service.ErrSoundTooLong):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "TOO_LONG", "The video is longer than 300 seconds", nil)
	case errors.Is(err, service.ErrSoundNotReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "NOT_READY", "The video is not ready", nil)
	case errors.Is(err, service.ErrSoundNoAudio):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "NO_AUDIO", "The video has no audio", nil)
	default:
		slog.Error("sound: ensure failed", "media_id", c.Param("mediaId"), "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "SOUND_UNAVAILABLE", "The sound could not be produced; retry", nil)
	}
}

// soundContentType is what every sound is stored as: AAC in an M4A
// container (service/sounds.go).
const soundContentType = "audio/mp4"

// ServeAudioTrack — GET /v1/audio/:audioId/serve
func (h *Handler) ServeAudioTrack(c *gin.Context) {
	url, ok := h.authorizedSoundURL(c)
	if !ok {
		return
	}
	writeDeliveryRedirect(c, url)
}

// HeadAudioTrack — HEAD /v1/audio/:audioId/serve
//
// The GET's own decision, signer included, with the URL discarded: a 200
// here means the GET would have produced its redirect.
func (h *Handler) HeadAudioTrack(c *gin.Context) {
	if _, ok := h.authorizedSoundURL(c); !ok {
		return
	}
	writeDeliveryCacheHeaders(c)
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Type", soundContentType)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
}

// authorizedSoundURL is the /url route's decision; when it refuses, the
// refusal has been written.
func (h *Handler) authorizedSoundURL(c *gin.Context) (string, bool) {
	audioID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return "", false
	}
	url, err := h.recordsSvc().AudioTrackURLForViewer(c.Request.Context(), deliveryViewer(c), audioID)
	if err != nil {
		writeDeliveryErrorAs(c, err, "Audio track not found")
		return "", false
	}
	return url, true
}
