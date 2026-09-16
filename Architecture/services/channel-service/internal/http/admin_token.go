// Admin actions on behalf of a human admin, arriving from admin-service
// (admin console, Wave 2 — Content apps, Chat). Same pattern as food-service.
//
// admin-service is the console's one backend. It resolves the admin's
// permissions with identity, enforces MFA, step-up and two-person rules, and
// writes its own audit row; then it calls channel-service with a service
// token it signs for THIS call:
//
//	iss   admin-service
//	aud   chat
//	scope [the one permission admin-service checked, e.g. chat:reports.act]
//	act   the admin's user id
//	exp   60 s
//
// channel-service trusts none of that because of where the request came
// from. The internal key is no evidence and neither is X-User-Id (anyone
// holding the key can set one). The token is: only admin-service holds the
// private key, the scope names what it checked, and the actor is signed.
package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/channel-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// IssuerAdminService is the only caller whose tokens reach admin routes.
const IssuerAdminService = "admin-service"

// Chat admin permissions for channels. Names identity's catalogue already
// has (identity-platform/services/auth-service/internal/permissions) are
// reused; the ones marked NEW are not in the catalogue yet.
const (
	PermStatsRead        = "chat:stats.read"   // NEW
	PermReportsRead      = "chat:reports.read" // NEW
	PermReportsAct       = "chat:reports.act"
	PermChannelsModerate = "chat:channels.moderate"
)

// AdminPermissions lists every permission an admin-service token may carry
// to channel-service. Deployment registers admin-service with exactly these.
var AdminPermissions = []string{PermStatsRead, PermReportsRead, PermReportsAct, PermChannelsModerate}

// Error codes for the token path.
const (
	CodeAdminTokenRequired        = "ADMIN_SERVICE_TOKEN_REQUIRED"
	CodeAdminPermissionScope      = "ADMIN_PERMISSION_NOT_IN_TOKEN"
	CodeAdminActorRequired        = "ADMIN_ACTOR_REQUIRED"
	CodeServiceCredentialRequired = "SERVICE_CREDENTIAL_REQUIRED"
	CodeServiceTokenRejected      = "SERVICE_TOKEN_REJECTED"
)

const ctxAdminActor = "channel_admin_actor"

// InternalAdminPrefix is the token-only admin family. It sits under the
// service's own prefix with an `internal` segment, so the gateway refuses it
// from the edge (internalroutes.IsInternalPath). It is registered BEFORE the
// engine-wide internal-key middleware and is exempt from the communities kill
// switch: the key is no evidence here, and moderation must work while the
// product is switched off.
const InternalAdminPrefix = "/v1/broadcast-channels/internal/admin"

// ChannelAdmin is what the admin handlers need from the service. The seam
// lets the token tests record the actor without a database.
type ChannelAdmin interface {
	AdminListReports(ctx context.Context, status string, limit int, cursor string) ([]store.ReportListItem, string, error)
	AdminGetReport(ctx context.Context, reportID uuid.UUID) (*store.ReportListItem, error)
	AdminDecideReport(ctx context.Context, actor, reportID uuid.UUID, decision, reason string) (*store.ReportListItem, error)
	AdminSuspendChannel(ctx context.Context, actor, channelID uuid.UUID, reason string) (*store.ChannelStatusChange, error)
	AdminUnsuspendChannel(ctx context.Context, actor, channelID uuid.UUID, reason string) (*store.ChannelStatusChange, error)
	AdminStats(ctx context.Context) (*store.AdminStats, error)
}

