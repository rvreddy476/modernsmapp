package http

// Admin console calls from admin-service (admin console, Wave 2 — Money).
// Same pattern as food and dating.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules and
// writes admin.audit_log; then it calls payments with a service token it signs
// for THIS call:
//
//	iss   admin-service
//	aud   payments
//	scope [the one permission admin-service checked, e.g. payments:refunds.read]
//	act   the admin's user id
//	exp   ≤ 5 min (admin-service uses 60 s)
//
// Why the existing verifier and not a second token family: payments already
// registers each caller with its own key and its own operation allowlist
// (SERVICE_CALLER_<NAME>_OPS), and Verify already checks the operation against
// both the token's scope and that allowlist. A console permission is just
// another operation. So admin-service is one more entry in SERVICE_CALLERS,
// with OPS drawn only from AdminPermissions and no REFTYPES, and three rules
// keep it apart from the money callers:
//
//  1. Boot (ValidateCallerPolicy): admin-service may hold admin permissions
//     only, and no other caller may hold one.
//  2. The admin routes (requireAdminToken) admit only issuer admin-service,
//     only with the route's permission, and only with a signed act. There is
//     no internal-key fallback and X-User-Id is never read.
//  3. The money routes (requireOp) refuse issuer admin-service whatever its
//     token or registration says, so it can never create an intent, verify,
//     refund or read through the service family.

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/payments-service/internal/config"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = config.AdminServiceCaller

// InternalAdminPrefix is the token-only admin family. The gateway does not
// proxy /v1/payments at all.
const InternalAdminPrefix = "/v1/payments/internal/admin"

// Payments admin permissions. Names identity's catalogue already has
// (identity-platform/services/auth-service/internal/permissions) are reused;
// those marked NEW are not in the catalogue yet.
const (
	PermStatsRead          = "payments:stats.read" // NEW
	PermRefundsRead        = "payments:refunds.read"
	PermRefundIssue        = "payments:refund.issue"
	PermIntentsRead        = "payments:intents.read"        // NEW
	PermReconciliationRead = "payments:reconciliation.read" // NEW
	PermApplicationsRead   = "payments:applications.read"   // NEW
	PermApplicationsManage = "payments:applications.manage"
	PermAuditRead          = "payments:audit.read"
)

// AdminPermissions is every permission an admin-service token may carry to
// payments. Deployment registers admin-service with exactly these OPS.
var AdminPermissions = []string{
	PermStatsRead, PermRefundsRead, PermRefundIssue, PermIntentsRead, PermReconciliationRead,
	PermApplicationsRead, PermApplicationsManage, PermAuditRead,
}

// Error codes for the admin token path.
const (
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
)

const ctxAdminActor = "payments_admin_actor"

func isAdminPermission(op string) bool {
	for _, p := range AdminPermissions {
		if p == op {
			return true
		}
	}
	return false
}

// ValidateCallerPolicy is the boot rule for one SERVICE_CALLERS entry.
// admin-service must declare at least one op, only admin permissions, and no
// reference types (it acts on none). Every other caller must declare
// reference types and may hold no admin permission.
func ValidateCallerPolicy(name string, ops, refTypes []string) error {
	if len(ops) == 0 {
		return fmt.Errorf("caller %q declares no OPS", name)
	}
	if name == IssuerAdminService {
		if len(refTypes) != 0 {
			return fmt.Errorf("caller %q must not declare REFTYPES: it acts on no reference type", name)
		}
		for _, op := range ops {
			if !isAdminPermission(op) {
				return fmt.Errorf("caller %q may hold admin permissions only, not %q", name, op)
			}
		}
		return nil
	}
	if len(refTypes) == 0 {
		return fmt.Errorf("caller %q must declare REFTYPES", name)
	}
	for _, op := range ops {
		if isAdminPermission(op) {
			return fmt.Errorf("caller %q may not hold the admin permission %q; only %s may", name, op, IssuerAdminService)
		}
	}
	return nil
}

