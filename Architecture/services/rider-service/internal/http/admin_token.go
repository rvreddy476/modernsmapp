// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — Mopedu). Same pattern as food-service.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls rider with a service token it signs
// for THIS call:
//
//	iss   admin-service
//	aud   rider
//	scope [the one permission admin-service checked, e.g. rider:rides.cancel]
//	act   the admin's user id
//	exp   60 s
//
// Rider trusts none of that because of where the request came from. The
// internal key is no evidence (the gateway stamps it on edge traffic to
// /v1/rider) and neither is X-User-Id (anyone holding the key can set one).
// The token is: only admin-service holds the private key, the scope names
// what it checked, and the actor is signed.
//
// The token-only family lives at /v1/rider/internal/admin/*, OUTSIDE the
// internal-key group, so the key neither helps nor is required there. The
// LEGACY /v1/rider/admin/* family is unchanged: X-User-Id + admin scope
// behind the internal key, for the gateway.
package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Rider admin permissions. Names that identity's catalogue already has
// (identity-platform/services/auth-service/internal/permissions, AppRider)
// are reused; the ones marked NEW are not in the catalogue yet.
//
// rider:kyc.reveal exists in the catalogue but rider has no reveal route:
// the document queue (rider:documents.review) is what a reviewer sees, and
// admin-service treats it as a step-up read because a document row carries
// the document number.
const (
	PermStatsRead       = "rider:stats.read"    // NEW
	PermPartnersRead    = "rider:partners.read" // NEW
	PermPartnersApprove = "rider:partners.approve"
	PermPartnersSuspend = "rider:partners.suspend" // NEW: suspend and block
	PermDocumentsReview = "rider:documents.review"
	PermVehiclesReview  = "rider:vehicles.review" // NEW
	PermPaymentsRead    = "rider:payments.read"   // NEW
	PermPaymentsSettle  = "rider:payments.settle" // verify a subscription payment
	PermPaymentsReject  = "rider:payments.reject" // NEW
	PermRidesRead       = "rider:rides.read"
	PermRidesCancel     = "rider:rides.cancel"     // NEW
	PermRatingsModerate = "rider:ratings.moderate" // NEW
	PermComplaintsAct   = "rider:complaints.act"
	PermIncidentsRead   = "rider:incidents.read"
	PermIncidentsAct    = "rider:incidents.act" // NEW: acknowledge and resolve
	PermIncidentsReveal = "rider:incidents.reveal"
	PermCitiesManage    = "rider:cities.manage" // NEW: cities and zones
	PermFaresManage     = "rider:fares.manage"
	PermReportsRead     = "rider:reports.read" // NEW
	PermAuditRead       = "rider:audit.read"
)

// AdminPermissions lists every permission an admin-service token may carry
// to rider. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermStatsRead, PermPartnersRead, PermPartnersApprove, PermPartnersSuspend,
	PermDocumentsReview, PermVehiclesReview, PermPaymentsRead, PermPaymentsSettle, PermPaymentsReject,
	PermRidesRead, PermRidesCancel, PermRatingsModerate, PermComplaintsAct,
	PermIncidentsRead, PermIncidentsAct, PermIncidentsReveal, PermCitiesManage, PermFaresManage,
	PermReportsRead, PermAuditRead,
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
	ctxAdminTokenPerm = "rider_admin_token_perm"
	ctxServiceCaller  = "rider_service_caller"
)

// InternalAdminPrefix is the token-only admin family. It is registered on
// the engine root, outside the internal-key group; the gateway refuses
// /internal/ from the edge.
const InternalAdminPrefix = "/v1/rider/internal/admin"

// identityHeaders are the gateway-stamped headers the token path ignores.
// They are dropped from the request on admission so no handler can read
// one by mistake: on this path the actor is the signed act claim, nothing
// else.
var identityHeaders = []string{"X-User-Id", "X-User-ID", middleware.ScopesHeader, middleware.AdminRoleHeader, "X-Internal-Service-Key"}

// WithServiceAuth installs the service-token verifier (ServiceCallersFromEnv).
// nil means no token is accepted.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

