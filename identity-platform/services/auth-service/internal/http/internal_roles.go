package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

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
// The edge does not forward these routes at all: "/v1/auth/internal" is in
// api-gateway/pkg/routepolicy.ForbiddenPrefixes (with tests there), so the
// gateway refuses the prefix even for a moderator or admin whose token would
// otherwise pass its /internal/ admin gate. Services call this in-cluster,
// so it never needs to be proxied. (An earlier version of this comment
// described that hole as open; it was closed at the edge.)
//
// WHO THE CALLER IS
//
// The shared key proves the caller is inside the cluster, not WHICH service
// it is. The `service` field is self-declared. It is recorded as the audit
// actor, and it gates the timed role below, but that gate guards against a
// wiring bug in another service, not against a caller that holds the key and
// lies. Binding the caller's name needs a signed service token, which this
// module cannot import today (see RequireServiceCallerNoUser).
//
// WHAT THEY MAY DO
//
//   - The ecosystem roles (roles.Ecosystem()), permanently: expires_at must
//     NOT be sent (400 EXPIRES_AT_NOT_ALLOWED).
//   - The timed role premium (roles.Timed()), only from its owning service
//     (roles.TimedRoleGranter: monetization-service; anyone else gets 403
//     ROLE_NOT_GRANTABLE) and only with a future expires_at (400
//     EXPIRES_AT_REQUIRED when absent, 400 INVALID_EXPIRY when not in the
//     future). premium is NOT in Ecosystem(), so it can never be granted
//     without an expiry.
//
// Enforced in service.GrantEcosystemRole / RevokeEcosystemRole, not here, so
// it holds for every caller of the service layer.

type internalRoleRequest struct {
	// UserID is the account the role is granted to or revoked from.
	UserID string `json:"user_id" binding:"required"`
	// Role must be one of: seller, restaurant_owner, delivery_partner,
	// rider_partner, service_professional, or premium (timed; see ExpiresAt).
	Role string `json:"role" binding:"required"`
	// ExpiresAt (RFC 3339) is REQUIRED for a timed role (premium) on grant
	// and must be in the future; it replaces the expiry the role had, so an
	// extension or a shortening is the same call with a new time. It must be
	// absent for an ecosystem role. Ignored on revoke.
	ExpiresAt *time.Time `json:"expires_at"`
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
	case errors.Is(err, service.ErrTimedRoleWrongService):
		// Same code as any other refused role, so the shared client
		// classifies it as a permanent caller bug; the detail says why.
		api.Error(c.Writer, http.StatusForbidden, "ROLE_NOT_GRANTABLE", err.Error(),
			map[string]any{"grantable_roles": roles.Ecosystem(), "timed_role_owner": timedRoleOwners()}, nil)
	case errors.Is(err, service.ErrExpiryRequired):
		api.Error(c.Writer, http.StatusBadRequest, "EXPIRES_AT_REQUIRED", err.Error(), nil, nil)
	case errors.Is(err, service.ErrInvalidExpiry):
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_EXPIRY", err.Error(), nil, nil)
	case errors.Is(err, service.ErrExpiryNotAllowed):
		api.Error(c.Writer, http.StatusBadRequest, "EXPIRES_AT_NOT_ALLOWED", err.Error(), nil, nil)
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

// timedRoleOwners maps each timed role to the one service that may grant it.
func timedRoleOwners() map[string]string {
	out := map[string]string{}
	for _, r := range roles.Timed() {
		out[r] = roles.TimedRoleGranter(r)
	}
	return out
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
// auth.user_roles is keyed on (user_id, role, app) and the insert is ON
// CONFLICT DO NOTHING. An approval flow that retries, or a backfill run
// twice, must not fail.
//
// A timed role (premium) is different: the insert is an UPSERT that replaces
// expires_at, so re-granting with a later time extends the role and with an
// earlier one shortens it. A shortening revokes the user's sessions (they
// sign in again and the new token carries the shorter grant):
//
//	{"user_id":"<uuid>","role":"premium","service":"monetization-service",
//	 "reason":"pass PP-9 paid","expires_at":"2026-12-14T00:00:00Z"}
//
// The new role reaches the user's access token on its NEXT MINT. Refresh
// re-mints, so POST /v1/auth/refresh is enough — no re-login. An expired
// timed role never reaches a token: every role read filters on expires_at.
func (h *Handler) InternalGrantRole(c *gin.Context) {
	req, target, ok := h.bindInternalRole(c)
	if !ok {
		return
	}
	if err := h.svc.GrantEcosystemRole(c.Request.Context(), req.Service, target, req.Role, req.Reason, req.ExpiresAt); err != nil {
		h.writeInternalRoleErr(c, err)
		return
	}
	h.log.Info("service role granted", "service", req.Service, "user_id", target,
		"role", req.Role, "expires_at", req.ExpiresAt, "reason", detailFor(req), "request_id", RequestIDFromContext(c))
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
// premium may be revoked only by its owning service. Revoking a premium row
// that is still live revokes the user's sessions so no token keeps the scope;
// revoking one that has already lapsed does not.
//
// NOTE: for an ecosystem role, revoking does NOT invalidate access tokens
// already minted with the role. The scope disappears on the next mint (≤ ACCESS_TOKEN_TTL, 15 minutes
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
