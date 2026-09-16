package http

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/middleware"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Admin console sign-in on its own host (admin console Wave 1, B3).
// Rules: internal/service/admin_login.go.
//
// The admin console session is NOT the consumer session. It has its own
// cookies, written only by the routes in this file:
//
//	admin_access_token   HttpOnly  SameSite=Strict  Path=/  host-only  Secure*
//	admin_refresh_token  HttpOnly  SameSite=Strict  Path=/  host-only  Secure*
//	admin_csrf_token     readable  SameSite=Strict  Path=/  host-only  Secure*
//
// *Secure unless ADMIN_COOKIE_SECURE=false outside production.
//
// Host-only means no Domain attribute, ever: COOKIE_DOMAIN is not consulted,
// so a cookie set through the admin host's same-origin proxy is sent back only
// to that exact host, never to a consumer zone on a sibling subdomain. These
// routes never read or write access_token / refresh_token / csrf_token, and
// the consumer routes never read or write the admin_* cookies.
//
// Route family (the path, not a header, selects admin mode, so a consumer page
// cannot ask for it):
//
//	POST /v1/auth/admin-session/login        {identifier, password}   → pending token
//	POST /v1/auth/admin-session/verify-2fa   {pending_token, code}    → admin cookies
//	POST /v1/auth/admin-session/refresh      admin cookies + CSRF     → rotated admin cookies
//	POST /v1/auth/admin-session/logout       admin refresh cookie     → cleared admin cookies
//	POST /v1/auth/admin-session/step-up      admin cookies + CSRF {otp}
//	GET  /v1/auth/admin-session              admin cookie             → session status

const (
	adminAccessCookieName  = "admin_access_token"
	adminRefreshCookieName = "admin_refresh_token"
	adminCSRFCookieName    = "admin_csrf_token"
	csrfHeaderName         = "X-CSRF-Token"
)

// Stable error codes of the admin-session routes (besides AUTH_FAILED,
// RATE_LIMITED, INVALID_OTP, OTP_REPLAYED, MFA_NOT_ENROLLED, CSRF_FAILED).
const (
	CodeNotAdmin               = "NOT_ADMIN"
	CodeAccountNotActive       = "ACCOUNT_NOT_ACTIVE"
	CodeAdminSignInExpired     = "ADMIN_SIGN_IN_EXPIRED"
	CodeWrongSession           = "WRONG_SESSION"
	CodeAdminAccessEnded       = "ADMIN_ACCESS_ENDED"
	CodePermissionsUnavailable = "PERMISSIONS_UNAVAILABLE"
)

// RegisterAdminSessionRoutes mounts /v1/auth/admin-session/*. adminAuthMW must
// be AdminAuthMiddlewareWithKeys (admin cookie only, sk=admin only).
func (h *Handler) RegisterAdminSessionRoutes(r *gin.Engine, adminAuthMW gin.HandlerFunc) {
	g := r.Group("/v1/auth/admin-session")
	// The same limiters as the consumer login: 10/IP and 5/identifier per
	// 15 min on both public steps, plus 5 TOTP attempts per account in the
	// service.
	g.POST("/login", middleware.LoginRateLimit(h.rdb), h.AdminSessionLogin)
	g.POST("/verify-2fa", middleware.LoginRateLimit(h.rdb), h.AdminSessionVerify2FA)
	g.POST("/refresh", middleware.AdminRefreshRateLimit(h.rdb, adminRefreshCookieName), requireAdminCSRF(), h.AdminSessionRefresh)
	// Logout needs no CSRF pair: SameSite=Strict already keeps the cookie off
	// cross-site requests, and a sign-out must work even with a lost csrf
	// cookie. It only ever ends an admin session.
	g.POST("/logout", h.AdminSessionLogout)
	g.GET("", adminAuthMW, h.AdminSessionStatus)
	g.POST("/step-up", adminAuthMW, requireAdminCSRF(), middleware.LoginRateLimit(h.rdb), h.AdminSessionStepUp)
}

