// HTTP handlers for consent to sensitive data (Dating plan lane D9).
//
// GET /v1/dating/consents returns the caller's state for every consent type.
// PUT /v1/dating/consents/:type takes {"granted": true|false}; a withdrawal
// clears what the consent covered.
//
// Types: sensitive_religion, sensitive_community, biometric_selfie, echoes.
// Setting a covered field or starting a selfie check without the consent is
// refused elsewhere with 422 CONSENT_REQUIRED (respondServiceError).
package http

import (
	"encoding/json"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetConsents — GET /v1/dating/consents.
func (h *Handler) GetConsents(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListConsents(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// PutConsent — PUT /v1/dating/consents/:type {"granted": bool}.
func (h *Handler) PutConsent(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		Granted *bool `json:"granted"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)).Decode(&body); err != nil || body.Granted == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", `body must be {"granted": true|false}`, nil)
		return
	}
	out, err := h.svc.SetConsent(c.Request.Context(), userID, c.Param("type"), *body.Granted)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "UPDATE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
