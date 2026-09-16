// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — MStore). Same pattern as dating-service.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls commerce with a service token it
// signs for THIS call:
//
//	iss   admin-service
//	aud   commerce
//	scope [the one permission admin-service checked, e.g. commerce:seller.approve]
//	act   the admin's user id
//	exp   <= 5 min (admin-service uses 60 s)
//
// Commerce trusts none of that because of where the request came from. The
// internal key is no evidence (the gateway stamps it on edge traffic to
// /v1/commerce) and neither is X-User-Id (anyone holding the key can set it).
// The token is: only admin-service holds the private key, the scope names what
// it checked, and the actor is signed.
//
// The existing key routes under /v1/commerce/internal/* are unchanged; this
// family mirrors the admin ones under /v1/commerce/internal/admin/*, where
// only a token is accepted.
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

// ServiceAuthHeader carries a service token (same header as payments and dating).
const ServiceAuthHeader = "X-Service-Authorization"

// AudienceCommerce is the audience commerce accepts.
const AudienceCommerce = "commerce"

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Commerce admin permissions, spelled as identity's catalogue spells them
// (identity-platform/services/auth-service/internal/permissions). The ones
// marked NEW are not in the catalogue yet.
const (
	PermStatsRead        = "commerce:stats.read"   // NEW
	PermSellersRead      = "commerce:sellers.read" // NEW — moderators need the queue without approve
	PermSellerApprove    = "commerce:seller.approve"
	PermSellerSuspend    = "commerce:seller.suspend"
	PermProductsModerate = "commerce:products.moderate"
	PermKYCVerify        = "commerce:kyc.verify"
	PermPayoutsRead      = "commerce:payouts.read"
	PermCODSettle        = "commerce:cod.settle"
	PermCatalogueEdit    = "commerce:catalogue.edit"
	PermBannersEdit      = "commerce:banners.edit"     // NEW
	PermJobsRead         = "commerce:jobs.read"        // NEW
	PermComplianceRead   = "commerce:compliance.read"  // NEW
	PermComplianceSweep  = "commerce:compliance.sweep" // NEW
)

// AdminPermissions lists every permission an admin-service token may carry
// to commerce. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermStatsRead, PermSellersRead, PermSellerApprove, PermSellerSuspend, PermProductsModerate,
	PermKYCVerify, PermPayoutsRead, PermCODSettle, PermCatalogueEdit, PermBannersEdit,
	PermJobsRead, PermComplianceRead, PermComplianceSweep,
}

// Error codes for the token path.
const (
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
)

// ctxAdminActor holds the signed act of an admitted admin-service token.
const ctxAdminActor = "commerce_admin_token_actor"

// InternalAdminPrefix is the token-only admin family. Outside the internal-key
// group and exempt from the gateway-trust key (the token is the credential);
// the gateway refuses /internal/ from the edge.
const InternalAdminPrefix = "/v1/commerce/internal/admin"

// WithServiceVerifier installs the verifier for admin-service tokens (built by
// ServiceCallersFromEnv). nil means no token is accepted: every route in the
// token family answers 401.
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

// requireAdminToken admits ONLY an admin-service token for audience commerce
// whose scope holds perm and whose act is a valid user id.
//
// Refused: no token (401); no verifier configured (401); bad signature,
// unknown caller, wrong audience, expired or over-long tokens (403); a caller
// other than admin-service (403); a scope without perm (403); a missing or
// malformed act (403). On success the actor is act, and any X-User-Id on the
// request is removed so no handler can read a header-chosen actor.
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("commerce: requireAdminToken needs the route's permission")
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		raw := rawServiceToken(c)
		if raw == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired,
				"an admin-service token is required", nil)
			c.Abort()
			return
		}
		if h.verifier == nil || h.verifier.Callers() == 0 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
				"service tokens are not accepted by this deployment", nil)
			c.Abort()
			return
		}
		verified, err := h.verifier.Verify(raw, perm, "")
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			slog.WarnContext(ctx, "commerce: admin token lacks the route permission", "required", perm, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
				"the token does not carry the permission this route requires", gin.H{"required": perm})
			c.Abort()
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "commerce: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		if verified.Issuer != IssuerAdminService {
			slog.WarnContext(ctx, "commerce: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		actor, err := uuid.Parse(verified.Actor)
		if err != nil || actor == uuid.Nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired,
				"the token does not name the acting admin", nil)
			c.Abort()
			return
		}
		c.Request.Header.Del("X-User-Id")
		c.Set(ctxAdminActor, actor)
		c.Next()
	}
}

