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
	"unicode"
	"unicode/utf8"

	"github.com/atpost/identity-auth-service/internal/config"
	"github.com/atpost/identity-auth-service/internal/middleware"
	"github.com/atpost/identity-auth-service/internal/rollout"
	"github.com/atpost/identity-auth-service/internal/service"
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
	DeleteAccount(ctx context.Context, userID uuid.UUID) error
	// RBAC role management (superadmin-gated in the service layer)
	GrantRole(ctx context.Context, actorID, targetID uuid.UUID, role string) error
	RevokeRole(ctx context.Context, actorID, targetID uuid.UUID, role string) error
	ListUserRoles(ctx context.Context, actorID, targetID uuid.UUID) ([]store.UserRole, error)
	ListAdminAudit(ctx context.Context, actorID uuid.UUID, limit int) ([]store.AdminAuditEntry, error)
	// 2FA
	Setup2FA(ctx context.Context, userID uuid.UUID) (*service.TwoFASetupResponse, error)
	Verify2FASetup(ctx context.Context, userID uuid.UUID, code string) error
	Disable2FA(ctx context.Context, userID uuid.UUID, password, code string) error
	Verify2FA(ctx context.Context, userID uuid.UUID, code, pendingToken string) (*service.AuthResponse, error)
	// OAuth
	GetOAuthRedirectURL(ctx context.Context, provider string) (string, error)
	HandleOAuthCallback(ctx context.Context, provider, code, state string) (*service.OAuthCallbackResult, error)
	HandleOAuthToken(ctx context.Context, provider, accessToken string) (*service.OAuthCallbackResult, error)
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
	return &Handler{svc: svc, cfg: cfg, log: logger, rdb: rdb, flags: flags}
}

func (h *Handler) RegisterRoutes(r *gin.Engine, authMW, csrfMW gin.HandlerFunc) {
	v1 := r.Group("/v1/auth")
	{
		// Public routes
		// SR-6: RETIRED. These generated a code, stored it, and sent nothing —
		// there is no SMS integration in this service, so the code could never
		// arrive. The rate limiters stay in front of them so a client looping
		// on the 410 still cannot hammer Redis. See retired_sms_routes.go.
		v1.POST("/request-otp", middleware.OTPRateLimit(h.rdb), retiredSMSRoute)
		v1.POST("/verify-otp", middleware.LoginRateLimit(h.rdb), retiredSMSRoute)
		// H2 (arch review): /register is direct password signup. Without a
		// limit an attacker can spam-create accounts to exhaust handle/email
		// namespace or burn captcha quotas. Reuse the login limiter (10/IP
		// /15min, 5/identifier/15min) — same abuse surface.
		// Idempotency runs BEFORE the rate limiter: a client replaying a
		// request it never saw the response to should get its original result
		// back, not be counted again toward a limit it did not knowingly spend.
		v1.POST("/register",
			Idempotency(h.idempotency, "POST /v1/auth/register", h.log),
			middleware.LoginRateLimit(h.rdb),
			h.Register)
		v1.POST("/login", middleware.LoginRateLimit(h.rdb), h.Login)
		// H2: refresh-token flood is unlikely (long opaque tokens) but a
		// stolen refresh could DoS the service before fingerprint mismatch
		// burns the session. IP-only cap is cheap defence in depth.
		v1.POST("/refresh", middleware.LoginRateLimit(h.rdb), h.Refresh)
		v1.POST("/logout", h.Logout)
		v1.GET("/health", h.Health)

		// 2FA public route (called after login returns requires_2fa).
		// H2: was unprotected — a 6-digit TOTP code is 10^6 attempts.
		// LoginRateLimit (5/identifier/15min + 10/IP/15min) makes
		// online brute force infeasible.
		v1.POST("/2fa/verify", middleware.LoginRateLimit(h.rdb), h.Verify2FA)

		// A13 — anomaly step-up. Public (the user is mid-login and
		// doesn't have a session yet). Both endpoints consume a
		// pending_token issued by Login/VerifyOTP when the risk band
		// is high AND LOGIN_ANOMALY_ENFORCE=enforce. The /verify-email
		// path is gated by the password-reset rate limiter (same
		// abuse surface — email-OTP burn). The /verify-2fa path
		// piggy-backs on the login limiter.
		v1.POST("/anomaly/verify-email", middleware.PasswordResetRateLimit(h.rdb), h.AnomalyVerifyEmail)
		v1.POST("/anomaly/verify-2fa", middleware.LoginRateLimit(h.rdb), h.AnomalyVerify2FA)

		// OAuth routes (public)
		v1.GET("/oauth/:provider", h.OAuthRedirect)
		v1.GET("/oauth/:provider/callback", h.OAuthCallback)
		v1.POST("/oauth/:provider/token", h.OAuthToken)
		// A5: OAuth pre-creation OTP-signup flow — used when the
		// provider didn't assert email_verified. complete-signup
		// sends the OTP to the supplied phone; verify-signup
		// finalises the account creation once the OTP matches.
		// Reuse the OTP rate-limiter so this can't be abused for
		// SMS-flood billing attacks.
		v1.POST("/oauth/complete-signup", middleware.OTPRateLimit(h.rdb), h.OAuthCompleteSignup)
		v1.POST("/oauth/verify-signup", middleware.LoginRateLimit(h.rdb), h.OAuthVerifySignup)
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

		// Protected routes (require auth + CSRF)
		protected := v1.Group("", authMW, csrfMW)
		{
			protected.POST("/logout-all", h.LogoutAll)
			protected.GET("/sessions", h.ListSessions)
			protected.DELETE("/sessions/:id", h.RevokeSessionByID)
			protected.DELETE("/account", h.DeleteAccount)

			// RBAC role management — authorization (superadmin) is enforced in
			// the service layer against the live env∪DB source of truth, not a
			// (possibly stale) token scope.
			protected.POST("/admin/roles", h.GrantRole)
			protected.DELETE("/admin/roles", h.RevokeRole)
			protected.GET("/admin/roles/:userId", h.ListUserRoles)
			protected.GET("/admin/audit", h.ListAdminAudit)

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
		}
	}
}

