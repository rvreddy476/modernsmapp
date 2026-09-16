package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Admin sessions, step-up, force logout and the two-person holder count
// (admin console Wave 0, A2). Rules: internal/service/admin_session.go.

// Stable error codes.
const (
	CodeMFARequired    = "MFA_REQUIRED"
	CodeStepUpRequired = "STEP_UP_REQUIRED"
	CodeMFANotEnrolled = "MFA_NOT_ENROLLED"
	CodeInvalidOTP     = "INVALID_OTP"
	CodeOTPReplayed    = "OTP_REPLAYED"
)

type stepUpRequest struct {
	OTP string `json:"otp"`
}

// StepUp — POST /v1/auth/step-up {"otp":"123456"}  (auth + CSRF)
//
//	200 {"access_token","expires_at","step_up_at","step_up_valid_until","admin_mfa"}
//
// Re-verifies TOTP for the caller's session and returns a fresh access token
// (also set as the access_token cookie) carrying step_up_at. The refresh
// token is unchanged. Valid for 300 s for role changes, force logout and, in
// admin-service, money actions, KYC reveal and bans.
//
//	400 BAD_REQUEST        no otp
//	401 INVALID_OTP        wrong or expired code
//	401 OTP_REPLAYED       code already used in its window (any TOTP path)
//	401 SESSION_REVOKED    the session behind the token is gone
//	403 MFA_NOT_ENROLLED   no authenticator enrolled
//	429 RATE_LIMITED       5 attempts / 15 min per account (+ login IP limiter)
func (h *Handler) StepUp(c *gin.Context) {
	userID, ok := h.callerID(c)
	if !ok {
		return
	}
	sa, ok := service.SessionAuthFrom(c.Request.Context())
	if !ok || sa.SessionID == uuid.Nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "a session token is required", nil, nil)
		return
	}
	var req stepUpRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.OTP) == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "otp is required", nil, nil)
		return
	}
	resp, err := h.svc.StepUp(c.Request.Context(), userID, sa.SessionID, req.OTP)
	if err != nil {
		h.writeStepUpErr(c, err)
		return
	}
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     accessTokenCookieName,
		Value:    resp.AccessToken,
		Path:     "/",
		Domain:   h.cfg.CookieDomain,
		Expires:  resp.ExpiresAt,
		Secure:   h.cfg.CookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func (h *Handler) writeStepUpErr(c *gin.Context, err error) {
	if throttled, ok := service.AsThrottled(err); ok {
		retryAfter := int(throttled.RetryAfter.Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))
		api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED",
			"Too many step-up attempts. Try again later.",
			map[string]any{"retry_after_seconds": retryAfter}, nil)
		return
	}
	switch {
	case errors.Is(err, service.ErrInvalidOTP):
		api.Error(c.Writer, http.StatusUnauthorized, CodeInvalidOTP, "invalid two-factor code", nil, nil)
	case errors.Is(err, service.ErrTOTPReplay):
		api.Error(c.Writer, http.StatusUnauthorized, CodeOTPReplayed, "this code was already used; wait for the next one", nil, nil)
	case errors.Is(err, service.ErrTOTPNotEnrolled):
		api.Error(c.Writer, http.StatusForbidden, CodeMFANotEnrolled, "enrol an authenticator app first (POST /v1/auth/2fa/setup)", nil, nil)
	case errors.Is(err, service.ErrSessionNotLive):
		api.Error(c.Writer, http.StatusUnauthorized, "SESSION_REVOKED", "Session has been revoked", nil, nil)
	case errors.Is(err, service.ErrWrongSessionKind):
		// An admin console session on the consumer route (use
		// POST /v1/auth/admin-session/step-up).
		api.Error(c.Writer, http.StatusUnauthorized, CodeWrongSession, "this session cannot be used here", nil, nil)
	default:
		h.log.Error("step-up failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	}
}

type forceLogoutRequest struct {
	Reason string `json:"reason"`
}

// ForceLogout — POST /v1/auth/admin/users/:userId/sessions/revoke {"reason"}
//
//	200 {"user_id","sessions_revoked":N}
//
// Superadmin with an admin MFA session and a step-up younger than 300 s.
// Every live session of the user is revoked, audited in the same
// transaction (action session.force_logout), and marked sess_revoked:<sid>.
// Errors as the role routes: 403 FORBIDDEN / MFA_REQUIRED / STEP_UP_REQUIRED,
// 400 REASON_REQUIRED.
func (h *Handler) ForceLogout(c *gin.Context) {
	actor, ok := h.callerID(c)
	if !ok {
		return
	}
	target, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil, nil)
		return
	}
	var req forceLogoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "reason is required", nil, nil)
		return
	}
	n, err := h.svc.ForceLogout(c.Request.Context(), actor, target, req.Reason)
	if err != nil {
		h.writeRoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"user_id": target.String(), "sessions_revoked": n}, nil)
}

// InternalPermissionHolders —
// GET /v1/auth/internal/permissions/:permission/holders?exclude_user_id=<uuid>
//
//	200 {"permission":"payments:refund.issue","count":1}
//
// Service-only (internal key; any user identity header is refused). count is
// the number of ACTIVE accounts OTHER than exclude_user_id that hold the
// permission now and have TOTP enrolled — i.e. possible second approvers.
// exclude_user_id is required so a caller cannot forget to exclude the
// requester. A database failure is a 503, never a guessed number.
func (h *Handler) InternalPermissionHolders(c *gin.Context) {
	permission := c.Param("permission")
	exclude, err := uuid.Parse(strings.TrimSpace(c.Query("exclude_user_id")))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "exclude_user_id (uuid) is required", nil, nil)
		return
	}
	n, err := h.svc.CountOtherHolders(c.Request.Context(), permission, exclude)
	if err != nil {
		if errors.Is(err, service.ErrInvalidPermission) {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PERMISSION", "permission must be <app>:<action> with a known app", nil, nil)
			return
		}
		h.log.Error("holder count failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "PERMISSIONS_UNAVAILABLE", "holders could not be counted", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"permission": permission, "count": n}, nil)
}
