package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Monetization permissions, exactly as monetization-service checks them
// (monetization-service/internal/http/admin_token.go). Those marked NEW there
// are not yet in identity's catalogue; see the report.
const (
	permMonStatsRead       = "monetization:stats.read"
	permMonFraudReview     = "monetization:fraud.review"
	permMonWalletFreeze    = "monetization:wallet.freeze"
	permMonWalletUnfreeze  = "monetization:wallet.unfreeze"
	permMonWalletRebuild   = "monetization:wallet.rebuild"
	permMonFundRead        = "monetization:fund.read"
	permMonFundRates       = "monetization:fund.rates"
	permMonCreatorsSuspend = "monetization:creators.suspend"
	permMonFundSettle      = "monetization:fund.settle"
	permMonFundReverse     = "monetization:fund.reverse"
	permMonFundBudget      = "monetization:fund.budget"
	permMonDisputesRead    = "monetization:disputes.read"
	permMonDisputesAct     = "monetization:disputes.act"
	permMonRefundIssue     = "monetization:refund.issue"
	permMonPayoutsRead     = "monetization:payouts.read"
	permMonAuditRead       = "monetization:audit.read"
)

// Monetization operations that run (or may run) through two-person approval.
const (
	opMonRatesSet        = "monetization.fund.rates.set"
	opMonBandsSet        = "monetization.fund.quality_bands.set"
	opMonBudgetSet       = "monetization.fund.budget.set"
	opMonSettleDay       = "monetization.fund.settle"
	opMonSettlePeriod    = "monetization.fund.settle_period"
	opMonSettleCreator   = "monetization.fund.creator.settle_period"
	opMonEarningReverse  = "monetization.fund.earning.reverse"
	opMonRefundIssue     = "monetization.refund.issue"
	monetizationAuditApp = "monetization"
)

// CodeMonetizationNotLaunched is monetization's answer on every route while
// MONETIZATION_WRITES_ENABLED is off. admin-service surfaces it as a state.
const CodeMonetizationNotLaunched = "MONETIZATION_NOT_LAUNCHED"