// registerAdminTokenRoutes mounts the token-only admin family. Handlers are
// the ones the key routes use; the actor they record is the token's act.
//
//	GET    /stats                                    commerce:stats.read
//	GET    /sellers/queue, /sellers/:sellerId        commerce:sellers.read
//	POST   /sellers/:sellerId/approve|reject|request-changes   commerce:seller.approve
//	POST   /sellers/:sellerId/suspend|unsuspend      commerce:seller.suspend
//	POST   /sellers/:sellerId/kyc/verify             commerce:kyc.verify
//	GET    /products/queue, /products/:productId/submissions   commerce:products.moderate
//	POST   /products/:productId/approve|reject|request-changes commerce:products.moderate
//	GET    /payouts/pending                          commerce:payouts.read
//	POST   /cod-remittances/:remittanceId/settle     commerce:cod.settle
//	*      catalogue authoring (attribute definitions, enum values, category
//	       bindings, categories, schema state and publish)   commerce:catalogue.edit
//	GET/POST/PUT/DELETE /banners                     commerce:banners.edit
//	GET    /jobs/dead-letter                         commerce:jobs.read
//	GET    /compliance-gaps                          commerce:compliance.read
//	POST   /compliance-gaps/sweep                    commerce:compliance.sweep
func (h *Handler) registerAdminTokenRoutes(r *gin.Engine) {
	g := r.Group(InternalAdminPrefix)
	gate := h.requireAdminToken

	g.GET("/stats", gate(PermStatsRead), h.AdminGetStats)

	g.GET("/sellers/queue", gate(PermSellersRead), h.AdminListSellerQueue)
	g.GET("/sellers/:sellerId", gate(PermSellersRead), h.AdminGetSeller)
	g.POST("/sellers/:sellerId/approve", gate(PermSellerApprove), h.AdminApproveSeller)
	g.POST("/sellers/:sellerId/reject", gate(PermSellerApprove), h.AdminRejectSeller)
	g.POST("/sellers/:sellerId/request-changes", gate(PermSellerApprove), h.AdminRequestSellerChanges)
	g.POST("/sellers/:sellerId/suspend", gate(PermSellerSuspend), h.AdminSuspendSeller)
	g.POST("/sellers/:sellerId/unsuspend", gate(PermSellerSuspend), h.AdminUnsuspendSeller)
	g.POST("/sellers/:sellerId/kyc/verify", gate(PermKYCVerify), h.AdminVerifySellerKYC)

	g.GET("/products/queue", gate(PermProductsModerate), h.AdminListProductQueue)
	g.GET("/products/:productId/submissions", gate(PermProductsModerate), h.AdminProductSubmissions)
	g.POST("/products/:productId/approve", gate(PermProductsModerate), h.AdminApproveProduct)
	g.POST("/products/:productId/reject", gate(PermProductsModerate), h.AdminRejectProduct)
	g.POST("/products/:productId/request-changes", gate(PermProductsModerate), h.AdminRequestProductChanges)

	g.GET("/payouts/pending", gate(PermPayoutsRead), h.AdminListPendingPayouts)
	g.POST("/cod-remittances/:remittanceId/settle", gate(PermCODSettle), h.AdminSettleCODRemittance)

	g.GET("/attribute-definitions", gate(PermCatalogueEdit), h.AdminListAttributeDefinitions)
	g.POST("/attribute-definitions", gate(PermCatalogueEdit), h.AdminCreateAttributeDefinition)
	g.GET("/attribute-definitions/:defId", gate(PermCatalogueEdit), h.AdminGetAttributeDefinition)
	g.PATCH("/attribute-definitions/:defId", gate(PermCatalogueEdit), h.AdminPatchAttributeDefinition)
	g.GET("/attribute-definitions/:defId/impact", gate(PermCatalogueEdit), h.AdminAttributeDefinitionImpact)
	g.GET("/attribute-definitions/:defId/enum-values", gate(PermCatalogueEdit), h.AdminListEnumValues)
	g.POST("/attribute-definitions/:defId/enum-values", gate(PermCatalogueEdit), h.AdminCreateEnumValue)
	g.PUT("/attribute-definitions/:defId/enum-values/order", gate(PermCatalogueEdit), h.AdminReorderEnumValues)
	g.PATCH("/attribute-definitions/:defId/enum-values/:valueId", gate(PermCatalogueEdit), h.AdminPatchEnumValue)
	g.GET("/categories/:categoryId/attributes", gate(PermCatalogueEdit), h.AdminGetCategoryAttributes)
	g.PUT("/categories/:categoryId/attributes", gate(PermCatalogueEdit), h.AdminPutCategoryAttributes)
	g.POST("/categories", gate(PermCatalogueEdit), h.AdminCreateCategory)
	g.PATCH("/categories/:categoryId", gate(PermCatalogueEdit), h.AdminPatchCategory)
	g.GET("/attribute-schema", gate(PermCatalogueEdit), h.AdminAttributeSchemaState)
	g.POST("/attribute-schema/publish", gate(PermCatalogueEdit), h.AdminPublishAttributeSchema)

	g.GET("/banners", gate(PermBannersEdit), h.AdminListBanners)
	g.POST("/banners", gate(PermBannersEdit), h.AdminSaveBanner)
	g.PUT("/banners/:bannerId", gate(PermBannersEdit), h.AdminSaveBannerByID)
	g.DELETE("/banners/:bannerId", gate(PermBannersEdit), h.AdminDeleteBanner)

	g.GET("/jobs/dead-letter", gate(PermJobsRead), h.AdminListDeadLetterJobs)
	g.GET("/compliance-gaps", gate(PermComplianceRead), h.AdminComplianceGaps)
	g.POST("/compliance-gaps/sweep", gate(PermComplianceSweep), h.AdminSweepComplianceGaps)
}

// AdminGetStats — GET /v1/commerce/internal/admin/stats (admin-service token,
// commerce:stats.read). Read-only dashboard counts.
func (h *Handler) AdminGetStats(c *gin.Context) {
	stats, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "commerce: admin stats", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "QUERY_FAILED", "stats unavailable", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}
