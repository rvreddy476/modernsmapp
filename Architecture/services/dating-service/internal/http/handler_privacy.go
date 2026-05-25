// HTTP handlers for §P1-3 privacy controls.
//
// GET  /v1/dating/profile/privacy  — returns the caller's five-flag row.
// PATCH /v1/dating/profile/privacy — partial-update; nil-valued fields
//                                    are preserved server-side.
package http

import (
	"errors"
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetPrivacy returns the caller's current privacy settings.
func (h *Handler) GetPrivacy(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	p, err := h.svc.GetPrivacy(c.Request.Context(), userID)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "profile not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// PatchPrivacy applies a partial update to the caller's privacy row.
// Body shape mirrors store.PrivacyUpdate — every field is optional
// and a nil value preserves the existing column.
func (h *Handler) PatchPrivacy(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body store.PrivacyUpdate
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	p, err := h.svc.UpdatePrivacy(c.Request.Context(), userID, body)
	if err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "profile not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}
