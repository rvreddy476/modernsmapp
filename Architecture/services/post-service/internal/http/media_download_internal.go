package http

import (
	"errors"
	"net/http"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// MTube download (2026-09-27) — the content-authority answer media-service
// asks before it signs GET /v1/media/:id/download.
//
//	GET /v1/internal/media-download-allowed?media_id=<uuid>&viewer_id=<uuid>
//	  200 {"allowed": true|false}
//	  400 {"allowed": false}   malformed ids
//	  503 {"allowed": false}   relationship state unresolved (retry, do not cache)
//
// Register it in one line next to the media-access routes in handler.go
// (RegisterRoutes, directly under `r.POST("/v1/internal/media-access/batch",
// h.MediaAccessBatch)`):
//
//	r.GET("/v1/internal/media-download-allowed", h.MediaDownloadAllowed)
//
// That places it under the same internal-key middleware every /v1 route
// carries; the gateway does not expose /internal/ paths, and media-service
// is its only intended caller. The decision itself (posts.allow_download on
// a post the viewer can see, after the ordinary media-access gate) lives in
// service/media_download.go.

type mediaDownloadAllowedResponse struct {
	Allowed bool `json:"allowed"`
}

// MediaDownloadAllowed — GET /v1/internal/media-download-allowed
func (h *Handler) MediaDownloadAllowed(c *gin.Context) {
	mediaID, err := uuid.Parse(c.Query("media_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, mediaDownloadAllowedResponse{Allowed: false})
		return
	}
	viewerID, err := uuid.Parse(c.Query("viewer_id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, mediaDownloadAllowedResponse{Allowed: false})
		return
	}
	allowed, err := h.svc.ViewerMayDownloadMedia(c.Request.Context(), viewerID, mediaID)
	if err != nil {
		if errors.Is(err, service.ErrStoryPolicyUnresolved) {
			c.JSON(http.StatusServiceUnavailable, mediaDownloadAllowedResponse{Allowed: false})
			return
		}
		c.JSON(http.StatusInternalServerError, mediaDownloadAllowedResponse{Allowed: false})
		return
	}
	c.JSON(http.StatusOK, mediaDownloadAllowedResponse{Allowed: allowed})
}
