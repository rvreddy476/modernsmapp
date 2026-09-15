package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Dating plan lane D6 — internal dating photo routes.
//
// Same caller rules as the lane D5 face routes (face_compare_handler.go): the
// paths sit outside every api-gateway prefix, any gateway-set user identity
// header is refused (403 USER_CALLER_REFUSED) whatever else the request
// carries, and the internal service key is required (401). The routes exist
// only when the service is wired (a delivery signer is configured) and an
// internal key is set.
//
//	GET    /internal/v1/media/dating-photos/:mediaId/owner-status?requester_user_id=
//	POST   /internal/v1/media/dating-photos/:mediaId/prepare        {requester_user_id, detect_faces}
//	POST   /internal/v1/media/dating-photos/:mediaId/delivery-url   {owner_user_id, variant: full|blurred}
//	DELETE /internal/v1/media/dating-photos/:mediaId?requester_user_id=
//
// "dating-photos" is a static segment so these never collide with the
// /internal/v1/media/faces/* routes.
const (
	DatingPhotoOwnerStatusPath = "/internal/v1/media/dating-photos/:mediaId/owner-status"
	DatingPhotoPreparePath     = "/internal/v1/media/dating-photos/:mediaId/prepare"
	DatingPhotoDeliveryURLPath = "/internal/v1/media/dating-photos/:mediaId/delivery-url"
	DatingPhotoDeletePath      = "/internal/v1/media/dating-photos/:mediaId"
)

// Stable error codes for the dating photo routes (plus CodeMediaNotFound and
// CodeImageUnsupported from the face routes).
const (
	CodeMediaNotReady        = "MEDIA_NOT_READY"
	CodeMediaNotPrepared     = "MEDIA_NOT_PREPARED"
	CodeMediaStillReferenced = "STILL_REFERENCED"
	CodeMediaUnavailable     = "MEDIA_UNAVAILABLE"
)

// WithDatingPhotos wires the dating photo service. Nil leaves the routes
// unregistered.
func (h *Handler) WithDatingPhotos(s *service.DatingPhotoService) *Handler {
	h.datingPhotos = s
	return h
}

// RegisterDatingPhotoRoutes registers the internal routes when wired.
func (h *Handler) RegisterDatingPhotoRoutes(r *gin.Engine) {
	if h.datingPhotos == nil {
		return
	}
	if h.internalKey == "" {
		slog.Warn("media-service: dating photos are configured but INTERNAL_SERVICE_KEY is not — dating photo routes NOT registered")
		return
	}
	guard := func(handler gin.HandlerFunc) []gin.HandlerFunc {
		return []gin.HandlerFunc{refuseGatewayIdentity(), sharedmiddleware.RequireInternalKey(h.internalKey), handler}
	}
	r.GET(DatingPhotoOwnerStatusPath, guard(h.DatingPhotoOwnerStatus)...)
	r.POST(DatingPhotoPreparePath, guard(h.PrepareDatingPhoto)...)
	r.POST(DatingPhotoDeliveryURLPath, guard(h.DatingPhotoDeliveryURL)...)
	r.DELETE(DatingPhotoDeletePath, guard(h.DeleteDatingPhoto)...)
}

// DatingPhotoOwnerStatus — 200 status | 404 MEDIA_NOT_FOUND (missing or not
// the requester's) | 400.
func (h *Handler) DatingPhotoOwnerStatus(c *gin.Context) {
	mediaID, requester, ok := datingPhotoIDs(c, c.Query("requester_user_id"))
	if !ok {
		return
	}
	st, err := h.datingPhotos.OwnerStatus(c.Request.Context(), requester, mediaID)
	if err != nil {
		writeDatingPhotoError(c, err, mediaID)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

type prepareDatingPhotoRequest struct {
	RequesterUserID string `json:"requester_user_id"`
	DetectFaces     bool   `json:"detect_faces"`
}

// PrepareDatingPhoto — 200 status (prepared=true) | 404 | 409 MEDIA_NOT_READY
// | 422 IMAGE_UNSUPPORTED | 503.
func (h *Handler) PrepareDatingPhoto(c *gin.Context) {
	var body prepareDatingPhotoRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
		return
	}
	mediaID, requester, ok := datingPhotoIDs(c, body.RequesterUserID)
	if !ok {
		return
	}
	st, err := h.datingPhotos.Prepare(c.Request.Context(), requester, mediaID, body.DetectFaces)
	if err != nil {
		writeDatingPhotoError(c, err, mediaID)
		return
	}
	api.JSON(c.Writer, http.StatusOK, st, nil)
}

type datingPhotoDeliveryRequest struct {
	OwnerUserID string `json:"owner_user_id"`
	Variant     string `json:"variant"`
}

// DatingPhotoDeliveryURL — 200 {url, variant, expires_at} | 404 | 409
// MEDIA_NOT_READY / MEDIA_NOT_PREPARED | 400 | 503.
func (h *Handler) DatingPhotoDeliveryURL(c *gin.Context) {
	var body datingPhotoDeliveryRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
		return
	}
	mediaID, owner, ok := datingPhotoIDs(c, body.OwnerUserID)
	if !ok {
		return
	}
	out, err := h.datingPhotos.DeliveryURL(c.Request.Context(), owner, mediaID, strings.TrimSpace(body.Variant))
	if err != nil {
		writeDatingPhotoError(c, err, mediaID)
		return
	}
	c.Header("Cache-Control", "no-store")
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// DeleteDatingPhoto — 200 {status, objects_deleted, objects_failed} | 404
// (already gone, or not the requester's) | 409 STILL_REFERENCED | 503.
func (h *Handler) DeleteDatingPhoto(c *gin.Context) {
	mediaID, requester, ok := datingPhotoIDs(c, c.Query("requester_user_id"))
	if !ok {
		return
	}
	res, err := h.datingPhotos.Delete(c.Request.Context(), requester, mediaID)
	if err != nil {
		writeDatingPhotoError(c, err, mediaID)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{
		"status": "deleted", "objects_deleted": res.ObjectsDeleted, "objects_failed": res.ObjectsFailed,
	}, nil)
}

func datingPhotoIDs(c *gin.Context, rawUser string) (uuid.UUID, uuid.UUID, bool) {
	mediaID, errM := uuid.Parse(strings.TrimSpace(c.Param("mediaId")))
	user, errU := uuid.Parse(strings.TrimSpace(rawUser))
	if errM != nil || errU != nil || mediaID == uuid.Nil || user == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"mediaId and the user id must be UUIDs", nil)
		return uuid.Nil, uuid.Nil, false
	}
	return mediaID, user, true
}

func writeDatingPhotoError(c *gin.Context, err error, mediaID uuid.UUID) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrDatingPhotoNotFound), errors.Is(err, service.ErrAssetNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeMediaNotFound, "media not found", nil)
	case errors.Is(err, service.ErrDatingPhotoNotReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeMediaNotReady, "media is not a ready, moderation-passed image", nil)
	case errors.Is(err, service.ErrDatingPhotoNotPrepared):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeMediaNotPrepared, "media has not been prepared as a dating photo", nil)
	case errors.Is(err, service.ErrDatingPhotoUnsupported):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeImageUnsupported, "image cannot be prepared", nil)
	case errors.Is(err, service.ErrDatingPhotoInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
	case errors.Is(err, service.ErrAssetStillReferenced):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeMediaStillReferenced, "media is still referenced elsewhere", nil)
	default:
		slog.Error("media: dating photo request failed", "media_id", mediaID, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeMediaUnavailable, "dating photo service unavailable; retry later", nil)
	}
}
