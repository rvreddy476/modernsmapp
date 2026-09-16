// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — Money). Same pattern as food-service and
// dating-service.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls monetization with a service token
// it signs for THIS call:
//
//	iss   admin-service
//	aud   monetization
//	scope [the one permission admin-service checked, e.g. monetization:fund.reverse]
//	act   the admin's user id
//	exp   60 s
//
// Monetization trusts none of that because of where the request came from.
// The internal key is no evidence and neither is X-User-Id or X-Scopes. The
// token is: only admin-service holds the private key, the scope names what it
// checked, and the actor is signed.
//
// The token is authentication, not a way around the launch boundary. Every
// route in the family is judged, after the token, by the same flags as the
// legacy /v1/monetization/admin routes (tokenBoundary): with
// MONETIZATION_WRITES_ENABLED off every route answers 503
// MONETIZATION_NOT_LAUNCHED, and in MONETIZATION_MAINTENANCE only the admin
// corrections are open — refunds and dispute updates, which are not
// corrections, answer 503 MAINTENANCE exactly as their legacy routes do. No
// route in the family moves money out: payout requests are read-only here.
package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/monetization-service/internal/service"
	pgstore "github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Monetization admin permissions. Names that identity's catalogue already
// has (identity-platform/services/auth-service/internal/permissions) are
// reused; the ones marked NEW are not in the catalogue yet.
const (
	PermStatsRead       = "monetization:stats.read" // NEW
	PermFraudReview     = "monetization:fraud.review"
	PermWalletFreeze    = "monetization:wallet.freeze"    // NEW
	PermWalletUnfreeze  = "monetization:wallet.unfreeze"  // NEW
	PermWalletRebuild   = "monetization:wallet.rebuild"   // NEW
	PermFundRead        = "monetization:fund.read"        // NEW
	PermFundRates       = "monetization:fund.rates"       // rates and quality bands
	PermCreatorsSuspend = "monetization:creators.suspend" // NEW
	PermFundSettle      = "monetization:fund.settle"
	PermFundReverse     = "monetization:fund.reverse"
	PermFundBudget      = "monetization:fund.budget"
	PermDisputesRead    = "monetization:disputes.read" // NEW
	PermDisputesAct     = "monetization:disputes.act"  // NEW
	PermRefundIssue     = "monetization:refund.issue"  // NEW
	PermPayoutsRead     = "monetization:payouts.read"  // NEW
	PermAuditRead       = "monetization:audit.read"
)

// AdminPermissions lists every permission an admin-service token may carry
// to monetization. Deployment registers admin-service with exactly these ops.
var AdminPermissions = []string{
	PermStatsRead, PermFraudReview, PermWalletFreeze, PermWalletUnfreeze, PermWalletRebuild,
	PermFundRead, PermFundRates, PermCreatorsSuspend, PermFundSettle, PermFundReverse,
	PermFundBudget, PermDisputesRead, PermDisputesAct, PermRefundIssue, PermPayoutsRead,
	PermAuditRead,
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
	ctxTokenPath       = "monetization_admin_token_path"
	ctxAdminTokenPerms = "monetization_admin_token_perms"
	ctxAdminActor      = "monetization_admin_actor"
	ctxServiceCaller   = "monetization_service_caller"
)

// InternalAdminPrefix is the token-only admin family. It is registered
// outside the internal-key group; the gateway refuses /internal/ from the edge.
const InternalAdminPrefix = "/v1/monetization/internal/admin"

// WithServiceAuth installs the service-token verifier (ServiceCallersFromEnv).
// nil means no token is accepted.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// onTokenPath reports whether the request entered through the token family.
func onTokenPath(c *gin.Context) bool {
	v, ok := c.Get(ctxTokenPath)
	return ok && v == true
}