// requireAdminCSRF is the double submit for the admin cookies. It is enforced
// on every method it guards, whatever credential authenticated the request —
// there is no bearer exemption on the admin routes.
func requireAdminCSRF() gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader(csrfHeaderName)
		cookie, err := c.Cookie(adminCSRFCookieName)
		if err != nil || header == "" || cookie == "" ||
			subtle.ConstantTimeCompare([]byte(header), []byte(cookie)) != 1 {
			api.Error(c.Writer, http.StatusForbidden, "CSRF_FAILED", "Missing or invalid CSRF token", nil, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

// adminCookie builds one admin cookie. There is deliberately no Domain.
func (h *Handler) adminCookie(name, value string, expires time.Time, httpOnly bool) *http.Cookie {
	return &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     "/",
		Expires:  expires,
		Secure:   h.cfg.AdminCookieSecure,
		HttpOnly: httpOnly,
		SameSite: http.SameSiteStrictMode,
	}
}

func (h *Handler) adminSessionLifetime() time.Duration {
	if h.cfg.AdminSessionTTL > 0 {
		return h.cfg.AdminSessionTTL
	}
	return 12 * time.Hour
}

// setAdminCookies writes the three admin cookies for a fresh token pair.
func (h *Handler) setAdminCookies(c *gin.Context, tokens service.TokenPair) bool {
	csrf, err := generateCSRFToken()
	if err != nil {
		h.log.Error("admin session: csrf token generation failed", "err", err, "request_id", RequestIDFromContext(c))
		return false
	}
	sessionEnd := time.Now().Add(h.adminSessionLifetime())
	http.SetCookie(c.Writer, h.adminCookie(adminAccessCookieName, tokens.AccessToken, tokens.ExpiresAt, true))
	http.SetCookie(c.Writer, h.adminCookie(adminRefreshCookieName, tokens.RefreshToken, sessionEnd, true))
	http.SetCookie(c.Writer, h.adminCookie(adminCSRFCookieName, csrf, sessionEnd, false))
	return true
}

func (h *Handler) clearAdminCookies(c *gin.Context) {
	expired := time.Now().Add(-24 * time.Hour)
	for _, name := range []string{adminAccessCookieName, adminRefreshCookieName, adminCSRFCookieName} {
		ck := h.adminCookie(name, "", expired, name != adminCSRFCookieName)
		ck.MaxAge = -1
		http.SetCookie(c.Writer, ck)
	}
}

// adminSessionBody is what the browser learns about a new or refreshed admin
// session. Tokens are never in the body: they exist only as HttpOnly cookies.
type adminSessionBody struct {
	SessionID       string    `json:"session_id"`
	AdminMFA        bool      `json:"admin_mfa"`
	AccessExpiresAt time.Time `json:"access_expires_at"`
	User            struct {
		ID    string `json:"id"`
		Email string `json:"email,omitempty"`
	} `json:"user"`
}

func newAdminSessionBody(resp *service.AuthResponse) adminSessionBody {
	var body adminSessionBody
	body.SessionID = resp.SessionID.String()
	body.AdminMFA = true // only an admin MFA session is ever issued on these routes
	body.AccessExpiresAt = resp.Tokens.ExpiresAt.UTC()
	if resp.User != nil {
		body.User.ID = resp.User.ID.String()
		if resp.User.Email != nil {
			body.User.Email = *resp.User.Email
		}
	}
	return body
}

type adminLoginRequest struct {
	Identifier string `json:"identifier"`
	Email      string `json:"email"`
	Password   string `json:"password"`
}

// AdminSessionLogin — POST /v1/auth/admin-session/login
//
//	200 {"requires_2fa":true,"pending_token","expires_at"}   no cookies
//	400 INVALID_REQUEST          identifier and password are required
//	401 AUTH_FAILED              unknown account or wrong password
//	403 NOT_ADMIN                the account holds no admin permission
//	403 MFA_NOT_ENROLLED         no authenticator app enrolled
//	403 ACCOUNT_NOT_ACTIVE       pending, deactivated, suspended or deleting
//	429 RATE_LIMITED             login limiter
//	503 PERMISSIONS_UNAVAILABLE  permissions could not be read (fails closed)
func (h *Handler) AdminSessionLogin(c *gin.Context) {
	var req adminLoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "identifier and password are required", nil, nil)
		return
	}
	identifier := strings.TrimSpace(req.Identifier)
	if identifier == "" {
		identifier = strings.TrimSpace(req.Email)
	}
	if identifier == "" || req.Password == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "identifier and password are required", nil, nil)
		return
	}
	challenge, err := h.svc.AdminLogin(c.Request.Context(), identifier, req.Password, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.log.Warn("admin sign-in refused", "err", err, "identifier", maskIdentifier(identifier), "request_id", RequestIDFromContext(c))
		h.writeAdminSessionErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, challenge, nil)
}

type adminVerifyRequest struct {
	PendingToken string `json:"pending_token"`
	Code         string `json:"code"`
}