// MonetizationRoutes is the route table under /v1/admin/monetization. Paths
// mirror monetization's token family one for one.
var MonetizationRoutes = []productRoute{
	{method: http.MethodGet, path: "/stats", operation: "monetization.stats", permission: permMonStatsRead},

	{method: http.MethodGet, path: "/fraud-reviews", operation: "monetization.fraud_reviews.list", permission: permMonFraudReview},
	{method: http.MethodPatch, path: "/fraud-reviews/:id", operation: "monetization.fraud_review.resolve", permission: permMonFraudReview, stepUp: true, targetType: "monetization_fraud_review"},

	{method: http.MethodPost, path: "/wallet/:userId/freeze", operation: "monetization.wallet.freeze", permission: permMonWalletFreeze, stepUp: true, targetType: "monetization_wallet"},
	{method: http.MethodPost, path: "/wallet/:userId/unfreeze", operation: "monetization.wallet.unfreeze", permission: permMonWalletUnfreeze, stepUp: true, targetType: "monetization_wallet"},
	{method: http.MethodPost, path: "/wallet/:userId/rebuild", operation: "monetization.wallet.rebuild", permission: permMonWalletRebuild, stepUp: true, targetType: "monetization_wallet"},

	{method: http.MethodGet, path: "/creator-fund/rates", operation: "monetization.fund.rates.list", permission: permMonFundRead},
	{method: http.MethodPut, path: "/creator-fund/rates", operation: opMonRatesSet, permission: permMonFundRates, stepUp: true, twoPerson: true, targetType: "monetization_fund_rate"},
	{method: http.MethodPut, path: "/creator-fund/quality-bands", operation: opMonBandsSet, permission: permMonFundRates, stepUp: true, twoPerson: true, targetType: "monetization_fund_quality_band"},
	{method: http.MethodPost, path: "/creator-fund/:userId/suspend", operation: "monetization.fund.creator.suspend", permission: permMonCreatorsSuspend, stepUp: true, targetType: "monetization_creator"},
	{method: http.MethodPost, path: "/creator-fund/:userId/unsuspend", operation: "monetization.fund.creator.unsuspend", permission: permMonCreatorsSuspend, stepUp: true, targetType: "monetization_creator"},
	{method: http.MethodPost, path: "/creator-fund/settle", operation: opMonSettleDay, permission: permMonFundSettle, stepUp: true, twoPerson: true, targetType: "monetization_fund_day"},
	{method: http.MethodPost, path: "/creator-fund/settle-period", operation: opMonSettlePeriod, permission: permMonFundSettle, stepUp: true, twoPerson: true, targetType: "monetization_fund_period"},
	{method: http.MethodPost, path: "/creator-fund/:userId/settle-period", operation: opMonSettleCreator, permission: permMonFundSettle, stepUp: true, twoPerson: true, targetType: "monetization_creator"},
	{method: http.MethodPost, path: "/creator-fund/earnings/:id/reverse", operation: opMonEarningReverse, permission: permMonFundReverse, stepUp: true, twoPerson: true, targetType: "monetization_fund_earning"},
	{method: http.MethodGet, path: "/creator-fund/earnings/:id", operation: "monetization.fund.earning.read", permission: permMonFundRead, targetType: "monetization_fund_earning"},
	{method: http.MethodGet, path: "/creator-fund/budgets", operation: "monetization.fund.budgets.list", permission: permMonFundRead},
	{method: http.MethodPut, path: "/creator-fund/budgets", operation: opMonBudgetSet, permission: permMonFundBudget, stepUp: true, twoPerson: true, targetType: "monetization_fund_budget"},

	{method: http.MethodGet, path: "/disputes", operation: "monetization.disputes.list", permission: permMonDisputesRead},
	{method: http.MethodPatch, path: "/disputes/:id", operation: "monetization.dispute.act", permission: permMonDisputesAct, stepUp: true, targetType: "monetization_dispute"},
	// Step-up always; two-person at or above the refund threshold.
	{method: http.MethodPost, path: "/refunds", operation: opMonRefundIssue, permission: permMonRefundIssue, stepUp: true, mayTwoPerson: true, targetType: "monetization_transaction"},

	{method: http.MethodGet, path: "/payout-requests", operation: "monetization.payout_requests.list", permission: permMonPayoutsRead},
	{method: http.MethodGet, path: "/audit-logs", operation: "monetization.audit.list", permission: permMonAuditRead},
}

// RegisterMonetizationRoutes adds the Monetization dashboard under
// /v1/admin/monetization.
//
//	two-person   creator-fund rates, quality bands, budgets, settle,
//	             settle-period (all and per creator), earning reversal, always;
//	             refunds AT OR ABOVE ADMIN_REFUND_TWO_PERSON_THRESHOLD_PAISE
//	step-up      every write: fraud decisions, wallet freeze/unfreeze/rebuild,
//	             creator suspend/unsuspend, disputes, refunds, and the above
//
// While monetization's writes are disabled every route answers the
// not-launched state (monetizationAnswered).
func (h *Handler) RegisterMonetizationRoutes(r *gin.Engine) {
	p := product{app: monetizationAuditApp, label: "Monetization", prefix: "/v1/admin/monetization", client: h.monetization,
		answered: monetizationAnswered}

	// A per-registration copy: decisions are attached here, never to the
	// shared table.
	routes := make([]productRoute, len(MonetizationRoutes))
	copy(routes, MonetizationRoutes)
	special := map[string]gin.HandlerFunc{}
	for i := range routes {
		if routes[i].operation == opMonRefundIssue {
			routes[i].decide = monetizationRefundDecision(h.refundThresholdPaise)
		}
		rt := routes[i]
		if rt.twoPerson || rt.mayTwoPerson {
			h.approvals.Register(monetizationAuditApp, rt.operation, h.monetizationExecutor(rt))
			special[rt.operation] = h.monetizationStoredCall(p, rt)
		}
	}
	h.registerProduct(r, p, routes, special)
}

