// HTTP handlers for /v1/dating/verification.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// The Aadhaar number is NEVER accepted by these endpoints. The Aadhaar
// flow uses DigiLocker (Setu/Signzy partner): the mobile app opens the
// authorize URL, the user authenticates with UIDAI/DigiLocker, and the
// partner returns an *opaque* assertion id. We never see the number.
package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// StartAadhaar — POST /v1/dating/verification/aadhaar/start.
// Returns the DigiLocker authorize URL the mobile app must open.
func (h *Handler) StartAadhaar(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.StartAadhaarFlow(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VERIFICATION_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AadhaarCallbackRequest — body for the callback. Only the OAuth-style
// code + the PKCE state nonce. NO Aadhaar number is accepted here.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
type aadhaarCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// AadhaarCallback — POST /v1/dating/verification/aadhaar/callback.
func (h *Handler) AadhaarCallback(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body aadhaarCallbackRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if body.Code == "" || body.State == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "code and state required", nil)
		return
	}
	out, err := h.svc.CompleteAadhaarFlow(c.Request.Context(), userID, body.Code, body.State)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VERIFICATION_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// SelfieRequest — body for the selfie endpoint. The mobile client runs
// the on-device face-detection model and uploads ONLY the embedding (a
// fixed-length float vector). The raw selfie image never reaches us.
type selfieRequest struct {
	Embedding []float64 `json:"embedding"`
}

// SubmitSelfie — POST /v1/dating/verification/selfie.
func (h *Handler) SubmitSelfie(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body selfieRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if len(body.Embedding) == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "embedding required", nil)
		return
	}
	out, err := h.svc.CompleteSelfieFlow(c.Request.Context(), userID, body.Embedding)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "VERIFICATION_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
