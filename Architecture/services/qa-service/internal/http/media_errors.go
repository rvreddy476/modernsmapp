package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/qa-service/internal/mediaclaims"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// writeMediaError maps the attach gate's refusals (internal/mediaclaims). The
// codes and statuses are post-service's and channel-service's for the same
// refusal, so a client handles every surface the same way. Checked before the
// message switch in handleServiceError: "media does not exist" must not land
// on whatever substring rule happens to match it.
func writeMediaError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, mediaclaims.ErrMediaNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "MEDIA_NOT_FOUND", "media does not exist", nil)
	case errors.Is(err, mediaclaims.ErrMediaNotOwned):
		// 403 and a message that says nothing about the foreign asset.
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "MEDIA_NOT_OWNED", "You cannot attach this media", nil)
	case errors.Is(err, mediaclaims.ErrMediaNotReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "MEDIA_NOT_READY", "that media has not finished uploading", nil)
	case errors.Is(err, mediaclaims.ErrMediaUnavailable):
		// 503: the caller did nothing wrong; nothing was stored. Retryable.
		slog.Error("qa-service: media verification could not be performed", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE", "media could not be checked right now; try again", nil)
	default:
		return false
	}
	return true
}
