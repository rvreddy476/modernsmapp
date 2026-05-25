package http

import (
	"errors"
	"net/http"
	"net/url"

	"github.com/gin-gonic/gin"
	"github.com/atpost/identity-shared/api"
)

func (h *Handler) OAuthRedirect(c *gin.Context) {
	provider := c.Param("provider")
	if err := validateOAuthProvider(provider); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	url, err := h.svc.GetOAuthRedirectURL(c.Request.Context(), provider)
	if err != nil {
		h.log.Error("OAuth redirect failed", "err", err, "provider", provider, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to generate OAuth URL", nil, nil)
		return
	}

	c.Redirect(http.StatusTemporaryRedirect, url)
}

func (h *Handler) OAuthCallback(c *gin.Context) {
	provider := c.Param("provider")
	if err := validateOAuthProvider(provider); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	code := c.Query("code")
	state := c.Query("state")
	if code == "" || state == "" {
		errMsg := c.Query("error")
		if errMsg == "" {
			errMsg = "missing code or state parameter"
		}
		h.log.Warn("OAuth callback missing parameters", "provider", provider, "error", errMsg, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "OAUTH_FAILED", errMsg, nil, nil)
		return
	}

	result, err := h.svc.HandleOAuthCallback(c.Request.Context(), provider, code, state)
	if err != nil {
		h.log.Error("OAuth callback failed", "err", err, "provider", provider, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "OAuth authentication failed", nil, nil)
		return
	}

	// A5: when the OAuth provider didn't assert email_verified and
	// no local user exists, the service stalls the flow and returns
	// a pending-signup ticket. The browser-redirect path forwards
	// the user to a frontend "complete signup" page carrying the
	// opaque token + provider; the frontend then prompts for a
	// phone number and posts to /oauth/complete-signup.
	if result.Pending != nil {
		redirect := h.cfg.FrontendURL +
			"/auth/oauth/complete-signup?token=" + url.QueryEscape(result.Pending.PendingToken) +
			"&provider=" + url.QueryEscape(result.Pending.Provider) +
			"&email=" + url.QueryEscape(result.Pending.Email)
		c.Redirect(http.StatusTemporaryRedirect, redirect)
		return
	}

	// Set auth cookies and redirect to frontend
	h.setAuthCookies(c, result.Auth.Tokens)
	c.Redirect(http.StatusTemporaryRedirect, h.cfg.FrontendURL+"/auth/callback")
}

type OAuthTokenRequest struct {
	Token string `json:"token" binding:"required"`
}

func (h *Handler) OAuthToken(c *gin.Context) {
	provider := c.Param("provider")
	if err := validateOAuthProvider(provider); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	var req OAuthTokenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	result, err := h.svc.HandleOAuthToken(c.Request.Context(), provider, req.Token)
	if err != nil {
		h.log.Error("OAuth token login failed", "err", err, "provider", provider, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "OAuth authentication failed", nil, nil)
		return
	}

	// A5: mobile flow — return the pending-signup payload as JSON so
	// the app can pivot into its OTP-collection screen. 202 Accepted
	// distinguishes the half-complete state from a real 200 login.
	if result.Pending != nil {
		api.JSON(c.Writer, http.StatusAccepted, result.Pending, nil)
		return
	}

	h.setAuthCookies(c, result.Auth.Tokens)
	api.JSON(c.Writer, http.StatusOK, result.Auth, nil)
}

// OAuthCompleteSignupRequest is the body of POST /v1/auth/oauth/complete-signup.
// The caller has the opaque pending token from the OAuth callback
// (browser query string OR /oauth/:provider/token JSON response) and
// the phone number they want to attach to the new account.
type OAuthCompleteSignupRequest struct {
	PendingToken string `json:"pending_token" binding:"required"`
	Phone        string `json:"phone" binding:"required"`
}

// OAuthCompleteSignup accepts a pending-signup token + phone and
// dispatches an SMS OTP. Returns 200 with a generic message; the
// frontend then prompts the user for the code.
func (h *Handler) OAuthCompleteSignup(c *gin.Context) {
	var req OAuthCompleteSignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	if err := h.svc.CompleteOAuthSignup(c.Request.Context(), req.PendingToken, req.Phone); err != nil {
		h.log.Warn("oauth complete-signup failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "OAUTH_SIGNUP_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "otp sent"}, nil)
}

// OAuthVerifySignupRequest is the body of POST /v1/auth/oauth/verify-signup.
// The user has typed in the OTP they received; we verify it, materialise
// the account row with phone_verified=true / email_verified=false, and
// return a normal session.
type OAuthVerifySignupRequest struct {
	PendingToken string `json:"pending_token" binding:"required"`
	OTP          string `json:"otp" binding:"required"`
	DeviceID     string `json:"device_id"`
	Platform     string `json:"platform"`
}

// OAuthVerifySignup verifies the OTP, creates the account, links the
// OAuth identity, and issues a session.
func (h *Handler) OAuthVerifySignup(c *gin.Context) {
	var req OAuthVerifySignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	resp, err := h.svc.VerifyOAuthSignup(
		c.Request.Context(),
		req.PendingToken, req.OTP,
		req.DeviceID, req.Platform,
		c.ClientIP(), c.Request.UserAgent(),
	)
	if err != nil {
		h.log.Warn("oauth verify-signup failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "OAUTH_VERIFY_FAILED", err.Error(), nil, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusCreated, resp, nil)
}

func validateOAuthProvider(provider string) error {
	switch provider {
	case "google", "github", "apple":
		return nil
	default:
		return errors.New("unsupported provider: " + provider)
	}
}
