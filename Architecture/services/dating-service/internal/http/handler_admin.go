package http

import (
	"net/http"
	"strconv"
	"strings"

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

// ListPanicIncidents — GET /v1/dating/admin/safety/panic?status=&limit=&offset=
//
// The on-call queue (lane D8): paginated, filterable by open / acknowledged
// / resolved, and never coordinates — each row says only has_location.
func (h *Handler) ListPanicIncidents(c *gin.Context) {
	status := c.Query("status")
	limit := parseIntQuery(c, "limit", 50, 200)
	offset := parseIntQuery(c, "offset", 0, 100000)
	items, err := h.svc.ListPanicIncidents(c.Request.Context(), status, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	resp := gin.H{"items": items, "limit": limit, "offset": offset}
	if len(items) == limit {
		resp["next_offset"] = offset + limit
	}
	api.JSON(c.Writer, http.StatusOK, resp, nil)
}

// GetPanicIncident — GET /v1/dating/admin/safety/panic/:id
//
// One incident with its full-precision location and client context. Every
// view is audited ("panic_viewed", actor = the admin's X-User-Id); if the
// audit cannot be written the response is refused.
func (h *Handler) GetPanicIncident(c *gin.Context) {
	adminID, ok := adminActor(c)
	if !ok {
		return
	}
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	inc, err := h.svc.GetPanicIncidentForAdmin(c.Request.Context(), adminID, id)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, inc, nil)
}

// resolvePanicRequest is the body of POST /v1/dating/admin/safety/panic/:id/resolve.
type resolvePanicRequest struct {
	Note string `json:"note"`
}

// ResolvePanic — POST /v1/dating/admin/safety/panic/:id/resolve
//
// Body: {note?}. Closes the incident (acknowledging it too when skipped)
// and audits "panic_resolved" with the note. Idempotent. The response
// carries no coordinates.
func (h *Handler) ResolvePanic(c *gin.Context) {
	adminID, ok := adminActor(c)
	if !ok {
		return
	}
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var body resolvePanicRequest
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
			return
		}
	}
	inc, err := h.svc.ResolvePanic(c.Request.Context(), adminID, id, body.Note)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "RESOLVE_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"status": inc.Status, "resolved_at": inc.ResolvedAt}, nil)
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

// ReportActionPermission is the permission one report action needs.
// Suspending a profile, and lifting a suspension, are enforcement against a
// person (dating:users.ban, admins only); the rest are moderation of the
// report itself (dating:reports.act).
func ReportActionPermission(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "suspend", "reinstate":
		return PermUsersBan
	}
	return PermReportsAct
}

// ActOnReport — POST /v1/dating/admin/reports/:id/action
// Body: {action, target_user_id?}. Allowed actions: dismiss /
// resolved / warn / review / restrict / suspend / reinstate. Every action
// applies to the report's own target; a target_user_id naming anyone else
// is refused with 409 REPORT_TARGET_MISMATCH before anything changes.
// Review, restrict, suspend + reinstate move the reported user through the
// profile status machine (pending_review / restricted / suspended, or back
// to their remembered step), which fires deck-cache invalidation
// downstream. A refused edge returns 409 PROFILE_TRANSITION_NOT_ALLOWED.
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
	if !adminMay(c, ReportActionPermission(body.Action)) {
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
