package http

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/permissions"
	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// roleRequest is the body of POST and DELETE /v1/auth/admin/roles.
//
//	{"user_id":"<uuid>","role":"moderator","app":"dating",
//	 "expires_at":"2026-12-31T00:00:00Z","reason":"dating pilot moderator"}
//
// app is optional (absent or "" = platform-wide); expires_at is optional and
// ignored on DELETE; reason is required.
type roleRequest struct {
	UserID    string     `json:"user_id" binding:"required"`
	Role      string     `json:"role" binding:"required"`
	App       string     `json:"app"`
	ExpiresAt *time.Time `json:"expires_at"`
	Reason    string     `json:"reason"`
}

// bindRoleRequest parses the body into a service request.
func (h *Handler) bindRoleRequest(c *gin.Context) (service.RoleChangeRequest, bool) {
	var req roleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"user_id, role and reason are required; expires_at must be RFC 3339", nil, nil)
		return service.RoleChangeRequest{}, false
	}
	target, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user_id", nil, nil)
		return service.RoleChangeRequest{}, false
	}
	return service.RoleChangeRequest{
		TargetID: target, Role: req.Role, App: req.App, ExpiresAt: req.ExpiresAt, Reason: req.Reason,
	}, true
}

// roleResponse echoes the applied grant.
func roleResponse(status string, req service.RoleChangeRequest) gin.H {
	var app any
	if a := strings.TrimSpace(req.App); a != "" {
		app = a
	}
	out := gin.H{"status": status, "user_id": req.TargetID.String(), "role": strings.TrimSpace(req.Role), "app": app}
	if status == "granted" {
		out["expires_at"] = req.ExpiresAt
	}
	return out
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
		api.Error(c.Writer, http.StatusForbidden, CodeMFARequired,
			"an admin session with two-factor authentication is required: enrol TOTP, then sign in with it or POST /v1/auth/step-up", nil, nil)
	case errors.Is(err, service.ErrStepUpRequired):
		api.Error(c.Writer, http.StatusForbidden, CodeStepUpRequired,
			"a fresh two-factor check is required: POST /v1/auth/step-up, then retry within 5 minutes", nil, nil)
	case errors.Is(err, service.ErrInvalidRole):
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"invalid role (allowed: "+strings.Join(roles.All(), ", ")+")",
			map[string]any{"allowed_roles": roles.All()}, nil)
	case errors.Is(err, service.ErrInvalidApp):
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_APP",
			"invalid app (allowed: "+strings.Join(permissions.Apps(), ", ")+")",
			map[string]any{"allowed_apps": permissions.Apps()}, nil)
	case errors.Is(err, service.ErrRoleNotScopable):
		api.Error(c.Writer, http.StatusBadRequest, "ROLE_NOT_SCOPABLE", err.Error(), nil, nil)
	case errors.Is(err, service.ErrReasonRequired):
		api.Error(c.Writer, http.StatusBadRequest, "REASON_REQUIRED", err.Error(), nil, nil)
	case errors.Is(err, service.ErrInvalidExpiry):
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_EXPIRY", err.Error(), nil, nil)
	case errors.Is(err, service.ErrSelfGrant):
		api.Error(c.Writer, http.StatusForbidden, "SELF_GRANT_REFUSED", err.Error(), nil, nil)
	case errors.Is(err, service.ErrLastSuperadmin):
		api.Error(c.Writer, http.StatusConflict, "LAST_SUPERADMIN", err.Error(), nil, nil)
	case errors.Is(err, service.ErrEnvBootstrapRole):
		api.Error(c.Writer, http.StatusConflict, "ENV_BOOTSTRAP_ROLE", err.Error(), nil, nil)
	default:
		h.log.Error("role op failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	}
}

// GrantRole — POST /v1/auth/admin/roles. Superadmin only. See roleRequest.
func (h *Handler) GrantRole(c *gin.Context) {
	actor, ok := h.callerID(c)
	if !ok {
		return
	}
	req, ok := h.bindRoleRequest(c)
	if !ok {
		return
	}
	if err := h.svc.GrantRole(c.Request.Context(), actor, req); err != nil {
		h.writeRoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, roleResponse("granted", req), nil)
}

// RevokeRole — DELETE /v1/auth/admin/roles. Superadmin only. See roleRequest.
func (h *Handler) RevokeRole(c *gin.Context) {
	actor, ok := h.callerID(c)
	if !ok {
		return
	}
	req, ok := h.bindRoleRequest(c)
	if !ok {
		return
	}
	if err := h.svc.RevokeRole(c.Request.Context(), actor, req); err != nil {
		h.writeRoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, roleResponse("revoked", req), nil)
}

// ListAdminAudit — GET /v1/auth/admin/audit?limit=N. Superadmin only.
func (h *Handler) ListAdminAudit(c *gin.Context) {
	actor, ok := h.callerID(c)
	if !ok {
		return
	}
	limit := 0
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	entries, err := h.svc.ListAdminAudit(c.Request.Context(), actor, limit)
	if err != nil {
		h.writeRoleErr(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"entries": entries}, nil)
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
