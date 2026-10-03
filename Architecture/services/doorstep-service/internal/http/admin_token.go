// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console). Copied from food-service internal/http/admin_token.go
// (token path only: Doorstep has no legacy X-User-Id admin family).
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls doorstep with a service token it
// signs for THIS call:
//
//	iss   admin-service
//	aud   doorstep
//	scope [the one permission admin-service checked, e.g. doorstep:catalogue.write]
//	act   the admin's user id
//	exp   60 s
//
// Doorstep trusts none of that because of where the request came from. The
// internal key is no evidence (the gateway stamps it on edge traffic to
// /v1/doorstep) and neither is X-User-Id (anyone holding the key can set
// one). The token is: only admin-service holds the private key, the scope
// names what it checked, and the actor is signed.
//
// The token-only family lives at /v1/doorstep/internal/admin/*, OUTSIDE the
// internal-key group, so the key neither helps nor is required there; the
// gateway refuses /internal/ from the edge.
package http

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/doorstep-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AudienceDoorstep is the audience doorstep-service accepts on service tokens.
const AudienceDoorstep = "doorstep"

// ServiceAuthHeader carries a caller's service token. The gateway never sets
// it, and Authorization stays free for the gateway's own use.
const ServiceAuthHeader = "X-Service-Authorization"

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Doorstep admin permissions (contracts/doorstep x-doorstep-permissions; the
// identity catalogue entries are lane L-D's).
const (
	PermCatalogueRead      = "doorstep:catalogue.read"
	PermCatalogueWrite     = "doorstep:catalogue.write"
	PermConfigWrite        = "doorstep:config.write"
	PermProsRead           = "doorstep:pros.read"
	PermProsApprove        = "doorstep:pros.approve"
	PermProsSuspend        = "doorstep:pros.suspend"
	PermDocumentsReview    = "doorstep:documents.review"
	PermBookingsRead       = "doorstep:bookings.read"
	PermBookingsCancel     = "doorstep:bookings.cancel"
	PermBookingsRedispatch = "doorstep:bookings.redispatch"
	PermRefundsIssue       = "doorstep:refunds.issue"
	PermIncidentsRead      = "doorstep:incidents.read"
	PermIncidentsAct       = "doorstep:incidents.act"
	PermTicketsAct         = "doorstep:tickets.act"
	PermRatingsModerate    = "doorstep:ratings.moderate"
	PermSettlementsRead    = "doorstep:settlements.read"
	PermStatsRead          = "doorstep:stats.read"
	PermAuditRead          = "doorstep:audit.read"
)

// AdminPermissions lists every permission an admin-service token may carry
// to doorstep. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermCatalogueRead, PermCatalogueWrite, PermConfigWrite, PermProsRead, PermProsApprove, PermProsSuspend,
	PermDocumentsReview, PermBookingsRead, PermBookingsCancel, PermBookingsRedispatch, PermRefundsIssue,
	PermIncidentsRead, PermIncidentsAct, PermTicketsAct, PermRatingsModerate, PermSettlementsRead, PermStatsRead,
	PermAuditRead,
}

// Error codes for the token path (same strings as food and rider).
const (
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
)

const (
	ctxAdminActor = "doorstep_admin_actor"
	ctxAdminPerm  = "doorstep_admin_perm"
)

// InternalAdminPrefix is the token-only admin family.
const InternalAdminPrefix = "/v1/doorstep/internal/admin"

