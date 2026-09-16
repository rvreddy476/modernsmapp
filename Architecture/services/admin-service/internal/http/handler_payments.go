package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Payments permissions, exactly as payments-service checks them
// (payments-service/internal/http/admin_token.go).
const (
	permPayStatsRead          = "payments:stats.read"
	permPayRefundsRead        = "payments:refunds.read"
	permPayRefundIssue        = "payments:refund.issue"
	permPayIntentsRead        = "payments:intents.read"
	permPayReconciliationRead = "payments:reconciliation.read"
	permPayApplicationsRead   = "payments:applications.read"
	permPayApplicationsManage = "payments:applications.manage"
	permPayAuditRead          = "payments:audit.read"
)

const (
	paymentsAuditApp    = "payments"
	opPayRefundResolve  = "payments.refund.resolve"
	paymentsQueryAppID  = "application_id"
	ctxPaymentsApp      = "admin.payments.application"
	ctxPaymentsPaise    = "admin.payments.refund_paise"
	CodeApplicationID   = "INVALID_APPLICATION_ID"
	CodeApplicationNeed = "APPLICATION_ID_REQUIRED"
	CodeApplicationDeny = "APPLICATION_NOT_PERMITTED"
)

// Refund resolutions payments accepts (postgres.IsValidResolution there).
const (
	resolutionTestData         = "test_data"
	resolutionRefundedManually = "refunded_manually"
	resolutionWrittenOff       = "written_off"
)

// paymentsApplications maps an application-scoped admin's app to its payments
// application key, as payments' registry seeds them
// (payments-service/database/migrations/010_applications.sql, 011_dating_application.sql).
var paymentsApplications = []struct{ app, application string }{
	{"commerce", "mstore"},
	{"food", "feast"},
	{"dating", "dating"},
}

// paymentsApplicationPattern is payments' application key shape
// (shared/paymentsclient ValidateApplicationID).
var paymentsApplicationPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// confinedPaymentsPermission is how an admin scoped to one product app holds a
// payments view of THAT app only: payments:<resource>.<action> becomes
// <app>:payments_<resource>.<action>, e.g. food:payments_refunds.read.
//
// Why a product-app permission: identity resolves a grant into permissions of
// the grant's app, so an app-scoped Feast role can only ever hold "food:…".
// A "payments:…" permission comes from a platform-wide grant or a grant on
// the payments app itself — both see every application.
func confinedPaymentsPermission(app, perm string) string {
	return app + ":payments_" + strings.TrimPrefix(perm, paymentsAuditApp+":")
}

// PaymentsRoutes is the route table under /v1/admin/payments. Every route is
// confinable to one application.
var PaymentsRoutes = []productRoute{
	{method: http.MethodGet, path: "/stats", operation: "payments.stats", permission: permPayStatsRead},
	{method: http.MethodGet, path: "/refunds/needs-attention", operation: "payments.refunds.needs_attention", permission: permPayRefundsRead},
	{method: http.MethodGet, path: "/refunds/:commandId", operation: "payments.refund.read", permission: permPayRefundsRead, targetType: "payments_refund_command"},
	// Step-up always; two-person for refunded_manually and written_off AT OR
	// ABOVE the refund threshold (test_data moves no money).
	{method: http.MethodPost, path: "/refunds/:commandId/resolve", operation: opPayRefundResolve, permission: permPayRefundIssue, stepUp: true, mayTwoPerson: true, targetType: "payments_refund_command"},
	{method: http.MethodGet, path: "/intents", operation: "payments.intents.list", permission: permPayIntentsRead},
	{method: http.MethodGet, path: "/intents/:id", operation: "payments.intent.read", permission: permPayIntentsRead, targetType: "payments_intent"},
	{method: http.MethodGet, path: "/reconciliation", operation: "payments.reconciliation", permission: permPayReconciliationRead},
	{method: http.MethodGet, path: "/applications", operation: "payments.applications.list", permission: permPayApplicationsRead},
	{method: http.MethodPatch, path: "/applications/:applicationId", operation: "payments.application.update", permission: permPayApplicationsManage, stepUp: true, targetType: "payments_application"},
	{method: http.MethodGet, path: "/audit/payments", operation: "payments.audit.payments", permission: permPayAuditRead},
	{method: http.MethodGet, path: "/audit/applications", operation: "payments.audit.applications", permission: permPayAuditRead},
}