// NotLaunchedState is the answer to a monetization read while the product is
// not launched: a state for the console to show, not an error.
type NotLaunchedState struct {
	State   string `json:"state"` // "not_launched"
	App     string `json:"app"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

const notLaunchedMessage = "Monetization is not launched: money actions are not available yet."

// monetizationNotLaunched reports monetization's 503 MONETIZATION_NOT_LAUNCHED.
// Any other 503 (MAINTENANCE included) is passed through unchanged.
func monetizationNotLaunched(resp service.ProductResponse) bool {
	if resp.Status != http.StatusServiceUnavailable {
		return false
	}
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	return json.Unmarshal(resp.Body, &env) == nil && env.Error.Code == CodeMonetizationNotLaunched
}

// notLaunchedDetails is the error details a write carries.
var notLaunchedDetails = map[string]string{"state": "not_launched", "app": monetizationAuditApp}

// monetizationAnswered surfaces the not-launched state. A read answers 200
// {"data":{"state":"not_launched",...}} so the console renders the product as
// not launched; a write answers 503 MONETIZATION_NOT_LAUNCHED with
// details.state = not_launched, because nothing was done and a 2xx must never
// suggest otherwise. The audit row records the upstream 503 as a failure.
func monetizationAnswered(c *gin.Context, info *auditInfo, resp service.ProductResponse) bool {
	if !monetizationNotLaunched(resp) {
		return false
	}
	status := resp.Status
	info.statusCode = &status
	info.outcome = postgres.AuditOutcomeFailure
	info.set("state", "not_launched")
	if c.Request.Method == http.MethodGet {
		api.JSON(c.Writer, http.StatusOK, NotLaunchedState{
			State: "not_launched", App: monetizationAuditApp, Code: CodeMonetizationNotLaunched, Message: notLaunchedMessage,
		}, nil)
		return true
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable, CodeMonetizationNotLaunched,
		notLaunchedMessage, notLaunchedDetails)
	return true
}

// notLaunchedErrorBody is the same write answer, for an approval execution.
func notLaunchedErrorBody() []byte {
	b, _ := json.Marshal(api.Response{Error: &api.APIError{
		Code: CodeMonetizationNotLaunched, Message: notLaunchedMessage, Details: notLaunchedDetails,
	}})
	return b
}

// --- refunds (threshold decided from the body, before any call) ---

// monetizationRefundDecision reads amount_paise (monetization requires it)
// and applies the threshold: AT OR ABOVE is two-person.
func monetizationRefundDecision(threshold int64) func(*gin.Context, adminauth.Permissions) (Decision, error) {
	return func(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
		if !perms.Has(permMonRefundIssue) {
			return Decision{}, nil // the gate refuses
		}
		raw, _, err := jsonBody(c)
		if err != nil || len(raw) == 0 {
			return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON with transaction_id, amount_paise and reason")
		}
		paise, _, err := monetizationRefundBody(raw)
		if err != nil {
			return Decision{}, err
		}
		return Decision{
			TwoPerson: refundNeedsTwoPerson(paise, threshold),
			Audit:     map[string]any{"amount_paise": paise, "refund_threshold_paise": threshold},
		}, nil
	}
}

func monetizationRefundBody(raw []byte) (paise int64, transactionID string, err error) {
	var b struct {
		TransactionID string       `json:"transaction_id"`
		AmountPaise   *json.Number `json:"amount_paise"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&b) != nil {
		return 0, "", badRequest(CodeInvalidBody, "The refund body is malformed")
	}
	id, perr := uuid.Parse(b.TransactionID)
	if perr != nil {
		return 0, "", badRequest(CodeInvalidID, "transaction_id must be a transaction id")
	}
	if b.AmountPaise == nil {
		return 0, "", badRequest(CodeInvalidBody, "amount_paise is required")
	}
	n, perr := strconv.ParseInt(b.AmountPaise.String(), 10, 64)
	if perr != nil || n <= 0 || n > maxRefundPaise {
		return 0, "", badRequest(CodeInvalidBody, "amount_paise must be a positive whole number of paise")
	}
	return n, id.String(), nil
}

