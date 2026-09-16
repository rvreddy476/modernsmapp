package http

import (
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/o11y/trace"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Dating permissions, as identity's catalogue names them.
const (
	permDatingStatsRead    = "dating:stats.read"
	permDatingReportsRead  = "dating:reports.read"
	permDatingReportsAct   = "dating:reports.act"
	permDatingUsersBan     = "dating:users.ban"
	permDatingPhotosReview = "dating:photos.review"
	permDatingSelfieReview = "dating:selfie.review"
	permDatingPanicRead    = "dating:panic.read"
	permDatingPanicAct     = "dating:panic.act"
	permDatingPanicReveal  = "dating:panic.reveal"
	permDatingRiskRead     = "dating:risk.read"
	permDatingAuditRead    = "dating:audit.read"
)

// Error codes for the Dating routes.
const (
	CodeProductUnavailable  = "PRODUCT_UNAVAILABLE"
	CodeUseEnforcementRoute = "USE_ENFORCEMENT_ROUTE"
	CodeInvalidAction       = "INVALID_ACTION"
	CodeInvalidID           = "INVALID_ID"
)

// Report actions, split by the permission they need. Moderation of the report
// is dating:reports.act; suspending a profile or lifting a suspension is
// enforcement against a person, dating:users.ban with a fresh step-up.
var (
	datingModerationActions  = map[string]bool{"dismiss": true, "resolved": true, "warn": true, "review": true, "restrict": true}
	datingEnforcementActions = map[string]bool{"suspend": true, "reinstate": true}
)

// datingRoute is one declared Dating admin route: admin-service's path, the
// dating path it calls, and what the gate requires.
type datingRoute struct {
	method     string
	path       string // under /v1/admin/dating
	operation  string
	permission string
	stepUp     bool
}

// DatingRoutes is the route → permission table, exported for tests and the
// report. Every entry forwards to dating's token-only admin family with a
// token scoped to exactly its permission.
var DatingRoutes = []datingRoute{
	{http.MethodGet, "/stats", "dating.stats", permDatingStatsRead, false},
	{http.MethodGet, "/reports", "dating.reports.list", permDatingReportsRead, false},
	{http.MethodPost, "/reports/:reportId/action", "dating.report.act", permDatingReportsAct, false},
	{http.MethodPost, "/reports/:reportId/enforce", "dating.report.enforce", permDatingUsersBan, true},
	{http.MethodGet, "/photos/pending", "dating.photos.pending", permDatingPhotosReview, false},
	{http.MethodPost, "/photos/:photoId/decision", "dating.photo.decide", permDatingPhotosReview, false},
	{http.MethodGet, "/selfies/pending", "dating.selfies.pending", permDatingSelfieReview, false},
	{http.MethodPost, "/selfies/:userId/decision", "dating.selfie.decide", permDatingSelfieReview, false},
	{http.MethodGet, "/panic", "dating.panic.list", permDatingPanicRead, false},
	{http.MethodGet, "/panic/:incidentId", "dating.panic.reveal", permDatingPanicReveal, true},
	{http.MethodPost, "/panic/:incidentId/ack", "dating.panic.ack", permDatingPanicAct, false},
	{http.MethodPost, "/panic/:incidentId/resolve", "dating.panic.resolve", permDatingPanicAct, false},
	{http.MethodGet, "/risk", "dating.risk.list", permDatingRiskRead, false},
	{http.MethodGet, "/audit", "dating.audit.list", permDatingAuditRead, false},
}

// RegisterDatingRoutes adds the Dating dashboard under /v1/admin/dating.
//
// Two-person: none. The catalogue marks no Dating permission two-person, and
// a Dating suspension is reversible (reinstate), not a permanent ban; the
// founder's two-person rule is for permanent bans, which land platform-wide
// in identity. Suspend and reinstate instead need dating:users.ban (admins
// only) and a fresh step-up, as does revealing a panic incident's GPS.
func (h *Handler) RegisterDatingRoutes(r *gin.Engine, dc *service.ProductClient) {
	const p = "/v1/admin/dating"
	handlers := map[string]gin.HandlerFunc{
		"dating.stats":          h.datingForward(dc, http.MethodGet, fixed("/stats"), nil, "", ""),
		"dating.reports.list":   h.datingForward(dc, http.MethodGet, fixed("/reports"), passQuery("status", "category", "limit", "offset"), "", ""),
		"dating.report.act":     h.datingReportAction(dc, datingModerationActions, false),
		"dating.report.enforce": h.datingReportAction(dc, datingEnforcementActions, true),
		"dating.photos.pending": h.datingForward(dc, http.MethodGet, fixed("/photos/pending"), passQuery("limit"), "", ""),
		"dating.photo.decide":   h.datingPhotoDecision(dc),
		"dating.selfies.pending": h.datingForward(dc, http.MethodGet, fixed("/verification/selfie/pending"),
			passQuery("limit"), "", ""),
		"dating.selfie.decide": h.datingSelfieDecision(dc),
		"dating.panic.list":    h.datingForward(dc, http.MethodGet, fixed("/safety/panic"), passQuery("status", "limit", "offset"), "", ""),
		"dating.panic.reveal": h.datingForward(dc, http.MethodGet, idPath("incidentId", "/safety/panic/", ""),
			nil, "panic_incident", "incidentId"),
		"dating.panic.ack":     h.datingPanicWrite(dc, "/ack"),
		"dating.panic.resolve": h.datingPanicWrite(dc, "/resolve"),
		"dating.risk.list":     h.datingForward(dc, http.MethodGet, fixed("/risk"), passQuery("level", "limit", "offset"), "", ""),
		"dating.audit.list":    h.datingForward(dc, http.MethodGet, fixed("/audit"), passQuery("actor", "target_user_id", "action", "limit", "offset"), "", ""),
	}
	for _, rt := range DatingRoutes {
		hf, ok := handlers[rt.operation]
		if !ok {
			panic("admin dating: no handler for " + rt.operation)
		}
		h.gate.Handle(r, rt.method, p+rt.path,
			Requirement{Operation: rt.operation, Permission: rt.permission, StepUp: rt.stepUp}, hf)
	}
}

func fixed(path string) func(*gin.Context) (string, error) {
	return func(*gin.Context) (string, error) { return path, nil }
}

// idPath builds a dating path around one uuid route parameter.
func idPath(param, before, after string) func(*gin.Context) (string, error) {
	return func(c *gin.Context) (string, error) {
		id, err := uuid.Parse(c.Param(param))
		if err != nil {
			return "", errInvalidID
		}
		return before + id.String() + after, nil
	}
}

var errInvalidID = errors.New("invalid id")

func passQuery(keys ...string) func(*gin.Context) url.Values {
	return func(c *gin.Context) url.Values {
		q := url.Values{}
		for _, k := range keys {
			if v := c.Query(k); v != "" {
				q.Set(k, v)
			}
		}
		return q
	}
}

// datingCall runs one dating call as the gate's admin, with the gate's
// permission as the token scope, and writes the result and audit row.
func (h *Handler) datingCall(c *gin.Context, dc *service.ProductClient, method, path string, query url.Values, body any) {
	info := auditFrom(c)
	req, ok := effectiveRequirement(c)
	if !ok || req.Permission == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared", nil)
		return
	}
	ctx := c.Request.Context()
	if rid := requestIDFrom(c); rid != "" && trace.RequestIDFrom(ctx) == "" {
		ctx = trace.WithRequestID(ctx, rid)
	}
	data, status, err := dc.Call(ctx, method, path, query, req.Permission, actorFrom(c), body)
	if errors.Is(err, service.ErrProductUnavailable) {
		zero := 0
		info.statusCode = &zero
		info.set("error", "service token key not configured")
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, CodeProductUnavailable,
			"Dating admin actions are not configured on this deployment", nil)
		return
	}
	writeUpstream(c, info, data, status, err)
}