// AdminSessionVerify2FA — POST /v1/auth/admin-session/verify-2fa
//
//	200 {"session_id","admin_mfa":true,"access_expires_at","user":{id,email}}
//	    + Set-Cookie admin_access_token, admin_refresh_token, admin_csrf_token
//	400 INVALID_REQUEST        pending_token and code are required
//	401 INVALID_OTP            wrong or expired code (recovery codes are refused)
//	401 OTP_REPLAYED           code already used
//	401 ADMIN_SIGN_IN_EXPIRED  pending sign-in unknown, expired or used
//	403 NOT_ADMIN / MFA_NOT_ENROLLED / ACCOUNT_NOT_ACTIVE   re-checked live
//	429 RATE_LIMITED           5 codes / 15 min per account (+ login limiter)
func (h *Handler) AdminSessionVerify2FA(c *gin.Context) {
	var req adminVerifyRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.PendingToken) == "" || strings.TrimSpace(req.Code) == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "pending_token and code are required", nil, nil)
		return
	}
	resp, err := h.svc.AdminVerify2FA(c.Request.Context(), req.PendingToken, req.Code)
	if err != nil {
		h.log.Warn("admin sign-in 2fa refused", "err", err, "request_id", RequestIDFromContext(c))
		h.writeAdminSessionErr(c, err)
		return
	}
	if !h.setAdminCookies(c, resp.Tokens) {
		_ = h.svc.AdminLogout(c.Request.Context(), resp.Tokens.RefreshToken)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, newAdminSessionBody(resp), nil)
}

// AdminSessionRefresh — POST /v1/auth/admin-session/refresh  (X-CSRF-Token)
//
// Reads ONLY the admin_refresh_token cookie; a body token or the consumer
// refresh_token cookie is never consulted. On any refusal the admin cookies
// are cleared.
//
//	200 {"session_id","admin_mfa":true,"access_expires_at","user"} + rotated cookies
//	401 AUTH_FAILED          missing, unknown, expired or consumer token
//	401 ADMIN_ACCESS_ENDED   the holder is no longer an active TOTP admin (session revoked)
//	403 CSRF_FAILED
func (h *Handler) AdminSessionRefresh(c *gin.Context) {
	refreshToken, _ := c.Cookie(adminRefreshCookieName)
	if refreshToken == "" {
		h.clearAdminCookies(c)
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}
	resp, err := h.svc.AdminRefreshSession(c.Request.Context(), refreshToken, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.log.Warn("admin refresh refused", "err", err, "request_id", RequestIDFromContext(c))
		if errors.Is(err, service.ErrAdminPermissionsUnavailable) {
			api.Error(c.Writer, http.StatusServiceUnavailable, CodePermissionsUnavailable, "admin permissions could not be checked; try again", nil, nil)
			return
		}
		h.clearAdminCookies(c)
		if errors.Is(err, service.ErrAdminAccessLost) {
			api.Error(c.Writer, http.StatusUnauthorized, CodeAdminAccessEnded, "admin access has ended for this session; sign in again", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}
	if !h.setAdminCookies(c, resp.Tokens) {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, newAdminSessionBody(resp), nil)
}

// AdminSessionLogout — POST /v1/auth/admin-session/logout
//
//	200 {"status":"ok"} and the admin cookies cleared. Revokes the admin
//	session named by admin_refresh_token; a consumer session is never touched.
func (h *Handler) AdminSessionLogout(c *gin.Context) {
	refreshToken, _ := c.Cookie(adminRefreshCookieName)
	if err := h.svc.AdminLogout(c.Request.Context(), refreshToken); err != nil {
		h.log.Error("admin logout failed", "err", err, "request_id", RequestIDFromContext(c))
		h.clearAdminCookies(c)
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	h.clearAdminCookies(c)
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// adminCaller reads the verified caller and session id set by the admin auth
// middleware.
func (h *Handler) adminCaller(c *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	userID, ok := h.callerID(c)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	sa, ok := service.SessionAuthFrom(c.Request.Context())
	if !ok || sa.SessionID == uuid.Nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "a session token is required", nil, nil)
		return uuid.Nil, uuid.Nil, false
	}
	return userID, sa.SessionID, true
}

// AdminSessionStatus — GET /v1/auth/admin-session
//
//	200 {"user_id","email","admin_mfa","session_expires_at","step_up_valid_until"?}
//	401 UNAUTHORIZED / WRONG_SESSION / SESSION_REVOKED
func (h *Handler) AdminSessionStatus(c *gin.Context) {
	userID, sessionID, ok := h.adminCaller(c)
	if !ok {
		return
	}
	status, err := h.svc.AdminSessionStatusFor(c.Request.Context(), userID, sessionID)
	if err != nil {
		h.writeAdminSessionErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, status, nil)
}

// AdminSessionStepUp — POST /v1/auth/admin-session/step-up {"otp"}  (X-CSRF-Token)
//
// POST /v1/auth/step-up for the admin session: re-verifies TOTP and re-issues
// admin_access_token with step_up_at. The token is not in the body.
//
//	200 {"expires_at","step_up_at","step_up_valid_until","admin_mfa"}
//	errors as POST /v1/auth/step-up, plus 401 WRONG_SESSION for a consumer session
func (h *Handler) AdminSessionStepUp(c *gin.Context) {
	userID, sessionID, ok := h.adminCaller(c)
	if !ok {
		return
	}
	var req stepUpRequest
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.OTP) == "" {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "otp is required", nil, nil)
		return
	}
	resp, err := h.svc.AdminStepUp(c.Request.Context(), userID, sessionID, req.OTP)
	if err != nil {
		if errors.Is(err, service.ErrWrongSessionKind) {
			h.writeAdminSessionErr(c, err)
			return
		}
		h.writeStepUpErr(c, err)
		return
	}
	http.SetCookie(c.Writer, h.adminCookie(adminAccessCookieName, resp.AccessToken, resp.ExpiresAt, true))
	body := *resp
	body.AccessToken = ""
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"expires_at":          body.ExpiresAt,
		"step_up_at":          body.StepUpAt,
		"step_up_valid_until": body.StepUpValidUntil,
		"admin_mfa":           body.AdminMFA,
	}, nil)
}

