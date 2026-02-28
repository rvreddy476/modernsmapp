package http

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/identity-platform/auth-service/internal/config"
	"github.com/identity-platform/auth-service/internal/store"
	"github.com/identity-platform/auth-service/internal/service"
	"github.com/identity-platform/shared/api"
)

const (
	accessTokenCookieName  = "access_token"
	refreshTokenCookieName = "refresh_token"
	csrfCookieName         = "csrf_token"
)

type Handler struct {
	svc AuthService
	cfg *config.Config
	log *slog.Logger
}

type AuthService interface {
	RequestOTP(ctx context.Context, phone, purpose string) error
	VerifyOTP(ctx context.Context, phone, code, purpose, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	RegisterWithPassword(ctx context.Context, phone, email, password, firstName, lastName, dob, gender string) (*service.AuthResponse, error)
	LoginWithPassword(ctx context.Context, identifier, password, deviceID, platform, ip, userAgent string) (*service.AuthResponse, error)
	RefreshSession(ctx context.Context, refreshToken, ip, userAgent string) (*service.AuthResponse, error)
	Logout(ctx context.Context, refreshToken string) error
	LogoutAll(ctx context.Context, userID uuid.UUID) (int64, error)
	ListSessions(ctx context.Context, userID uuid.UUID) ([]store.Session, error)
	RevokeSessionByID(ctx context.Context, userID, sessionID uuid.UUID) error
	DeleteAccount(ctx context.Context, userID uuid.UUID) error
	// 2FA
	Setup2FA(ctx context.Context, userID uuid.UUID) (*service.TwoFASetupResponse, error)
	Verify2FASetup(ctx context.Context, userID uuid.UUID, code string) error
	Disable2FA(ctx context.Context, userID uuid.UUID, password, code string) error
	Verify2FA(ctx context.Context, userID uuid.UUID, code, pendingToken string) (*service.AuthResponse, error)
	// OAuth
	GetOAuthRedirectURL(ctx context.Context, provider string) (string, error)
	HandleOAuthCallback(ctx context.Context, provider, code, state string) (*service.AuthResponse, error)
	HandleOAuthToken(ctx context.Context, provider, accessToken string) (*service.AuthResponse, error)
}

func New(svc AuthService, cfg *config.Config, logger *slog.Logger) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return &Handler{svc: svc, cfg: cfg, log: logger}
}

func (h *Handler) RegisterRoutes(r *gin.Engine, authMW, csrfMW gin.HandlerFunc) {
	v1 := r.Group("/v1/auth")
	{
		// Public routes
		v1.POST("/request-otp", h.RequestOTP)
		v1.POST("/verify-otp", h.VerifyOTP)
		v1.POST("/register", h.Register)
		v1.POST("/login", h.Login)
		v1.POST("/refresh", h.Refresh)
		v1.POST("/logout", h.Logout)
		v1.GET("/health", h.Health)

		// 2FA public route (called after login returns requires_2fa)
		v1.POST("/2fa/verify", h.Verify2FA)

		// OAuth routes (public)
		v1.GET("/oauth/:provider", h.OAuthRedirect)
		v1.GET("/oauth/:provider/callback", h.OAuthCallback)
		v1.POST("/oauth/:provider/token", h.OAuthToken)

		// Protected routes (require auth + CSRF)
		protected := v1.Group("", authMW, csrfMW)
		{
			protected.POST("/logout-all", h.LogoutAll)
			protected.GET("/sessions", h.ListSessions)
			protected.DELETE("/sessions/:id", h.RevokeSessionByID)
			protected.DELETE("/account", h.DeleteAccount)

			// 2FA management (protected)
			protected.POST("/2fa/setup", h.Setup2FA)
			protected.POST("/2fa/verify-setup", h.Verify2FASetup)
			protected.POST("/2fa/disable", h.Disable2FA)
		}
	}
}

func (h *Handler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
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
		h.log.Warn("otp verification failed", "err", err, "phone", maskPhone(req.Phone), "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "Authentication failed", nil, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

type RegisterRequest struct {
	Phone     string `json:"phone"`
	Email     string `json:"email"`
	Password  string `json:"password" binding:"required"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	DOB       string `json:"dob"`
	Gender    string `json:"gender"`
}

func (h *Handler) Register(c *gin.Context) {
	var req RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Validation failed: "+err.Error(), nil, nil)
		return
	}

	if req.Phone == "" && req.Email == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Either phone or email must be provided", nil, nil)
		return
	}

	if req.Password == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Password cannot be empty", nil, nil)
		return
	}

	resp, err := h.svc.RegisterWithPassword(c.Request.Context(), req.Phone, req.Email, req.Password, req.FirstName, req.LastName, req.DOB, req.Gender)
	if err != nil {
		h.log.Error("registration failed", "err", err, "phone", maskPhone(req.Phone), "email", maskEmail(req.Email), "request_id", RequestIDFromContext(c))
		if errors.Is(err, store.ErrUserExists) {
			api.Error(c.Writer, http.StatusConflict, "USER_EXISTS", "User already exists", nil, nil)
		} else {
			api.Error(c.Writer, http.StatusBadRequest, "REGISTRATION_FAILED", "Registration failed", nil, nil)
		}
		return
	}

	h.setAuthCookies(c, resp.Tokens)
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
	refreshToken, err := c.Cookie(refreshTokenCookieName)
	if err != nil || refreshToken == "" {
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
		ID        uuid.UUID  `json:"id"`
		DeviceID  string     `json:"device_id"`
		Platform  string     `json:"platform"`
		IP        string     `json:"ip"`
		UserAgent string     `json:"user_agent"`
		CreatedAt time.Time  `json:"created_at"`
		ExpiresAt time.Time  `json:"expires_at"`
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

func (h *Handler) DeleteAccount(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	if err := h.svc.DeleteAccount(c.Request.Context(), userID); err != nil {
		h.log.Error("delete account failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
		return
	}

	h.clearAuthCookies(c)
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": "ok", "message": "Account scheduled for deletion in 30 days"}, nil)
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
	case "login", "register":
		return nil
	default:
		return errors.New("purpose must be login or register")
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