// WithAuditWriter replaces the audit sink for both admin families (tests;
// production writes rider_admin_audit_logs through the store).
func (h *Handler) WithAuditWriter(w middleware.AuditWriter) *Handler {
	h.audit = w
	return h
}

func (h *Handler) auditWriter() middleware.AuditWriter {
	if h.audit != nil {
		return h.audit
	}
	return h.svc.Store()
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// requireAdminToken admits ONLY an admin-service token carrying perm. On
// success the signed act claim becomes the admin actor
// (middleware.AdminUserKey), which every admin handler and the audit
// middleware already read; the gateway identity headers are dropped.
// No token → 401, whatever else the request carries.
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("rider: requireAdminToken needs the route's permission")
	}
	return func(c *gin.Context) {
		if rawServiceToken(c) == "" {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired,
				"an admin-service token is required", nil)
			c.Abort()
			return
		}
		if !h.authorizeAdminToken(c, perm) {
			c.Abort()
			return
		}
		c.Next()
	}
}

// authorizeAdminToken verifies the request's token for perm and, on success,
// records the admitted permission and the signed actor.
//
// Refused (403 unless noted): no verifier configured (401); bad signature,
// unknown caller, wrong audience, expired or over-long tokens; a caller other
// than admin-service; a token whose scope lacks perm; and a missing or
// malformed act claim. X-User-Id and X-Scopes riding on the same request
// are ignored — they never add or replace anything.
func (h *Handler) authorizeAdminToken(c *gin.Context, perm string) bool {
	ctx := c.Request.Context()
	if h.verifier == nil || h.verifier.Callers() == 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
			"service tokens are not accepted by this deployment", nil)
		return false
	}
	verified, err := h.verifier.Verify(rawServiceToken(c), perm, "")
	if err != nil {
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			slog.WarnContext(ctx, "rider: admin token lacks the route permission", "required", perm, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
				"the token does not carry the permission this route requires", gin.H{"required": perm})
			return false
		}
		slog.WarnContext(ctx, "rider: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	if verified.Issuer != IssuerAdminService {
		slog.WarnContext(ctx, "rider: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	actor, err := uuid.Parse(verified.Actor)
	if err != nil || actor == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired,
			"the token does not name the acting admin", nil)
		return false
	}
	for _, name := range identityHeaders {
		c.Request.Header.Del(name)
	}
	c.Set(ctxServiceCaller, verified.Issuer)
	c.Set(ctxAdminTokenPerm, perm)
	c.Set(middleware.AdminUserKey, actor)
	return true
}

// adminRoute is one admin console operation: the same handler serves both
// families, the permission gates only the token-only one.
type adminRoute struct {
	method, path, perm string
	handler            gin.HandlerFunc
}