// tokenActor is the signed act claim of an admitted admin-service token.
func tokenActor(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxAdminActor)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// requireAdminToken admits ONLY an admin-service token carrying perm.
// No token → 401, whatever else the request carries.
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("monetization: requireAdminToken needs the route's permission")
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
// malformed act claim. X-User-Id and X-Scopes riding on the same request are
// ignored — they never add or replace anything.
func (h *Handler) authorizeAdminToken(c *gin.Context, perm string) bool {
	ctx := c.Request.Context()
	if h.verifier == nil || h.verifier.Callers() == 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
			"service tokens are not accepted by this deployment", nil)
		return false
	}
	verified, err := h.verifier.Verify(rawServiceToken(c), perm, "")
	if errors.Is(err, servicetoken.ErrScopeDenied) {
		slog.WarnContext(ctx, "monetization: admin token lacks the route permission", "required", perm, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
			"the token does not carry the permission this route requires", gin.H{"required": perm})
		return false
	}
	if err != nil {
		slog.WarnContext(ctx, "monetization: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
		return false
	}
	if verified.Issuer != IssuerAdminService {
		slog.WarnContext(ctx, "monetization: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
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
	c.Set(ctxAdminTokenPerms, map[string]bool{perm: true})
	c.Set(ctxAdminActor, actor)
	return true
}

// tokenBoundary applies the launch boundary to one token-family route, after
// the token has been verified. It is the same line the legacy admin routes
// are held to (launchBoundary); the token changes who may call, never
// whether the flags allow the call.
func (h *Handler) tokenBoundary(maintenanceOpen bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.maintenance {
			if maintenanceOpen {
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"error": gin.H{
					"code":    "MAINTENANCE",
					"message": "Monetization is in maintenance: only admin corrections are being served.",
				},
			})
			return
		}
		if h.writesEnabled {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"code":    "MONETIZATION_NOT_LAUNCHED",
				"message": "Money actions are not available in this beta.",
			},
		})
	}
}

// adminRoute is one route of the token family.
type adminRoute struct {
	method, path string
	perm         string
	// maintenanceOpen: open in maintenance mode. True for the routes that
	// mirror /v1/monetization/admin (which maintenance keeps open for the
	// operator's corrections) and for admin reads; false for refunds and
	// dispute updates, whose legacy routes maintenance closes.
	maintenanceOpen bool
	handler         gin.HandlerFunc
}

// adminRoutes is the token family: every existing monetization admin
// operation, plus the dashboard's reads, each with its permission.
func (h *Handler) adminRoutes() []adminRoute {
	return []adminRoute{
		{http.MethodGet, "/stats", PermStatsRead, true, h.GetAdminStats},

		// Fraud reviews.
		{http.MethodGet, "/fraud-reviews", PermFraudReview, true, h.ListPendingFraudReviews},
		{http.MethodPatch, "/fraud-reviews/:id", PermFraudReview, true, h.ResolveFraudReviewAdmin},

		// Wallet (creator ledger).
		{http.MethodPost, "/wallet/:userId/freeze", PermWalletFreeze, true, h.FreezeWallet},
		{http.MethodPost, "/wallet/:userId/unfreeze", PermWalletUnfreeze, true, h.UnfreezeWallet},
		{http.MethodPost, "/wallet/:userId/rebuild", PermWalletRebuild, true, h.RebuildWallet},

		// Creator fund.
		{http.MethodGet, "/creator-fund/rates", PermFundRead, true, h.ListCreatorFundRatesAdmin},
		{http.MethodPut, "/creator-fund/rates", PermFundRates, true, h.SetCreatorFundRate},
		{http.MethodPut, "/creator-fund/quality-bands", PermFundRates, true, h.SetCreatorFundQualityBand},
		{http.MethodPost, "/creator-fund/:userId/suspend", PermCreatorsSuspend, true, h.SuspendCreatorFund},
		{http.MethodPost, "/creator-fund/:userId/unsuspend", PermCreatorsSuspend, true, h.UnsuspendCreatorFund},
		{http.MethodPost, "/creator-fund/settle", PermFundSettle, true, h.ForceAccrueCreatorFundDay},
		{http.MethodPost, "/creator-fund/settle-period", PermFundSettle, true, h.SettleCreatorFundPeriod},
		{http.MethodPost, "/creator-fund/:userId/settle-period", PermFundSettle, true, h.SettleCreatorFundPeriodForCreator},
		{http.MethodPost, "/creator-fund/earnings/:id/reverse", PermFundReverse, true, h.ReverseCreatorFundEarning},
		{http.MethodGet, "/creator-fund/earnings/:id", PermFundRead, true, h.GetCreatorFundEarningAdmin},
		{http.MethodGet, "/creator-fund/budgets", PermFundRead, true, h.ListCreatorFundBudgets},
		{http.MethodPut, "/creator-fund/budgets", PermFundBudget, true, h.SetCreatorFundBudget},

		// Disputes and refunds. Not corrections: closed in maintenance.
		{http.MethodGet, "/disputes", PermDisputesRead, true, h.ListOpenDisputesAdmin},
		{http.MethodPatch, "/disputes/:id", PermDisputesAct, false, h.ResolveDisputeAdmin},
		{http.MethodPost, "/refunds", PermRefundIssue, false, h.ProcessRefund},

		// Payout queue (read-only; payouts stay off) and the audit trail.
		{http.MethodGet, "/payout-requests", PermPayoutsRead, true, h.ListPayoutRequestsAdmin},
		{http.MethodGet, "/audit-logs", PermAuditRead, true, h.ListAuditLogAdmin},
	}
}

