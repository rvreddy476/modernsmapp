// HTTP layer core: the Handler type, the AuthService interface it calls, the
// constructor, credential rate limits, RegisterRoutes (the full route table),
// the health probe, auth cookie handling and the small shared helpers
// (OTP purpose check, CSRF token, masking, throttle responses). Route
// handlers live by group in me.go, login.go, registration.go, sessions.go,
// password_reset.go, verification.go and docs.go.

package http

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/middleware"
	"github.com/atpost/identity-auth-service/internal/permissions"
	"github.com/atpost/identity-auth-service/internal/rollout"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-auth-service/internal/servicetoken"
	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	accessTokenCookieName  = "access_token"
	refreshTokenCookieName = "refresh_token"
	csrfCookieName         = "csrf_token"
)

type Handler struct {
	svc   AuthService
	cfg   *config.Config
	log   *slog.Logger
	rdb   *redis.Client
	flags *rollout.Flags
	// idempotency backs the Idempotency middleware. Nil disables replay, which
	// is what unit tests that do not exercise it get.
	idempotency idempotencyStore
	waStore     WebAuthnStore // set via SetWebAuthnStore; used only by the webauthn-tagged ceremony
	// adminTokens verifies admin-service tokens for /v1/auth/internal/admin/*
	// (admin_console.go). Nil: the family accepts nothing.
	adminTokens *servicetoken.Verifier
}

// SetIdempotencyStore installs the replay store. Separate from New so tests
// can construct a Handler without one.
func (h *Handler) SetIdempotencyStore(s idempotencyStore) { h.idempotency = s }