func (h *Handler) datingForward(dc *service.ProductClient, method string, path func(*gin.Context) (string, error),
	query func(*gin.Context) url.Values, targetType, targetParam string) gin.HandlerFunc {
	return func(c *gin.Context) {
		target, err := path(c)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid id", nil)
			return
		}
		if targetType != "" {
			info := auditFrom(c)
			info.targetType, info.targetID = targetType, c.Param(targetParam)
		}
		var q url.Values
		if query != nil {
			q = query(c)
		}
		h.datingCall(c, dc, method, target, q, nil)
	}
}

type datingReportActionReq struct {
	Action       string `json:"action"`
	TargetUserID string `json:"target_user_id"`
	Reason       string `json:"reason"`
}

// datingReportAction serves both report routes. The action route refuses the
// enforcement actions (they need users.ban and a step-up on their own route);
// the enforcement route accepts only them and requires a reason.
func (h *Handler) datingReportAction(dc *service.ProductClient, allowed map[string]bool, enforcement bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		reportID, err := uuid.Parse(c.Param("reportId"))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid report id", nil)
			return
		}
		var body datingReportActionReq
		_ = c.ShouldBindJSON(&body)
		action := strings.TrimSpace(body.Action)
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = "dating_report", reportID.String(), body.Reason
		info.set("action", action)
		if !allowed[action] {
			if !enforcement && datingEnforcementActions[action] {
				api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeUseEnforcementRoute,
					"Suspend and reinstate use POST /v1/admin/dating/reports/:reportId/enforce", nil)
				return
			}
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidAction, "Unknown or disallowed action", nil)
			return
		}
		if enforcement && strings.TrimSpace(body.Reason) == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeReasonRequired, "A reason is required for this action", nil)
			return
		}
		out := map[string]string{"action": action}
		if body.TargetUserID != "" {
			out["target_user_id"] = body.TargetUserID
		}
		h.datingCall(c, dc, http.MethodPost, "/reports/"+reportID.String()+"/action", nil, out)
	}
}

