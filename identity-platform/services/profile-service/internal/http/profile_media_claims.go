package http

import (
	"errors"
	"net/http"

	"github.com/atpost/identity-profile-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// writeProfileMediaError answers the media claim refusals of an avatar or
// cover write (service/media_claims.go). It reports false for any other
// error, which the caller answers as a 500. Nothing was written in any case
// answered here.
func writeProfileMediaError(c *gin.Context, err error, subtype string) bool {
	switch {
	case errors.Is(err, service.ErrMediaNotFound):
		// Deleted between the authority check and the claim: the same
		// answer as an asset that was never attachable.
		api.Error(c.Writer, http.StatusConflict, "MEDIA_NOT_ATTACHABLE",
			"Choose an owned, ready and approved "+subtype+" image", nil, nil)
	case errors.Is(err, service.ErrMediaUnavailable):
		api.Error(c.Writer, http.StatusServiceUnavailable, "MEDIA_AUTHORITY_UNAVAILABLE",
			"Profile image verification is unavailable", nil, nil)
	case errors.Is(err, service.ErrProfileNotFound):
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Profile not found", nil, nil)
	default:
		return false
	}
	return true
}
