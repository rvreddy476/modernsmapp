package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/user-service/internal/service"
	"github.com/gin-gonic/gin"
)

// writeMediaError maps the media attach refusals (service/media_claims.go).
// The codes and statuses are channel-service's and post-service's for the
// same refusal, so a client handles every surface the same way. Checked
// before any message-matching rule a handler has. Returns false for an error
// it does not own.
func writeMediaError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrMediaNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "MEDIA_NOT_FOUND", "media does not exist", nil)
	case errors.Is(err, service.ErrMediaNotOwned):
		// 403 and a message that says nothing about the foreign asset.
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "MEDIA_NOT_OWNED", "You cannot attach this media", nil)
	case errors.Is(err, service.ErrMediaNotReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "MEDIA_NOT_READY", "that media has not finished uploading", nil)
	case errors.Is(err, service.ErrMediaInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_MEDIA_ID", "media id is not valid", nil)
	case errors.Is(err, service.ErrPageDisabled):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "PAGE_DISABLED", "a disabled page cannot be given new media", nil)
	case errors.Is(err, service.ErrMediaUnavailable):
		// 503: the caller did nothing wrong; nothing was stored. Retryable.
		slog.Error("user-service: media verification or claim could not be performed", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE", "media could not be checked right now; try again", nil)
	default:
		return false
	}
	return true
}
