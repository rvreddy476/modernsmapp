package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

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
		respondServiceError(c, err, http.StatusInternalServerError, "CREATE_FAILED")
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
// Internal-only: gated by the same internal-service-key as the rest
// of dating-service. Body: {status: "approved"|"rejected"|"pending",
// reason?: string}. Triggers deck-cache invalidation + profile-state
// transition + (on rejection) photo.moderation_rejected event.
type setPhotoModerationRequest struct {
	Status string `json:"status" binding:"required"`
	Reason string `json:"reason"`
}

func (h *Handler) SetPhotoModerationStatus(c *gin.Context) {
	photoID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body setPhotoModerationRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	photo, err := h.svc.SetPhotoModerationStatus(c.Request.Context(), photoID, body.Status, body.Reason)
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
		if errors.Is(err, store.ErrPhotoNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "photo not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "DELETE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "deleted"}, nil)
}
