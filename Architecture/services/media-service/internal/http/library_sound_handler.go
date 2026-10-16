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

// Library sounds on posts (2026-10-15); the rules are service/library_sounds.go.
//
//	POST /v1/audio-library/:audioId/use      signed in
//	  200 {data: {"audio_track_id": "<uuid>", "sound": Sound}}
//	  400 BAD_REQUEST       bad id
//	  401 UNAUTHORIZED      no caller
//	  404 NOT_FOUND         no such library row, inactive, or withdrawn
//	  422 NOT_STORED        the row has no audio this service can deliver
//	  503 SOUND_UNAVAILABLE retry
//
//	GET  /v1/audio-library/:audioId/serve    public
//	  307 Location: the presigned audio (or the row's https URL)
//	HEAD /v1/audio-library/:audioId/serve
//	  200 no body; Content-Type audio/mp4, Accept-Ranges bytes
//	  404 NOT_FOUND "Audio track not found" — missing, inactive or undeliverable
//	  503 DEPENDENCY_UNAVAILABLE
//
// audio_track_id is an ordinary audio_tracks id: it goes wherever a client
// already sends one (post create, draft, publish), and the sound then
// plays, lists and counts like any other (/v1/audio/:id/serve,
// /v1/posts/by-sound/:id).

// librarySoundService is the slice of the service the library routes use.
type librarySoundService interface {
	Use(ctx context.Context, userID, libraryID uuid.UUID) (*postgres.AudioTrack, error)
	PreviewURL(ctx context.Context, libraryID uuid.UUID) (string, error)
}

func (h *Handler) librarySvc() librarySoundService {
	if h.library != nil {
		return h.library
	}
	return h.svc.LibrarySounds()
}

// UseAudioLibraryTrack — POST /v1/audio-library/:audioId/use
func (h *Handler) UseAudioLibraryTrack(c *gin.Context) {
	ctx := c.Request.Context()
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || userID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}
	libraryID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return
	}
	track, err := h.librarySvc().Use(ctx, userID, libraryID)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrLibrarySoundNotFound):
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Sound not found", nil)
		case errors.Is(err, service.ErrLibrarySoundNotStored):
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "NOT_STORED", "This library sound has no audio to play", nil)
		default:
			slog.Error("library sound: use failed", "library_id", libraryID, "error", err)
			api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "SOUND_UNAVAILABLE", "The sound could not be prepared; retry", nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"audio_track_id": track.ID, "sound": track}, nil)
}

// ServeAudioLibraryTrack — GET /v1/audio-library/:audioId/serve
func (h *Handler) ServeAudioLibraryTrack(c *gin.Context) {
	url, ok := h.libraryPreviewURL(c)
	if !ok {
		return
	}
	writeDeliveryRedirect(c, url)
}

// HeadAudioLibraryTrack — HEAD /v1/audio-library/:audioId/serve
func (h *Handler) HeadAudioLibraryTrack(c *gin.Context) {
	if _, ok := h.libraryPreviewURL(c); !ok {
		return
	}
	writeDeliveryCacheHeaders(c)
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Type", soundContentType)
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
}

func (h *Handler) libraryPreviewURL(c *gin.Context) (string, bool) {
	libraryID, err := uuid.Parse(c.Param("audioId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return "", false
	}
	url, err := h.librarySvc().PreviewURL(c.Request.Context(), libraryID)
	if err != nil {
		writeDeliveryErrorAs(c, err, "Audio track not found")
		return "", false
	}
	return url, true
}
