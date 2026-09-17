package http

import (
	"net/http"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/gin-gonic/gin"
)

// Q&A permissions, exactly as qa-service checks them
// (qa-service/internal/http/admin_token.go, AdminRoutes). Those marked NEW
// are not yet in identity's catalogue; see the report.
const (
	permQAStatsRead         = "qa:stats.read"   // NEW
	permQAReportsRead       = "qa:reports.read" // NEW
	permQAReportsAct        = "qa:reports.act"
	permQAQuestionsModerate = "qa:questions.moderate"
	permQAQuestionsMerge    = "qa:questions.merge" // NEW
	permQAAnswersModerate   = "qa:answers.moderate"
	permQACommentsModerate  = "qa:comments.moderate" // NEW
	permQAAuditRead         = "qa:audit.read"
)

const (
	qaAuditApp      = "qa"
	qaPrefix        = "/v1/admin/qa"
	maxReasonLength = 2000
)

// QARoutes is the route table under /v1/admin/qa, one for one with
// qa-service's AdminRoutes. Every write needs a reason (qa-service refuses
// one without; admin-service refuses first, so nothing is signed for a call
// that cannot succeed). Merge is irreversible: step-up.
var QARoutes = []productRoute{
	{method: http.MethodGet, path: "/stats", operation: "qa.stats", permission: permQAStatsRead},
	{method: http.MethodGet, path: "/reports", operation: "qa.reports.list", permission: permQAReportsRead},
	{method: http.MethodGet, path: "/reports/:reportId", operation: "qa.report.read", permission: permQAReportsRead, targetType: "qa_report"},
	{method: http.MethodPost, path: "/reports/:reportId/resolve", operation: "qa.report.resolve", permission: permQAReportsAct, targetType: "qa_report"},
	{method: http.MethodPost, path: "/reports/:reportId/dismiss", operation: "qa.report.dismiss", permission: permQAReportsAct, targetType: "qa_report"},
	{method: http.MethodPost, path: "/questions/:questionId/hide", operation: "qa.question.hide", permission: permQAQuestionsModerate, targetType: "qa_question"},
	{method: http.MethodPost, path: "/questions/:questionId/lock", operation: "qa.question.lock", permission: permQAQuestionsModerate, targetType: "qa_question"},
	{method: http.MethodPost, path: "/questions/:questionId/duplicate", operation: "qa.question.duplicate", permission: permQAQuestionsModerate, targetType: "qa_question"},
	{method: http.MethodPost, path: "/questions/:questionId/merge", operation: "qa.question.merge", permission: permQAQuestionsMerge, stepUp: true, targetType: "qa_question"},
	{method: http.MethodPost, path: "/answers/:answerId/hide", operation: "qa.answer.hide", permission: permQAAnswersModerate, targetType: "qa_answer"},
	{method: http.MethodPost, path: "/comments/:commentId/hide", operation: "qa.comment.hide", permission: permQACommentsModerate, targetType: "qa_comment"},
	{method: http.MethodGet, path: "/actions", operation: "qa.audit.list", permission: permQAAuditRead},
}

// RegisterQARoutes adds the Q&A dashboard under /v1/admin/qa.
//
//	step-up   question merge
//	reason    required on every write, checked before the call
func (h *Handler) RegisterQARoutes(r *gin.Engine) {
	p := product{app: qaAuditApp, label: "Q&A", prefix: qaPrefix, client: h.qa}
	routes := make([]productRoute, len(QARoutes))
	copy(routes, QARoutes)
	for i := range routes {
		if routes[i].method != http.MethodGet {
			routes[i].decide = requireReasonDecision
		}
	}
	h.registerProduct(r, p, routes, nil)
}

// requireReasonDecision refuses a write whose body has no reason (or an
// over-long one) before the gate's checks and before any product call.
func requireReasonDecision(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
	raw, fields, err := jsonBody(c)
	if err != nil || len(raw) == 0 {
		return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON with reason")
	}
	reason := strings.TrimSpace(stringField(fields, "reason"))
	if reason == "" {
		return Decision{}, badRequest(CodeReasonRequired, "reason is required")
	}
	if len(reason) > maxReasonLength {
		return Decision{}, badRequest(CodeInvalidBody, "reason must be at most 2000 characters")
	}
	return Decision{}, nil
}