// --- stored calls (two-person) ---

// storedCall is a monetization write exactly as the first admin sent it: the
// filled product path, the query and the body. The executor refuses a path
// that is not its own route's.
type storedCall struct {
	Path  string          `json:"path"`
	Query string          `json:"query,omitempty"`
	Body  json.RawMessage `json:"body,omitempty"`
	// AmountPaise is repeated for the approval summary (refunds).
	AmountPaise int64 `json:"amount_paise,omitempty"`
}

// storedPathMatches: same segments as the template, every :param a uuid.
func storedPathMatches(template, path string) bool {
	ts, ps := strings.Split(template, "/"), strings.Split(path, "/")
	if len(ts) != len(ps) {
		return false
	}
	for i := range ts {
		if strings.HasPrefix(ts[i], ":") {
			if _, err := uuid.Parse(ps[i]); err != nil {
				return false
			}
			continue
		}
		if ts[i] != ps[i] {
			return false
		}
	}
	return true
}

// monetizationStoredCall runs a two-person (or maybe two-person) route: the
// gate has already decided; submit when two-person, else call directly.
func (h *Handler) monetizationStoredCall(p product, rt productRoute) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		for _, s := range strings.Split(rt.path, "/") {
			if strings.HasPrefix(s, ":") {
				if _, err := uuid.Parse(c.Param(s[1:])); err != nil {
					api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid id", nil)
					return
				}
			}
		}
		path, last, err := productPath(c, rt.path)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		raw, fields, err := jsonBody(c)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be JSON", nil)
			return
		}
		call := storedCall{Path: path, Query: c.Request.URL.RawQuery, Body: raw}
		reason := stringField(fields, "reason", "notes", "note")
		targetID := last
		if rt.operation == opMonRefundIssue {
			paise, txn, _ := monetizationRefundBody(raw) // validated by the decision
			call.AmountPaise, targetID = paise, txn
		}
		if targetID == "" {
			targetID = monetizationTarget(c, fields)
		}

		req, _ := effectiveRequirement(c)
		if req.TwoPerson {
			h.submitTwoPerson(c, rt.targetType, targetID, reason, call)
			return
		}
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = rt.targetType, targetID, reason
		h.productCall(c, p, service.ProductRequest{Method: rt.method, Path: path, RawQuery: call.Query, RawBody: raw}, false)
	}
}

// monetizationTarget names a write without a path id: its content type,
// period or day, and region when given.
func monetizationTarget(c *gin.Context, fields map[string]any) string {
	parts := []string{}
	for _, v := range []string{stringField(fields, "content_type"), stringField(fields, "period_key"), c.Query("period"), c.Query("day")} {
		if v != "" {
			parts = append(parts, v)
		}
	}
	if region := stringField(fields, "region_code"); region != "" {
		parts = append(parts, region)
	}
	if len(parts) == 0 {
		return "all"
	}
	t := strings.Join(parts, "/")
	if len(t) > 200 {
		t = t[:200]
	}
	return t
}

var errStoredPath = errors.New("stored call path does not belong to this operation")

// monetizationExecutor replays a stored call as the approver, with a token
// scoped to the route's permission.
func (h *Handler) monetizationExecutor(rt productRoute) approvals.Executor {
	return func(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
		var sc storedCall
		if err := json.Unmarshal(payload, &sc); err != nil {
			return approvals.Result{Err: err}
		}
		if !storedPathMatches(rt.path, sc.Path) {
			return approvals.Result{Err: errStoredPath}
		}
		pr := service.ProductRequest{Method: rt.method, Path: sc.Path, RawQuery: sc.Query, Permission: rt.permission, Actor: actor}
		if len(sc.Body) > 0 && string(sc.Body) != "null" {
			pr.RawBody = sc.Body
		}
		resp, err := h.monetization.Do(ctx, pr)
		if err == nil && monetizationNotLaunched(resp) {
			resp.Body = notLaunchedErrorBody()
		}
		return approvals.Result{Data: resp.Body, Status: resp.Status, Err: err}
	}
}
