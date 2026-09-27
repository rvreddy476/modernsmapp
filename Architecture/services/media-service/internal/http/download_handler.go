package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube download (2026-09-27).
//
//	GET /v1/media/:mediaId/download
//	  307 → signed URL of the best MP4 (720p, else 480p, else the original)
//	        with Content-Disposition: attachment; filename="<mediaId>.mp4"
//	  403 DOWNLOAD_NOT_ALLOWED   caller is not the owner and post-service
//	                             did not say the post allows download
//	                             (anonymous callers land here too)
//	  404 NOT_FOUND              no such asset, or not a video
//	  503 DEPENDENCY_UNAVAILABLE post-service could not be asked
//
// No authMW, like every other media read: the viewer is the edge-verified
// X-User-Id, and the decision (service/download.go) fails closed without
// one. The redirect is private and short-lived exactly like /serve.

// downloadService is the slice of the service this endpoint uses; an
// interface so the handler tests drive every status path without
// PostgreSQL.
type downloadService interface {
	DownloadURL(ctx context.Context, viewerID, mediaID uuid.UUID) (string, error)
}

func (h *Handler) registerDownloadRoutes(v1 *gin.RouterGroup) {
	v1.GET("/:mediaId/download", h.DownloadMedia)
}

func (h *Handler) downloadSvc() downloadService {
	if h.downloads != nil {
		return h.downloads
	}
	return h.svc
}

// DownloadMedia — GET /v1/media/:mediaId/download
func (h *Handler) DownloadMedia(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	url, err := h.downloadSvc().DownloadURL(c.Request.Context(), deliveryViewer(c), mediaID)
	if err != nil {
		writeDownloadError(c, err)
		return
	}
	writeDeliveryRedirect(c, url)
}

func writeDownloadError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrDownloadNotAllowed):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "DOWNLOAD_NOT_ALLOWED", "Downloading this video is not allowed", nil)
	case errors.Is(err, service.ErrAssetNotFound), errors.Is(err, service.ErrDownloadNotVideo):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil)
	case errors.Is(err, delivery.ErrDeliveryUnresolved):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "Download permission could not be determined; retry", nil)
	case errors.Is(err, delivery.ErrDeliveryDenied):
		// The service maps a denial to ErrDownloadNotAllowed; a raw one can
		// only come from a fake, but it must never fall through to 500.
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "DOWNLOAD_NOT_ALLOWED", "Downloading this video is not allowed", nil)
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}
