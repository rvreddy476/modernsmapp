// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — Feast). Same pattern as dating-service.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls food with a service token it signs
// for THIS call:
//
//	iss   admin-service
//	aud   food
//	scope [the one permission admin-service checked, e.g. food:orders.cancel]
//	act   the admin's user id
//	exp   60 s
//
// Food trusts none of that because of where the request came from. The
// internal key is no evidence (the gateway stamps it on edge traffic to
// /v1/food) and neither is X-User-Id (anyone holding the key can set one).
// The token is: only admin-service holds the private key, the scope names
// what it checked, and the actor is signed.
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

// Food admin permissions. Names that identity's catalogue already has
// (identity-platform/services/auth-service/internal/permissions) are reused;
// the ones marked NEW are not in the catalogue yet.
const (
	PermStatsRead          = "food:stats.read" // NEW
	PermRestaurantApprove  = "food:restaurant.approve"
	PermRestaurantSuspend  = "food:restaurant.suspend" // NEW
	PermRiderApprove       = "food:delivery_partner.approve"
	PermRiderSuspend       = "food:delivery_partner.suspend" // NEW
	PermDocumentsReview    = "food:documents.review"
	PermPayoutAccountsRead = "food:payout_accounts.read" // NEW
	PermOrdersRead         = "food:orders.read"
	PermOrdersCancel       = "food:orders.cancel"
	PermRefundIssue        = "food:refund.issue"
	PermRefundsRead        = "food:refunds.read"        // NEW
	PermSettlementRead     = "food:settlement.read"     // NEW
	PermSettlementGenerate = "food:settlement.generate" // NEW
	PermSettlementMarkPaid = "food:settlement.mark_paid"
	PermMenuModerate       = "food:menu.moderate"
	PermReviewsModerate    = "food:reviews.moderate"
	PermTicketsAct         = "food:tickets.act"
	PermCouponsManage      = "food:coupons.manage"       // NEW
	PermServiceAreasManage = "food:service_areas.manage" // NEW
	PermReportsRead        = "food:reports.read"         // NEW
	PermFraudRead          = "food:fraud.read"           // NEW
	PermAuditRead          = "food:audit.read"
)

// AdminPermissions lists every permission an admin-service token may carry
// to food. Deployment registers admin-service with exactly these ops.
// (food:kyc.reveal exists in the catalogue but food has no reveal route: the
// admin KYC view carries masked numbers and media ids only.)
var AdminPermissions = []string{
	PermStatsRead, PermRestaurantApprove, PermRestaurantSuspend, PermRiderApprove, PermRiderSuspend,
	PermDocumentsReview, PermPayoutAccountsRead, PermOrdersRead, PermOrdersCancel, PermRefundIssue,
	PermRefundsRead, PermSettlementRead, PermSettlementGenerate, PermSettlementMarkPaid, PermMenuModerate,
	PermReviewsModerate, PermTicketsAct, PermCouponsManage, PermServiceAreasManage, PermReportsRead,
	PermFraudRead, PermAuditRead,
}

// Error codes for the token path.
const (
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
)

const (
	ctxAdminTokenPerms = "food_admin_token_perms"
	ctxAdminActor      = "food_admin_actor"
	ctxServiceCaller   = "food_service_caller"
)

// InternalAdminPrefix is the token-only admin family. It is registered
// outside the internal-key group; the gateway refuses /internal/ from the edge.
const InternalAdminPrefix = "/v1/food/internal/admin"

