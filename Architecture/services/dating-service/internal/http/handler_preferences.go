package http

import (
	"net/http"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetPreferences returns the caller's discovery preferences (creating
// defaults if none exist).
func (h *Handler) GetPreferences(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	prefs, err := h.svc.GetPreferences(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, prefs, nil)
}

// PutPreferences upserts the caller's discovery preferences.
func (h *Handler) PutPreferences(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body store.UpsertPreferencesParams
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	prefs, err := h.svc.UpsertPreferences(c.Request.Context(), userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPSERT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, prefs, nil)
}
