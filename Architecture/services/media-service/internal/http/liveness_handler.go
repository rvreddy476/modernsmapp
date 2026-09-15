package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// LivenessPath is the internal blink-liveness route (lane D5). Same
// authentication as FaceComparePath: internal key, no user identity, not
// reachable through the gateway.
const LivenessPath = "/internal/v1/media/faces/liveness"

// Liveness-specific error codes.
const (
	CodeVideoUnsupported      = "VIDEO_UNSUPPORTED"
	CodeFrameBurstUnsupported = "FRAME_BURST_UNSUPPORTED"
)

type livenessRequest struct {
	VideoMediaID     string   `json:"video_media_id"`
	FrameMediaIDs    []string `json:"frame_media_ids"`
	ReferenceMediaID string   `json:"reference_media_id"`
	RequesterUserID  string   `json:"requester_user_id"`
}

// CheckLiveness — POST /internal/v1/media/faces/liveness.
//
// Body: {video_media_id, reference_media_id, requester_user_id}. The video is
// the user's ≤4 s blink recording (a normal video upload, ready and
// moderation-passed); the reference is their approved primary photo.
// 200: {blinks_detected, frames_analysed, duration_ms, single_face,
// same_face_across_frames, similarity, match, reason?, provider}.
// A frame burst (frame_media_ids) is not supported: 400.
func (h *Handler) CheckLiveness(c *gin.Context) {
	ctx := c.Request.Context()
	var body livenessRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
		return
	}
	if len(body.FrameMediaIDs) > 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeFrameBurstUnsupported,
			"frame bursts are not supported; upload a short video as video_media_id", nil)
		return
	}
	video, errV := uuid.Parse(strings.TrimSpace(body.VideoMediaID))
	reference, errR := uuid.Parse(strings.TrimSpace(body.ReferenceMediaID))
	requester, errU := uuid.Parse(strings.TrimSpace(body.RequesterUserID))
	if errV != nil || errR != nil || errU != nil || video == uuid.Nil || reference == uuid.Nil || requester == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"video_media_id, reference_media_id and requester_user_id must be UUIDs", nil)
		return
	}
	out, err := h.faceCompare.CheckLiveness(ctx, requester, video, reference)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrFaceMediaNotFound):
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeMediaNotFound, "media not found", nil)
		case errors.Is(err, service.ErrFaceSameMedia):
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeSameMedia, "video and reference must be different media", nil)
		case errors.Is(err, service.ErrLivenessVideoUnsupported):
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeVideoUnsupported, "video cannot be analysed", nil)
		case errors.Is(err, service.ErrFaceImageUnsupported):
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeImageUnsupported, "reference image cannot be compared", nil)
		case errors.Is(err, processing.ErrFaceCompareUnavailable):
			api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeFaceCompareUnavailable, "liveness check is unavailable; retry later", nil)
		default:
			slog.Error("media: liveness failed", "video_media_id", video, "reference_media_id", reference, "error", err)
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "liveness check failed", nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
