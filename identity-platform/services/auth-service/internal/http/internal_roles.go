package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/identity-auth-service/internal/roles"
	"github.com/atpost/identity-auth-service/internal/service"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Service-to-service ecosystem role management.
//
// WHY THIS EXISTS SEPARATELY FROM /v1/auth/admin/roles
//
// The admin grant endpoint is guarded by authorizePrivileged: the caller must
// be a superadmin, identified by X-User-Id. A service approving a seller is
// not a superadmin and has no user to act as, so it could not use that path at
// all — which is why the four ecosystem roles lived as rows in four other
// services' databases and never reached the access token.
//
// WHAT GUARDS THESE ROUTES
//
// The route group is wrapped in RequireInternalServiceKey (see handler.go),
// the same mechanism and the same INTERNAL_SERVICE_KEY env var every other
// internal route in this service uses. The api-gateway strips
// X-Internal-Service-Key from every inbound request and injects its own
// (trustedIdentityHeaders + injectInternalKeyMiddleware in
// api-gateway/cmd/server/main.go), so a browser cannot present the key.
//
// That is NOT the same as "unreachable from a browser", and the difference is
// worth stating plainly rather than assuming. The gateway proxies /v1/auth,
// and its requireAdminForInternalPaths lets any request whose path contains
// /internal/ through when the USER's token carries admin, moderator or
// superadmin. So an ordinary user cannot reach these routes, but a moderator
// with a browser can — and could grant themselves `seller`. The correct fix is
// at the edge, not here (add "/v1/auth/internal" to
// api-gateway/pkg/routepolicy.ForbiddenPrefixes, exactly as payments is
// handled: services call this in-cluster, so it never needs to be proxied).
// That file belongs to another stream; this comment is the handoff.
//
// WHAT THEY MAY DO
//
// Only the four ecosystem roles. Enforced in service.GrantEcosystemRole, not
// here, so it holds for every caller of the service layer.

type internalRoleRequest struct {
	// UserID is the account the role is granted to or revoked from.
	UserID string `json:"user_id" binding:"required"`
	// Role must be one of: seller, restaurant_owner, delivery_partner,
	// rider_partner.
	Role string `json:"role" binding:"required"`
	// Service names the caller for the audit trail — "commerce-service",
	// "food-service", "rider-service". Required: the audit row records this as
	// the actor, and a blank one would be unattributable.
	//
	// It is self-declared, and honestly so: the shared internal key proves the
	// caller is inside the cluster, not which service it is. Treat the field
	// as attribution, not authentication.
	Service string `json:"service" binding:"required"`
	// Reason is optional free text recorded in the audit detail, e.g.
	// "seller application SA-1042 approved".
	Reason string `json:"reason"`
}

// writeInternalRoleErr maps the service-layer sentinels to HTTP.
func (h *Handler) writeInternalRoleErr(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrRoleNotGrantableByService):
		api.Error(c.Writer, http.StatusForbidden, "ROLE_NOT_GRANTABLE", err.Error(),
			map[string]any{"grantable_roles": roles.Ecosystem()}, nil)
	case errors.Is(err, service.ErrCallingServiceRequired):
		api.Error(c.Writer, http.StatusBadRequest, "SERVICE_REQUIRED", err.Error(), nil, nil)
	default:
		h.log.Error("internal role op failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil, nil)
	}
}

// bindInternalRole parses and validates the shared request body.
func (h *Handler) bindInternalRole(c *gin.Context) (internalRoleRequest, uuid.UUID, bool) {
	var req internalRoleRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST",
			"user_id, role and service are required", nil, nil)
		return req, uuid.Nil, false
	}
	target, err := uuid.Parse(strings.TrimSpace(req.UserID))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user_id", nil, nil)
		return req, uuid.Nil, false
	}
	return req, target, true
}

// detailFor builds the audit detail string, folding in an optional reason.
func detailFor(req internalRoleRequest) string {
	if r := strings.TrimSpace(req.Reason); r != "" {
		return r
	}
	return ""
}

// InternalGrantRole — POST /v1/auth/internal/roles
//
//	{"user_id":"<uuid>","role":"seller","service":"commerce-service",
//	 "reason":"seller application SA-1042 approved"}
//
//	200 {"status":"granted","user_id":"<uuid>","role":"seller"}
//
// IDEMPOTENT. Granting a role the user already holds is a 200, not an error —
// auth.user_roles is keyed on (user_id, role) and the insert is ON CONFLICT DO
// NOTHING. An approval flow that retries, or a backfill run twice, must not
// fail.
//
// The new role reaches the user's access token on its NEXT MINT. Refresh
// re-mints, so POST /v1/auth/refresh is enough — no re-login.
func (h *Handler) InternalGrantRole(c *gin.Context) {
	req, target, ok := h.bindInternalRole(c)
	if !ok {
		return
	}
	if err := h.svc.GrantEcosystemRole(c.Request.Context(), req.Service, target, req.Role, req.Reason); err != nil {
		h.writeInternalRoleErr(c, err)
		return
	}
	h.log.Info("ecosystem role granted", "service", req.Service, "user_id", target,
		"role", req.Role, "reason", detailFor(req), "request_id", RequestIDFromContext(c))
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"status": "granted", "user_id": target.String(), "role": req.Role,
	}, nil)
}

// InternalRevokeRole — DELETE /v1/auth/internal/roles
//
//	{"user_id":"<uuid>","role":"seller","service":"commerce-service",
//	 "reason":"seller suspended for policy violation"}
//
//	200 {"status":"revoked","user_id":"<uuid>","role":"seller"}
//
// IDEMPOTENT. Revoking a role the user does not hold is a 200.
//
// NOTE: revoking does NOT invalidate access tokens already minted with the
// role. The scope disappears on the next mint (≤ ACCESS_TOKEN_TTL, 15 minutes
// by default). A suspension that must take effect immediately needs the owning
// service to check its own state too — identity's role is the durable answer,
// not a real-time kill switch.
func (h *Handler) InternalRevokeRole(c *gin.Context) {
	req, target, ok := h.bindInternalRole(c)
	if !ok {
		return
	}
	if err := h.svc.RevokeEcosystemRole(c.Request.Context(), req.Service, target, req.Role, req.Reason); err != nil {
		h.writeInternalRoleErr(c, err)
		return
	}
	h.log.Info("ecosystem role revoked", "service", req.Service, "user_id", target,
		"role", req.Role, "reason", detailFor(req), "request_id", RequestIDFromContext(c))
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"status": "revoked", "user_id": target.String(), "role": req.Role,
	}, nil)
}

// InternalListRoles — GET /v1/auth/internal/roles/:userId
//
//	200 {"user_id":"<uuid>","roles":["seller"]}
//
// The read a service needs when it wants to know whether it should have a
// partner record at all — used by the backfill/reconciliation pass rather than
// by a request path. Returns the resolved, implication-expanded list, so a
// superadmin shows ["superadmin","admin","moderator"]. Always an array.
func (h *Handler) InternalListRoles(c *gin.Context) {
	target, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid user id", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"user_id": target.String(),
		"roles":   h.svc.ResolveRoles(c.Request.Context(), target),
	}, nil)
}