// RegisterPaymentsRoutes adds the Payments dashboard under /v1/admin/payments.
//
//	step-up      refund resolve (every resolution), application registry PATCH
//	two-person   refund resolve as refunded_manually or written_off AT OR ABOVE
//	             ADMIN_REFUND_TWO_PERSON_THRESHOLD_PAISE
//	confinement  every route: see paymentsScopeFor
func (h *Handler) RegisterPaymentsRoutes(r *gin.Engine) {
	p := product{app: paymentsAuditApp, label: "Payments", prefix: "/v1/admin/payments", client: h.payments}

	routes := make([]productRoute, len(PaymentsRoutes))
	copy(routes, PaymentsRoutes)
	special := map[string]gin.HandlerFunc{}
	for i := range routes {
		routes[i].admitsHeldAs = true
		if routes[i].operation == opPayRefundResolve {
			routes[i].decide = h.paymentsResolveDecision(h.refundThresholdPaise)
			special[opPayRefundResolve] = h.paymentsResolve(p)
			continue
		}
		routes[i].decide = paymentsDecision(routes[i].permission)
		special[routes[i].operation] = h.paymentsForward(p, routes[i])
	}

	h.approvals.Register(paymentsAuditApp, opPayRefundResolve, func(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
		var pl paymentsResolvePayload
		if err := json.Unmarshal(payload, &pl); err != nil {
			return approvals.Result{Err: err}
		}
		pr, err := paymentsResolveRequest(pl)
		if err != nil {
			return approvals.Result{Err: err}
		}
		pr.Permission, pr.Actor = permPayRefundIssue, actor
		resp, err := h.payments.Do(ctx, pr)
		return approvals.Result{Data: resp.Body, Status: resp.Status, Err: err}
	})
	h.registerProduct(r, p, routes, special)
}

// paymentsScope is who the admin is on payments for this request.
type paymentsScope struct {
	// application is sent as ?application_id= ("" = every application).
	application string
	// heldAs is the confined product-app permission that admitted the admin,
	// "" when they hold the payments permission itself.
	heldAs string
}

// paymentsScopeFor derives the application confinement from the admin's
// resolved permissions, before any call:
//
//   - holds payments:<x> (platform-wide or payments-app grant): sees every
//     application; a client application_id narrows and is passed through.
//   - holds <app>:payments_<x> for one or more product apps only: confined.
//     No client id → the one allowed application (or 400 when several);
//     a client id outside the allowed applications → 403, never widened.
//   - holds neither: an empty scope; the gate refuses the permission.
//
// More than one application_id in the query is refused outright.
func paymentsScopeFor(c *gin.Context, perms adminauth.Permissions, perm string) (paymentsScope, bool, error) {
	values := c.QueryArray(paymentsQueryAppID)
	if len(values) > 1 {
		return paymentsScope{}, false, badRequest(CodeApplicationID, "application_id may be given once")
	}
	client := ""
	if len(values) == 1 {
		client = strings.TrimSpace(values[0])
		if !paymentsApplicationPattern.MatchString(client) {
			return paymentsScope{}, false, badRequest(CodeApplicationID, "application_id is not well formed")
		}
	}
	if perms.Has(perm) {
		return paymentsScope{application: client}, true, nil
	}
	type allowed struct{ application, heldAs string }
	var list []allowed
	for _, m := range paymentsApplications {
		if cp := confinedPaymentsPermission(m.app, perm); perms.Has(cp) {
			list = append(list, allowed{m.application, cp})
		}
	}
	if len(list) == 0 {
		return paymentsScope{}, false, nil
	}
	if client == "" {
		if len(list) > 1 {
			return paymentsScope{}, false, badRequest(CodeApplicationNeed, "application_id is required: you may act on more than one application")
		}
		return paymentsScope{application: list[0].application, heldAs: list[0].heldAs}, true, nil
	}
	for _, a := range list {
		if a.application == client {
			return paymentsScope{application: a.application, heldAs: a.heldAs}, true, nil
		}
	}
	return paymentsScope{}, false, &DecisionError{Status: http.StatusForbidden, Code: CodeApplicationDeny,
		Message: "You may not act on application " + client}
}