// ServiceCallersFromEnv builds the service-token verifier (food/rider shape):
//
//	SERVICE_CALLERS=admin-service
//	SERVICE_CALLER_ADMIN_SERVICE_KID=a1
//	SERVICE_CALLER_ADMIN_SERVICE_PUBKEY=<base64 ed25519 public key>
//	SERVICE_CALLER_ADMIN_SERVICE_OPS=doorstep:catalogue.read,...
//
// A blank SERVICE_CALLERS returns (nil, nil): no token is accepted and the
// admin family answers 401. A named caller with a missing key or an empty
// operation list is a configuration error, never "allow everything".
func ServiceCallersFromEnv(getenv func(string) string) (*servicetoken.Verifier, error) {
	raw := strings.TrimSpace(getenv("SERVICE_CALLERS"))
	if raw == "" {
		return nil, nil
	}
	v := servicetoken.NewVerifier(AudienceDoorstep)
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		prefix := "SERVICE_CALLER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		kid := strings.TrimSpace(getenv(prefix + "_KID"))
		pub := strings.TrimSpace(getenv(prefix + "_PUBKEY"))
		var ops []string
		for _, p := range strings.Split(getenv(prefix+"_OPS"), ",") {
			if p = strings.TrimSpace(p); p != "" {
				ops = append(ops, p)
			}
		}
		if kid == "" || pub == "" {
			return nil, fmt.Errorf("caller %q is missing %s_KID or %s_PUBKEY", name, prefix, prefix)
		}
		if len(ops) == 0 {
			return nil, fmt.Errorf("caller %q must declare %s_OPS", name, prefix)
		}
		if err := v.RegisterBase64(name, kid, pub, ops, nil); err != nil {
			return nil, fmt.Errorf("caller %q: %w", name, err)
		}
	}
	if v.Callers() == 0 {
		return nil, fmt.Errorf("SERVICE_CALLERS produced no usable entries")
	}
	return v, nil
}

// WithServiceAuth installs the service-token verifier. nil accepts no token.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// requireAdminToken admits ONLY an admin-service token carrying perm.
//
// Refused: no token (401 ADMIN_SERVICE_TOKEN_REQUIRED); no verifier configured
// (401 SERVICE_CREDENTIAL_REQUIRED); bad signature, unknown caller, wrong
// audience, expired or over-long token, or a caller other than admin-service
// (403 SERVICE_TOKEN_REJECTED); a scope without perm (403
// ADMIN_PERMISSION_NOT_IN_TOKEN); a missing or malformed act claim (403
// ADMIN_ACTOR_REQUIRED). X-User-Id and X-Scopes on the same request are
// ignored.
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("doorstep: requireAdminToken needs the route's permission")
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if rawServiceToken(c) == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired, "an admin-service token is required", nil)
			c.Abort()
			return
		}
		if h.verifier == nil || h.verifier.Callers() == 0 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
				"service tokens are not accepted by this deployment", nil)
			c.Abort()
			return
		}
		verified, err := h.verifier.Verify(rawServiceToken(c), perm, "")
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			slog.WarnContext(ctx, "doorstep: admin token lacks the route permission", "required", perm, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
				"the token does not carry the permission this route requires", gin.H{"required": perm})
			c.Abort()
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "doorstep: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		if verified.Issuer != IssuerAdminService {
			slog.WarnContext(ctx, "doorstep: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		actor, err := uuid.Parse(verified.Actor)
		if err != nil || actor == uuid.Nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
			c.Abort()
			return
		}
		c.Set(ctxAdminActor, actor)
		c.Set(ctxAdminPerm, perm)
		c.Next()
	}
}

// adminActor is the audited actor of an admitted admin request.
func adminActor(c *gin.Context) store.Actor {
	a, _ := c.Get(ctxAdminActor)
	p, _ := c.Get(ctxAdminPerm)
	id, _ := a.(uuid.UUID)
	perm, _ := p.(string)
	return store.Actor{UserID: id, Permission: perm}
}

// adminRoute is one row of the admin route table.
type adminRoute struct {
	method, path, perm string
	handler            gin.HandlerFunc
}

