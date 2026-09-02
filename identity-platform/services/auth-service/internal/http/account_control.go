package http

// Account control — TikTok "Deactivate" and "Delete permanently".
//
// This replaces the LB-6 stance where DELETE /v1/auth/account answered a 503
// and mutated nothing. That stance existed because the only deletion path
// emitted user.deletion_requested at request time, whose consumers erase
// graph/post/user/dating slices immediately — so a "30-day window" was a lie
// and a partial, irreversible erasure was the likely outcome.
//
// The auth side of a real orchestrator now exists (internal/purge): deletion
// is a scheduled state that ONLY a purge worker escalates after the window,
// and credentials are anonymised ONLY after every required service has acked
// its erasure. Per-service purge consumers are a separate workstream; until
// they exist the worker keeps re-requesting and never completes a purge, which
// is the safe direction.

import (
	"errors"
	"net/http"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AccountControlRequest carries the password re-verification. A signed-in
// device is not sufficient authority to disable or delete an account.
type AccountControlRequest struct {
	Password string `json:"password" binding:"required"`
}

// bindAccountControl parses the user id from the auth middleware and the
// password from the body. Writes the error response itself on failure.
func (h *Handler) bindAccountControl(c *gin.Context) (uuid.UUID, string, bool) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return uuid.Nil, "", false
	}
	var req AccountControlRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Password == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"password is required to confirm this action", nil, nil)
		return uuid.Nil, "", false
	}
	return userID, req.Password, true
}

// writeAccountControlError maps the service sentinels. Everything else is a
// 500 with no detail.
func (h *Handler) writeAccountControlError(c *gin.Context, action string, userID uuid.UUID, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidPassword):
		h.log.Warn(action+" refused: password mismatch", "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusUnauthorized, "INVALID_PASSWORD",
			"The password you entered is incorrect", nil, nil)
	case errors.Is(err, service.ErrLifecycleConflict):
		h.log.Warn(action+" refused: account state conflict", "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusConflict, "ACCOUNT_STATE_CONFLICT", err.Error(), nil, nil)
	default:
		h.log.Error(action+" failed", "err", err, "user_id", userID, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	}
}

// DeactivateAccount — POST /v1/auth/account/deactivate.
//
// Reversible: the next successful password login reactivates. Every session
// is revoked, so the caller's own cookies are cleared in the response.
func (h *Handler) DeactivateAccount(c *gin.Context) {
	userID, password, ok := h.bindAccountControl(c)
	if !ok {
		return
	}
	if err := h.svc.DeactivateAccount(c.Request.Context(), userID, password); err != nil {
		h.writeAccountControlError(c, "deactivate", userID, err)
		return
	}
	h.clearAuthCookies(c)
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"account_status":           "deactivated",
		"sessions_revoked":         true,
		"reactivate_by_logging_in": true,
	}, nil)
}

// DeleteAccount — DELETE /v1/auth/account.
//
// Schedules a purge 30 days out. Nothing is erased now; a login inside the
// window cancels. Every session is revoked, so the caller's cookies are
// cleared in the response.
func (h *Handler) DeleteAccount(c *gin.Context) {
	userID, password, ok := h.bindAccountControl(c)
	if !ok {
		return
	}
	sched, err := h.svc.DeleteAccount(c.Request.Context(), userID, password)
	if err != nil {
		h.writeAccountControlError(c, "delete account", userID, err)
		return
	}
	h.clearAuthCookies(c)
	api.JSON(c.Writer, http.StatusOK, sched, nil)
}
