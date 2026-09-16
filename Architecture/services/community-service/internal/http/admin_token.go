// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — Content apps, Chat). Same pattern as food-service
// and channel-service.
//
// admin-service resolves the admin's permissions, enforces MFA, step-up and
// two-person rules and writes its own audit row; then it calls community-service
// with a service token it signs for THIS call:
//
//	iss   admin-service
//	aud   chat
//	scope [the one permission admin-service checked, e.g. chat:reports.act]
//	act   the admin's user id
//	exp   60 s
//
// The internal key is no evidence and neither is X-User-Id. Only the token is.
//
// Scope is deliberately small: the community report queue (list, detail) and a
// decision (uphold or dismiss, with a reason), plus stats. A decision does not
// enforce anything — removing a member, deleting a post or archiving a community
// are owner and moderator flows and are not wired here. community-service
// also has no route that files a community report, so the queue stays empty
// until one exists.
package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/community-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Chat admin permissions for communities. Names identity's catalogue already has
// are reused; the ones marked NEW are not in the catalogue yet.
const (
	PermStatsRead   = "chat:stats.read"   // NEW
	PermReportsRead = "chat:reports.read" // NEW
	PermReportsAct  = "chat:reports.act"
)

// AdminPermissions lists every permission an admin-service token may carry
// to community-service. Deployment registers admin-service with exactly these.
var AdminPermissions = []string{PermStatsRead, PermReportsRead, PermReportsAct}

// Error codes for the token path.
const (
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
)

const ctxAdminActor = "community_admin_actor"

// InternalAdminPrefix is the token-only admin family, under the service's
// own prefix with an `internal` segment so the gateway refuses it from the
// edge. Registered BEFORE the engine-wide internal-key middleware: the key is
// no evidence here.
const InternalAdminPrefix = "/v1/communities/internal/admin"

// CommunityAdmin is what the admin handlers need; the store in production.
type CommunityAdmin interface {
	AdminListCommunityReports(ctx context.Context, status string, limit int, cursor string) ([]store.CommunityReport, string, error)
	AdminGetCommunityReport(ctx context.Context, id uuid.UUID) (*store.CommunityReport, error)
	AdminDecideCommunityReport(ctx context.Context, actor, id uuid.UUID, status, reason string) (*store.CommunityReport, error)
	AdminCommunityStats(ctx context.Context) (*store.CommunityAdminStats, error)
}

// WithServiceAuth installs the service-token verifier (ServiceCallersFromEnv).
// nil means no token is accepted.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
	return h
}

// WithAdmin installs the admin backend (the store).
func (h *Handler) WithAdmin(a CommunityAdmin) *Handler {
	h.admin = a
	return h
}

func rawServiceToken(c *gin.Context) string {
	return strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
}

// adminActor is the signed act claim of the admitted token. The admin
// handlers take the actor ONLY from here, never from X-User-Id.
func adminActor(c *gin.Context) (uuid.UUID, bool) {
	v, ok := c.Get(ctxAdminActor)
	if !ok {
		return uuid.Nil, false
	}
	id, ok := v.(uuid.UUID)
	return id, ok && id != uuid.Nil
}

// requireAdminToken admits ONLY an admin-service token carrying perm and a
// valid act. No token → 401, whatever else the request carries.
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("community-service: requireAdminToken needs the route's permission")
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
			slog.WarnContext(ctx, "community-service: admin token lacks the route permission", "required", perm, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
				"the token does not carry the permission this route requires", gin.H{"required": perm})
			c.Abort()
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "community-service: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		if verified.Issuer != IssuerAdminService {
			slog.WarnContext(ctx, "community-service: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
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
		c.Set(ctxAdminActor, actor)
		c.Next()
	}
}

// registerAdminTokenRoutes declares the token-only admin family.
func (h *Handler) registerAdminTokenRoutes(r *gin.Engine) {
	g := r.Group(InternalAdminPrefix)
	g.GET("/stats", h.requireAdminToken(PermStatsRead), h.AdminStats)
	g.GET("/reports", h.requireAdminToken(PermReportsRead), h.AdminListReports)
	g.GET("/reports/:reportId", h.requireAdminToken(PermReportsRead), h.AdminGetReport)
	g.POST("/reports/:reportId/decision", h.requireAdminToken(PermReportsAct), h.AdminDecideReport)
}

// AdminDecisionRequest is the body of POST .../reports/:id/decision.
type AdminDecisionRequest struct {
	Decision string `json:"decision"` // uphold | dismiss
	Reason   string `json:"reason"`
}

const adminReasonMaxRunes = 1000

func (h *Handler) adminBackendReady(c *gin.Context) bool {
	if h.admin == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, "ADMIN_UNAVAILABLE", "admin backend unavailable", nil)
		return false
	}
	return true
}

func writeAdminError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, store.ErrAdminNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "not found", nil)
	case errors.Is(err, store.ErrAdminConflict):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "CONFLICT", err.Error(), nil)
	case errors.Is(err, store.ErrAdminActorRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the acting admin is required", nil)
	case strings.HasPrefix(err.Error(), "invalid"):
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "VALIDATION_ERROR", err.Error(), nil)
	default:
		slog.ErrorContext(ctx, "community-service: admin action failed", "error", err, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "admin action failed", nil)
	}
}

func adminReportID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("reportId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid reportId", nil)
		return uuid.Nil, false
	}
	return id, true
}

// AdminStats — GET .../stats (chat:stats.read).
func (h *Handler) AdminStats(c *gin.Context) {
	if !h.adminBackendReady(c) {
		return
	}
	out, err := h.admin.AdminCommunityStats(c.Request.Context())
	if err != nil {
		writeAdminError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AdminListReports — GET .../reports?status=&limit=&cursor= (chat:reports.read).
func (h *Handler) AdminListReports(c *gin.Context) {
	if !h.adminBackendReady(c) {
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "50")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	items, next, err := h.admin.AdminListCommunityReports(c.Request.Context(), c.Query("status"), limit, c.Query("cursor"))
	if err != nil {
		writeAdminError(c, err)
		return
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, items, meta)
}

// AdminGetReport — GET .../reports/:reportId (chat:reports.read).
func (h *Handler) AdminGetReport(c *gin.Context) {
	if !h.adminBackendReady(c) {
		return
	}
	id, ok := adminReportID(c)
	if !ok {
		return
	}
	out, err := h.admin.AdminGetCommunityReport(c.Request.Context(), id)
	if err != nil {
		writeAdminError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AdminDecideReport — POST .../reports/:reportId/decision (chat:reports.act).
// Body {"decision":"uphold|dismiss","reason":"..."}. The reviewer is act.
func (h *Handler) AdminDecideReport(c *gin.Context) {
	if !h.adminBackendReady(c) {
		return
	}
	actor, ok := adminActor(c)
	if !ok {
		writeAdminError(c, store.ErrAdminActorRequired)
		return
	}
	id, ok := adminReportID(c)
	if !ok {
		return
	}
	var req AdminDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
		return
	}
	var status string
	switch strings.ToLower(strings.TrimSpace(req.Decision)) {
	case "uphold":
		status = store.ReportUpheld
	case "dismiss":
		status = store.ReportDismissed
	default:
		writeAdminError(c, errors.New("invalid: decision must be uphold or dismiss"))
		return
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" || len([]rune(reason)) > adminReasonMaxRunes {
		writeAdminError(c, errors.New("invalid: reason is required and at most 1000 characters"))
		return
	}
	out, err := h.admin.AdminDecideCommunityReport(c.Request.Context(), actor, id, status, reason)
	if err != nil {
		writeAdminError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