func scopeAudit(s paymentsScope) map[string]any {
	audit := map[string]any{}
	if s.application != "" {
		audit["application_id"] = s.application
	}
	if s.heldAs != "" {
		audit["application_confined"] = true
	}
	return audit
}

// paymentsDecision confines one read or write; the gate then checks the
// payments permission or the confined one it admitted by.
func paymentsDecision(perm string) func(*gin.Context, adminauth.Permissions) (Decision, error) {
	return func(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
		s, ok, err := paymentsScopeFor(c, perms, perm)
		if err != nil || !ok {
			return Decision{}, err
		}
		c.Set(ctxPaymentsApp, s.application)
		return Decision{HeldAs: s.heldAs, Audit: scopeAudit(s)}, nil
	}
}

// paymentsApplicationFrom is the application the decision settled on; ok is
// false when no decision ran (never forward then).
func paymentsApplicationFrom(c *gin.Context) (string, bool) {
	v, ok := c.Get(ctxPaymentsApp)
	if !ok {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

// paymentsQuery is the console query with application_id replaced by the
// decided one: a confined admin's is always sent.
func paymentsQuery(c *gin.Context, application string) url.Values {
	q := c.Request.URL.Query()
	q.Del(paymentsQueryAppID)
	if application != "" {
		q.Set(paymentsQueryAppID, application)
	}
	return q
}

func (h *Handler) paymentsForward(p product, rt productRoute) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		application, ok := paymentsApplicationFrom(c)
		if !ok {
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared", nil)
			return
		}
		path, last, err := productPath(c, rt.path)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		info := auditFrom(c)
		if rt.targetType != "" {
			info.targetType, info.targetID = rt.targetType, last
		}
		pr := service.ProductRequest{Method: rt.method, Path: path, Query: paymentsQuery(c, application)}
		if rt.method != http.MethodGet {
			raw, fields, err := jsonBody(c)
			if err != nil {
				api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be JSON", nil)
				return
			}
			pr.RawBody = raw
			info.reason = stringField(fields, "reason", "note")
		}
		h.productCall(c, p, pr, false)
	}
}

// --- refund resolve ---

type paymentsResolveBody struct {
	Resolution string `json:"resolution"`
	Note       string `json:"note"`
}

func readPaymentsResolveBody(c *gin.Context) (paymentsResolveBody, error) {
	raw, _, err := jsonBody(c)
	if err != nil || len(raw) == 0 {
		return paymentsResolveBody{}, badRequest(CodeInvalidBody, "The request body must be JSON with resolution and note")
	}
	var b paymentsResolveBody
	if json.Unmarshal(raw, &b) != nil {
		return paymentsResolveBody{}, badRequest(CodeInvalidBody, "The resolve body is malformed")
	}
	b.Resolution = strings.TrimSpace(b.Resolution)
	switch b.Resolution {
	case resolutionTestData, resolutionRefundedManually, resolutionWrittenOff:
	default:
		return b, badRequest(CodeInvalidAction, "resolution must be refunded_manually, written_off or test_data")
	}
	if strings.TrimSpace(b.Note) == "" {
		return b, badRequest(CodeReasonRequired, "A note is required to resolve a refund")
	}
	return b, nil
}

