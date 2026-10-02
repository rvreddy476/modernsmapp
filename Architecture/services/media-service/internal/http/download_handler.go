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

// The one file-download route (MTube, 2026-09-27; OWNER ONLY since
// 2026-10-02: a viewer never receives a file — "Keep a copy" is an offline
// copy inside the app, fetched through /serve).
//
//	GET /v1/media/:mediaId/download
//	  307 → signed URL of the best MP4 (720p, else 480p, else the original)
//	        with Content-Disposition: attachment; filename="<mediaId>.mp4"
//	        for the UPLOADER, or for a trusted service acting for an
//	        administrator (the internal key and no viewer identity)
//	  404 NOT_FOUND              no such asset, not a video, or the caller
//	                             is not the uploader — one answer, whatever
//	                             the post's allow_download says, and the
//	                             same for an anonymous caller
//	  503 DEPENDENCY_UNAVAILABLE the record or the signer could not answer
//
// No authMW, like every other media read: the viewer is the edge-verified
// X-User-Id, and the decision (service/download.go) fails closed without
// one. The redirect is private and short-lived exactly like /serve.

// downloadService is the slice of the service this endpoint uses; an
// interface so the handler tests drive every status path without
// PostgreSQL.
type downloadService interface {
	DownloadURL(ctx context.Context, viewerID, mediaID uuid.UUID) (string, error)
	DownloadURLForService(ctx context.Context, mediaID uuid.UUID) (string, error)
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
	var url string
	if h.downloadForAdministrator(c) {
		url, err = h.downloadSvc().DownloadURLForService(c.Request.Context(), mediaID)
	} else {
		url, err = h.downloadSvc().DownloadURL(c.Request.Context(), deliveryViewer(c), mediaID)
	}
	if err != nil {
		writeDownloadError(c, err)
		return
	}
	writeDeliveryRedirect(c, url)
}

// downloadForAdministrator reports a trusted service acting for an
// administrator: the request carries this service's internal key AND no
// viewer identity. The gateway strips a client-sent copy of the key and
// stamps none on /v1/media (see trustedServiceCaller), so no edge request
// can reach this branch; a service that forwards a viewer's X-User-Id is
// judged as that viewer, by the owner rule.
func (h *Handler) downloadForAdministrator(c *gin.Context) bool {
	return h.trustedServiceCaller(c) && c.GetHeader("X-User-Id") == ""
}

func writeDownloadError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrDownloadNotAllowed), errors.Is(err, delivery.ErrDeliveryDenied),
		errors.Is(err, service.ErrAssetNotFound), errors.Is(err, service.ErrDownloadNotVideo):
		// One answer for "no such video" and "not yours": the route is the
		// creator's, and to anyone else the asset is simply not there.
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Media not found", nil)
	case errors.Is(err, delivery.ErrDeliveryUnresolved):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "DEPENDENCY_UNAVAILABLE", "The download could not be prepared; retry", nil)
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
	}
}
