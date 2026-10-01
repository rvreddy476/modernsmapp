// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Live page). Same pattern as commerce-service
// internal/http/admin_token.go and payments/dating.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls live with a service token it
// signs for THIS call:
//
//	iss   admin-service
//	aud   live
//	scope [the permission(s) admin-service checked, e.g. live:streams.stop]
//	act   the admin's user id
//	exp   <= 5 min
//
// Live trusts none of that because of where the request came from. The
// internal key is no evidence (the gateway stamps it on edge traffic to
// /v1/livestream) and neither is X-User-Id. The token is: only
// admin-service holds the private key, the scope names what it checked, and
// the actor is signed. Live also writes its own append-only audit row
// (live_admin_audit) for every admin write, in the same transaction.
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

// ServiceAuthHeader carries a service token (same header as commerce,
// payments and dating).
const ServiceAuthHeader = "X-Service-Authorization"

// AudienceLive is the audience live accepts (admin-service LiveAudience).
const AudienceLive = "live"

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Live admin permissions, spelled as identity's catalogue spells them.
const (
	PermStreamsRead  = "live:streams.read"
	PermStreamsStop  = "live:streams.stop"
	PermReportsRead  = "live:reports.read"
	PermReportsAct   = "live:reports.act"
	PermChatModerate = "live:chat.moderate"
	PermUsersBan     = "live:users.ban"
)

// AdminPermissions lists every permission an admin-service token may carry
// to live. Deployment registers admin-service with exactly these ops
// (SERVICE_CALLER_ADMIN_SERVICE_OPS).
var AdminPermissions = []string{
	PermStreamsRead, PermStreamsStop, PermReportsRead, PermReportsAct, PermChatModerate, PermUsersBan,
}

// Error codes for the token path.
const (
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
)

const (
	ctxAdminActor = "live_admin_token_actor"
	ctxAdminRaw   = "live_admin_token_raw"
)

// InternalAdminPrefix is the token-only admin family. Outside the
// internal-key group (the token is the credential); the gateway refuses
// /internal/ from the edge.
const InternalAdminPrefix = "/v1/livestream/internal/admin"

// WithServiceVerifier installs the verifier for admin-service tokens (built
// by ServiceCallersFromEnv). nil means no token is accepted: every route in
// the token family answers 401.
func (h *Handler) WithServiceVerifier(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// tokenActor returns the signed actor of an admitted admin-service token.
func tokenActor(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxAdminActor)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// requireAdminToken admits ONLY an admin-service token for audience live
// whose scope holds perm and whose act is a valid user id.
//
// Refused: no token (401); no verifier configured (401); bad signature,
// unknown caller, wrong audience, expired or over-long tokens (403); a
// caller other than admin-service (403); a scope without perm (403); a
// missing or malformed act (403). On success the actor is act, and any
// X-User-Id on the request is removed so no handler can read a
// header-chosen actor.
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("live: requireAdminToken needs the route's permission")
	}
	return func(c *gin.Context) {
		raw := rawServiceToken(c)
		if !h.admitAdminToken(c, raw, perm) {
			c.Abort()
			return
		}
		c.Request.Header.Del("X-User-Id")
		c.Set(ctxAdminRaw, raw)
		c.Next()
	}
}

// admitAdminToken verifies raw for perm and stores the actor; on refusal it
// writes the error response and returns false.
func (h *Handler) admitAdminToken(c *gin.Context, raw, perm string) bool {
	ctx := c.Request.Context()
	if raw == "" {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired,
			"an admin-service token is required", nil)
		return false
	}
	if h.verifier == nil || h.verifier.Callers() == 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
			"service tokens are not accepted by this deployment", nil)
		return false
	}
	verified, err := h.verifier.Verify(raw, perm, "")
	if errors.Is(err, servicetoken.ErrScopeDenied) {
		slog.WarnContext(ctx, "live: admin token lacks the permission", "required", perm, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
			"the token does not carry the permission this action requires", gin.H{"required": perm})
		return false
	}
	if err != nil {
		slog.WarnContext(ctx, "live: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	if verified.Issuer != IssuerAdminService {
		slog.WarnContext(ctx, "live: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	actor, err := uuid.Parse(verified.Actor)
	if err != nil || actor == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired,
			"the token does not name the acting admin", nil)
		return false
	}
	if prev, ok := tokenActor(c); ok && prev != actor {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	c.Set(ctxAdminActor, actor)
	return true
}

// requireExtraPermission re-verifies the admitted token for a second
// permission an action needs on top of its route's (report resolve:
// ban_user needs live:users.ban, remove_message needs live:chat.moderate).
// Verify checks both the token's scope and admin-service's registered
// operations, exactly as for the route permission.
func (h *Handler) requireExtraPermission(c *gin.Context, perm string) bool {
	raw, _ := c.Get(ctxAdminRaw)
	s, _ := raw.(string)
	return h.admitAdminToken(c, s, perm)
}