// WithAdmin enables the admin family. pendingAge is the reconciler's
// PAYMENTS_PENDING_AGE_SEC, the window past which an intent counts as stuck.
func (h *Handler) WithAdmin(svc AdminService, pendingAge time.Duration) *Handler {
	h.admin = svc
	if pendingAge <= 0 {
		pendingAge = 10 * time.Minute
	}
	h.pendingAge = pendingAge
	return h
}

// requireAdminToken admits ONLY an admin-service token carrying perm and a
// signed act. Refused: no verifier (401); no token (401) whatever else the
// request carries, including the internal key; a bad signature, unknown or
// expired caller, wrong audience or over-long token (403); a scope without
// perm (403); another issuer (403); a missing or malformed act (403).
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if !isAdminPermission(perm) {
		panic("payments: requireAdminToken needs an admin permission, got " + perm)
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		refuse := func(status int, code, msg string, details any) {
			api.ErrorWithContext(ctx, c.Writer, status, code, msg, details)
			c.Abort()
		}
		if !h.hasVerifier() {
			refuse(http.StatusUnauthorized, CodeServiceCredentialRequired, "service tokens are not accepted by this deployment", nil)
			return
		}
		raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
		if raw == "" {
			refuse(http.StatusUnauthorized, CodeAdminTokenRequired, "an admin-service token is required", nil)
			return
		}
		v, err := h.verifier.Verify(raw, perm, "")
		if errors.Is(err, servicetoken.ErrScopeDenied) {
			slog.WarnContext(ctx, "payments: admin token lacks the route permission", "required", perm, "path", c.Request.URL.Path)
			refuse(http.StatusForbidden, CodeAdminPermissionScope, "the token does not carry the permission this route requires",
				gin.H{"required": perm})
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "payments: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
			refuse(http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			return
		}
		if v.Issuer != IssuerAdminService {
			slog.WarnContext(ctx, "payments: admin route called by a non-admin service", "issuer", v.Issuer, "path", c.Request.URL.Path)
			refuse(http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			return
		}
		actor, err := uuid.Parse(v.Actor)
		if err != nil || actor == uuid.Nil {
			refuse(http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
			return
		}
		c.Set(ctxAdminActor, actor)
		c.Set("caller_domain", v.Issuer)
		c.Next()
	}
}

// adminActor is the signed act of an admitted admin token.
func adminActor(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxAdminActor)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// registerAdminRoutes declares the admin family, each route with the one
// permission it needs.
func (h *Handler) registerAdminRoutes(r *gin.Engine) {
	g := r.Group(InternalAdminPrefix)
	g.GET("/stats", h.requireAdminToken(PermStatsRead), h.AdminStats)

	g.GET("/refunds/needs-attention", h.requireAdminToken(PermRefundsRead), h.AdminListRefundsNeedingAttention)
	g.GET("/refunds/:commandId", h.requireAdminToken(PermRefundsRead), h.AdminGetRefund)
	g.POST("/refunds/:commandId/resolve", h.requireAdminToken(PermRefundIssue), h.AdminResolveRefund)

	g.GET("/intents", h.requireAdminToken(PermIntentsRead), h.AdminListIntents)
	g.GET("/intents/:id", h.requireAdminToken(PermIntentsRead), h.AdminGetIntent)

	g.GET("/reconciliation", h.requireAdminToken(PermReconciliationRead), h.AdminReconciliation)

	g.GET("/applications", h.requireAdminToken(PermApplicationsRead), h.AdminListApplications)
	g.PATCH("/applications/:applicationId", h.requireAdminToken(PermApplicationsManage), h.AdminUpdateApplication)

	g.GET("/audit/payments", h.requireAdminToken(PermAuditRead), h.AdminPaymentAudit)
	g.GET("/audit/applications", h.requireAdminToken(PermAuditRead), h.AdminApplicationAudit)
}
