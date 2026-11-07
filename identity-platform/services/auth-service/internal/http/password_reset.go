// Password reset routes: forgot-password (with its bounded work timeout) and
// reset-password. Pure move out of handler.go; behaviour is unchanged.

package http

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
)

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

	// M4 / L1 (login audit): the answer is always the same 200, and now so
	// is the TIME it takes. For a real account the work is a bcrypt hash of
	// the code plus a mail-provider call; for an unknown one it was nothing,
	// so the response time said whether the account existed. The work runs
	// after the response, on a context detached from the request (so the
	// client hanging up does not cancel the send) with its own deadline.
	identifier, requestID := req.Identifier, RequestIDFromContext(c)
	workCtx := context.WithoutCancel(c.Request.Context())
	go func() {
		ctx, cancel := context.WithTimeout(workCtx, forgotPasswordWorkTimeout)
		defer cancel()
		if err := h.svc.ForgotPassword(ctx, identifier); err != nil {
			h.log.Error("forgot-password failed", "err", err, "identifier", maskIdentifier(identifier), "request_id", requestID)
		}
	}()

	api.JSON(c.Writer, http.StatusOK, map[string]string{"message": "If the account exists, a reset code has been sent"}, nil)
}

// forgotPasswordWorkTimeout bounds the detached reset-code send.
const forgotPasswordWorkTimeout = 30 * time.Second

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
