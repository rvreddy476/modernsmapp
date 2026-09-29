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
	// Strikes (trust-safety strikes_handler.go): both need
	// trust_safety:strikes.manage and a fresh step-up; the audit row names
	// the struck user as its target.
	opTrustStrikeIssue = "trust.strike.issue"
	opTrustStrikeVoid  = "trust.strike.void"
)

// Strike severities, as trust-safety validates them (lower-case, trimmed).
var trustStrikeSeverities = map[string]bool{"warning": true, "strike": true, "severe_strike": true}

// maxStrikeIdempotencyKey is trust-safety's limit on idempotency_key.
const maxStrikeIdempotencyKey = 200

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
	// Issuing a strike: the body names the user (audit target) and carries the
	// console's idempotency_key, which trust-safety replays as 200.
	{method: http.MethodPost, path: "/strikes", operation: opTrustStrikeIssue, permission: permTrustStrikesManage, stepUp: true},
	// Voiding never deletes: trust-safety keeps the row and stops counting it.
	{method: http.MethodPost, path: "/strikes/:userId/void", operation: opTrustStrikeVoid, permission: permTrustStrikesManage, stepUp: true, targetType: "user"},
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

// trustStrikeIssueBody is what the console sends to issue a strike, forwarded
// normalised. Optional ids stay pointers so an absent field is not sent as "".
type trustStrikeIssueBody struct {
	UserID         string  `json:"user_id"`
	Reason         string  `json:"reason"`
	Severity       string  `json:"severity"`
	IdempotencyKey string  `json:"idempotency_key"`
	ContentType    string  `json:"content_type,omitempty"`
	ContentID      *string `json:"content_id,omitempty"`
	CaseID         *string `json:"case_id,omitempty"`
	StrikeGroup    string  `json:"strike_group,omitempty"`
}

type trustStrikeVoidBody struct {
	StrikeID string `json:"strike_id"`
	Reason   string `json:"reason"`
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
		case opTrustStrikeIssue:
			// Judged before the gate so a malformed strike is refused (and
			// audited as denied) without a step-up prompt or a product call.
			routes[i].decide = func(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
				b, err := readTrustStrikeIssue(c)
				if err != nil {
					return Decision{}, err
				}
				audit := map[string]any{"severity": b.Severity, "idempotency_key": b.IdempotencyKey}
				if b.ContentID != nil {
					audit["content_id"] = *b.ContentID
				}
				if b.CaseID != nil {
					audit["case_id"] = *b.CaseID
				}
				return Decision{Audit: audit}, nil
			}
		case opTrustStrikeVoid:
			routes[i].decide = func(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
				b, err := readTrustStrikeVoid(c)
				if err != nil {
					return Decision{}, err
				}
				return Decision{Audit: map[string]any{"strike_id": b.StrikeID}}, nil
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
		// Trust-safety's answer is passed through as it is: 201 on a new
		// strike, 200 on a replay of the same idempotency_key, 404 when the
		// strike is not that user's, 410 should the retired route ever be hit.
		opTrustStrikeIssue: func(c *gin.Context) {
			b, err := readTrustStrikeIssue(c)
			if err != nil { // the gate already refused this
				return
			}
			info := auditFrom(c)
			info.targetType, info.targetID, info.reason = "user", b.UserID, b.Reason
			h.productCall(c, p, service.ProductRequest{Method: http.MethodPost, Path: "/strikes", Body: b}, false)
		},
		opTrustStrikeVoid: func(c *gin.Context) {
			userID, err := uuid.Parse(c.Param("userId"))
			if err != nil {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid user id", nil)
				return
			}
			b, err := readTrustStrikeVoid(c)
			if err != nil {
				return
			}
			info := auditFrom(c)
			info.reason = b.Reason
			h.productCall(c, p, service.ProductRequest{Method: http.MethodPost, Path: "/strikes/" + userID.String() + "/void", Body: b}, false)
		},
	}
	h.registerProduct(r, p, routes, special)
}

// readTrustStrikeIssue validates an issue body the way trust-safety will, so
// the console hears the exact refusal without a round trip and the audit row
// carries a normalised severity. The idempotency_key is the console's, minted
// once per click and reused on its retry; it is required, as trust-safety
// requires it.
func readTrustStrikeIssue(c *gin.Context) (trustStrikeIssueBody, error) {
	raw, _, err := jsonBody(c)
	if err != nil {
		return trustStrikeIssueBody{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	var b trustStrikeIssueBody
	_ = json.Unmarshal(raw, &b)
	b.UserID = strings.TrimSpace(b.UserID)
	b.Reason = strings.TrimSpace(b.Reason)
	b.Severity = normaliseOutcome(b.Severity)
	b.IdempotencyKey = strings.TrimSpace(b.IdempotencyKey)
	b.ContentType = strings.TrimSpace(b.ContentType)
	b.StrikeGroup = strings.TrimSpace(b.StrikeGroup)
	if id, err := uuid.Parse(b.UserID); err != nil || id == uuid.Nil {
		return b, badRequest(CodeInvalidID, "user_id must be the struck user's id (a UUID)")
	}
	if b.Reason == "" {
		return b, badRequest(CodeInvalidAction, "reason is required")
	}
	if !trustStrikeSeverities[b.Severity] {
		return b, badRequest(CodeInvalidAction, "severity must be warning, strike or severe_strike")
	}
	if b.IdempotencyKey == "" || len(b.IdempotencyKey) > maxStrikeIdempotencyKey {
		return b, badRequest(CodeIdempotencyKeyRequired, "idempotency_key is required (at most 200 characters)")
	}
	for _, opt := range []struct {
		field string
		id    **string
	}{{"content_id", &b.ContentID}, {"case_id", &b.CaseID}} {
		if *opt.id == nil {
			continue
		}
		v := strings.TrimSpace(**opt.id)
		if v == "" {
			*opt.id = nil
			continue
		}
		if _, err := uuid.Parse(v); err != nil {
			return b, badRequest(CodeInvalidID, opt.field+" must be a UUID when given")
		}
		*opt.id = &v
	}
	return b, nil
}

func readTrustStrikeVoid(c *gin.Context) (trustStrikeVoidBody, error) {
	raw, _, err := jsonBody(c)
	if err != nil {
		return trustStrikeVoidBody{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	var b trustStrikeVoidBody
	_ = json.Unmarshal(raw, &b)
	b.StrikeID = strings.TrimSpace(b.StrikeID)
	b.Reason = strings.TrimSpace(b.Reason)
	if id, err := uuid.Parse(b.StrikeID); err != nil || id == uuid.Nil {
		return b, badRequest(CodeInvalidID, "strike_id must be the strike's id (a UUID)")
	}
	if b.Reason == "" {
		return b, badRequest(CodeInvalidAction, "reason is required")
	}
	return b, nil
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
