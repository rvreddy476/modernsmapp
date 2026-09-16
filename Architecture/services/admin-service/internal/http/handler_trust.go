package http

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Trust & safety permissions, exactly as trust-safety-service checks them
// (trust-safety-service/internal/http/admin_token.go).
const (
	permTrustStatsRead          = "trust_safety:stats.read"
	permTrustReportsRead        = "trust_safety:reports.read"
	permTrustReportsAct         = "trust_safety:reports.act"
	permTrustAppealsRead        = "trust_safety:appeals.read"
	permTrustAppealsAct         = "trust_safety:appeals.act"
	permTrustGrievancesRead     = "trust_safety:grievances.read"
	permTrustGrievancesAct      = "trust_safety:grievances.act"
	permTrustStrikesRead        = "trust_safety:strikes.read"
	permTrustStrikesManage      = "trust_safety:strikes.manage"
	permTrustVerificationReview = "trust_safety:verification.review"
	permTrustMediaLabelsRead    = "trust_safety:media_labels.read"
	permTrustKeywordFiltersRead = "trust_safety:keyword_filters.read"
	permTrustAuditRead          = "trust_safety:audit.read"
)

const (
	opTrustAppealDecide    = "trust.appeal.review"
	opTrustGrievanceUpdate = "trust.grievance.update"
)

// Outcomes, as trust-safety normalises them (lower-case, trimmed). The
// outcomes that need a fresh step-up: overturning an appeal, and closing a
// grievance as resolved or rejected.
var (
	trustAppealStatuses    = map[string]bool{"under_review": true, "upheld": true, "overturned": true}
	trustAppealStepUp      = map[string]bool{"overturned": true}
	trustGrievanceStatuses = map[string]bool{"": true, "acknowledged": true, "resolved": true, "rejected": true}
	trustGrievanceStepUp   = map[string]bool{"resolved": true, "rejected": true}
)

// TrustRoutes is the Trust & safety route table under /v1/admin/trust.
var TrustRoutes = []productRoute{
	{method: http.MethodGet, path: "/stats", operation: "trust.stats", permission: permTrustStatsRead},
	{method: http.MethodGet, path: "/reports", operation: "trust.reports.list", permission: permTrustReportsRead},
	{method: http.MethodGet, path: "/reports/:id", operation: "trust.report.read", permission: permTrustReportsRead, targetType: "trust_report"},
	{method: http.MethodPatch, path: "/reports/:id", operation: "trust.report.update", permission: permTrustReportsAct, targetType: "trust_report"},
	{method: http.MethodGet, path: "/appeals", operation: "trust.appeals.list", permission: permTrustAppealsRead,
		alternatives: []string{permTrustAppealsRead, permTrustAppealsAct}},
	// Step-up decided from the requested outcome: overturned.
	{method: http.MethodPatch, path: "/appeals/:id", operation: opTrustAppealDecide, permission: permTrustAppealsAct, targetType: "trust_appeal"},
	{method: http.MethodGet, path: "/grievances", operation: "trust.grievances.list", permission: permTrustGrievancesRead,
		alternatives: []string{permTrustGrievancesRead, permTrustGrievancesAct}},
	{method: http.MethodGet, path: "/grievances/:id", operation: "trust.grievance.read", permission: permTrustGrievancesRead, targetType: "trust_grievance",
		alternatives: []string{permTrustGrievancesRead, permTrustGrievancesAct}},
	{method: http.MethodGet, path: "/grievances/:id/history", operation: "trust.grievance.history", permission: permTrustGrievancesRead, targetType: "trust_grievance",
		alternatives: []string{permTrustGrievancesRead, permTrustGrievancesAct, permTrustAuditRead}},
	// Step-up decided from the requested status: resolved or rejected.
	{method: http.MethodPatch, path: "/grievances/:id", operation: opTrustGrievanceUpdate, permission: permTrustGrievancesAct, targetType: "trust_grievance"},
	{method: http.MethodGet, path: "/strikes/:userId", operation: "trust.strikes.read", permission: permTrustStrikesRead, targetType: "user",
		alternatives: []string{permTrustStrikesRead, permTrustStrikesManage}},
	{method: http.MethodGet, path: "/verification-requests", operation: "trust.verification_requests.list", permission: permTrustVerificationReview, stepUp: true},
	{method: http.MethodGet, path: "/media-labels/:mediaId", operation: "trust.media_labels.read", permission: permTrustMediaLabelsRead, targetType: "media"},
	{method: http.MethodGet, path: "/keyword-filters", operation: "trust.keyword_filters.list", permission: permTrustKeywordFiltersRead},
}

