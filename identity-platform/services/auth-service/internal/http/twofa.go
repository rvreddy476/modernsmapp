package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/identity-platform/shared/api"
)

type Setup2FARequest struct{}

func (h *Handler) Setup2FA(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	resp, err := h.svc.Setup2FA(c.Request.Context(), userID)
	if err != nil {
		h.log.Error("2FA setup failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "2FA_SETUP_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

type Verify2FASetupRequest struct {
	Code string `json:"code" binding:"required"`
}

func (h *Handler) Verify2FASetup(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	var req Verify2FASetupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.Verify2FASetup(c.Request.Context(), userID, req.Code); err != nil {
		h.log.Warn("2FA setup verification failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "2FA_VERIFY_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "2FA enabled successfully"}, nil)
}

type Disable2FARequest struct {
	Password string `json:"password" binding:"required"`
	Code     string `json:"code" binding:"required"`
}

func (h *Handler) Disable2FA(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return
	}

	var req Disable2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	if err := h.svc.Disable2FA(c.Request.Context(), userID, req.Password, req.Code); err != nil {
		h.log.Warn("2FA disable failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "2FA_DISABLE_FAILED", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "2FA disabled successfully"}, nil)
}

type Verify2FARequest struct {
	UserID       string `json:"user_id" binding:"required"`
	Code         string `json:"code" binding:"required"`
	PendingToken string `json:"pending_token" binding:"required"`
}

func (h *Handler) Verify2FA(c *gin.Context) {
	var req Verify2FARequest
	if err := c.ShouldBindJSON(&req); err != nil {
		h.log.Warn("invalid request payload", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	userID, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Invalid user ID", nil, nil)
		return
	}

	resp, err := h.svc.Verify2FA(c.Request.Context(), userID, req.Code, req.PendingToken)
	if err != nil {
		h.log.Warn("2FA verification failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "AUTH_FAILED", "2FA verification failed", nil, nil)
		return
	}

	h.setAuthCookies(c, resp.Tokens)
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}