// writeAdminSessionErr maps the admin sign-in errors to stable codes.
func (h *Handler) writeAdminSessionErr(c *gin.Context, err error) {
	if throttled, ok := service.AsThrottled(err); ok {
		retryAfter := int(throttled.RetryAfter.Seconds())
		if retryAfter < 1 {
			retryAfter = 1
		}
		c.Header("Retry-After", strconv.Itoa(retryAfter))
		api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", "Too many attempts. Try again later.",
			map[string]any{"retry_after_seconds": retryAfter}, nil)
		return
	}
	switch {
	case errors.Is(err, service.ErrAdminInvalidCredentials):
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
	case errors.Is(err, service.ErrNotAdmin):
		api.Error(c.Writer, http.StatusForbidden, CodeNotAdmin, "this account has no admin access", nil, nil)
	case errors.Is(err, service.ErrTOTPNotEnrolled):
		api.Error(c.Writer, http.StatusForbidden, CodeMFANotEnrolled,
			"admin accounts must enrol an authenticator app before signing in to the console", nil, nil)
	case errors.Is(err, service.ErrAdminAccountInactive):
		api.Error(c.Writer, http.StatusForbidden, CodeAccountNotActive, "this account is not active", nil, nil)
	case errors.Is(err, service.ErrAdminPermissionsUnavailable):
		api.Error(c.Writer, http.StatusServiceUnavailable, CodePermissionsUnavailable, "admin permissions could not be checked; try again", nil, nil)
	case errors.Is(err, service.ErrAdminPendingInvalid):
		api.Error(c.Writer, http.StatusUnauthorized, CodeAdminSignInExpired, "this sign-in has expired; enter your password again", nil, nil)
	case errors.Is(err, service.ErrInvalidOTP):
		api.Error(c.Writer, http.StatusUnauthorized, CodeInvalidOTP, "invalid two-factor code", nil, nil)
	case errors.Is(err, service.ErrTOTPReplay):
		api.Error(c.Writer, http.StatusUnauthorized, CodeOTPReplayed, "this code was already used; wait for the next one", nil, nil)
	case errors.Is(err, service.ErrWrongSessionKind):
		api.Error(c.Writer, http.StatusUnauthorized, CodeWrongSession, "an admin console session is required", nil, nil)
	case errors.Is(err, service.ErrAdminAccessLost):
		api.Error(c.Writer, http.StatusUnauthorized, CodeAdminAccessEnded, "admin access has ended for this session; sign in again", nil, nil)
	case errors.Is(err, service.ErrSessionNotLive):
		api.Error(c.Writer, http.StatusUnauthorized, "SESSION_REVOKED", "Session has been revoked", nil, nil)
	default:
		h.log.Error("admin session error", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	}
}
