// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 1 — B4 trust & safety page). Same pattern as
// dating-service (internal/http/admin_token.go there).
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls trust-safety with a service token
// it signs for THIS call:
//
//	iss   admin-service
//	aud   trust_safety
//	scope [the one permission admin-service checked, e.g. trust_safety:reports.act]
//	act   the admin's user id
//	exp   60 s
//
// Trust-safety trusts none of that because of where the request came from.
// The internal key is no evidence (the gateway stamps it on edge traffic)
// and neither is an actor header (anyone holding the key can set one). The
// token is: only admin-service holds the private key, the scope names what
// it checked, and the actor is signed. The actor written to
// trust.admin_audit on this path is the token's act claim and nothing else.
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

// ServiceAuthHeader carries the service token.
const ServiceAuthHeader = "X-Service-Authorization"

// Trust & safety admin permissions, spelled as identity's catalogue spells
// them (identity-platform/services/auth-service/internal/permissions).
// Names marked "not in catalogue" are proposed and granted to nobody until
// identity adds them.
const (
	PermStatsRead          = "trust_safety:stats.read" // not in catalogue
	PermReportsRead        = "trust_safety:reports.read"
	PermReportsAct         = "trust_safety:reports.act"
	PermAppealsRead        = "trust_safety:appeals.read" // not in catalogue
	PermAppealsAct         = "trust_safety:appeals.act"
	PermGrievancesRead     = "trust_safety:grievances.read" // not in catalogue
	PermGrievancesAct      = "trust_safety:grievances.act"
	PermStrikesRead        = "trust_safety:strikes.read" // not in catalogue
	PermStrikesManage      = "trust_safety:strikes.manage"
	PermVerificationReview = "trust_safety:verification.review"  // not in catalogue
	PermMediaLabelsRead    = "trust_safety:media_labels.read"    // not in catalogue
	PermKeywordFiltersRead = "trust_safety:keyword_filters.read" // not in catalogue
	PermAuditRead          = "trust_safety:audit.read"
)

// AdminPermissions lists every permission an admin-service token may carry
// to trust-safety. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermStatsRead, PermReportsRead, PermReportsAct, PermAppealsRead, PermAppealsAct,
	PermGrievancesRead, PermGrievancesAct, PermStrikesRead, PermStrikesManage,
	PermVerificationReview, PermMediaLabelsRead, PermKeywordFiltersRead, PermAuditRead,
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
	ctxAdminTokenPerms = "trust_admin_token_perms"
	ctxAdminActor      = "trust_admin_actor"
)

// InternalAdminPrefix is the token-only admin family. It sits beside the
// other service-only routes (/v1/internal/keyword-filters,
// /v1/internal/grievances/dating-reports), but is registered OUTSIDE the
// internal-key middleware; the gateway refuses /internal/ from the edge.
const InternalAdminPrefix = "/v1/internal/admin/trust"