// InternalGetUserContact returns contact fields for service-to-service use.
// Guarded by RequireInternalServiceKey — never expose to end users.
func (h *Handler) InternalGetUserContact(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil, nil)
		return
	}
	u, err := h.svc.GetUserContact(c.Request.Context(), userID)
	if err != nil || u == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "user not found", nil, nil)
		return
	}
	email := ""
	if u.Email != nil {
		email = *u.Email
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"user_id":        u.ID,
		"email":          email,
		"phone":          u.Phone,
		"email_verified": u.EmailVerified,
	}, nil)
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// Me returns the authenticated user's ID. Used by server-side proxies (e.g. chat proxy)
// to validate a bearer token and retrieve the corresponding user identity.
func (h *Handler) Me(c *gin.Context) {
	// X-User-Id is set by AuthMiddleware after validating the JWT.
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user identity", nil, nil)
		return
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user identity", nil, nil)
		return
	}
	user, err := h.svc.GetUserContact(c.Request.Context(), uid)
	if err != nil || user == nil {
		h.log.Error("account summary lookup failed", "err", err, "user_id", uid, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Unable to load account details", nil, nil)
		return
	}
	email := ""
	if user.Email != nil {
		email = *user.Email
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"id":                 userID,
		"user_id":            userID,
		"email":              email,
		"phone":              user.Phone,
		"email_verified":     user.EmailVerified,
		"phone_verified":     user.PhoneVerified,
		"two_factor_enabled": user.TwoFactorEnabled,
		"account_type":       user.AccountType,
		"account_status":     user.AccountStatus,
		"age_verification":   user.AgeVerification,
		"last_login_at":      user.LastLoginAt,
		"created_at":         user.CreatedAt,
	}, nil)
}