type AuthService interface {
	RequestOTP(ctx context.Context, phone, purpose string) error
	VerifyOTP(ctx context.Context, phone, code, purpose, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	RegisterWithPassword(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string) (*service.AuthResponse, error)
	// SR-6: registration now requires a date of birth and an explicit,
	// versioned consent. RegisterWithPassword is kept for existing internal
	// callers and delegates here with an empty consent, which fails the gate.
	RegisterWithConsent(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string, consent service.RegistrationConsent) (*service.AuthResponse, error)
	LoginWithPassword(ctx context.Context, identifier, password, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	RefreshSession(ctx context.Context, refreshToken, ip, userAgent string) (*service.AuthResponse, error)
	IssueSessionForUser(ctx context.Context, userID uuid.UUID, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	Logout(ctx context.Context, refreshToken string) error
	LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error)
	ListSessions(ctx context.Context, userID uuid.UUID) ([]store.Session, error)
	RevokeSessionByID(ctx context.Context, userID, sessionID uuid.UUID) error
	// Account control — both re-verify the password (ErrInvalidPassword).
	DeactivateAccount(ctx context.Context, userID uuid.UUID, password string) error
	DeleteAccount(ctx context.Context, userID uuid.UUID, password string) (*service.DeletionSchedule, error)
	// RBAC role management (superadmin-gated in the service layer)
	GrantRole(ctx context.Context, actorID uuid.UUID, req service.RoleChangeRequest) error
	RevokeRole(ctx context.Context, actorID uuid.UUID, req service.RoleChangeRequest) error
	PermissionsForUser(ctx context.Context, userID uuid.UUID) (permissions.Admin, error)
	ListUserRoles(ctx context.Context, actorID, targetID uuid.UUID) ([]store.UserRole, error)
	ListAdminAudit(ctx context.Context, actorID uuid.UUID, limit int) ([]store.AdminAuditEntry, error)
	// Admin sessions (A2): step-up, force logout, second-approver count.
	StepUp(ctx context.Context, userID, sessionID uuid.UUID, code string) (*service.StepUpResponse, error)
	ForceLogout(ctx context.Context, actorID, targetID uuid.UUID, reason string) (int, error)
	CountOtherHolders(ctx context.Context, permission string, excludeUserID uuid.UUID) (int, error)
	// Admin console Access page through admin-service (B4): the actor is the
	// token's signed act claim; see service/admin_console.go.
	ConsoleGrantRole(ctx context.Context, actor service.ConsoleActor, req service.RoleChangeRequest) error
	ConsoleRevokeRole(ctx context.Context, actor service.ConsoleActor, req service.RoleChangeRequest) error
	ConsoleForceLogout(ctx context.Context, actor service.ConsoleActor, targetID uuid.UUID, reason string) (int, error)
	ConsoleListRoleHolders(ctx context.Context, f store.RoleHolderFilter) (service.ConsoleRoleHolders, error)
	ConsoleListUserRoles(ctx context.Context, targetID uuid.UUID) ([]store.UserRole, error)
	ConsoleListAudit(ctx context.Context, f store.AuditFilter) ([]store.AdminAuditEntry, error)
	ConsoleSearchUsers(ctx context.Context, q string, limit int) ([]service.UserSearchResult, error)
	// Admin console sign-in on its own host (B3); see admin_login.go.
	AdminLogin(ctx context.Context, identifier, password, ip, userAgent string) (*service.AdminLoginChallenge, error)
	AdminVerify2FA(ctx context.Context, pendingToken, code string) (*service.AuthResponse, error)
	AdminRefreshSession(ctx context.Context, refreshToken, ip, userAgent string) (*service.AuthResponse, error)
	AdminLogout(ctx context.Context, refreshToken string) error
	AdminStepUp(ctx context.Context, userID, sessionID uuid.UUID, code string) (*service.StepUpResponse, error)
	AdminSessionStatusFor(ctx context.Context, userID, sessionID uuid.UUID) (*service.AdminSessionStatus, error)
	// Service-to-service ecosystem role management. Guarded at the route by
	// RequireInternalServiceKey and constrained in the service layer to the
	// four ecosystem roles — a service can never mint admin or superadmin.
	GrantEcosystemRole(ctx context.Context, callingService string, targetID uuid.UUID, role, reason string) error
	RevokeEcosystemRole(ctx context.Context, callingService string, targetID uuid.UUID, role, reason string) error
	// Role read path. ResolveRoles is env-allowlist ∪ DB, implication-expanded.
	ResolveRoles(ctx context.Context, userID uuid.UUID) []string
	CapabilitiesForUser(ctx context.Context, userID uuid.UUID) service.Capabilities
	// 2FA
	Setup2FA(ctx context.Context, userID uuid.UUID) (*service.TwoFASetupResponse, error)
	Verify2FASetup(ctx context.Context, userID uuid.UUID, code string) error
	Disable2FA(ctx context.Context, userID uuid.UUID, password, code string) error
	Verify2FA(ctx context.Context, userID uuid.UUID, code, pendingToken string) (*service.AuthResponse, error)
	// OAuth
	GetOAuthRedirectURL(ctx context.Context, provider string) (*service.OAuthStart, error)
	HandleOAuthCallback(ctx context.Context, provider, code, state, binding string, client service.OAuthClient) (*service.OAuthCallbackResult, error)
	HandleOAuthToken(ctx context.Context, provider, idToken string, client service.OAuthClient) (*service.OAuthCallbackResult, error)
	// A5: OAuth pre-creation OTP-signup flow.
	CompleteOAuthSignup(ctx context.Context, pendingToken, phone string) error
	VerifyOAuthSignup(ctx context.Context, pendingToken, otp, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	// Password reset
	ForgotPassword(ctx context.Context, identifier string) error
	ResetPassword(ctx context.Context, identifier, code, newPassword string) error
	// Email/Phone verification
	RequestEmailVerification(ctx context.Context, userID uuid.UUID) error
	VerifyEmail(ctx context.Context, userID uuid.UUID, code string) error
	// CLB-3: the public, transaction-scoped forms. The handler must NOT be
	// able to name an account itself — these take the server-issued
	// credential, and the user id is derived from it.
	VerifyEmailWithTransaction(ctx context.Context, transaction, code string) error
	ResendVerificationWithTransaction(ctx context.Context, transaction string) error
	RequestPhoneVerification(ctx context.Context, userID uuid.UUID) error
	VerifyPhone(ctx context.Context, userID uuid.UUID, code string) error
	// Trusted devices
	ListTrustedDevices(ctx context.Context, userID uuid.UUID) ([]store.TrustedDevice, error)
	TrustDevice(ctx context.Context, userID uuid.UUID, fingerprint string, deviceName *string) error
	RemoveTrustedDevice(ctx context.Context, userID, deviceID uuid.UUID) error
	// A13 — login anomaly inbox
	ListMyAnomalies(ctx context.Context, userID uuid.UUID, limit int) ([]store.LoginAnomaly, error)
	AcknowledgeMyAnomaly(ctx context.Context, userID, anomalyID uuid.UUID) error
	// A13 — anomaly step-up (graduated enforcement at login). Both
	// resolve methods consume a pending_token issued during login and
	// either mint the session (on success) or return one of the
	// service.ErrAnomaly* sentinels.
	ResolveAnomalyStepUpEmail(ctx context.Context, pendingToken, code string) (*service.AuthResponse, error)
	ResolveAnomalyStepUp2FA(ctx context.Context, pendingToken, code string) (*service.AuthResponse, error)
	// GDPR
	ExportUserData(ctx context.Context, userID string) (*service.DataExport, error)
	// Internal lookup
	GetUserContact(ctx context.Context, userID uuid.UUID) (*store.User, error)
	// Mini app sessions
	IssueMiniAppSession(ctx context.Context, appID, userID uuid.UUID, grantedPermissions []string) (*service.MiniAppSessionResponse, error)
	MiniAppJWKS(ctx context.Context) (*service.JSONWebKeySet, error)
}

func New(svc AuthService, cfg *config.Config, logger *slog.Logger, rdb *redis.Client) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	// Rollout flags are resolved per request from Redis with a short cache, so
	// an operator can flip enforcement off without a redeploy. Defaults come
	// from config so a Redis outage cannot change behaviour in either
	// direction. See internal/rollout.
	flags := rollout.New(rdb, logger, map[string]bool{
		rollout.FlagRegisterRequireGender: cfg.RegisterRequireGender,
	})
	h := &Handler{svc: svc, cfg: cfg, log: logger, rdb: rdb, flags: flags}
	// admin-service token verifier for the console family. A misconfigured
	// key is fatal, like an unreadable JWT_PRIVATE_KEY_PEM: the Access page
	// must not be silently disabled by a typo in a secret.
	v, err := AdminServiceVerifier(cfg)
	if err != nil {
		logger.Error("failed to load ADMIN_SERVICE_TOKEN_PUBKEY", "err", err)
		panic(fmt.Sprintf("invalid ADMIN_SERVICE_TOKEN_PUBKEY/KID: %v", err))
	}
	if v != nil {
		logger.Info("admin-service tokens accepted on /v1/auth/internal/admin", "kid", strings.TrimSpace(cfg.AdminServiceTokenKID))
	}
	h.adminTokens = v
	return h
}

// credentialLimit is the configured rate limit (AUTH_RATE_LIMIT_*) for one
// consumer credential action, in that action's own bucket.
func (h *Handler) credentialLimit(action middleware.CredentialAction) gin.HandlerFunc {
	return middleware.CredentialRateLimit(h.rdb, action, middleware.CredentialLimits{
		PerIP:      int64(h.cfg.AuthRateLimitPerIP),
		PerAccount: int64(h.cfg.AuthRateLimitPerAccount),
		Window:     h.cfg.AuthRateLimitWindow,
	})
}

// strictCredentialLimit keeps the old 10-per-IP numbers for routes whose users
// are not a carrier-NAT crowd (admin console, step-up, passkeys).
func (h *Handler) strictCredentialLimit(action middleware.CredentialAction) gin.HandlerFunc {
	return middleware.CredentialRateLimit(h.rdb, action, middleware.StrictCredentialLimits)
}

func (h *Handler) RegisterRoutes(r *gin.Engine, authMW, csrfMW gin.HandlerFunc) {
	v1 := r.Group("/v1/auth")
	v1.POST("/partner/phone/request", h.credentialLimit(middleware.ActionPartnerPhoneSend), h.partnerPhoneRequest)
	v1.POST("/partner/phone/verify", h.credentialLimit(middleware.ActionPartnerPhoneCheck), h.partnerPhoneVerify)
	{
		// Public routes
		// SR-6: RETIRED. These generated a code, stored it, and sent nothing —
		// there is no SMS integration in this service, so the code could never
		// arrive. The rate limiters stay in front of them so a client looping
		// on the 410 still cannot hammer Redis. See retired_sms_routes.go.
		v1.POST("/request-otp", middleware.OTPRateLimit(h.rdb), retiredSMSRoute)
		v1.POST("/verify-otp", h.strictCredentialLimit(middleware.ActionRetiredOTP), retiredSMSRoute)
		// H2 (arch review): /register is direct password signup. Without a
		// limit an attacker can spam-create accounts to exhaust handle/email
		// namespace or burn captcha quotas. The credential limiter, in a
		// bucket of its own (see credentialLimit).
		// Idempotency runs BEFORE the rate limiter: a client replaying a
		// request it never saw the response to should get its original result
		// back, not be counted again toward a limit it did not knowingly spend.
		v1.POST("/register",
			Idempotency(h.idempotency, "POST /v1/auth/register", h.log),
			h.credentialLimit(middleware.ActionRegister),
			h.Register)
		v1.POST("/login", h.credentialLimit(middleware.ActionLogin), h.Login)
		// H2: refresh-token flood is unlikely (long opaque tokens) but a
		// stolen refresh could DoS the service before fingerprint mismatch
		// burns the session. IP-only cap is cheap defence in depth.
		// Per-token, not per-IP: behind the gateway every user shares one IP.
		v1.POST("/refresh", middleware.RefreshRateLimit(h.rdb), h.Refresh)
		v1.POST("/logout", h.Logout)
		v1.GET("/health", h.Health)

		// 2FA public route (called after login returns requires_2fa).
		// H2: was unprotected — a 6-digit TOTP code is 10^6 attempts.
		// The per-account cap (5 per user_id per window) makes online brute
		// force infeasible; the per-IP cap is sized for carrier NAT.
		v1.POST("/2fa/verify", h.credentialLimit(middleware.ActionTwoFactor), h.Verify2FA)

		// A13 — anomaly step-up. Public (the user is mid-login and
		// doesn't have a session yet). Both endpoints consume a
		// pending_token issued by Login/VerifyOTP when the risk band
		// is high AND LOGIN_ANOMALY_ENFORCE=enforce. The /verify-email
		// path is gated by the password-reset rate limiter (same
		// abuse surface — email-OTP burn). The /verify-2fa path has a
		// credential bucket of its own.
		v1.POST("/anomaly/verify-email", middleware.PasswordResetRateLimit(h.rdb), h.AnomalyVerifyEmail)
		v1.POST("/anomaly/verify-2fa", h.credentialLimit(middleware.ActionAnomalyTwoFactor), h.AnomalyVerify2FA)

		// OAuth routes (public)
		v1.GET("/oauth/:provider", h.OAuthRedirect)
		v1.GET("/oauth/:provider/callback", h.OAuthCallback)
		v1.POST("/oauth/:provider/token", h.credentialLimit(middleware.ActionOAuthToken), h.OAuthToken)
		// A5: OAuth pre-creation OTP-signup flow — used when the
		// provider didn't assert email_verified. complete-signup
		// sends the OTP to the supplied phone; verify-signup
		// finalises the account creation once the OTP matches.
		// Reuse the OTP rate-limiter so this can't be abused for
		// SMS-flood billing attacks.
		v1.POST("/oauth/complete-signup", middleware.OTPRateLimit(h.rdb), h.OAuthCompleteSignup)
		v1.POST("/oauth/verify-signup", h.credentialLimit(middleware.ActionOAuthVerifySignup), h.OAuthVerifySignup)
		v1.GET("/.well-known/jwks.json", h.MiniAppJWKS)

		// Password reset (public)
		// Audit A12: rate-limit both endpoints so an attacker can't spam
		// SMS/email resets and lock the victim out via provider abuse.
		// Reset tokens themselves remain server-issued and short-lived.
		v1.POST("/forgot-password", middleware.PasswordResetRateLimit(h.rdb), h.ForgotPassword)
		v1.POST("/reset-password", middleware.PasswordResetRateLimit(h.rdb), h.ResetPassword)

		// CLB-3: email verification is PUBLIC and credential-scoped.
		//
		// These sat under the authenticated+CSRF group and derived the user
		// from X-User-Id. Only a verified bearer session causes that header to
		// be set, and registration deliberately issues none — so a pending
		// user could receive a code and had no route to submit it, and no way
		// to ask for another. The signup loop ended there for every user.
		//
		// They are not "unauthenticated": each call must present the opaque,
		// short-lived verification transaction the server issued to that one
		// account (see internal/store/verification_transaction.go). A raw user
		// id is not accepted, so being public does not mean being addressable
		// by account.
		//
		// Rate limiting is the OTP limiter — the same one guarding every other
		// code-sending path, so this cannot be turned into a mail-flood or a
		// code-grinding surface.
		v1.POST("/verify-email", middleware.OTPRateLimit(h.rdb), h.VerifyEmail)
		v1.POST("/resend-verification", middleware.OTPRateLimit(h.rdb), h.ResendVerification)
		// SR-6: RETIRED — no SMS delivery, so no phone code can arrive.
		v1.POST("/verify-phone", retiredSMSRoute)

		// Token introspection — auth only (no CSRF; safe GET used by server-side proxies)
		v1.GET("/me", authMW, h.Me)
		// Role switcher payload. Identity is the ecosystem's single role
		// authority, so this answers "which hats does this person wear" in one
		// request, without the client calling commerce, food and rider.
		v1.GET("/me/capabilities", authMW, h.MeCapabilities)

		// Protected routes (require auth + CSRF)
		protected := v1.Group("", authMW, csrfMW)
		{
			protected.POST("/logout-all", h.LogoutAll)
			protected.GET("/sessions", h.ListSessions)
			protected.DELETE("/sessions/:id", h.RevokeSessionByID)
			// Account control (see account_control.go). Deactivate is
			// reversible by logging in; DELETE schedules a purge 30 days out
			// that a login inside the window cancels. Both re-verify the
			// password and revoke every session.
			protected.POST("/account/deactivate", h.DeactivateAccount)
			protected.DELETE("/account", h.DeleteAccount)

			// RBAC role management — authorization (superadmin) is enforced in
			// the service layer against the live env∪DB source of truth, not a
			// (possibly stale) token scope.
			protected.POST("/admin/roles", h.GrantRole)
			protected.DELETE("/admin/roles", h.RevokeRole)
			protected.GET("/admin/roles/:userId", h.ListUserRoles)
			protected.GET("/admin/audit", h.ListAdminAudit)
			// Force logout: superadmin + admin MFA + fresh step-up, audited
			// in the same transaction. See admin_session.go.
			protected.POST("/admin/users/:userId/sessions/revoke", h.ForceLogout)

			// Step-up (A2): re-verify TOTP, get a token with step_up_at.
			// Login IP limiter here, plus a per-account limit in the service.
			protected.POST("/step-up", h.strictCredentialLimit(middleware.ActionStepUp), h.StepUp)

			// 2FA management (protected)
			protected.POST("/2fa/setup", h.Setup2FA)
			protected.POST("/2fa/verify-setup", h.Verify2FASetup)
			protected.POST("/2fa/disable", h.Disable2FA)

			// CLB-3: verify-email / verify-phone / resend-verification moved
			// to the public, transaction-scoped group above. A pending account
			// has no session, so requiring one here made them unreachable.

			// Trusted devices (protected)
			protected.GET("/trusted-devices", h.ListTrustedDevices)
			protected.DELETE("/trusted-devices/:id", h.RemoveTrustedDevice)
			protected.POST("/trust-device", h.TrustDevice)

			// A13 — login anomaly security inbox
			protected.GET("/security/anomalies", h.ListMyAnomalies)
			protected.POST("/security/anomalies/:id/ack", h.AcknowledgeMyAnomaly)

			// GDPR data portability
			protected.GET("/data-export", h.ExportUserData)
		}

		internal := v1.Group("/internal", RequireInternalServiceKey(h.cfg.InternalServiceKey))
		{
			internal.POST("/mini-app-session", h.CreateMiniAppSession)
			internal.GET("/users/:userId", h.InternalGetUserContact)
			// Ecosystem role grant/revoke for commerce, food and rider. See
			// internal_roles.go for the contract and the boundary.
			internal.POST("/roles", h.InternalGrantRole)
			internal.DELETE("/roles", h.InternalRevokeRole)
			internal.GET("/roles/:userId", h.InternalListRoles)
		}

		// Admin permission map for admin-service (and later other services).
		// Service-only: user identity headers are refused before the key is
		// checked, so a moderator the gateway lets through to /internal/ paths
		// is still turned away. See internal_permissions.go.
		v1.GET("/internal/users/:userId/permissions",
			RequireServiceCallerNoUser(h.cfg.InternalServiceKey), h.InternalUserPermissions)
		// Two-person approval support for admin-service: how many OTHER
		// active, TOTP-enrolled accounts hold a permission. Service-only.
		v1.GET("/internal/permissions/:permission/holders",
			RequireServiceCallerNoUser(h.cfg.InternalServiceKey), h.InternalPermissionHolders)
		// The admin console's Access page through admin-service: judged by
		// a signed admin-service token alone. See admin_console.go.
		h.registerConsoleRoutes(v1)
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) setAuthCookies(c *gin.Context, tokens service.TokenPair) {
	accessCookie := &http.Cookie{
		Name:     accessTokenCookieName,
		Value:    tokens.AccessToken,
		Path:     "/",
		Domain:   h.cfg.CookieDomain,
		Expires:  tokens.ExpiresAt,
		Secure:   h.cfg.CookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(c.Writer, accessCookie)

	refreshCookie := &http.Cookie{
		Name:     refreshTokenCookieName,
		Value:    tokens.RefreshToken,
		Path:     "/",
		Domain:   h.cfg.CookieDomain,
		Expires:  time.Now().Add(h.cfg.RefreshTokenTTL),
		Secure:   h.cfg.CookieSecure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(c.Writer, refreshCookie)

	csrfToken, err := generateCSRFToken()
	if err != nil {
		h.log.Warn("failed to generate csrf token", "err", err, "request_id", RequestIDFromContext(c))
		return
	}
	csrfCookie := &http.Cookie{
		Name:     csrfCookieName,
		Value:    csrfToken,
		Path:     "/",
		Domain:   h.cfg.CookieDomain,
		Expires:  time.Now().Add(h.cfg.RefreshTokenTTL),
		Secure:   h.cfg.CookieSecure,
		HttpOnly: false,
		SameSite: http.SameSiteLaxMode,
	}
	http.SetCookie(c.Writer, csrfCookie)
}

func (h *Handler) clearAuthCookies(c *gin.Context) {
	expired := time.Now().Add(-24 * time.Hour)
	for _, name := range []string{accessTokenCookieName, refreshTokenCookieName, csrfCookieName} {
		http.SetCookie(c.Writer, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			Domain:   h.cfg.CookieDomain,
			Expires:  expired,
			MaxAge:   -1,
			Secure:   h.cfg.CookieSecure,
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
		})
	}
}

func validateOTPPurpose(purpose string) error {
	switch purpose {
	case "login", "register", "password_reset", "email_verify", "phone_verify":
		return nil
	default:
		return errors.New("purpose must be one of: login, register, password_reset, email_verify, phone_verify")
	}
}

func generateCSRFToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func maskPhone(phone string) string {
	trimmed := strings.TrimSpace(phone)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 4 {
		return strings.Repeat("*", len(trimmed))
	}
	return strings.Repeat("*", len(trimmed)-2) + trimmed[len(trimmed)-2:]
}

// writeIfThrottled answers 429 with Retry-After when err is a service
// *ErrThrottled, and reports whether it did.
func writeIfThrottled(c *gin.Context, err error, message string) bool {
	throttled, ok := service.AsThrottled(err)
	if !ok {
		return false
	}
	retryAfter := int(throttled.RetryAfter.Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	c.Header("Retry-After", strconv.Itoa(retryAfter))
	api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED", message,
		map[string]any{"retry_after_seconds": retryAfter}, nil)
	return true
}

func maskEmail(email string) string {
	trimmed := strings.TrimSpace(email)
	if trimmed == "" {
		return ""
	}
	parts := strings.SplitN(trimmed, "@", 2)
	if len(parts) != 2 {
		return "***"
	}
	local := parts[0]
	if len(local) > 1 {
		local = local[:1] + "***"
	} else {
		local = "***"
	}
	return local + "@" + parts[1]
}

func maskIdentifier(identifier string) string {
	if strings.Contains(identifier, "@") {
		return maskEmail(identifier)
	}
	return maskPhone(identifier)
}
