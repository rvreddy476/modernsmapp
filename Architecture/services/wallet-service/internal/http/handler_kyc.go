package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetKYC handles GET /v1/wallet/kyc.
func (h *Handler) GetKYC(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	rec, err := h.svc.GetKYC(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "KYC_ERROR")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, rec)
}

// StartAadhaar handles POST /v1/wallet/kyc/aadhaar/start. Generates a
// DigiLocker authorize URL the client opens.
func (h *Handler) StartAadhaar(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	if h.digilockerBaseURL == "" || h.aadhaarVerifier == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "DIGILOCKER_NOT_CONFIGURED", "digilocker integration not configured", nil)
		return
	}
	out, err := h.svc.StartAadhaar(c.Request.Context(), userID, h.digilockerBaseURL, h.digilockerRedirect)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "KYC_START_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

type aadhaarCallbackRequest struct {
	Code  string `json:"code"`
	State string `json:"state"`
}

// AadhaarCallback handles POST /v1/wallet/kyc/aadhaar/callback.
func (h *Handler) AadhaarCallback(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	if h.aadhaarVerifier == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "DIGILOCKER_NOT_CONFIGURED", "digilocker integration not configured", nil)
		return
	}
	var req aadhaarCallbackRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	rec, err := h.svc.CompleteAadhaar(c.Request.Context(), userID, req.Code, req.State, h.aadhaarVerifier)
	if err != nil {
		respondServiceError(c, err, http.StatusBadGateway, "DIGILOCKER_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, rec)
}

type panRequest struct {
	PANNumber string `json:"pan_number"`
}

// SubmitPAN handles POST /v1/wallet/kyc/pan.
func (h *Handler) SubmitPAN(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var req panRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	rec, err := h.svc.SubmitPAN(c.Request.Context(), userID, req.PANNumber)
	if err != nil {
		respondServiceError(c, err, http.StatusBadRequest, "INVALID_PAN")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, rec)
}
