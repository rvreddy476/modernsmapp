package http

// Email two-step verification for app users. Rules: service/twofa_email.go.
//
//	POST /v1/auth/2fa/email/start   {"password","purpose":"enable"|"disable"}   (auth + CSRF)
//	  200 {"challenge_token","expires_at","email_masked"}
//	POST /v1/auth/2fa/email/enable  {"challenge_token","code"}                  (auth + CSRF)
//	  200 {"enabled":true,"method":"email"}
//	POST /v1/auth/2fa/disable       {"challenge_token","code"} (email) | {"password","code"} (TOTP)
//	  200 {"message","enabled":false,"method":""}
//	POST /v1/auth/2fa/resend        {"pending_token"}                           (public)
//	  200 {"sent":true,"expires_at","email_masked","resend_after_seconds":60}
//	POST /v1/auth/step-up/email/start                                           (auth + CSRF)
//	  200 {"challenge_token","expires_at","email_masked"}
//	POST /v1/auth/step-up           {"challenge_token","code"} (email) | {"otp"} (TOTP)
//
// Errors (stable codes):
//
//	400 INVALID_REQUEST             bad body / purpose
//	400 INVALID_CODE                wrong, expired or capped emailed code
//	400 INVALID_CHALLENGE           unknown, expired, spent or foreign challenge_token
//	401 INVALID_CREDENTIALS         wrong password (start)
//	401 AUTH_FAILED                 unknown or expired pending_token (resend)
//	401 SESSION_REVOKED             the session behind the token is gone
//	403 TOTP_NOT_OFFERED            /2fa/setup, /2fa/verify-setup for a non-admin
//	409 EMAIL_NOT_VERIFIED          no verified email on the account
//	409 TWO_FACTOR_ALREADY_ENABLED  enable while 2FA is on
//	409 TWO_FACTOR_NOT_ENABLED      disable / step-up while 2FA is off
//	409 TWO_FACTOR_METHOD_MISMATCH  an email flow on a TOTP account, or the reverse
//	429 RATE_LIMITED                cooldown or per-account cap (Retry-After)
//	503 EMAIL_UNAVAILABLE           the code could not be sent

import (
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Stable error codes for the email 2FA contract.
const (
	CodeInvalidCode             = "INVALID_CODE"
	CodeInvalidChallenge        = "INVALID_CHALLENGE"
	CodeInvalidCredentials      = "INVALID_CREDENTIALS"
	CodeTOTPNotOffered          = "TOTP_NOT_OFFERED"
	CodeEmailNotVerified        = "EMAIL_NOT_VERIFIED"
	CodeTwoFactorAlreadyEnabled = "TWO_FACTOR_ALREADY_ENABLED"
	CodeTwoFactorNotEnabled     = "TWO_FACTOR_NOT_ENABLED"
	CodeTwoFactorMethodMismatch = "TWO_FACTOR_METHOD_MISMATCH"
	CodeEmailUnavailable        = "EMAIL_UNAVAILABLE"
)

type startEmail2FARequest struct {
	Password string `json:"password"`
	Purpose  string `json:"purpose"`
}

// StartEmail2FA — POST /v1/auth/2fa/email/start.
func (h *Handler) StartEmail2FA(c *gin.Context) {
	userID, ok := h.callerID(c)
	if !ok {
		return
	}
	var req startEmail2FARequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" || strings.TrimSpace(req.Purpose) == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "password and purpose are required", nil, nil)
		return
	}
	resp, err := h.svc.StartEmail2FA(c.Request.Context(), userID, req.Password, strings.TrimSpace(req.Purpose))
	if err != nil {
		h.writeTwoFAEmailErr(c, err, "2FA email start failed")
		return
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

type emailChallengeCodeRequest struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
}

func bindChallengeCode(c *gin.Context) (emailChallengeCodeRequest, bool) {
	var req emailChallengeCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.ChallengeToken == "" || strings.TrimSpace(req.Code) == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "challenge_token and code are required", nil, nil)
		return req, false
	}
	req.Code = strings.TrimSpace(req.Code)
	return req, true
}

// EnableEmail2FA — POST /v1/auth/2fa/email/enable.
func (h *Handler) EnableEmail2FA(c *gin.Context) {
	userID, ok := h.callerID(c)
	if !ok {
		return
	}
	req, ok := bindChallengeCode(c)
	if !ok {
		return
	}
	if err := h.svc.EnableEmail2FA(c.Request.Context(), userID, req.ChallengeToken, req.Code); err != nil {
		h.writeTwoFAEmailErr(c, err, "2FA email enable failed")
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"enabled": true, "method": "email"}, nil)
}

