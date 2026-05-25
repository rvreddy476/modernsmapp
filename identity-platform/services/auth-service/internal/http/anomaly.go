package http

import (
	"errors"
	"net/http"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

// AnomalyStepUpRequest is the body shape shared by both step-up verify
// endpoints. Frontend and mobile send the same payload; only the URL
// differs.
type AnomalyStepUpRequest struct {
	PendingToken string `json:"pending_token" binding:"required"`
	Code         string `json:"code" binding:"required"`
}

// AnomalyVerifyEmail finishes a step-up login via the email-OTP path.
// Public route — the user is mid-login and has no session yet. The
// pending_token was issued by the Login/VerifyOTP response when the
// anomaly band was high AND enforcement is on.
//
// Success: mints the real session, sets auth cookies, returns the
// standard AuthResponse envelope.
// Invalid code / expired pending: 401 with a specific code so the UI
// can either resend the OTP or fall back to TOTP.
func (h *Handler) AnomalyVerifyEmail(c *gin.Context) {
	var req AnomalyStepUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("anomaly verify-email: invalid payload",
			"err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	resp, err := h.svc.ResolveAnomalyStepUpEmail(c.Request.Context(), req.PendingToken, req.Code)
	if err != nil {
		h.respondStepUpError(c, "verify-email", err)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// AnomalyVerify2FA finishes a step-up login via the existing TOTP
// channel (or a recovery code). Shape is identical to AnomalyVerifyEmail
// so the UI can swap based on the user's chosen method.
func (h *Handler) AnomalyVerify2FA(c *gin.Context) {
	var req AnomalyStepUpRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("anomaly verify-2fa: invalid payload",
			"err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	resp, err := h.svc.ResolveAnomalyStepUp2FA(c.Request.Context(), req.PendingToken, req.Code)
	if err != nil {
		h.respondStepUpError(c, "verify-2fa", err)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// respondStepUpError maps the service-layer step-up sentinels to
// stable HTTP error codes so the UI doesn't need to string-match the
// message. Centralised so both verify endpoints stay in sync.
func (h *Handler) respondStepUpError(c *gin.Context, op string, err error) {
	switch {
	case errors.Is(err, service.ErrAnomalyPendingInvalid):
		h.log.Warn("anomaly "+op+": invalid pending",
			"err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "STEP_UP_EXPIRED",
			"Your verification session has expired. Please sign in again.", nil, nil)
	case errors.Is(err, service.ErrAnomalyCodeInvalid):
		h.log.Warn("anomaly "+op+": bad code",
			"err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "STEP_UP_CODE_INVALID",
			"Incorrect code. Please try again.", nil, nil)
	case errors.Is(err, service.ErrAnomalyStepUpUnavailable):
		h.log.Warn("anomaly "+op+": no channel",
			"err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "STEP_UP_UNAVAILABLE",
			"This channel is not available for your account.", nil, nil)
	case errors.Is(err, service.ErrTOTPReplay):
		api.Error(c.Writer, http.StatusUnauthorized, "STEP_UP_CODE_REPLAYED",
			"This code has already been used. Please generate a new one.", nil, nil)
	default:
		h.log.Error("anomaly "+op+": internal error",
			"err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR",
			"Internal server error", nil, nil)
	}
}
