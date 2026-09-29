package http

import (
	"context"
	"net/http"
	"strconv"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HEAD on the serve routes (channel RSS feeds, 2026-09-29).
//
//	HEAD /v1/media/:mediaId/serve
//	HEAD /v1/media/:mediaId/serve/:variant
//	  200  no body, no Location
//	       Content-Type    the rendition's mime (original: media_assets.mime_type)
//	       Content-Length  media_variants.size_bytes (original: file_size_bytes);
//	                       omitted when no length was ever recorded
//	       Accept-Ranges   bytes
//	       Cache-Control / Vary exactly as the GET's redirect
//	  404  NOT_FOUND — denied, no such asset, or no such rendition: the GET's
//	       own answer, byte for byte. `hls` has no row and so no length: 404.
//	  503  DEPENDENCY_UNAVAILABLE — the audience could not be determined
//
// WHY HEAD IS ANSWERED HERE AND NOT BY THE REDIRECT
//
// Feed validators and podcast apps HEAD an enclosure before they list or
// download it. The GET answers 307 to a signed S3/MinIO (or CloudFront) URL,
// and that signature is for GET: a HEAD that follows the redirect is refused,
// so the enclosure looks broken to exactly the clients a feed is for. The
// type and the length are already in this service's rows, so the HEAD is
// answered from them.
//
// The decision is the GET's (service/serve_head.go): same scopes, same gate
// call, same fail-closed behaviour. An anonymous-scoped asset, which the GET
// streams, is answered from the rows too — a HEAD never opens an object.

// serveHeadService is the slice of the service the two routes use; an
// interface so the handler tests drive the real decision without PostgreSQL.
type serveHeadService interface {
	HeadForViewer(ctx context.Context, viewerID, mediaID uuid.UUID, variant string) (*service.ServeHead, error)
}

func (h *Handler) headsSvc() serveHeadService {
	if h.heads != nil {
		return h.heads
	}
	return h.svc.ServeHeads()
}

// HeadMedia — HEAD /v1/media/:mediaId/serve
func (h *Handler) HeadMedia(c *gin.Context) {
	h.headServe(c, "original")
}

// HeadMediaVariant — HEAD /v1/media/:mediaId/serve/:variant
func (h *Handler) HeadMediaVariant(c *gin.Context) {
	h.headServe(c, c.Param("variant"))
}

func (h *Handler) headServe(c *gin.Context, variant string) {
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid media ID", nil)
		return
	}
	head, err := h.headsSvc().HeadForViewer(c.Request.Context(), deliveryViewer(c), mediaID, variant)
	if err != nil {
		writeDeliveryError(c, err)
		return
	}
	writeServeHead(c, head)
}

func writeServeHead(c *gin.Context, head *service.ServeHead) {
	writeDeliveryCacheHeaders(c)
	c.Header("Accept-Ranges", "bytes")
	c.Header("Content-Type", head.ContentType)
	if head.Size >= 0 {
		c.Header("Content-Length", strconv.FormatInt(head.Size, 10))
	}
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
}