// registerTokenAdminRoutes mounts the token family on the engine, outside
// the internal-key group. Chain per route: mark the token path → verify the
// token for the route's permission → the launch boundary → the handler.
func (h *Handler) registerTokenAdminRoutes(r *gin.Engine) {
	g := r.Group(InternalAdminPrefix)
	for _, rt := range h.adminRoutes() {
		g.Handle(rt.method, rt.path, h.tokenChain(rt)...)
	}
}

// tokenChain is the handler chain of one token-family route.
func (h *Handler) tokenChain(rt adminRoute) []gin.HandlerFunc {
	markTokenPath := func(c *gin.Context) {
		c.Set(ctxTokenPath, true)
		c.Next()
	}
	return []gin.HandlerFunc{markTokenPath, h.requireAdminToken(rt.perm), h.tokenBoundary(rt.maintenanceOpen), rt.handler}
}

// adminActor is the acting admin for an audited write: the token's act on
// the token path, X-User-Id behind the admin scope on the legacy path.
func adminActor(c *gin.Context) (service.AdminActor, bool) {
	id, ok := getAdminID(c)
	if !ok {
		return service.AdminActor{}, false
	}
	via := service.ViaGateway
	if onTokenPath(c) {
		via = service.ViaAdminService
	}
	return service.AdminActor{ID: id, IP: c.ClientIP(), Via: via}, true
}

// ---------------------------------------------------------------------------
// Dashboard reads (token family only)
// ---------------------------------------------------------------------------

// GetAdminStats — GET /v1/monetization/internal/admin/stats
// (monetization:stats.read). Read-only dashboard counts; money in paise.
func (h *Handler) GetAdminStats(c *gin.Context) {
	stats, err := h.svc.AdminStats(c.Request.Context())
	if err != nil {
		slog.ErrorContext(c.Request.Context(), "monetization: admin stats", "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "MONETIZATION_ADMIN_STATS_FAILED", "stats unavailable", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, stats, nil)
}

// ListOpenDisputesAdmin — GET .../disputes: open and investigating disputes.
func (h *Handler) ListOpenDisputesAdmin(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	limit, offset := parseLimitOffset(c)
	disputes, err := h.svc.ListOpenDisputes(c.Request.Context(), limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	if disputes == nil {
		disputes = []pgstore.Dispute{}
	}
	api.JSON(c.Writer, http.StatusOK, disputes, nil)
}

// ListPayoutRequestsAdmin — GET .../payout-requests?status=&limit=&offset=.
// Read-only: nothing here approves, submits or releases a payout.
func (h *Handler) ListPayoutRequestsAdmin(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	limit, offset := parseLimitOffset(c)
	rows, err := h.svc.ListPayoutRequests(c.Request.Context(), strings.TrimSpace(c.Query("status")), limit, offset)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, rows, nil)
}

// ListAuditLogAdmin — GET .../audit-logs?table=&operation=&performer_id=&before=&limit=.
// Newest first; page with before = the last row's created_at (RFC 3339).
func (h *Handler) ListAuditLogAdmin(c *gin.Context) {
	if _, ok := getAdminID(c); !ok {
		return
	}
	f := pgstore.AuditLogFilter{
		TableName: strings.TrimSpace(c.Query("table")),
		Operation: strings.TrimSpace(c.Query("operation")),
	}
	if v := c.Query("performer_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid performer_id", nil)
			return
		}
		f.PerformerID = id
	}
	if v := c.Query("before"); v != "" {
		t, err := time.Parse(time.RFC3339Nano, v)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "before must be RFC 3339", nil)
			return
		}
		f.Before = t
	}
	if v := c.Query("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Limit = n
		}
	}
	rows, err := h.svc.ListAuditLog(c.Request.Context(), f)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	var meta *api.Meta
	if f.Limit > 0 && len(rows) == f.Limit {
		meta = &api.Meta{NextCursor: rows[len(rows)-1].CreatedAt.Format(time.RFC3339Nano)}
	}
	api.JSON(c.Writer, http.StatusOK, rows, meta)
}