// paymentsResolveDecision decides the gate from the resolution, before any
// resolve call: test_data is step-up only; refunded_manually and written_off
// are two-person at or above the threshold, judged by the refund's stored
// amount (read first, confined like the resolve); an amount that cannot be
// established counts as at or above.
func (h *Handler) paymentsResolveDecision(threshold int64) func(*gin.Context, adminauth.Permissions) (Decision, error) {
	return func(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
		s, ok, err := paymentsScopeFor(c, perms, permPayRefundIssue)
		if err != nil || !ok {
			return Decision{}, err // the gate refuses; nothing is looked up
		}
		id, err := uuid.Parse(c.Param("commandId"))
		if err != nil {
			return Decision{}, badRequest(CodeInvalidID, "Invalid refund command id")
		}
		b, err := readPaymentsResolveBody(c)
		if err != nil {
			return Decision{}, err
		}
		c.Set(ctxPaymentsApp, s.application)
		audit := scopeAudit(s)
		audit["resolution"] = b.Resolution
		if b.Resolution == resolutionTestData {
			return Decision{HeldAs: s.heldAs, Audit: audit}, nil
		}
		audit["refund_threshold_paise"] = threshold
		paise, known := h.paymentsRefundPaise(c, perms, s, id)
		if !known {
			audit["amount_basis"] = "unknown"
			return Decision{HeldAs: s.heldAs, TwoPerson: true, Audit: audit}, nil
		}
		c.Set(ctxPaymentsPaise, paise)
		audit["amount_paise"] = paise
		return Decision{HeldAs: s.heldAs, TwoPerson: refundNeedsTwoPerson(paise, threshold), Audit: audit}, nil
	}
}

// paymentsRefundPaise reads the refund command's amount with a
// payments:refunds.read token, only when the admin may read it in the same
// scope (the payments permission, or the confined one for the same app).
func (h *Handler) paymentsRefundPaise(c *gin.Context, perms adminauth.Permissions, s paymentsScope, id uuid.UUID) (int64, bool) {
	mayRead := perms.Has(permPayRefundsRead)
	if s.heldAs != "" {
		mayRead = perms.Has(confinedPaymentsPermission(adminauth.AppOf(s.heldAs), permPayRefundsRead))
	}
	if !mayRead {
		return 0, false
	}
	pr := service.ProductRequest{Method: http.MethodGet, Path: "/refunds/" + id.String(), Permission: permPayRefundsRead, Actor: actorFrom(c)}
	if s.application != "" {
		pr.Query = url.Values{paymentsQueryAppID: {s.application}}
	}
	resp, err := h.payments.Do(productContext(c), pr)
	if err != nil || resp.Status != http.StatusOK {
		return 0, false
	}
	var ref struct {
		AmountMinor int64  `json:"amount_minor"`
		Currency    string `json:"currency"`
	}
	if json.Unmarshal(envelopeData(resp.Body), &ref) != nil || ref.AmountMinor <= 0 || !strings.EqualFold(ref.Currency, "INR") {
		return 0, false
	}
	return ref.AmountMinor, true
}

type paymentsResolvePayload struct {
	CommandID     string `json:"command_id"`
	Resolution    string `json:"resolution"`
	Note          string `json:"note"`
	ApplicationID string `json:"application_id,omitempty"`
	// AmountPaise is the amount the threshold was judged on (summary only).
	AmountPaise int64 `json:"amount_paise,omitempty"`
}

func paymentsResolveRequest(p paymentsResolvePayload) (service.ProductRequest, error) {
	id, err := uuid.Parse(p.CommandID)
	if err != nil {
		return service.ProductRequest{}, err
	}
	pr := service.ProductRequest{
		Method: http.MethodPost, Path: "/refunds/" + id.String() + "/resolve",
		Body: paymentsResolveBody{Resolution: p.Resolution, Note: p.Note},
	}
	if p.ApplicationID != "" {
		pr.Query = url.Values{paymentsQueryAppID: {p.ApplicationID}}
	}
	return pr, nil
}

func (h *Handler) paymentsResolve(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		application, ok := paymentsApplicationFrom(c)
		if !ok {
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared", nil)
			return
		}
		id, err := uuid.Parse(c.Param("commandId"))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid refund command id", nil)
			return
		}
		b, _ := readPaymentsResolveBody(c) // validated by the decision
		payload := paymentsResolvePayload{CommandID: id.String(), Resolution: b.Resolution, Note: b.Note, ApplicationID: application}
		if v, ok := c.Get(ctxPaymentsPaise); ok {
			payload.AmountPaise, _ = v.(int64)
		}
		req, _ := effectiveRequirement(c)
		if req.TwoPerson {
			h.submitTwoPerson(c, "payments_refund_command", id.String(), b.Note, payload)
			return
		}
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = "payments_refund_command", id.String(), b.Note
		pr, err := paymentsResolveRequest(payload)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidBody, "Invalid request", nil)
			return
		}
		h.productCall(c, p, pr, false)
	}
}