type resend2FARequest struct {
	PendingToken string `json:"pending_token"`
}

// Resend2FA — POST /v1/auth/2fa/resend (public; the pending token from
// login names the account).
func (h *Handler) Resend2FA(c *gin.Context) {
	var req resend2FARequest
	if err := c.ShouldBindJSON(&req); err != nil || req.PendingToken == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "pending_token is required", nil, nil)
		return
	}
	resp, err := h.svc.Resend2FACode(c.Request.Context(), req.PendingToken)
	if err != nil {
		if errors.Is(err, service.ErrPending2FAInvalid) {
			api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "sign-in expired; sign in again", nil, nil)
			return
		}
		h.writeTwoFAEmailErr(c, err, "2FA resend failed")
		return
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// StartStepUpEmail — POST /v1/auth/step-up/email/start.
func (h *Handler) StartStepUpEmail(c *gin.Context) {
	userID, ok := h.callerID(c)
	if !ok {
		return
	}
	sa, ok := service.SessionAuthFrom(c.Request.Context())
	if !ok || sa.SessionID == uuid.Nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "a session token is required", nil, nil)
		return
	}
	resp, err := h.svc.StartStepUpEmail(c.Request.Context(), userID, sa.SessionID)
	if err != nil {
		h.writeTwoFAEmailErr(c, err, "email step-up start failed")
		return
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// writeTOTPNotOffered answers 403 TOTP_NOT_OFFERED and reports whether it did.
func writeTOTPNotOffered(c *gin.Context, err error) bool {
	if !errors.Is(err, service.ErrTOTPNotOffered) {
		return false
	}
	api.Error(c.Writer, http.StatusForbidden, CodeTOTPNotOffered,
		"authenticator apps are for admin accounts; turn on email two-step verification with POST /v1/auth/2fa/email/start", nil, nil)
	return true
}

// writeTwoFAEmailErr maps the email 2FA errors to the stable contract codes.
func (h *Handler) writeTwoFAEmailErr(c *gin.Context, err error, logMsg string) {
	if writeIfThrottled(c, err, "Too many attempts. Try again later.") {
		return
	}
	switch {
	case errors.Is(err, service.ErrInvalidCredentials):
		api.Error(c.Writer, http.StatusUnauthorized, CodeInvalidCredentials, "incorrect password", nil, nil)
	case errors.Is(err, service.ErrVerifyCodeInvalid):
		api.Error(c.Writer, http.StatusBadRequest, CodeInvalidCode, "invalid or expired code", nil, nil)
	case errors.Is(err, service.ErrEmailChallengeInvalid):
		api.Error(c.Writer, http.StatusBadRequest, CodeInvalidChallenge, err.Error(), nil, nil)
	case errors.Is(err, service.ErrInvalidTwoFAPurpose):
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
	case errors.Is(err, service.ErrTwoFactorEmailUnverified):
		api.Error(c.Writer, http.StatusConflict, CodeEmailNotVerified, err.Error(), nil, nil)
	case errors.Is(err, service.ErrTwoFactorAlreadyEnabled):
		api.Error(c.Writer, http.StatusConflict, CodeTwoFactorAlreadyEnabled, err.Error(), nil, nil)
	case errors.Is(err, service.ErrTwoFactorNotEnabled):
		api.Error(c.Writer, http.StatusConflict, CodeTwoFactorNotEnabled, err.Error(), nil, nil)
	case errors.Is(err, service.ErrTwoFactorMethodMismatch):
		api.Error(c.Writer, http.StatusConflict, CodeTwoFactorMethodMismatch, err.Error(), nil, nil)
	case errors.Is(err, service.ErrEmailDeliveryUnavailable):
		h.log.Error(logMsg, "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, CodeEmailUnavailable, service.ErrEmailDeliveryUnavailable.Error(), nil, nil)
	case errors.Is(err, service.ErrSessionNotLive):
		api.Error(c.Writer, http.StatusUnauthorized, "SESSION_REVOKED", "Session has been revoked", nil, nil)
	case errors.Is(err, service.ErrWrongSessionKind):
		api.Error(c.Writer, http.StatusUnauthorized, CodeWrongSession, "this session cannot be used here", nil, nil)
	default:
		h.log.Error(logMsg, "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	}
}
