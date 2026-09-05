package http

import (
	"errors"
	"net/http"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ReprocessMediaRequest carries the optional operator rotation override.
type ReprocessMediaRequest struct {
	// RotateDegrees: display rotation to stamp onto the original before
	// processing, degrees counter-clockwise, a multiple of 90. Omit or 0 to
	// re-run with the file's own metadata.
	RotateDegrees int `json:"rotate_degrees"`
}

// ReprocessMedia — POST /v1/media/internal/:mediaId/reprocess
//
// Service-to-service only (Tube thumbnail sideways, 2026-09-05). Queues the
// transcode worker to redo a video's thumbnail, renditions, HLS ladder and
// measured size from the original object. An empty body is a plain re-run.
//
//	202 {"media_id","event_id","rotate_degrees","status":"queued"}
//	400 BAD_REQUEST        bad id, bad body, or a non-quarter-turn rotation
//	404 NOT_FOUND          no such asset
//	409 NOT_VIDEO          asset has no transcode pipeline
//	409 TRANSCODE_IN_FLIGHT a request or completion is still unpublished
func (h *Handler) ReprocessMedia(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	var body ReprocessMediaRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "body must be {\"rotate_degrees\": <multiple of 90>} or empty", nil)
			return
		}
	}
	res, err := h.svc.ReprocessVideo(c.Request.Context(), mediaID, body.RotateDegrees)
	switch {
	case errors.Is(err, service.ErrBadRotation):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	case errors.Is(err, service.ErrAssetNotFound):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil)
		return
	case errors.Is(err, service.ErrNotVideo):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "NOT_VIDEO", err.Error(), nil)
		return
	case errors.Is(err, service.ErrTranscodeInFlight):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "TRANSCODE_IN_FLIGHT", err.Error(), nil)
		return
	case err != nil:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, res, nil)
}
