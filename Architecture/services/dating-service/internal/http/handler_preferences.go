package http

import (
	"net/http"

	"github.com/atpost/dating-service/internal/service"
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
	prefs, err := h.svc.GetPreferencesView(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, prefs, nil)
}

// GetProfileOptions — GET /v1/dating/profile/options (mechanic M6): the
// fixed lists for interests, languages, height, the lifestyle basics and the
// distance buckets.
func (h *Handler) GetProfileOptions(c *gin.Context) {
	if _, ok := getUserID(c); !ok {
		return
	}
	api.JSON(c.Writer, http.StatusOK, service.GetProfileOptions(), nil)
}

// PutPreferences upserts the caller's discovery preferences.
func (h *Handler) PutPreferences(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body service.PreferencesInput
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	prefs, err := h.svc.PutPreferences(c.Request.Context(), userID, body)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPSERT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, prefs, nil)
}
