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

// GetClientConfig — GET /v1/dating/client-config (mechanic M18): the
// switches the apps act on locally, e.g. screen protection.
func (h *Handler) GetClientConfig(c *gin.Context) {
	if _, ok := getUserID(c); !ok {
		return
	}
	api.JSON(c.Writer, http.StatusOK, h.svc.ClientConfig(), nil)
}

// GetHideKnown — GET /v1/dating/hide-known (mechanic M16).
func (h *Handler) GetHideKnown(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetHideKnown(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// PutHideKnown — PUT /v1/dating/hide-known {enabled} (mechanic M16). 503
// HIDE_KNOWN_UNAVAILABLE when the connections cannot be read to turn it on.
func (h *Handler) PutHideKnown(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Enabled == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "enabled is required", nil)
		return
	}
	out, err := h.svc.PutHideKnown(c.Request.Context(), userID, *body.Enabled)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