// WithServiceAuth installs the service-token verifier (ServiceCallersFromEnv).
// nil means no token is accepted.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// tokenActor is the signed act claim of an admitted admin-service token.
// currentUserID and requireUser return it in place of X-User-Id, so every
// admin handler audits the real actor without reading a header.
func tokenActor(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxAdminActor)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// requireAdminToken admits ONLY an admin-service token (authorizeAdminToken).
// No token → 401, whatever else the request carries.
func (h *Handler) requireAdminToken(perms ...string) gin.HandlerFunc {
	if len(perms) == 0 {
		panic("food: requireAdminToken needs the route's permission")
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

// requireAdmin gates one LEGACY /v1/food/admin route. perms[0] is the route's
// permission; further perms are ones the handler narrows to per action.
//
// A request carrying a service token is judged ONLY by the token. Without
// one, today's rule is unchanged: X-User-Id plus an admin or superadmin
// scope (requireAdminScope). admin-service calls the token-only mirror.
func (h *Handler) requireAdmin(perms ...string) gin.HandlerFunc {
	if len(perms) == 0 {
		panic("food: requireAdmin needs the route's permission")
	}
	legacy := h.requireAdminScope()
	return func(c *gin.Context) {
		if rawServiceToken(c) != "" {
			if !h.authorizeAdminToken(c, perms) {
				c.Abort()
				return
			}
			c.Next()
			return
		}
		legacy(c)
	}
}

// authorizeAdminToken verifies the request's token for one of perms and, on
// success, records the admitted permissions and the signed actor.
//
// Refused (403 unless noted): no verifier configured (401); bad signature,
// unknown caller, wrong audience, expired or over-long tokens; a caller other
// than admin-service; a token whose scope holds none of perms; and a missing
// or malformed act claim. X-User-Id and X-Scopes riding on the same request
// are ignored — they never add or replace anything.
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
		slog.WarnContext(ctx, "food: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	if verified == nil {
		slog.WarnContext(ctx, "food: admin token lacks the route permission", "required", perms[0], "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
			"the token does not carry the permission this route requires", gin.H{"required": perms[0]})
		return false
	}
	if verified.Issuer != IssuerAdminService {
		slog.WarnContext(ctx, "food: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
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

// RestaurantStatusPermission is the permission one restaurant status change
// needs. Making a restaurant ACTIVE is an approval (and passes the FSSAI
// gate); every other status takes it off the platform, which is suspension.
func RestaurantStatusPermission(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "ACTIVE", "APPROVED":
		return PermRestaurantApprove
	}
	return PermRestaurantSuspend
}

// DeliveryPartnerStatusPermission is the same rule for riders: APPROVED or
// ACTIVE is an approval; anything else stops the rider working.
func DeliveryPartnerStatusPermission(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "ACTIVE", "APPROVED":
		return PermRiderApprove
	}
	return PermRiderSuspend
}

// registerAdminRoutes declares the admin console routes once, with the
// permission each needs, under whichever gate the caller passes: requireAdmin
// (token, or LEGACY X-User-Id + admin scope) or requireAdminToken (token only).
func (h *Handler) registerAdminRoutes(g *gin.RouterGroup, gate func(perms ...string) gin.HandlerFunc) {
	g.GET("/dashboard", gate(PermStatsRead), h.AdminDashboard)

	// Restaurants and riders.
	g.GET("/restaurants/pending", gate(PermRestaurantApprove), h.AdminPendingRestaurants)
	g.POST("/restaurants/:restaurantId/approve", gate(PermRestaurantApprove), h.AdminApproveRestaurant)
	g.POST("/restaurants/:restaurantId/reject", gate(PermRestaurantApprove), h.AdminRejectRestaurant)
	g.PATCH("/restaurants/:restaurantId/status", gate(PermRestaurantSuspend, PermRestaurantApprove), h.AdminSetRestaurantStatus)
	g.GET("/delivery-partners/pending", gate(PermRiderApprove), h.AdminPendingDeliveryPartners)
	g.POST("/delivery-partners/:partnerId/approve", gate(PermRiderApprove), h.AdminApproveDeliveryPartner)
	g.POST("/delivery-partners/:partnerId/reject", gate(PermRiderApprove), h.AdminRejectDeliveryPartner)
	g.PATCH("/delivery-partners/:partnerId/status", gate(PermRiderSuspend, PermRiderApprove), h.AdminSetDeliveryPartnerStatus)

	// Documents, KYC (masked view) and payout accounts (masked).
	g.POST("/restaurants/:restaurantId/documents/:docId/decide", gate(PermDocumentsReview), h.AdminDecideRestaurantDocument)
	g.GET("/delivery-partners/:partnerId/kyc", gate(PermDocumentsReview), h.AdminGetDeliveryPartnerKYC)
	g.POST("/delivery-partners/:partnerId/documents/:docId/decide", gate(PermDocumentsReview), h.AdminDecideDeliveryPartnerDocument)
	g.GET("/payout-accounts", gate(PermPayoutAccountsRead), h.AdminListPayoutAccounts)

	// Orders and refunds.
	g.GET("/orders", gate(PermOrdersRead), h.AdminListOrders)
	g.GET("/orders/:orderId", gate(PermOrdersRead), h.AdminGetOrder)
	g.POST("/orders/:orderId/cancel", gate(PermOrdersCancel), h.AdminCancelOrder)
	g.POST("/orders/:orderId/refund", gate(PermRefundIssue), h.AdminRefundOrder)
	g.GET("/refunds", gate(PermRefundsRead), h.AdminListRefunds)
	g.POST("/refunds/:refundId/decide", gate(PermRefundIssue), h.AdminDecideRefund)

	// Settlements.
	g.POST("/settlements/generate", gate(PermSettlementGenerate), h.AdminGenerateSettlements)
	g.GET("/settlements/restaurants", gate(PermSettlementRead), h.AdminListRestaurantSettlements)
	g.POST("/settlements/restaurants/:settlementId/mark-paid", gate(PermSettlementMarkPaid), h.AdminMarkRestaurantSettlementPaid)
	g.GET("/settlements/delivery-partners", gate(PermSettlementRead), h.AdminListDeliverySettlements)
	g.POST("/settlements/delivery-partners/:settlementId/mark-paid", gate(PermSettlementMarkPaid), h.AdminMarkDeliverySettlementPaid)
	g.POST("/settlements/files", gate(PermSettlementGenerate), h.AdminGenerateSettlementFile)
	g.GET("/settlements/files", gate(PermSettlementRead), h.AdminListSettlementFiles)
	g.GET("/settlements/files/:id/download", gate(PermSettlementRead), h.AdminDownloadSettlementFile)

	// Moderation and support.
	g.GET("/moderation/queue", gate(PermMenuModerate), h.AdminListPendingModeration)
	g.POST("/moderation/menu-items/:itemId", gate(PermMenuModerate), h.AdminModerateMenuItem)
	g.DELETE("/item-reviews/:reviewId", gate(PermReviewsModerate), h.AdminHideItemReview)
	g.GET("/support/tickets", gate(PermTicketsAct), h.AdminListTickets)
	g.POST("/support/tickets/:ticketId/status", gate(PermTicketsAct), h.AdminSetTicketStatus)

	// Catalogue of offers and where food delivers.
	g.GET("/coupons", gate(PermCouponsManage), h.AdminListCoupons)
	g.POST("/coupons", gate(PermCouponsManage), h.AdminCreateCoupon)
	g.PATCH("/coupons/:couponId", gate(PermCouponsManage), h.AdminUpdateCoupon)
	g.GET("/service-areas", gate(PermServiceAreasManage), h.AdminListServiceAreas)
	g.POST("/service-areas", gate(PermServiceAreasManage), h.AdminCreateServiceArea)
	g.PATCH("/service-areas/:areaId", gate(PermServiceAreasManage), h.AdminUpdateServiceArea)

	// Reports, fraud and audit.
	g.GET("/reports/restaurant-sla", gate(PermReportsRead), h.AdminRestaurantSLAReport)
	g.GET("/reports/delivery-sla", gate(PermReportsRead), h.AdminDeliverySLAReport)
	g.GET("/reports/payment-recon", gate(PermReportsRead), h.AdminPaymentReconReport)
	g.GET("/reports/refunds", gate(PermReportsRead), h.AdminRefundsReport)
	g.GET("/reports/coupon-abuse", gate(PermReportsRead), h.AdminCouponAbuseReport)
	g.GET("/reports/compliance", gate(PermReportsRead), h.AdminComplianceReport)
	g.GET("/reports/orders", gate(PermReportsRead), h.AdminOrderReport)
	g.GET("/reports/revenue", gate(PermReportsRead), h.AdminRevenueReport)
	g.GET("/fraud/top", gate(PermFraudRead), h.AdminTopFraudUsers)
	g.GET("/audit-logs", gate(PermAuditRead), h.AdminAuditLogs)
}

// GetAdminStats — GET /v1/food/internal/admin/stats (admin-service token,
// food:stats.read). Read-only dashboard counts; money in integer paise.
func (h *Handler) GetAdminStats(c *gin.Context) {
	stats, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "food: admin stats", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FOOD_ADMIN_STATS_FAILED", "stats unavailable", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, stats)
}
