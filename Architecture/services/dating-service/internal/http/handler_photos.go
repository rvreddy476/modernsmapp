package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// Lane D6 stable error codes for photo attach, delete and image delivery.
const (
	CodePhotoMediaNotFound    = "PHOTO_MEDIA_NOT_FOUND"
	CodePhotoMediaNotReady    = "PHOTO_MEDIA_NOT_READY"
	CodePhotoMediaUnsupported = "PHOTO_MEDIA_UNSUPPORTED"
	CodePhotoMediaUnavailable = "PHOTO_MEDIA_UNAVAILABLE"
	CodePhotoLimitReached     = "PHOTO_LIMIT_REACHED"
	CodePhotoAlreadyAttached  = "PHOTO_ALREADY_ATTACHED"
)

// respondPhotoError maps the photo safety errors, then falls back to
// respondServiceError.
func (h *Handler) respondPhotoError(c *gin.Context, err error, defaultCode int, defaultCodeName string) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, store.ErrPhotoNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "photo not found", nil)
	case errors.Is(err, service.ErrPhotoMediaNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodePhotoMediaNotFound, "media not found", nil)
	case errors.Is(err, service.ErrPhotoMediaNotReady):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodePhotoMediaNotReady,
			"media must be a finished, moderation-passed image", nil)
	case errors.Is(err, service.ErrPhotoMediaUnsupported):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodePhotoMediaUnsupported, "image cannot be used", nil)
	case errors.Is(err, service.ErrPhotoMediaUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodePhotoMediaUnavailable,
			"photos are unavailable right now; try again shortly", nil)
	case errors.Is(err, store.ErrPhotoLimitReached):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodePhotoLimitReached, "photo limit reached",
			map[string]any{"max_photos": h.svc.PhotoSafety().MaxPhotos})
	case errors.Is(err, store.ErrPhotoAlreadyAttached):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodePhotoAlreadyAttached, "this image is already one of your photos", nil)
	default:
		respondServiceError(c, err, defaultCode, defaultCodeName)
	}
}

// GetPhotoImage — GET /v1/dating/photos/:id/full and /blurred (lane D6).
//
// Decides the caller's audience for this variant on every request
// (service.PhotoImageURL) and redirects to media-service's short-lived signed
// URL. A variant the caller may not have is 404, like a missing photo. The
// redirect is private and cached for 60s at most — shorter than the signature
// — so an unmatch or block stops rendering within a minute.
func (h *Handler) GetPhotoImage(variant string) gin.HandlerFunc {
	return func(c *gin.Context) {
		viewerID, ok := getUserID(c)
		if !ok {
			return
		}
		photoID, ok := parseUUID(c, "id")
		if !ok {
			return
		}
		u, err := h.svc.PhotoImageURL(c.Request.Context(), viewerID, photoID, variant)
		if err != nil {
			h.respondPhotoError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
			return
		}
		c.Header("Cache-Control", "private, max-age=60")
		c.Header("Vary", "X-User-Id")
		c.Redirect(http.StatusTemporaryRedirect, u)
	}
}

// ListPhotos returns the caller's photos.
func (h *Handler) ListPhotos(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	photos, err := h.svc.ListPhotos(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	// Lane D10: an empty list is [], never null, so a client can render it
	// without a null check.
	if photos == nil {
		photos = []store.Photo{}
	}
	api.JSON(c.Writer, http.StatusOK, photos, nil)
}

// ListMyPhotos — GET /v1/dating/photos/me[?status=rejected]
//
// Owner-only view of the caller's photos including the
// moderation_reason column. Backs the §P1-2 "Why was my photo
// rejected?" UI: the postmatch profile-photo grid calls this with
// status=rejected (or no filter) to render the moderator note inline
// next to each photo.
//
// Internal-key gated (same as the rest of /v1/dating/*); the
// X-User-Id header identifies the owner. The endpoint never exposes
// another user's moderation state — the WHERE clause pins to the
// caller's user_id.
func (h *Handler) ListMyPhotos(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	status := c.Query("status")
	photos, err := h.svc.ListMyPhotos(c.Request.Context(), userID, status)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	if photos == nil {
		photos = []store.Photo{}
	}
	api.JSON(c.Writer, http.StatusOK, photos, nil)
}

// CreatePhoto inserts a new photo for the caller.
func (h *Handler) CreatePhoto(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body store.CreatePhotoParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	photo, err := h.svc.CreatePhoto(c.Request.Context(), userID, body)
	if err != nil {
		h.respondPhotoError(c, err, http.StatusInternalServerError, "CREATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, photo, nil)
}

// UpdatePhoto applies a partial update to a caller-owned photo.
func (h *Handler) UpdatePhoto(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	photoID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body store.UpdatePhotoParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	photo, err := h.svc.UpdatePhoto(c.Request.Context(), userID, photoID, body)
	if err != nil {
		if errors.Is(err, store.ErrPhotoNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "photo not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, photo, nil)
}

// SetPhotoModerationStatus — POST /v1/dating/photos/:id/moderation
// Admin / moderator only (requireAdmin): a plain user holding the
// gateway-injected key gets 403, so nobody approves their own photo.
// Body: {status: "approved"|"rejected"|"pending", reason?: string}.
// Triggers deck-cache invalidation + profile-state transition + (on
// rejection) photo.moderation_rejected event.
//
// dating_admin_audit records the admin's gateway-derived X-User-Id as
// the actor; no actor → refused. PRODUCTION_GAP_ANALYSIS.md §P0-8.
type setPhotoModerationRequest struct {
	Status string `json:"status" binding:"required"`
	Reason string `json:"reason"`
}

func (h *Handler) SetPhotoModerationStatus(c *gin.Context) {
	adminID, ok := adminActor(c)
	if !ok {
		return
	}
	photoID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body setPhotoModerationRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	photo, err := h.svc.SetPhotoModerationStatus(c.Request.Context(), adminID, photoID, body.Status, body.Reason)
	if err != nil {
		if errors.Is(err, store.ErrPhotoNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "photo not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, photo, nil)
}

// DeletePhoto removes a caller-owned photo.
func (h *Handler) DeletePhoto(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	photoID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeletePhoto(c.Request.Context(), userID, photoID); err != nil {
		h.respondPhotoError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "deleted"}, nil)
}