func (h *Handler) MiniAppJWKS(c *gin.Context) {
	jwks, err := h.svc.MiniAppJWKS(c.Request.Context())
	if err != nil {
		api.Error(c.Writer, http.StatusServiceUnavailable, "JWKS_UNAVAILABLE", "Mini app JWKS is not configured", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, jwks, nil)
}

type CreateMiniAppSessionRequest struct {
	AppID              string   `json:"app_id" binding:"required"`
	UserID             string   `json:"user_id" binding:"required"`
	GrantedPermissions []string `json:"granted_permissions"`
}

func (h *Handler) CreateMiniAppSession(c *gin.Context) {
	var req CreateMiniAppSessionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	appID, err := uuid.Parse(req.AppID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid app_id", nil, nil)
		return
	}
	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid user_id", nil, nil)
		return
	}

	resp, err := h.svc.IssueMiniAppSession(c.Request.Context(), appID, userID, req.GrantedPermissions)
	if err != nil {
		if err.Error() == "MINI_APP_SESSION_UNAVAILABLE" {
			api.Error(c.Writer, http.StatusServiceUnavailable, "SESSION_UNAVAILABLE", "Mini app session issuance is not configured", nil, nil)
			return
		}
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to issue mini app session", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

type RequestOTPRequest struct {
	Phone   string `json:"phone" binding:"required"`
	Purpose string `json:"purpose" binding:"required"`
}

func (h *Handler) RequestOTP(c *gin.Context) {
	var req RequestOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := validateOTPPurpose(req.Purpose); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.RequestOTP(c.Request.Context(), req.Phone, req.Purpose); err != nil {
		h.log.Error("failed to request otp", "err", err, "phone", maskPhone(req.Phone), "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "otp sent"}, nil)
}

type VerifyOTPRequest struct {
	Phone    string `json:"phone" binding:"required"`
	OTP      string `json:"otp" binding:"required"`
	Purpose  string `json:"purpose"`
	DeviceID string `json:"device_id"`
	Platform string `json:"platform"`
}

func (h *Handler) VerifyOTP(c *gin.Context) {
	var req VerifyOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if req.Purpose == "" {
		req.Purpose = "login"
	}
	if err := validateOTPPurpose(req.Purpose); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	resp, err := h.svc.VerifyOTP(c.Request.Context(), req.Phone, req.OTP, req.Purpose, req.DeviceID, req.Platform, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		// A13 — anomaly step-up envelope on a successful OTP that
		// landed in a novel device/network. Same shape as the Login
		// handler above.
		if errors.Is(err, service.ErrAnomalyStepUpRequired) && resp != nil {
			api.JSON(c.Writer, http.StatusOK, resp, nil)
			return
		}
		if errors.Is(err, service.ErrAnomalyStepUpUnavailable) {
			api.Error(c.Writer, http.StatusUnauthorized, "STEP_UP_UNAVAILABLE",
				"This sign-in looks unusual and your account has no recovery channel set up. Please contact support.", nil, nil)
			return
		}
		h.log.Warn("otp verification failed", "err", err, "phone", maskPhone(req.Phone), "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}

	// If 2FA or step-up is required, return pending envelope without cookies.
	if resp.Requires2FA || resp.RequiresStepUp {
		api.JSON(c.Writer, http.StatusOK, resp, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

type RegisterRequest struct {
	// Phone is accepted but is no longer sufficient on its own — see the
	// EMAIL_REQUIRED check in Register. SMS delivery does not exist, so a
	// phone-only account could never be verified or recovered.
	Phone    string `json:"phone"`
	Email    string `json:"email"`
	Password string `json:"password" binding:"required"`
	// Mandatory. See validatePersonName and the checks in Register: binding
	// alone would accept a string of spaces, so both are re-checked there.
	FirstName string `json:"first_name" binding:"required"`
	LastName  string `json:"last_name" binding:"required"`
	// DOB is MANDATORY (SR-6). It was optional, and the age check returned nil
	// when it was absent, so omitting the field bypassed the gate entirely.
	DOB    string `json:"dob"`
	Gender string `json:"gender"`
	// AcceptedTerms must be explicitly true. A bool defaulting to false means a
	// client that omits it is refused — a consent that defaults to granted is
	// not consent.
	AcceptedTerms bool `json:"accepted_terms"`
	// TermsVersion records WHICH text the user was shown, so a later audit can
	// answer that question from data rather than from a deployment timeline.
	TermsVersion string `json:"terms_version"`
}

// validatePersonName enforces what a human name may contain.
//
// Unicode letters, not ASCII: the product is India-first, so Devanagari,
// Telugu and Tamil names must pass exactly as Latin ones do. Spaces, hyphens
// and apostrophes are allowed for "O'Brien", "Devi Prasad", "Anne-Marie".
//
// unicode.IsMark is required, and its absence was a real defect. IsLetter
// covers Unicode category L only; Indic vowel signs (matras), viramas and
// nuktas are category M. Without IsMark this function accepted the consonant
// stems of an Indic name and rejected its vowels — "रघुवरन" failed on U+0941,
// "ரகுவரன்" on U+0BC1 — so only mark-free names such as "कमल" got through.
// Since first and last name became mandatory, that blocked most Devanagari,
// Telugu and Tamil speakers from registering under their real name at all.
//
// Digits are rejected deliberately. A name field accepting "user123" is how
// people end up putting handles in it, and the handle is a separate,
// uniqueness-checked concept. Widening to category M does not widen to
// symbols, digits or emoji — see TestValidatePersonName.
func validatePersonName(name string) error {
	const maxLen = 50
	if utf8.RuneCountInString(name) > maxLen {
		return fmt.Errorf("name must be %d characters or fewer", maxLen)
	}
	for _, r := range name {
		switch {
		case unicode.IsLetter(r), unicode.IsMark(r), unicode.IsSpace(r), r == '-', r == '\'':
			continue
		default:
			return errors.New("name may contain letters, spaces, hyphens and apostrophes only")
		}
	}
	return nil
}

// genderValues is the closed set a registration may record.
//
// Stored tokens, deliberately lowercase and stable. Display wording lives in
// the clients; changing "Others" to "Prefer to self-describe" must never
// change what is already in the database.
var genderValues = []string{"male", "female", "other"}

func allowedGenders() []string {
	out := make([]string, len(genderValues))
	copy(out, genderValues)
	return out
}

func isAllowedGender(v string) bool {
	for _, allowed := range genderValues {
		if v == allowed {
			return true
		}
	}
	return false
}

func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Validation failed: "+err.Error(), nil, nil)
		return
	}

	// SR-6: EMAIL is the launch identifier. Phone registration is retired
	// because no SMS delivery exists — a phone-only account could never
	// receive a verification or recovery code, so it could never be recovered.
	if req.Email == "" {
		api.Error(c.Writer, http.StatusBadRequest, "EMAIL_REQUIRED",
			"An email address is required. Phone registration is not available: "+
				"SMS delivery is not enabled, so a phone-only account could never "+
				"receive a verification or password-reset code.", nil, nil)
		return
	}

	if req.Password == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Password cannot be empty", nil, nil)
		return
	}

	// Real first and last names are MANDATORY.
	//
	// They were previously optional, which is why accounts exist with no name
	// at all and the app falls back to showing "anonymous". Validated here
	// rather than by binding tags alone, because `binding:"required"` accepts
	// a string of spaces.
	//
	// Distinct codes per field so the client can mark the offending input
	// instead of showing one message for both.
	req.FirstName = strings.TrimSpace(req.FirstName)
	req.LastName = strings.TrimSpace(req.LastName)

	if req.FirstName == "" {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "FIRST_NAME_REQUIRED",
			"First name is required.", nil, nil)
		return
	}
	if req.LastName == "" {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "LAST_NAME_REQUIRED",
			"Last name is required.", nil, nil)
		return
	}
	if err := validatePersonName(req.FirstName); err != nil {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "NAME_INVALID", err.Error(), nil, nil)
		return
	}
	if err := validatePersonName(req.LastName); err != nil {
		api.Error(c.Writer, http.StatusUnprocessableEntity, "NAME_INVALID", err.Error(), nil, nil)
		return
	}

	// Gender: expand/contract rollout.
	//
	// The column is `gender TEXT DEFAULT ''` with no enum and no CHECK, so
	// nothing below the API enforces anything. "Required" enforced only in one
	// app is not required at all — a second client, or a direct API call,
	// walks straight past it.
	//
	// EXPAND (flag off, the default): an absent gender is accepted, but a
	// supplied one must be valid. Clients can start sending the field before
	// anything depends on it, and existing clients that omit it keep working.
	//
	// CONTRACT (flag on): absence is rejected too.
	//
	// The split matters because promoting an optional field to required is a
	// breaking change for every client that predates it — including the
	// Flutter app, which sends no gender at all. Enforcing both halves at once
	// takes those clients down with no way back short of a redeploy.
	req.Gender = strings.ToLower(strings.TrimSpace(req.Gender))
	if req.Gender == "" {
		if h.flags.Enabled(c.Request.Context(), rollout.FlagRegisterRequireGender) {
			api.Error(c.Writer, http.StatusUnprocessableEntity, "GENDER_REQUIRED",
				"Gender is required.",
				map[string]any{"allowed": allowedGenders()}, nil)
			return
		}
	} else if !isAllowedGender(req.Gender) {
		// Enforced in BOTH phases. Accepting arbitrary text into a column that
		// will later be constrained just moves the problem to the backfill.
		api.Error(c.Writer, http.StatusUnprocessableEntity, "GENDER_INVALID",
			"Gender must be one of the supported values.",
			map[string]any{"allowed": allowedGenders()}, nil)
		return
	}

	resp, err := h.svc.RegisterWithConsent(c.Request.Context(), req.Phone, req.Email, req.Password,
		req.FirstName, req.LastName, req.DOB, req.Gender,
		service.RegistrationConsent{
			Accepted: req.AcceptedTerms,
			Version:  req.TermsVersion,
		})
	if err != nil {
		h.log.Error("registration failed", "err", err, "phone", maskPhone(req.Phone), "email", maskEmail(req.Email), "request_id", RequestIDFromContext(c))
		switch {
		case errors.Is(err, store.ErrUserExists):
			api.Error(c.Writer, http.StatusConflict, "USER_EXISTS", "User already exists", nil, nil)
		case errors.Is(err, service.ErrPasswordTooShort) || errors.Is(err, service.ErrPasswordTooWeak):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "WEAK_PASSWORD", err.Error(), nil, nil)
		// SR-6: the age and consent failures carry their own codes and the
		// real reason. A generic "Registration failed" would leave a user
		// retrying a form that can never succeed.
		case errors.Is(err, service.ErrUnderage):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "UNDERAGE", err.Error(), nil, nil)
		case errors.Is(err, service.ErrDOBRequired) || errors.Is(err, service.ErrDOBMalformed) ||
			errors.Is(err, service.ErrDOBInFuture):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "INVALID_DOB", err.Error(), nil, nil)
		case errors.Is(err, service.ErrConsentRequired) || errors.Is(err, service.ErrConsentVersionMismatch):
			api.Error(c.Writer, http.StatusUnprocessableEntity, "CONSENT_REQUIRED", err.Error(),
				map[string]any{"current_terms_version": service.CurrentTermsVersion}, nil)
		default:
			api.Error(c.Writer, http.StatusBadRequest, "REGISTRATION_FAILED", "Registration failed", nil, nil)
		}
		return
	}

	// LB-5: registration issues NO session, so there are no cookies to set.
	// The account is pending until the emailed code is verified — calling
	// setAuthCookies here would write empty credentials and, worse, imply the
	// client is signed in.
	api.JSON(c.Writer, http.StatusCreated, resp, nil)
}