type trustAppealBody struct {
	Status string `json:"status"`
	Note   string `json:"note"`
}

type trustGrievanceBody struct {
	Status          string  `json:"status,omitempty"`
	ResolutionNotes *string `json:"resolution_notes,omitempty"`
	AssignedTo      *string `json:"assigned_to,omitempty"`
}

func normaliseOutcome(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// RegisterTrustRoutes adds the Trust & safety dashboard under /v1/admin/trust.
// The outcome-dependent gates are decided from the body before the gate's
// step-up check and before trust-safety is called; the body forwarded is the
// normalised one the gate judged.
func (h *Handler) RegisterTrustRoutes(r *gin.Engine) {
	p := product{app: "trust_safety", label: "Trust & safety", prefix: "/v1/admin/trust", client: h.trust}

	routes := make([]productRoute, len(TrustRoutes))
	copy(routes, TrustRoutes)
	for i := range routes {
		switch routes[i].operation {
		case opTrustAppealDecide:
			routes[i].decide = func(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
				b, err := readTrustAppeal(c)
				if err != nil {
					return Decision{}, err
				}
				return Decision{StepUp: trustAppealStepUp[b.Status], Audit: map[string]any{"status": b.Status}}, nil
			}
		case opTrustGrievanceUpdate:
			routes[i].decide = func(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
				b, err := readTrustGrievance(c)
				if err != nil {
					return Decision{}, err
				}
				return Decision{StepUp: trustGrievanceStepUp[b.Status], Audit: map[string]any{"status": b.Status}}, nil
			}
		}
	}

	special := map[string]gin.HandlerFunc{
		opTrustAppealDecide: func(c *gin.Context) {
			id, ok := trustID(c)
			if !ok {
				return
			}
			b, err := readTrustAppeal(c)
			if err != nil { // the gate already refused this
				return
			}
			info := auditFrom(c)
			info.targetID, info.reason = id, b.Note
			h.productCall(c, p, service.ProductRequest{Method: http.MethodPatch, Path: "/appeals/" + id, Body: b}, false)
		},
		opTrustGrievanceUpdate: func(c *gin.Context) {
			id, ok := trustID(c)
			if !ok {
				return
			}
			b, err := readTrustGrievance(c)
			if err != nil {
				return
			}
			info := auditFrom(c)
			info.targetID = id
			if b.ResolutionNotes != nil {
				info.reason = *b.ResolutionNotes
			}
			h.productCall(c, p, service.ProductRequest{Method: http.MethodPatch, Path: "/grievances/" + id, Body: b}, false)
		},
	}
	h.registerProduct(r, p, routes, special)
}

func trustID(c *gin.Context) (string, bool) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid id", nil)
		return "", false
	}
	return id.String(), true
}

func readTrustAppeal(c *gin.Context) (trustAppealBody, error) {
	raw, _, err := jsonBody(c)
	if err != nil {
		return trustAppealBody{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	var b trustAppealBody
	_ = json.Unmarshal(raw, &b)
	b.Status = normaliseOutcome(b.Status)
	if !trustAppealStatuses[b.Status] {
		return b, badRequest(CodeInvalidAction, "status must be under_review, upheld or overturned")
	}
	return b, nil
}

func readTrustGrievance(c *gin.Context) (trustGrievanceBody, error) {
	raw, _, err := jsonBody(c)
	if err != nil {
		return trustGrievanceBody{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	var b trustGrievanceBody
	_ = json.Unmarshal(raw, &b)
	b.Status = normaliseOutcome(b.Status)
	if !trustGrievanceStatuses[b.Status] {
		return b, badRequest(CodeInvalidAction, "status must be acknowledged, resolved or rejected")
	}
	return b, nil
}