// adminRoutes is the admin console route table. The LEGACY family registers
// it as-is under AdminGuard + AuditAdmin; the token-only family gates each
// entry with requireAdminToken(perm) and then audits it.
func (h *Handler) adminRoutes() []adminRoute {
	get, post, patch := http.MethodGet, http.MethodPost, http.MethodPatch
	return []adminRoute{
		{get, "/dashboard", PermStatsRead, h.AdminDashboard},

		// Partners: approve/reject is one severity, suspend/block another.
		{get, "/partners", PermPartnersRead, h.AdminListPartners},
		{get, "/partners/:id", PermPartnersRead, h.AdminGetPartner},
		{post, "/partners/:id/approve", PermPartnersApprove, h.AdminApprovePartner},
		{post, "/partners/:id/reject", PermPartnersApprove, h.AdminRejectPartner},
		{post, "/partners/:id/suspend", PermPartnersSuspend, h.AdminSuspendPartner},
		{post, "/partners/:id/block", PermPartnersSuspend, h.AdminBlockPartner},

		{get, "/documents", PermDocumentsReview, h.AdminListDocuments},
		{post, "/documents/:id/verify", PermDocumentsReview, h.AdminVerifyDocument},
		{post, "/documents/:id/reject", PermDocumentsReview, h.AdminRejectDocument},

		{get, "/vehicles", PermVehiclesReview, h.AdminListVehicles},
		{post, "/vehicles/:id/verify", PermVehiclesReview, h.AdminVerifyVehicle},
		{post, "/vehicles/:id/reject", PermVehiclesReview, h.AdminRejectVehicle},

		// Subscription payments: verify settles money, reject does not.
		{get, "/payments", PermPaymentsRead, h.AdminListPayments},
		{post, "/payments/:id/verify", PermPaymentsSettle, h.AdminVerifyPayment},
		{post, "/payments/:id/reject", PermPaymentsReject, h.AdminRejectPayment},

		{get, "/rides", PermRidesRead, h.AdminListRides},
		{get, "/rides/live", PermRidesRead, h.AdminListLiveRides},
		// Contact alerts carry trusted contacts' phone numbers: a reveal.
		{get, "/safety/incidents/:id/alerts", PermIncidentsReveal, h.AdminListSafetyContactAlerts},
		{post, "/rides/:id/rating/visibility", PermRatingsModerate, h.AdminHideRideRating},
		{get, "/reports/matching-health", PermReportsRead, h.AdminMatchingHealthReport},
		{get, "/reports/partner-quality", PermReportsRead, h.AdminPartnerQualityReport},
		{get, "/reports/supply-demand", PermReportsRead, h.AdminSupplyDemandReport},
		{get, "/reports/safety", PermReportsRead, h.AdminSafetyIncidentReport},
		{get, "/reports/compliance", PermReportsRead, h.AdminPartnerComplianceReport},
		{post, "/rides/:id/cancel", PermRidesCancel, h.AdminCancelRide},

		{get, "/complaints", PermComplaintsAct, h.AdminListComplaints},
		{post, "/complaints/:id/update-status", PermComplaintsAct, h.AdminUpdateComplaint},

		{get, "/safety-incidents", PermIncidentsRead, h.AdminListSafetyIncidents},
		{post, "/safety-incidents/:id/acknowledge", PermIncidentsAct, h.AdminAcknowledgeIncident},
		{post, "/safety-incidents/:id/resolve", PermIncidentsAct, h.AdminResolveIncident},

		{post, "/cities", PermCitiesManage, h.AdminCreateCity},
		{patch, "/cities/:id", PermCitiesManage, h.AdminUpdateCity},
		{post, "/zones", PermCitiesManage, h.AdminCreateZone},
		{patch, "/zones/:id", PermCitiesManage, h.AdminUpdateZone},
		{post, "/fare-rules", PermFaresManage, h.AdminCreateFareRule},
		{patch, "/fare-rules/:id", PermFaresManage, h.AdminUpdateFareRule},

		{get, "/audit-logs", PermAuditRead, h.AdminListAuditLogs},

		// S4 reports.
		{get, "/reports/revenue", PermReportsRead, h.AdminRevenueReport},
		{get, "/reports/cohort-retention", PermReportsRead, h.AdminCohortRetention},
		{get, "/reports/customer-cohort", PermReportsRead, h.AdminCustomerCohort},
		{get, "/reports/cron-runs", PermReportsRead, h.AdminCronRuns},
	}
}

// registerInternalAdminRoutes declares the token-only family: per route,
// the token gate first (a refused call is never audited, exactly as the
// LEGACY AdminGuard runs before AuditAdmin), then the audit middleware that
// records the signed actor, then the shared handler. Plus the stats route,
// which only this family has.
func (h *Handler) registerInternalAdminRoutes(r *gin.Engine) {
	audit := middleware.AuditAdmin(h.auditWriter())
	g := r.Group(InternalAdminPrefix)
	g.GET("/stats", h.requireAdminToken(PermStatsRead), audit, h.GetAdminStats)
	for _, rt := range h.adminRoutes() {
		g.Handle(rt.method, rt.path, h.requireAdminToken(rt.perm), audit, rt.handler)
	}
}

// GetAdminStats — GET /v1/rider/internal/admin/stats (admin-service token,
// rider:stats.read). Read-only dashboard counts; money in integer paise.
func (h *Handler) GetAdminStats(c *gin.Context) {
	c.Set(middleware.AuditActionKey, "stats.view")
	c.Set(middleware.AuditTargetKindKey, "dashboard")
	stats, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "rider: admin stats", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "RIDER_ADMIN_STATS_FAILED", "stats unavailable", nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, stats)
}
