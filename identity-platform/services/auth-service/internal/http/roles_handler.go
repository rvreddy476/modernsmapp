package http

import (
	"errors"
	"net/http"

	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type roleRequest struct {
	UserID string `json:"user_id" binding:"required"`
	Role   string `json:"role" binding:"required"`
}

// callerID extracts the verified caller's user id from the gateway/authMW-set
// X-User-Id header. Returns false (and writes 401) when absent/invalid.
func (h *Handler) callerID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

// writeRoleErr maps service errors to HTTP status codes.
func (h *Handler) writeRoleErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrNotSuperadmin):
		api.Error(c.Writer, http.StatusForbidden, "FORBIDDEN", "superadmin role required", nil, nil)
	case errors.Is(err, service.ErrMFARequired):
		api.Error(c.Writer, http.StatusForbidden, "MFA_REQUIRED", "enable two-factor auth to perform admin actions", nil, nil)
	default:
		h.log.Error("role op failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	}
}

// GrantRole — POST /v1/auth/admin/roles {user_id, role}. Superadmin only.
func (h *Handler) GrantRole(c *gin.Context) {
	actor, ok := h.callerID(c)
	if !ok {
		return
	}
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "user_id and role are required", nil, nil)
		return
	}
	target, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user_id", nil, nil)
		return
	}
	if err := h.svc.GrantRole(c.Request.Context(), actor, target, req.Role); err != nil {
		if err.Error() == "invalid role" {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid role (allowed: superadmin, admin, moderator)", nil, nil)
			return
		}
		h.writeRoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "granted", "user_id": req.UserID, "role": req.Role}, nil)
}

// RevokeRole — DELETE /v1/auth/admin/roles {user_id, role}. Superadmin only.
func (h *Handler) RevokeRole(c *gin.Context) {
	actor, ok := h.callerID(c)
	if !ok {
		return
	}
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "user_id and role are required", nil, nil)
		return
	}
	target, err := uuid.Parse(req.UserID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user_id", nil, nil)
		return
	}
	if err := h.svc.RevokeRole(c.Request.Context(), actor, target, req.Role); err != nil {
		h.writeRoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "revoked", "user_id": req.UserID, "role": req.Role}, nil)
}

// ListUserRoles — GET /v1/auth/admin/roles/:userId. Superadmin only.
func (h *Handler) ListUserRoles(c *gin.Context) {
	actor, ok := h.callerID(c)
	if !ok {
		return
	}
	target, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil, nil)
		return
	}
	roles, err := h.svc.ListUserRoles(c.Request.Context(), actor, target)
	if err != nil {
		h.writeRoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"user_id": target, "roles": roles}, nil)
}
