package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
)

// Pulse dating clips — internal routes for dating-service.
//
// Same caller rules as the dating photo routes (dating_photo_handler.go):
// outside every api-gateway prefix, any gateway-set identity header is
// refused (403 USER_CALLER_REFUSED), the internal service key is required
// (401). The routes exist only when MEDIA_DATING_CLIPS_ENABLED=true, a
// delivery signer is configured and an internal key is set; otherwise they
// answer 404 like any unknown path.
//
//	GET    /internal/v1/media/dating-clips/:mediaId/owner-status?requester_user_id=
//	POST   /internal/v1/media/dating-clips/:mediaId/prepare        {requester_user_id}
//	POST   /internal/v1/media/dating-clips/:mediaId/delivery-url   {owner_user_id}
//	DELETE /internal/v1/media/dating-clips/:mediaId?requester_user_id=
const (
	DatingClipOwnerStatusPath = "/internal/v1/media/dating-clips/:mediaId/owner-status"
	DatingClipPreparePath     = "/internal/v1/media/dating-clips/:mediaId/prepare"
	DatingClipDeliveryURLPath = "/internal/v1/media/dating-clips/:mediaId/delivery-url"
	DatingClipDeletePath      = "/internal/v1/media/dating-clips/:mediaId"
)

// Stable error codes for the dating clip routes.
const (
	CodeClipNotFound    = "CLIP_NOT_FOUND"
	CodeClipUnsupported = "CLIP_UNSUPPORTED"
	CodeClipNotReady    = "CLIP_NOT_READY"
	CodeClipTooLong     = "CLIP_TOO_LONG"
)

// WithDatingClips wires the dating clip service. Nil leaves the routes
// unregistered (the feature flag is off).
func (h *Handler) WithDatingClips(s *service.DatingClipService) *Handler {
	h.datingClips = s
	return h
}

// RegisterDatingClipRoutes registers the internal routes when wired.
func (h *Handler) RegisterDatingClipRoutes(r *gin.Engine) {
	if h.datingClips == nil {
		return
	}
	if h.internalKey == "" {
		slog.Warn("media-service: dating clips are enabled but INTERNAL_SERVICE_KEY is not set — dating clip routes NOT registered")
		return
	}
	guard := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{refuseGatewayIdentity(), sharedmiddleware.RequireInternalKey(h.internalKey), handler}
	}
	r.GET(DatingClipOwnerStatusPath, guard(h.DatingClipOwnerStatus)...)
	r.POST(DatingClipPreparePath, guard(h.PrepareDatingClip)...)
	r.POST(DatingClipDeliveryURLPath, guard(h.DatingClipDeliveryURL)...)
	r.DELETE(DatingClipDeletePath, guard(h.DeleteDatingClip)...)
}

// DatingClipOwnerStatus — 200 status | 404 CLIP_NOT_FOUND (missing or not
// the requester's) | 400 | 503.
func (h *Handler) DatingClipOwnerStatus(c *gin.Context) {
	mediaID, requester, ok := datingPhotoIDs(c, c.Query("requester_user_id"))
	if !ok {
		return
	}
	st, err := h.datingClips.OwnerStatus(c.Request.Context(), requester, mediaID)
	if err != nil {
		h.writeDatingClipError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

type prepareDatingClipRequest struct {
	RequesterUserID string `json:"requester_user_id"`
}

// PrepareDatingClip — 200 {kind, duration_ms} | 404 | 409 CLIP_NOT_READY |
// 422 CLIP_UNSUPPORTED / CLIP_TOO_LONG | 400 | 503.
func (h *Handler) PrepareDatingClip(c *gin.Context) {
	var body prepareDatingClipRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
		return
	}
	mediaID, requester, ok := datingPhotoIDs(c, body.RequesterUserID)
	if !ok {
		return
	}
	out, err := h.datingClips.Prepare(c.Request.Context(), requester, mediaID)
	if err != nil {
		h.writeDatingClipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

type datingClipDeliveryRequest struct {
	OwnerUserID string `json:"owner_user_id"`
}

// DatingClipDeliveryURL — 200 {kind, url, poster_url?, expires_at} | 404
// (every refusal) | 400 | 503.
func (h *Handler) DatingClipDeliveryURL(c *gin.Context) {
	var body datingClipDeliveryRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
		return
	}
	mediaID, owner, ok := datingPhotoIDs(c, body.OwnerUserID)
	if !ok {
		return
	}
	out, err := h.datingClips.DeliveryURL(c.Request.Context(), owner, mediaID)
	if err != nil {
		h.writeDatingClipError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// DeleteDatingClip — 200 {status, objects_deleted, objects_failed} | 404
// (already gone, or not the requester's) | 409 STILL_REFERENCED | 503.
func (h *Handler) DeleteDatingClip(c *gin.Context) {
	mediaID, requester, ok := datingPhotoIDs(c, c.Query("requester_user_id"))
	if !ok {
		return
	}
	res, err := h.datingClips.Delete(c.Request.Context(), requester, mediaID)
	if err != nil {
		h.writeDatingClipError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"status": "deleted", "objects_deleted": res.ObjectsDeleted, "objects_failed": res.ObjectsFailed,
	}, nil)
}

func (h *Handler) writeDatingClipError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrDatingClipNotFound), errors.Is(err, service.ErrAssetNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeClipNotFound, "clip not found", nil)
	case errors.Is(err, service.ErrDatingClipNotReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeClipNotReady, "clip is still processing; retry later", nil)
	case errors.Is(err, service.ErrDatingClipTooLong):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeClipTooLong, "clip is longer than the limit",
			map[string]any{"max_ms": h.datingClips.MaxMs()})
	case errors.Is(err, service.ErrDatingClipUnsupported):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeClipUnsupported, "media cannot be used as a clip", nil)
	case errors.Is(err, service.ErrDatingClipInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
	case errors.Is(err, service.ErrAssetStillReferenced):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeMediaStillReferenced, "media is still referenced elsewhere", nil)
	default:
		slog.Error("media: dating clip request failed", "media_id", c.Param("mediaId"), "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeMediaUnavailable, "dating clip service unavailable; retry later", nil)
	}
}