// WithServiceAuth installs the service-token verifier (ServiceCallersFromEnv).
// nil means no token is accepted.
func (h *Handler) WithServiceAuth(v *servicetoken.Verifier) *Handler {
	h.verifier = v
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
//
// Refused (403 unless noted): no verifier configured (401); bad signature,
// unknown caller, wrong audience, expired or over-long tokens; a caller other
// than admin-service; a token whose scope lacks perm; and a missing or
// malformed act. X-User-Id, X-Scopes and the internal key are ignored.
func (h *Handler) requireAdminToken(perm string) gin.HandlerFunc {
	if perm == "" {
		panic("channel-service: requireAdminToken needs the route's permission")
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
			slog.WarnContext(ctx, "channel-service: admin token lacks the route permission", "required", perm, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminPermissionScope,
				"the token does not carry the permission this route requires", gin.H{"required": perm})
			c.Abort()
			return
		}
		if err != nil {
			slog.WarnContext(ctx, "channel-service: admin token refused", "reason", err.Error(), "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		if verified.Issuer != IssuerAdminService {
			slog.WarnContext(ctx, "channel-service: admin route called by a non-admin service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
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
	g.GET("/channel-reports", h.requireAdminToken(PermReportsRead), h.AdminListReports)
	g.GET("/channel-reports/:reportId", h.requireAdminToken(PermReportsRead), h.AdminGetReport)
	g.POST("/channel-reports/:reportId/decision", h.requireAdminToken(PermReportsAct), h.AdminDecideReport)
	g.POST("/channels/:channelId/suspend", h.requireAdminToken(PermChannelsModerate), h.AdminSuspendChannel)
	g.POST("/channels/:channelId/unsuspend", h.requireAdminToken(PermChannelsModerate), h.AdminUnsuspendChannel)
}

// AdminDecisionRequest is the body of POST .../channel-reports/:id/decision.
type AdminDecisionRequest struct {
	Decision string `json:"decision"` // uphold | dismiss
	Reason   string `json:"reason"`
}

// AdminReasonRequest is the body of suspend and unsuspend.
type AdminReasonRequest struct {
	Reason string `json:"reason"`
}

// adminBackendReady answers 503 when the handler was built without a service.
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
		slog.ErrorContext(ctx, "channel-service: admin action failed", "error", err, "path", c.Request.URL.Path)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "admin action failed", nil)
	}
}

func adminPathID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+param, nil)
		return uuid.Nil, false
	}
	return id, true
}

func bindAdminBody(c *gin.Context, dst any) bool {
	if err := c.ShouldBindJSON(dst); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
		return false
	}
	return true
}

// AdminStats — GET .../stats (chat:stats.read).
func (h *Handler) AdminStats(c *gin.Context) {
	if !h.adminBackendReady(c) {
		return
	}
	out, err := h.admin.AdminStats(c.Request.Context())
	if err != nil {
		writeAdminError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AdminListReports — GET .../channel-reports?status=&limit=&cursor=
// (chat:reports.read). status defaults to open; "all" lists every state.
func (h *Handler) AdminListReports(c *gin.Context) {
	if !h.adminBackendReady(c) {
		return
	}
	limit := 50
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "50")); err == nil && l > 0 && l <= 200 {
		limit = l
	}
	items, next, err := h.admin.AdminListReports(c.Request.Context(), c.Query("status"), limit, c.Query("cursor"))
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

// AdminGetReport — GET .../channel-reports/:reportId (chat:reports.read).
func (h *Handler) AdminGetReport(c *gin.Context) {
	if !h.adminBackendReady(c) {
		return
	}
	id, ok := adminPathID(c, "reportId")
	if !ok {
		return
	}
	out, err := h.admin.AdminGetReport(c.Request.Context(), id)
	if err != nil {
		writeAdminError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AdminDecideReport — POST .../channel-reports/:reportId/decision
// (chat:reports.act). Body {"decision":"uphold|dismiss","reason":"..."}.
// The reviewer is the token's act claim.
func (h *Handler) AdminDecideReport(c *gin.Context) {
	if !h.adminBackendReady(c) {
		return
	}
	actor, ok := adminActor(c)
	if !ok {
		writeAdminError(c, store.ErrAdminActorRequired)
		return
	}
	id, ok := adminPathID(c, "reportId")
	if !ok {
		return
	}
	var req AdminDecisionRequest
	if !bindAdminBody(c, &req) {
		return
	}
	out, err := h.admin.AdminDecideReport(c.Request.Context(), actor, id, req.Decision, req.Reason)
	if err != nil {
		writeAdminError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// AdminSuspendChannel — POST .../channels/:channelId/suspend
// (chat:channels.moderate). Body {"reason":"..."}.
func (h *Handler) AdminSuspendChannel(c *gin.Context) {
	h.adminChannelStatus(c, true)
}

// AdminUnsuspendChannel — POST .../channels/:channelId/unsuspend
// (chat:channels.moderate). Body {"reason":"..."}.
func (h *Handler) AdminUnsuspendChannel(c *gin.Context) {
	h.adminChannelStatus(c, false)
}

func (h *Handler) adminChannelStatus(c *gin.Context, suspend bool) {
	if !h.adminBackendReady(c) {
		return
	}
	actor, ok := adminActor(c)
	if !ok {
		writeAdminError(c, store.ErrAdminActorRequired)
		return
	}
	id, ok := adminPathID(c, "channelId")
	if !ok {
		return
	}
	var req AdminReasonRequest
	if !bindAdminBody(c, &req) {
		return
	}
	var (
		out *store.ChannelStatusChange
		err error
	)
	if suspend {
		out, err = h.admin.AdminSuspendChannel(c.Request.Context(), actor, id, req.Reason)
	} else {
		out, err = h.admin.AdminUnsuspendChannel(c.Request.Context(), actor, id, req.Reason)
	}
	if err != nil {
		writeAdminError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