// WithServiceAuth installs the service-token verifier. nil means no token is
// accepted and every admin-family route answers 401.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// RegisterAdminTokenRoutes declares the token-only admin family. main MUST
// call it before installing the internal-key middleware on the engine, so
// these routes are judged by the token alone.
//
//	GET   /stats                     stats.read
//	GET   /reports                   reports.read
//	GET   /reports/:id               reports.read
//	PATCH /reports/:id               reports.act                     (audited)
//	GET   /appeals                   appeals.read | appeals.act
//	PATCH /appeals/:id               appeals.act                     (audited)
//	GET   /grievances                grievances.read | grievances.act (?status=, ?overdue=true)
//	GET   /grievances/:id            grievances.read | grievances.act
//	GET   /grievances/:id/history    grievances.read | grievances.act | audit.read
//	PATCH /grievances/:id            grievances.act                  (audited; officer hand-over)
//	GET   /strikes/:userId           strikes.read | strikes.manage
//	GET   /verification-requests     verification.review
//	GET   /media-labels/:mediaId     media_labels.read
//	GET   /keyword-filters           keyword_filters.read (platform scope by default)
//
// The first permission is the one named in a refusal. A read admits the
// matching act permission too, so a moderator (who holds the act names
// today) can work the queue before the read names exist in the catalogue.
func (h *Handler) RegisterAdminTokenRoutes(r gin.IRouter) {
	g := r.Group(InternalAdminPrefix)
	gate := h.requireAdminToken
	g.GET("/stats", gate(PermStatsRead), h.GetAdminStats)
	g.GET("/reports", gate(PermReportsRead), h.ListReports)
	g.GET("/reports/:id", gate(PermReportsRead), h.GetReport)
	g.PATCH("/reports/:id", gate(PermReportsAct), h.UpdateReport)
	g.GET("/appeals", gate(PermAppealsRead, PermAppealsAct), h.AdminListAppeals)
	g.PATCH("/appeals/:id", gate(PermAppealsAct), h.ReviewAppeal)
	g.GET("/grievances", gate(PermGrievancesRead, PermGrievancesAct), h.ListGrievances)
	g.GET("/grievances/:id", gate(PermGrievancesRead, PermGrievancesAct), h.GetGrievance)
	g.GET("/grievances/:id/history", gate(PermGrievancesRead, PermGrievancesAct, PermAuditRead), h.GetGrievanceHistory)
	g.PATCH("/grievances/:id", gate(PermGrievancesAct), h.UpdateGrievance)
	g.GET("/strikes/:userId", gate(PermStrikesRead, PermStrikesManage), h.GetUserStrikes)
	g.GET("/verification-requests", gate(PermVerificationReview), h.AdminListVerificationRequests)
	g.GET("/media-labels/:mediaId", gate(PermMediaLabelsRead), h.GetMediaLabels)
	g.GET("/keyword-filters", gate(PermKeywordFiltersRead), h.GetKeywordFilters)
}

// requireAdminToken admits ONLY an admin-service token (see
// authorizeAdminToken). No token → 401, whatever else the request carries.
func (h *Handler) requireAdminToken(perms ...string) gin.HandlerFunc {
	if len(perms) == 0 {
		panic("trust-safety: requireAdminToken needs the route's permission")
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
// or malformed act claim. Gateway identity headers, X-Scopes or the internal
// key riding on the same request are ignored — they never add or replace
// anything.
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
		slog.WarnContext(ctx, "trust-safety: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	if verified == nil {
		slog.WarnContext(ctx, "trust-safety: admin token lacks the route permission", "required", perms[0], "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
			"the token does not carry the permission this route requires", gin.H{"required": perms[0]})
		return false
	}
	if verified.Issuer != IssuerAdminService {
		slog.WarnContext(ctx, "trust-safety: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	actor, err := uuid.Parse(verified.Actor)
	if err != nil || actor == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired,
			"the token does not name the acting admin", nil)
		return false
	}
	c.Set(ctxAdminTokenPerms, granted)
	c.Set(ctxAdminActor, actor)
	return true
}

// tokenActor is the signed act claim of a request admitted by
// requireAdminToken; ok=false on every other path.
func tokenActor(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxAdminActor)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// viaAdminToken reports whether the request was admitted by an admin-service
// token. Only requireAdminToken sets the context it reads; the legacy routes
// never do, whatever headers they carry.
func viaAdminToken(c *gin.Context) bool {
	_, ok := tokenActor(c)
	return ok
}

// adminAllowed is the handlers' admin check: a token-admitted request has
// already passed its route permission; a LEGACY request still needs the
// gateway-set "admin" scope, exactly as before.
func adminAllowed(c *gin.Context) bool {
	if viaAdminToken(c) {
		return true
	}
	return hasScope(c.GetHeader("X-Scopes"), "admin")
}

// adminUserID is the acting admin for handlers that record a user id outside
// trust.admin_audit: the token's act on the token path, X-User-Id otherwise.
func adminUserID(c *gin.Context) (uuid.UUID, bool) {
	if id, ok := tokenActor(c); ok {
		return id, true
	}
	id, err := uuid.Parse(strings.TrimSpace(c.GetHeader("X-User-Id")))
	return id, err == nil && id != uuid.Nil
}

// GetAdminStats — GET /v1/internal/admin/trust/stats (admin-service token,
// trust_safety:stats.read). Read-only dashboard counts, no personal data.
func (h *Handler) GetAdminStats(c *gin.Context) {
	stats, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "trust-safety: admin stats failed", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", "failed to load stats", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}
