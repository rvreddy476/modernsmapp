package http

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Posts by media (2026-09-27) — which posts attach a media asset.
//
//	GET /v1/internal/posts/by-media/:mediaId
//	  200 {"media_id": "<uuid>", "post_ids": ["<uuid>", ...]}   ([] when none)
//	  400 {"error": "invalid_media_id"}
//	  503 {"error": "unresolved"}   retry; never read as "no posts"
//
// search-service calls it when media-service announces a media-level fact
// (MediaSubtitlesChanged → has_subtitles) to find the post documents the
// fact belongs to. Ids only: the answer includes posts of every visibility,
// which is why it is internal.
//
// Registered with one line in handler.go (RegisterRoutes, next to
// `r.GET("/v1/internal/posts/:id/visibility", h.PostVisibility)`):
//
//	r.GET("/v1/internal/posts/by-media/:mediaId", h.PostsByMediaInternal)
//
// which places it under the internal-key middleware every route carries
// when INTERNAL_SERVICE_KEY is set; the gateway does not expose /internal/.

// postsByMediaReader is the one read this route makes. *service.Service
// implements it; tests substitute a fake.
type postsByMediaReader interface {
	PostIDsByMediaID(ctx context.Context, mediaID uuid.UUID) ([]uuid.UUID, error)
}

// PostsByMediaInternal — GET /v1/internal/posts/by-media/:mediaId
func (h *Handler) PostsByMediaInternal(c *gin.Context) {
	var reader postsByMediaReader
	if h.svc != nil {
		reader = h.svc
	}
	servePostsByMedia(c, reader)
}

func servePostsByMedia(c *gin.Context, reader postsByMediaReader) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil || mediaID == uuid.Nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_media_id"})
		return
	}
	if reader == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unresolved"})
		return
	}
	ids, err := reader.PostIDsByMediaID(c.Request.Context(), mediaID)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "unresolved"})
		return
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	c.JSON(http.StatusOK, gin.H{"media_id": mediaID.String(), "post_ids": out})
}
