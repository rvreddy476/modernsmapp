package http

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AnonymizeMedia — POST /v1/media/internal/:mediaId/anonymize
//
// Service-to-service (group-service, before it writes an anonymous post).
// Puts the asset into the anonymous scope: record for the uploader only,
// bytes streamed by this service. Idempotent. 404 when there is no such
// asset; 409 when the asset already carries an incompatible scope.
func (h *Handler) AnonymizeMedia(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	ok, err := h.svc.MarkAnonymous(c.Request.Context(), mediaID)
	if err != nil {
		if errors.Is(err, postgres.ErrScopeConflict) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusConflict, "SCOPE_CONFLICT", "media already carries another access scope", nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
		return
	}
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "anonymous"}, nil)
}

// ServeHLSSegment — GET /v1/media/:mediaId/hls-seg/:name
//
// Only anonymous assets' playlists point here; every other asset's segments
// are signed object URLs. Anything else answers not-found.
func (h *Handler) ServeHLSSegment(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	res, err := h.svc.StreamAnonymousSegment(c.Request.Context(), deliveryViewer(c), mediaID, c.Param("name"), c.GetHeader("Range"))
	if err != nil {
		writeStreamError(c, err)
		return
	}
	writeStream(c, res)
}

// tryStreamAnonymous serves the bytes itself when the asset is anonymous.
// It reports handled=true whenever it wrote a response; false means "not an
// anonymous asset — take the redirect path".
func (h *Handler) tryStreamAnonymous(c *gin.Context, mediaID uuid.UUID, variant string) (handled bool) {
	res, err := h.svc.StreamAnonymous(c.Request.Context(), deliveryViewer(c), mediaID, variant, c.GetHeader("Range"))
	if err != nil {
		if errors.Is(err, service.ErrNotAnonymousScope) {
			return false
		}
		writeStreamError(c, err)
		return true
	}
	writeStream(c, res)
	return true
}

func writeStreamError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrRangeNotSatisfiable) {
		c.Status(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	// ErrNotAnonymousScope on the segment route is also a not-found: only
	// anonymous assets are served here. writeDeliveryError folds every
	// non-delivery error into not-found already.
	writeDeliveryError(c, err)
}

func writeStream(c *gin.Context, res *service.StreamResult) {
	c.Header("Accept-Ranges", "bytes")
	// Private to this viewer and never stored: the whole point is that the
	// bytes travel under this URL and no other.
	c.Header("Cache-Control", "private, no-store")
	c.Header("Vary", "Cookie, Authorization, X-User-Id")
	if res.Partial {
		c.Header("Content-Range", fmt.Sprintf("bytes %d-%d/%d", res.Start, res.End, res.Size))
		c.Data(http.StatusPartialContent, res.ContentType, res.Data)
		return
	}
	c.Data(http.StatusOK, res.ContentType, res.Data)
}
