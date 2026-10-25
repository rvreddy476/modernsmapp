package http

import (
	"errors"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Media claims (service/media_claims.go): the refusals a write that attaches
// media to a non-FK column can meet, on the routes that had no media error
// writer of their own (reel drafts, events, the cover frame).
//
//	503 MEDIA_CLAIMS_UNAVAILABLE  media-service could not record the
//	                              reference; nothing was written; retry
//	403 MEDIA_NOT_OWNED           the asset is not the caller's upload
//	422 MEDIA_NOT_FOUND           the asset does not exist (or was deleted
//	                              before it could be attached)
//
// The statuses are PostEditErrorStatus's, so every route answers the same
// refusal the same way.

const mediaClaimsUnavailableMessage = "Media could not be attached right now. Try again."

// writeMediaClaimsError answers ErrMediaClaimsUnavailable; false for any
// other error.
func writeMediaClaimsError(c *gin.Context, err error) bool {
	if !errors.Is(err, service.ErrMediaClaimsUnavailable) {
		return false
	}
	status, code := service.PostEditErrorStatus(err)
	api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, mediaClaimsUnavailableMessage, nil)
	return true
}

// writeOwnMediaError answers the claimed-media refusals of the reel draft and
// event routes; false for any other error.
func writeOwnMediaError(c *gin.Context, err error) bool {
	switch {
	case writeMediaClaimsError(c, err):
	case errors.Is(err, service.ErrMediaNotOwned):
		// A message that discloses nothing about the asset.
		status, code := service.PostEditErrorStatus(err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, "You cannot attach this media", nil)
	case errors.Is(err, service.ErrMediaNotFound):
		status, code := service.PostEditErrorStatus(err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, "media does not exist", nil)
	default:
		return false
	}
	return true
}
