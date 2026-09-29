package http

import (
	"errors"
	"net/http"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Copyright Match phase 1 — internal fingerprint routes. Service-to-service
// only (behind the internal key); nothing here is reachable through the
// gateway, and the responses carry no URLs and no user ids.

// EnqueueFingerprintRequest is the optional body of the POST.
type EnqueueFingerprintRequest struct {
	// Retry re-queues a job that is failed or skipped. A queued, claimed or
	// done job is always left alone.
	Retry bool `json:"retry"`
}

// EnqueueFingerprint — POST /v1/media/internal/:mediaId/fingerprint
//
//	202 {"media_id","created","job":{...}}   created=false means the row already existed
//	400 BAD_REQUEST        bad id or body
//	404 NOT_FOUND          no such asset
//	409 NOT_READY          not a ready video of a settled generation
func (h *Handler) EnqueueFingerprint(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	var body EnqueueFingerprintRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "body must be {\"retry\": bool} or empty", nil)
			return
		}
	}
	res, err := h.svc.EnqueueFingerprint(c.Request.Context(), mediaID, body.Retry)
	switch {
	case errors.Is(err, service.ErrAssetNotFound):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil)
		return
	case errors.Is(err, service.ErrFingerprintNotReady):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "NOT_READY", err.Error(), nil)
		return
	case err != nil:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusAccepted, res, nil)
}

// GetFingerprintStatus — GET /v1/media/internal/:mediaId/fingerprint
//
//	200 FingerprintStatus
//	404 NOT_FOUND
func (h *Handler) GetFingerprintStatus(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	res, err := h.svc.GetFingerprintStatus(c.Request.Context(), mediaID)
	switch {
	case errors.Is(err, service.ErrAssetNotFound):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil)
		return
	case err != nil:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}
