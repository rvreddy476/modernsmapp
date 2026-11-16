package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Retired sounds (2026-11-16, migration 032).
//
// When its author deletes a video, the sound taken from it is RETIRED: the
// reels that already play it keep playing it. A takedown is the other case:
// copyright or moderation removes the sound itself, and with it every
// reel's copy of it.
//
//	POST /v1/media/internal/sounds/:audioId/takedown   internal key only;
//	                                                   a gateway identity is refused
//	  200 {data: {"status": "taken_down", "objects": <queued object count>}}
//	      the row is kept (posts name it) as status 'deleted' with no keys;
//	      repeating it is a 200 with objects 0
//	  400 BAD_REQUEST     bad id
//	  404 NOT_FOUND       no such sound
//	  409 LIBRARY_SOUND   a platform library sound: withdraw it through the
//	                      admin sound library ("everywhere") instead
//	  503 SOUND_TAKEDOWN_UNAVAILABLE  store fault; retry
//
// No caller is wired yet: the admin console's copyright/moderation action
// is the intended one.

// SoundTakedownPath is the route under /v1/media/internal.
const SoundTakedownPath = "/sounds/:audioId/takedown"

// soundTakedownService is the slice of the service the route uses.
type soundTakedownService interface {
	TakeDownSound(ctx context.Context, soundID uuid.UUID) (int, error)
}

func (h *Handler) takedownSvc() soundTakedownService {
	if h.takedowns != nil {
		return h.takedowns
	}
	return h.svc
}

// TakeDownSoundInternal — POST /v1/media/internal/sounds/:audioId/takedown
func (h *Handler) TakeDownSoundInternal(c *gin.Context) {
	ctx := c.Request.Context()
	soundID, err := uuid.Parse(c.Param("audioId"))
	if err != nil || soundID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid audio ID", nil)
		return
	}
	objects, err := h.takedownSvc().TakeDownSound(ctx, soundID)
	switch {
	case errors.Is(err, service.ErrSoundNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Sound not found", nil)
		return
	case errors.Is(err, service.ErrLibrarySoundTakedown):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "LIBRARY_SOUND",
			"A library sound is withdrawn through the sound library", nil)
		return
	case err != nil:
		slog.ErrorContext(ctx, "sound takedown failed", "audio_track_id", soundID, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "SOUND_TAKEDOWN_UNAVAILABLE",
			"The sound could not be taken down; retry", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "taken_down", "objects": objects}, nil)
}
