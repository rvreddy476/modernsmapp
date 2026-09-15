package http

import (
	"net/http"
	"strconv"

	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Admin read-side handlers for /admin/dating console (PRODUCTION_GAP_
// ANALYSIS.md §P0-8). Every route is registered behind requireAdmin
// (auth.go): the gateway does NOT admin-gate /v1/dating/admin, so the
// internal key proves nothing and dating checks the gateway-set scopes
// itself. Mutating handlers record the admitted admin's X-User-Id as the
// audit actor (adminActor); X-Admin-Id is never read.

// ListReports — GET /v1/dating/admin/reports?status=&category=&limit=&offset=
func (h *Handler) ListReports(c *gin.Context) {
	status := c.Query("status")
	category := c.Query("category")
	limit := parseIntQuery(c, "limit", 50, 200)
	offset := parseIntQuery(c, "offset", 0, 100000)
	items, err := h.svc.ListReports(c.Request.Context(), status, category, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "limit": limit, "offset": offset}, nil)
}

// ListPanicEvents — GET /v1/dating/admin/safety/panic?limit=
func (h *Handler) ListPanicEvents(c *gin.Context) {
	limit := parseIntQuery(c, "limit", 100, 200)
	items, err := h.svc.ListPanicEvents(c.Request.Context(), limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "limit": limit}, nil)
}

// ListPendingPhotos — GET /v1/dating/admin/photos/pending?limit=
func (h *Handler) ListPendingPhotos(c *gin.Context) {
	limit := parseIntQuery(c, "limit", 50, 200)
	items, err := h.svc.ListPendingPhotos(c.Request.Context(), limit)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "limit": limit}, nil)
}

// actOnReportRequest is the body of POST /v1/dating/admin/reports/:id/action.
type actOnReportRequest struct {
	Action       string `json:"action" binding:"required"`
	TargetUserID string `json:"target_user_id"`
}

// ActOnReport — POST /v1/dating/admin/reports/:id/action
// Body: {action, target_user_id?}. Allowed actions: dismiss /
// resolved / warn / review / restrict / suspend. Review, restrict +
// suspend require target_user_id and flip the reported user's
// profile_status (pending_review / restricted / suspended), which
// fires deck-cache invalidation downstream.
//
// The dating_admin_audit actor is the admin's gateway-derived X-User-Id
// (requireAdmin). No actor → the action is refused.
func (h *Handler) ActOnReport(c *gin.Context) {
	adminID, ok := adminActor(c)
	if !ok {
		return
	}
	reportID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body actOnReportRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	var targetID uuid.UUID
	if body.TargetUserID != "" {
		parsed, perr := uuid.Parse(body.TargetUserID)
		if perr != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid target_user_id", nil)
			return
		}
		targetID = parsed
	}
	newStatus, err := h.svc.ActOnReport(c.Request.Context(), adminID, reportID, targetID, body.Action)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ACTION_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": newStatus}, nil)
}

// AcknowledgePanic — POST /v1/dating/admin/safety/panic/:id/ack
//
// Stamps acknowledged_at + acknowledged_by on the named panic
// safety_event row and emits dating.safety.panic.acknowledged so the
// user who triggered the alert sees "support has reviewed your panic
// request" in-app. Idempotent: second ack hits the already-acked
// branch in the store and short-circuits without re-emitting.
//
// Admin scope required (requireAdmin). The admin's gateway-derived
// X-User-Id is the acknowledged_by actor and the dating_admin_audit
// actor; no actor → the acknowledgement is refused.
func (h *Handler) AcknowledgePanic(c *gin.Context) {
	adminID, ok := adminActor(c)
	if !ok {
		return
	}
	panicID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.AcknowledgePanic(c.Request.Context(), panicID, adminID); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "ACK_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"acknowledged": true}, nil)
}

// ListAdminAudit — GET /v1/dating/admin/audit?actor=&target_user_id=&action=&limit=&offset=
//
// Surfaces the dating_admin_audit append-only log for the console
// audit view. Filters are optional and AND-combined; the store
// clamps limit to [1, 200].
func (h *Handler) ListAdminAudit(c *gin.Context) {
	limit := parseIntQuery(c, "limit", 50, 200)
	offset := parseIntQuery(c, "offset", 0, 100000)

	var f store.AdminAuditFilter
	if raw := c.Query("actor"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid actor", nil)
			return
		}
		f.ActorAdminID = id
	}
	if raw := c.Query("target_user_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid target_user_id", nil)
			return
		}
		f.TargetUserID = id
	}
	f.Action = c.Query("action")

	items, err := h.svc.ListAdminAudit(c.Request.Context(), f, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "limit": limit, "offset": offset}, nil)
}

// parseIntQuery reads a positive int query param, clamped to [1, max]
// with a default fallback. Used by all three admin list endpoints.
func parseIntQuery(c *gin.Context, name string, def, max int) int {
	raw := c.Query(name)
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
