package http

import (
	"errors"
	"net/http"

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

	resp, err := h.svc.HandleOAuthCallback(c.Request.Context(), provider, code, state)
	if err != nil {
		h.log.Error("OAuth callback failed", "err", err, "provider", provider, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "OAuth authentication failed", nil, nil)
		return
	}

	// Set auth cookies and redirect to frontend
	h.setAuthCookies(c, resp.Tokens)
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

	resp, err := h.svc.HandleOAuthToken(c.Request.Context(), provider, req.Token)
	if err != nil {
		h.log.Error("OAuth token login failed", "err", err, "provider", provider, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "OAuth authentication failed", nil, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

func validateOAuthProvider(provider string) error {
	switch provider {
	case "google", "github", "apple":
		return nil
	default:
		return errors.New("unsupported provider: " + provider)
	}
}
