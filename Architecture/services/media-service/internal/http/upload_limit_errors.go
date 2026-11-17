package http

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Upload-path refusal codes added by lane media-upload (2026-11-17). Every
// one carries a message a client can show as is.
//
//	413 VIDEO_TOO_LONG              {"max_duration_seconds": n, "limit": "processing_duration"}
//	    a declared (init) or probed (confirm/complete) length over
//	    MEDIA_MAX_VIDEO_DURATION_SEC. Final for this file.
//	429 UPLOAD_QUOTA_EXCEEDED       {"limit": "daily_video_bytes"|"daily_video_seconds",
//	                                 "max": n, "used": n, "requested": n, "window_seconds": 86400}
//	    Retry-After: 3600. At confirm the upload is kept (pending_upload):
//	    confirm/complete may be retried later.
//	429 LONG_VIDEO_IN_PROGRESS      {"limit": "inflight_long_videos", "max": n, "used": n}
//	    Retry-After: 600. Same retry rule as above.
//	503 UPLOAD_LIMITER_UNAVAILABLE  Retry-After: 30. The upload limits could
//	    not be read; nothing was charged. Transient.
//	410 UPLOAD_EXPIRED              a resumable session past its expiry; start
//	    a new upload.
//	409 UPLOAD_INCOMPLETE           complete before every part landed; read
//	    the status and send the missing parts.
const (
	CodeVideoTooLong             = "VIDEO_TOO_LONG"
	CodeUploadQuotaExceeded      = "UPLOAD_QUOTA_EXCEEDED"
	CodeLongVideoInProgress      = "LONG_VIDEO_IN_PROGRESS"
	CodeUploadLimiterUnavailable = "UPLOAD_LIMITER_UNAVAILABLE"
	CodeUploadExpired            = "UPLOAD_EXPIRED"
	CodeUploadIncomplete         = "UPLOAD_INCOMPLETE"
)

// writeUploadLimitError answers err when it is one of the refusals above
// and reports whether it did.
func writeUploadLimitError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	var quota *service.UploadQuotaError
	switch {
	case errors.Is(err, service.ErrVideoTooLong):
		limit := service.CurrentUploadLimits().VideoMaxDurationSeconds
		api.ErrorWithContext(ctx, c.Writer, http.StatusRequestEntityTooLarge, CodeVideoTooLong,
			service.VideoTooLongMessage(limit),
			map[string]any{"max_duration_seconds": int64(limit), "limit": "processing_duration"})
	case errors.As(err, &quota):
		code, retry := CodeUploadQuotaExceeded, 3600
		details := map[string]any{"limit": quota.Limit, "max": quota.Max, "used": quota.Used}
		if quota.Limit == service.QuotaLimitInflightLong {
			code, retry = CodeLongVideoInProgress, 600
		} else {
			details["requested"] = quota.Asked
			details["window_seconds"] = 86400
		}
		c.Header("Retry-After", strconv.Itoa(retry))
		api.ErrorWithContext(ctx, c.Writer, http.StatusTooManyRequests, code, quota.Message(), details)
	case errors.Is(err, service.ErrUploadLimiterUnavailable):
		c.Header("Retry-After", "30")
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeUploadLimiterUnavailable,
			"Uploads are paused for a moment. Try again in a minute.", nil)
	case errors.Is(err, service.ErrResumableExpired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusGone, CodeUploadExpired,
			"The upload took too long and expired. Start it again.", nil)
	case errors.Is(err, service.ErrResumableIncomplete):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeUploadIncomplete, err.Error(), nil)
	case errors.Is(err, service.ErrResumableNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Upload not found", nil)
	default:
		return false
	}
	return true
}