// adminRoutes is the A1 admin-internal table: catalogue and config CRUD and
// the audit trail. Reads need catalogue.read; catalogue writes
// catalogue.write; cities, zones, slots, cancellation and commission
// config.write.
func (h *Handler) adminRoutes() []adminRoute {
	return []adminRoute{
		{http.MethodGet, "/cities", PermCatalogueRead, h.adminListCities},
		{http.MethodPost, "/cities", PermConfigWrite, h.adminCreateCity},
		{http.MethodPatch, "/cities/:code", PermConfigWrite, h.adminUpdateCity},
		{http.MethodGet, "/zones", PermCatalogueRead, h.adminListZones},
		{http.MethodPost, "/zones", PermConfigWrite, h.adminCreateZone},
		{http.MethodPatch, "/zones/:id", PermConfigWrite, h.adminUpdateZone},
		{http.MethodGet, "/categories", PermCatalogueRead, h.adminListCategories},
		{http.MethodPost, "/categories", PermCatalogueWrite, h.adminCreateCategory},
		{http.MethodPatch, "/categories/:id", PermCatalogueWrite, h.adminUpdateCategory},
		{http.MethodGet, "/skills", PermCatalogueRead, h.adminListSkills},
		{http.MethodPost, "/skills", PermCatalogueWrite, h.adminCreateSkill},
		{http.MethodGet, "/services", PermCatalogueRead, h.adminListServices},
		{http.MethodPost, "/services", PermCatalogueWrite, h.adminCreateService},
		{http.MethodGet, "/services/:id", PermCatalogueRead, h.adminGetService},
		{http.MethodPatch, "/services/:id", PermCatalogueWrite, h.adminUpdateService},
		{http.MethodPost, "/services/:id/options", PermCatalogueWrite, h.adminCreateOption},
		{http.MethodPatch, "/options/:id", PermCatalogueWrite, h.adminUpdateOption},
		{http.MethodPost, "/services/:id/addon-groups", PermCatalogueWrite, h.adminCreateAddonGroup},
		{http.MethodPatch, "/addon-groups/:id", PermCatalogueWrite, h.adminUpdateAddonGroup},
		{http.MethodPost, "/addon-groups/:id/addons", PermCatalogueWrite, h.adminCreateAddon},
		{http.MethodPatch, "/addons/:id", PermCatalogueWrite, h.adminUpdateAddon},
		{http.MethodGet, "/prices", PermCatalogueRead, h.adminListPrices},
		{http.MethodPost, "/prices", PermCatalogueWrite, h.adminCreatePrice},
		{http.MethodGet, "/rate-cards", PermCatalogueRead, h.adminListRateCards},
		{http.MethodPost, "/rate-cards", PermCatalogueWrite, h.adminCreateRateCard},
		{http.MethodPatch, "/rate-cards/:id", PermCatalogueWrite, h.adminUpdateRateCard},
		{http.MethodGet, "/slot-configs", PermCatalogueRead, h.adminListSlotConfigs},
		{http.MethodPost, "/slot-configs", PermConfigWrite, h.adminCreateSlotConfig},
		{http.MethodPatch, "/slot-configs/:id", PermConfigWrite, h.adminUpdateSlotConfig},
		{http.MethodGet, "/cancellation-rules", PermCatalogueRead, h.adminListCancellationRules},
		{http.MethodPost, "/cancellation-rules", PermConfigWrite, h.adminCreateCancellationRule},
		{http.MethodPatch, "/cancellation-rules/:id", PermConfigWrite, h.adminUpdateCancellationRule},
		{http.MethodGet, "/commission-rules", PermCatalogueRead, h.adminListCommissionRules},
		{http.MethodPost, "/commission-rules", PermConfigWrite, h.adminCreateCommissionRule},
		{http.MethodPatch, "/commission-rules/:id", PermConfigWrite, h.adminUpdateCommissionRule},
		{http.MethodGet, "/audit-logs", PermAuditRead, h.adminAuditLogs},
	}
}

// registerInternalAdminRoutes mounts the table on the engine root, outside
// the internal-key group.
func (h *Handler) registerInternalAdminRoutes(r *gin.Engine) {
	g := r.Group(InternalAdminPrefix)
	for _, rt := range h.adminRoutes() {
		g.Handle(rt.method, rt.path, h.requireAdminToken(rt.perm), rt.handler)
	}
}
