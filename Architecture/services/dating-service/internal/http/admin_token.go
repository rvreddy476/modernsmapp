// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — Dating).
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls dating with a service token it
// signs for THIS call:
//
//	iss   admin-service
//	aud   dating
//	scope [the one permission admin-service checked, e.g. dating:reports.act]
//	act   the admin's user id
//	exp   60 s
//
// Dating trusts none of that because of where the request came from. The
// internal key is no evidence (the gateway stamps it on edge traffic to
// /v1/dating) and neither is an actor header (anyone holding the key can
// set one). The token is: only admin-service holds the private key, the
// scope names what it checked, and the actor is signed.
package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Dating admin permissions, spelled as identity's catalogue spells them
// (identity-platform/services/auth-service/internal/permissions).
const (
	PermStatsRead    = "dating:stats.read"
	PermReportsRead  = "dating:reports.read"
	PermReportsAct   = "dating:reports.act"
	PermUsersBan     = "dating:users.ban"
	PermPhotosReview = "dating:photos.review"
	PermSelfieReview = "dating:selfie.review"
	PermPanicRead    = "dating:panic.read"
	PermPanicAct     = "dating:panic.act"
	PermPanicReveal  = "dating:panic.reveal"
	PermRiskRead     = "dating:risk.read"
	PermAuditRead    = "dating:audit.read"
)

// AdminPermissions lists every permission an admin-service token may carry
// to dating. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermStatsRead, PermReportsRead, PermReportsAct, PermUsersBan, PermPhotosReview,
	PermSelfieReview, PermPanicRead, PermPanicAct, PermPanicReveal, PermRiskRead, PermAuditRead,
}

// Error codes for the token path.
const (
	CodeAdminTokenRequired   = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired   = "ADMIN_ACTOR_REQUIRED"
)

const ctxAdminTokenPerms = "dating_admin_token_perms"

// The token-only admin family. Outside the internal-key group, like the
// other /internal routes; the gateway refuses /internal/ from the edge.
const InternalAdminPrefix = "/v1/dating/internal/admin"

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// requireAdminToken admits ONLY an admin-service token (see authorizeAdminToken).
// No token → 401, whatever else the request carries.
func (h *Handler) requireAdminToken(perms ...string) gin.HandlerFunc {
	if len(perms) == 0 {
		panic("dating: requireAdminToken needs the route's permission")
	}
	return func(c *gin.Context) {
		if rawServiceToken(c) == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired,
				"an admin-service token is required", nil)
			c.Abort()
			return
		}
		if !h.authorizeAdminToken(c, perms) {
			c.Abort()
			return
		}
		c.Next()
	}
}

// authorizeAdminToken verifies the request's token for one of perms and, on
// success, records the admitted permissions and the signed actor.
//
// Refused (403 unless noted): no verifier configured (401); bad signature,
// unknown caller, wrong audience, expired or over-long tokens; a caller other
// than admin-service; a token whose scope holds none of perms; and a missing
// or malformed act claim. A gateway identity, scopes or X-Admin-Id riding on
// the same request are ignored — they never add or replace anything.
func (h *Handler) authorizeAdminToken(c *gin.Context, perms []string) bool {
	ctx := c.Request.Context()
	if h.verifier == nil || h.verifier.Callers() == 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
			"service tokens are not accepted by this deployment", nil)
		return false
	}
	raw := rawServiceToken(c)
	granted := map[string]bool{}
	var verified *servicetoken.Verified
	for _, perm := range perms {
		v, err := h.verifier.Verify(raw, perm, "")
		if err == nil {
			granted[perm] = true
			verified = v
			continue
		}
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			continue
		}
		// Anything but a scope miss is about the token itself; no other
		// permission can rescue it.
		slog.WarnContext(ctx, "dating: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	if verified == nil {
		slog.WarnContext(ctx, "dating: admin token lacks the route permission", "required", perms[0], "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
			"the token does not carry the permission this route requires", gin.H{"required": perms[0]})
		return false
	}
	if verified.Issuer != IssuerAdminService {
		slog.WarnContext(ctx, "dating: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	actor, err := uuid.Parse(verified.Actor)
	if err != nil || actor == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired,
			"the token does not name the acting admin", nil)
		return false
	}
	c.Set(ctxServiceCaller, verified.Issuer)
	c.Set(ctxAdminTokenPerms, granted)
	c.Set(ctxAdminActor, actor)
	return true
}

// adminMay reports whether the admitted caller may take an action that needs
// perm, answering 403 when not. A token caller may only do what its signed
// scope names; the LEGACY scopes path has no per-permission detail and keeps
// today's all-or-nothing admin reach.
func adminMay(c *gin.Context, perm string) bool {
	v, ok := c.Get(ctxAdminTokenPerms)
	if !ok {
		return true
	}
	if granted, _ := v.(map[string]bool); granted[perm] {
		return true
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
		"the token does not carry the permission this action requires", gin.H{"required": perm})
	return false
}