type LoginRequest struct {
	Identifier string `json:"identifier"`
	Email      string `json:"email"`
	Phone      string `json:"phone"`
	Password   string `json:"password" binding:"required"`
	DeviceID   string `json:"device_id"`
	Platform   string `json:"platform"`
}

func (h *Handler) Login(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	identifier := req.Identifier
	if identifier == "" {
		if req.Email != "" {
			identifier = req.Email
		} else if req.Phone != "" {
			identifier = req.Phone
		}
	}

	if identifier == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "identifier, email, or phone is required", nil, nil)
		return
	}

	resp, err := h.svc.LoginWithPassword(c.Request.Context(), identifier, req.Password, req.DeviceID, req.Platform, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		// LB-5: a pending (unverified) account. Distinct from bad credentials
		// so the client can offer "resend verification" instead of showing
		// "wrong password" to someone whose password is correct.
		if errors.Is(err, service.ErrEmailNotVerified) {
			// CLB-3: the resumption path a user who closed the app needs.
			//
			// The password has already been checked, so this response is only
			// reachable by someone who owns the account. It carries a FRESH
			// verification transaction and no session at all — the client can
			// finish signing up with it, and it is useless for anything else.
			// Without this the pending state was a dead end: the credential
			// from registration was gone with the process, and both verify and
			// resend needed a session the account cannot have.
			details := map[string]any{
				"verify_via": "POST /v1/auth/verify-email",
				"resend_via": "POST /v1/auth/resend-verification",
			}
			if resp != nil && resp.VerificationToken != "" {
				details[VerificationTransactionField] = resp.VerificationToken
				if resp.VerificationExpiresAt != nil {
					details["verification_expires_at"] = *resp.VerificationExpiresAt
				}
			}
			api.Error(c.Writer, http.StatusForbidden, "EMAIL_NOT_VERIFIED", err.Error(), details, nil)
			return
		}
		// A13 — anomaly step-up required. The service has already
		// stashed the pending session + dispatched the email-OTP if
		// applicable. Render a 200 with the step-up envelope so the
		// UI can pivot without falling into the generic "auth failed"
		// banner. We deliberately don't use 401 here because the
		// password DID match — the request is now in a "halt for
		// second channel" state, semantically closer to 2FA than a
		// rejection.
		if errors.Is(err, service.ErrAnomalyStepUpRequired) && resp != nil {
			api.JSON(c.Writer, http.StatusOK, resp, nil)
			return
		}
		if errors.Is(err, service.ErrAnomalyStepUpUnavailable) {
			h.log.Warn("login refused: no anomaly step-up channel",
				"identifier", maskIdentifier(identifier), "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusUnauthorized, "STEP_UP_UNAVAILABLE",
				"This sign-in looks unusual and your account has no recovery channel set up. Please contact support.", nil, nil)
			return
		}
		h.log.Warn("login failed", "err", err, "identifier", maskIdentifier(identifier), "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}

	// If 2FA is required, return the pending response without setting auth cookies
	if resp.Requires2FA {
		api.JSON(c.Writer, http.StatusOK, resp, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func (h *Handler) Refresh(c *gin.Context) {
	// Try cookie first, then fall back to JSON body (for mobile clients)
	refreshToken, _ := c.Cookie(refreshTokenCookieName)
	if refreshToken == "" {
		var body struct {
			RefreshToken string `json:"refresh_token"`
		}
		if err := c.ShouldBindJSON(&body); err == nil && body.RefreshToken != "" {
			refreshToken = body.RefreshToken
		}
	}
	if refreshToken == "" {
		h.log.Warn("missing refresh token", "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing refresh token", nil, nil)
		return
	}

	resp, err := h.svc.RefreshSession(c.Request.Context(), refreshToken, c.ClientIP(), c.Request.UserAgent())
	if err != nil {
		h.log.Warn("refresh failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func (h *Handler) Logout(c *gin.Context) {
	refreshToken, _ := c.Cookie(refreshTokenCookieName)
	if err := h.svc.Logout(c.Request.Context(), refreshToken); err != nil {
		h.log.Error("logout failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	h.clearAuthCookies(c)
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// --- Protected endpoints ---

func (h *Handler) LogoutAll(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	count, err := h.svc.LogoutAll(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("logout-all failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok", "sessions_revoked": count}, nil)
}

func (h *Handler) ListSessions(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	sessions, err := h.svc.ListSessions(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("list sessions failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	// Scrub refresh token hashes from response
	type sessionResponse struct {
		ID        uuid.UUID `json:"id"`
		DeviceID  string    `json:"device_id"`
		Platform  string    `json:"platform"`
		IP        string    `json:"ip"`
		UserAgent string    `json:"user_agent"`
		CreatedAt time.Time `json:"created_at"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	result := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		result = append(result, sessionResponse{
			ID:        s.ID,
			DeviceID:  s.DeviceID,
			Platform:  s.Platform,
			IP:        s.IP,
			UserAgent: s.UserAgent,
			CreatedAt: s.CreatedAt,
			ExpiresAt: s.ExpiresAt,
		})
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) RevokeSessionByID(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	sessionID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid session ID", nil, nil)
		return
	}

	if err := h.svc.RevokeSessionByID(c.Request.Context(), userID, sessionID); err != nil {
		h.log.Warn("revoke session failed", "err", err, "user_id", userID, "session_id", sessionID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Session not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

// ListMyAnomalies renders the user-facing security inbox — every
// detection (new IP, new device, refresh-fingerprint mismatch) that
// fired against this user's account. Read-only; the inbox shows the
// last 20 by default.
func (h *Handler) ListMyAnomalies(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}
	anomalies, err := h.svc.ListMyAnomalies(c.Request.Context(), userID, 20)
	if err != nil {
		h.log.Error("list anomalies failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	if anomalies == nil {
		anomalies = []store.LoginAnomaly{}
	}
	api.JSON(c.Writer, http.StatusOK, anomalies, nil)
}

// AcknowledgeMyAnomaly clears an inbox entry. Used by the security UI
// after the user has reviewed an alert and confirmed it was them.
// Idempotent: already-acknowledged rows no-op.
func (h *Handler) AcknowledgeMyAnomaly(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}
	anomalyID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid anomaly ID", nil, nil)
		return
	}
	if err := h.svc.AcknowledgeMyAnomaly(c.Request.Context(), userID, anomalyID); err != nil {
		h.log.Warn("ack anomaly failed", "err", err, "user_id", userID, "anomaly_id", anomalyID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok"}, nil)
}

func (h *Handler) DeleteAccount(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	// LB-6: this endpoint makes NO mutation.
	//
	// It previously called DeleteAccount, which marked the account
	// `pending_deletion` and emitted `user.deletion_requested`. Nothing in
	// this repository purges a pending_deletion account, but that event does
	// reach other services — so some could erase their slice while the rest
	// of the data stays, producing PARTIAL IRREVERSIBLE erasure. That is
	// worse than either finishing the pipeline or not starting it.
	//
	// The user stays signed in: revoking sessions here would be a real,
	// user-visible effect on an endpoint that is meant to do nothing.
	h.log.Info("self-service deletion requested while disabled",
		"user_id", userID, "request_id", RequestIDFromContext(c))

	api.Error(c.Writer, http.StatusServiceUnavailable,
		DeletionUnavailableCode, DeletionUnavailableMessage,
		CurrentDeletionDetails(), nil)
}

// --- Password Reset ---

type ForgotPasswordRequest struct {
	Identifier string `json:"identifier" binding:"required"` // email or phone
}

func (h *Handler) ForgotPassword(c *gin.Context) {
	var req ForgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.ForgotPassword(c.Request.Context(), req.Identifier); err != nil {
		h.log.Error("forgot-password failed", "err", err, "identifier", maskIdentifier(req.Identifier), "request_id", RequestIDFromContext(c))
		// Always return 200 to prevent user enumeration
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "If the account exists, a reset code has been sent"}, nil)
}

type ResetPasswordRequest struct {
	Identifier  string `json:"identifier" binding:"required"`
	Code        string `json:"code" binding:"required"`
	NewPassword string `json:"new_password" binding:"required"`
}

func (h *Handler) ResetPassword(c *gin.Context) {
	var req ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.ResetPassword(c.Request.Context(), req.Identifier, req.Code, req.NewPassword); err != nil {
		h.log.Warn("reset-password failed", "err", err, "identifier", maskIdentifier(req.Identifier), "request_id", RequestIDFromContext(c))
		if errors.Is(err, service.ErrPasswordTooShort) || errors.Is(err, service.ErrPasswordTooWeak) {
			api.Error(c.Writer, http.StatusUnprocessableEntity, "WEAK_PASSWORD", err.Error(), nil, nil)
		} else {
			api.Error(c.Writer, http.StatusBadRequest, "RESET_FAILED", "Password reset failed", nil, nil)
		}
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "Password reset successfully"}, nil)
}

// --- Email/Phone Verification ---

// VerificationTransactionField is the request key carrying the server-issued
// verification credential. Named so tests can assert on the exact contract
// rather than a string literal that could drift.
const VerificationTransactionField = "verification_token"

type VerifyEmailRequest struct {
	// CLB-3: the account is named by this server-issued credential.
	//
	// There is deliberately NO user_id field. On a public route a
	// caller-supplied id would let anyone grind codes against any account they
	// can name, which is exactly the shape this endpoint must not have.
	VerificationToken string `json:"verification_token" binding:"required"`
	Code              string `json:"code" binding:"required"`
}

func (h *Handler) VerifyEmail(c *gin.Context) {
	var req VerifyEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VerifyEmailWithTransaction(c.Request.Context(), req.VerificationToken, req.Code); err != nil {
		// One response for every failure — invalid credential, wrong purpose,
		// expired, replayed, wrong code. Distinguishing them would let a
		// caller probe which credentials and accounts exist.
		h.log.Warn("email verification failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "VERIFY_FAILED", "Email verification failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "Email verified successfully"}, nil)
}

type VerifyPhoneRequest struct {
	Code string `json:"code" binding:"required"`
}

func (h *Handler) VerifyPhone(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	var req VerifyPhoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.VerifyPhone(c.Request.Context(), userID, req.Code); err != nil {
		h.log.Warn("phone verification failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "VERIFY_FAILED", "Phone verification failed", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "Phone verified successfully"}, nil)
}

type ResendVerificationRequest struct {
	Type string `json:"type" binding:"required"` // "email" or "phone"
	// CLB-3: as on VerifyEmailRequest, there is deliberately no user_id.
	VerificationToken string `json:"verification_token" binding:"required"`
}

func (h *Handler) ResendVerification(c *gin.Context) {
	var req ResendVerificationRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	// CLB-3: resend is the recovery path for a user whose first email never
	// arrived, so it must work with NO session — but it must still be scoped
	// to one account by a credential the server issued, not by a user id the
	// caller picked.
	switch req.Type {
	case "email":
		if err := h.svc.ResendVerificationWithTransaction(c.Request.Context(), req.VerificationToken); err != nil {
			// A quota denial is NOT folded into VERIFY_FAILED. The generic
			// error exists so an attacker cannot distinguish an invalid token
			// from a wrong code; a rate limit reveals nothing about the token
			// (you must already hold a valid one to be counted), and telling a
			// legitimate user "wait" rather than "that failed" is the
			// difference between waiting and retrying in a loop.
			if throttled, ok := service.AsThrottled(err); ok {
				retryAfter := int(throttled.RetryAfter.Seconds())
				if retryAfter < 1 {
					retryAfter = 1
				}
				c.Header("Retry-After", strconv.Itoa(retryAfter))
				api.Error(c.Writer, http.StatusTooManyRequests, "RATE_LIMITED",
					"Too many verification emails requested. Please wait before trying again.",
					map[string]any{"retry_after_seconds": retryAfter}, nil)
				return
			}
			h.log.Warn("resend email verification failed", "err", err, "request_id", RequestIDFromContext(c))
			api.Error(c.Writer, http.StatusBadRequest, "VERIFY_FAILED", "Could not resend verification", nil, nil)
			return
		}
	case "phone":
		// SR-6: no SMS delivery exists, so a phone code can never arrive.
		// Reporting success here would be a lie the user acts on.
		api.Error(c.Writer, http.StatusGone, "SMS_UNAVAILABLE",
			"Phone verification is unavailable; verify by email", nil, nil)
		return
	default:
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "type must be email or phone", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "Verification code sent"}, nil)
}

// --- Trusted Devices ---

func (h *Handler) ListTrustedDevices(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	devices, err := h.svc.ListTrustedDevices(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("list trusted devices failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, devices, nil)
}

func (h *Handler) RemoveTrustedDevice(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	deviceID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid device ID", nil, nil)
		return
	}

	if err := h.svc.RemoveTrustedDevice(c.Request.Context(), userID, deviceID); err != nil {
		h.log.Warn("remove trusted device failed", "err", err, "user_id", userID, "device_id", deviceID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Device not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok"}, nil)
}

type TrustDeviceRequest struct {
	Fingerprint string  `json:"fingerprint" binding:"required"`
	DeviceName  *string `json:"device_name"`
}

func (h *Handler) TrustDevice(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	var req TrustDeviceRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.TrustDevice(c.Request.Context(), userID, req.Fingerprint, req.DeviceName); err != nil {
		h.log.Error("trust device failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "ok", "message": "Device trusted"}, nil)
}

// ExportUserData returns all personal data held by the auth service for the
// requesting user as a downloadable JSON file (GDPR data portability).
func (h *Handler) ExportUserData(c *gin.Context) {
	userID := c.GetHeader("X-User-Id")
	if userID == "" {
		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": gin.H{"code": "UNAUTHORIZED"}})
		return
	}
	export, err := h.svc.ExportUserData(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("export user data failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": gin.H{"code": "INTERNAL_ERROR", "message": err.Error()}})
		return
	}
	c.Header("Content-Disposition", "attachment; filename=data-export.json")
	c.JSON(http.StatusOK, export)
}

// Docs Routes
func (h *Handler) RegisterDocsRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/auth")
	{
		v1.GET("/openapi.json", h.OpenAPISpec)
		v1.GET("/docs", h.ScalarDocs)
	}
}

func (h *Handler) OpenAPISpec(c *gin.Context) {
	c.File("./docs/openapi.json")
}

func (h *Handler) ScalarDocs(c *gin.Context) {
	html := `<!doctype html>
<html>
  <head>
    <title>Auth Service API</title>
    <meta charset="utf-8" />
    <meta
      name="viewport"
      content="width=device-width, initial-scale=1" />
  </head>
  <body>
    <script
      id="api-reference"
      data-url="./openapi.json"></script>
    <script src="https://cdn.jsdelivr.net/npm/@scalar/api-reference"></script>
  </body>
</html>`
	c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
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
