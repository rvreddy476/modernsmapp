package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterResumableRoutes registers resumable upload endpoints.
func (h *Handler) RegisterResumableRoutes(r *gin.Engine, authMW gin.HandlerFunc) {
	v1 := r.Group("/v1/media/upload")
	{
		v1.POST("/resumable/init", authMW, h.InitResumableUpload)
		v1.GET("/resumable/:uploadId", authMW, h.GetResumableUploadStatus)
		v1.POST("/resumable/:uploadId/chunk", authMW, h.UploadChunk)
		v1.POST("/resumable/:uploadId/complete", authMW, h.CompleteResumableUpload)
	}
}

type InitResumableUploadRequest struct {
	FileType   string `json:"file_type" binding:"required,oneof=image video"`
	MimeType   string `json:"mime_type" binding:"required"`
	TotalBytes int64  `json:"total_bytes" binding:"required,min=1"`
	// DurationMs is the video's length as the client read it (optional):
	// over the length cap the upload is refused before it starts (lv-30).
	DurationMs int64 `json:"duration_ms" binding:"min=0"`
}

func (h *Handler) InitResumableUpload(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	var req InitResumableUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	resp, err := h.svc.InitResumableUpload(c.Request.Context(), userID, req.FileType, req.MimeType, req.TotalBytes,
		service.WithDeclaredDurationMs(req.DurationMs))
	if err != nil {
		if writeUploadLimitError(c, err) {
			return
		}
		if strings.Contains(err.Error(), "upload rate limit exceeded") {
			// The count limit InitUpload answers 429 for; this route said 400.
			c.Header("Retry-After", "3600")
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", err.Error(), nil)
			return
		}
		if errors.Is(err, service.ErrVideoTooLarge) {
			writeVideoTooLarge(c, err)
			return
		}
		if errors.Is(err, service.ErrResumableUploadTooLarge) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusRequestEntityTooLarge, "FILE_TOO_LARGE", err.Error(),
				map[string]any{"max_bytes": service.MaxResumableUploadBytes})
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// CodeVideoTooLarge: a video original over the cap the transcode worker
// takes (processing.VideoOriginalCap, env MEDIA_WORKER_MAX_ORIGINAL_BYTES),
// refused at init on either upload route, and again on a resumable chunk or
// complete whose bytes pass it (a session opened under a larger cap).
// Not USE_RESUMABLE_UPLOAD: the other route refuses it too.
//
//	413 {"error": {"code": "VIDEO_TOO_LARGE", "message": <VideoTooLargeMessage>,
//	      "details": {"max_bytes": <cap>, "limit": "processing_file_size"}}}
//
// The limit is on the FILE, for processing; long videos have no length cap.
const CodeVideoTooLarge = "VIDEO_TOO_LARGE"

// VideoTooLargeLimit is details.limit of a VIDEO_TOO_LARGE refusal.
const VideoTooLargeLimit = "processing_file_size"

func writeVideoTooLarge(c *gin.Context, err error) {
	if err != nil {
		slog.InfoContext(c.Request.Context(), "upload refused: video over the processing cap", "error", err)
	}
	limit := service.VideoOriginalCap()
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusRequestEntityTooLarge, CodeVideoTooLarge,
		service.VideoTooLargeMessage(limit),
		map[string]any{"max_bytes": limit, "limit": VideoTooLargeLimit})
}

func (h *Handler) GetResumableUploadStatus(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid upload ID", nil)
		return
	}

	upload, err := h.svc.GetResumableUploadStatus(c.Request.Context(), uploadID, userID)
	if err != nil {
		if writeUploadLimitError(c, err) {
			return
		}
		// A store failure is not "your upload is gone": a client discards a
		// session on 404, so an outage answers 500 and the client retries.
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "could not read the upload", nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, upload, nil)
}

// UploadChunk receives one part's raw bytes as the request body. The part
// index comes from the `part_number` query parameter (1-based) and the
// size from Content-Length. media-service streams the body straight into
// the object store's multipart upload.
func (h *Handler) UploadChunk(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid upload ID", nil)
		return
	}

	partNumber, err := strconv.Atoi(c.Query("part_number"))
	if err != nil || partNumber < 1 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "part_number query parameter required", nil)
		return
	}

	size := c.Request.ContentLength
	if size <= 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Content-Length header required", nil)
		return
	}

	resp, err := h.svc.UploadPart(c.Request.Context(), uploadID, userID, partNumber, c.Request.Body, size)
	if err != nil {
		if writeUploadLimitError(c, err) {
			return
		}
		if errors.Is(err, service.ErrVideoTooLarge) {
			writeVideoTooLarge(c, err)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func (h *Handler) CompleteResumableUpload(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return
	}

	uploadID, err := uuid.Parse(c.Param("uploadId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid upload ID", nil)
		return
	}

	media, err := h.svc.CompleteResumableUpload(c.Request.Context(), uploadID, userID)
	if err != nil {
		if writeUploadLimitError(c, err) {
			return
		}
		if errors.Is(err, service.ErrVideoTooLarge) {
			writeVideoTooLarge(c, err)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", err.Error(), nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, media, nil)
}