func (h *Handler) datingPhotoDecision(dc *service.ProductClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, err := uuid.Parse(c.Param("photoId"))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid photo id", nil)
			return
		}
		var body struct {
			Status string `json:"status"`
			Reason string `json:"reason"`
		}
		_ = c.ShouldBindJSON(&body)
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = "dating_photo", id.String(), body.Reason
		info.set("status", body.Status)
		h.datingCall(c, dc, http.MethodPost, "/photos/"+id.String()+"/moderation", nil,
			map[string]string{"status": body.Status, "reason": body.Reason})
	}
}

func (h *Handler) datingSelfieDecision(dc *service.ProductClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, err := uuid.Parse(c.Param("userId"))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid user id", nil)
			return
		}
		var body struct {
			Decision string `json:"decision"`
			Reason   string `json:"reason"`
		}
		_ = c.ShouldBindJSON(&body)
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = "dating_selfie", id.String(), body.Reason
		info.set("decision", body.Decision)
		h.datingCall(c, dc, http.MethodPost, "/verification/selfie/"+id.String()+"/review", nil,
			map[string]string{"decision": body.Decision, "reason": body.Reason})
	}
}

func (h *Handler) datingPanicWrite(dc *service.ProductClient, suffix string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		id, err := uuid.Parse(c.Param("incidentId"))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid incident id", nil)
			return
		}
		var body struct {
			Note string `json:"note"`
		}
		_ = c.ShouldBindJSON(&body)
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = "panic_incident", id.String(), body.Note
		var out any
		if suffix == "/resolve" {
			out = map[string]string{"note": body.Note}
		}
		h.datingCall(c, dc, http.MethodPost, "/safety/panic/"+id.String()+suffix, nil, out)
	}
}
