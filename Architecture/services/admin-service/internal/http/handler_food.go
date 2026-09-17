package http

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Feast permissions, exactly as food-service checks them
// (food-service/internal/http/admin_token.go). Several are not yet in
// identity's catalogue; see the report.
const (
	permFoodStatsRead          = "food:stats.read"
	permFoodRestaurantApprove  = "food:restaurant.approve"
	permFoodRestaurantSuspend  = "food:restaurant.suspend"
	permFoodRiderApprove       = "food:delivery_partner.approve"
	permFoodRiderSuspend       = "food:delivery_partner.suspend"
	permFoodDocumentsReview    = "food:documents.review"
	permFoodPayoutAccountsRead = "food:payout_accounts.read"
	permFoodOrdersRead         = "food:orders.read"
	permFoodOrdersCancel       = "food:orders.cancel"
	permFoodRefundIssue        = "food:refund.issue"
	permFoodRefundsRead        = "food:refunds.read"
	permFoodSettlementRead     = "food:settlement.read"
	permFoodSettlementGenerate = "food:settlement.generate"
	permFoodSettlementMarkPaid = "food:settlement.mark_paid"
	permFoodMenuModerate       = "food:menu.moderate"
	permFoodReviewsModerate    = "food:reviews.moderate"
	permFoodTicketsAct         = "food:tickets.act"
	permFoodCouponsManage      = "food:coupons.manage"
	permFoodServiceAreasManage = "food:service_areas.manage"
	permFoodReportsRead        = "food:reports.read"
	permFoodFraudRead          = "food:fraud.read"
	permFoodAuditRead          = "food:audit.read"
)

// Feast operations that can run through two-person approval.
const (
	opFoodRefundIssue          = "food.order.refund"
	opFoodRefundDecide         = "food.refund.decide"
	opFoodRestaurantMarkPaid   = "food.settlement.restaurant.mark_paid"
	opFoodDeliveryMarkPaid     = "food.settlement.delivery_partner.mark_paid"
	opFoodRestaurantStatus     = "food.restaurant.status"
	opFoodDeliveryPartnerState = "food.delivery_partner.status"
)

// Every refund is two-person (founder decision, 2026-09-17): there is no
// amount threshold. Issuing a refund is declared twoPerson in the table, so
// the gate needs a second holder whatever the decision reports; approving a
// refund request is decided per request (rejecting moves no money). The
// decisions validate the body and record the amount, when it is known, for
// the audit row and the approval summary only.

// ctxFoodRefundPaise carries a refund request's stored amount from the
// decision to the handler, for the approval summary only.
const ctxFoodRefundPaise = "admin.food.refund_paise"

// FoodRoutes is the Feast route table under /v1/admin/food. Every entry
// forwards to food's token-only family with a token scoped to its permission
// (for the status routes, the permission the requested status needs).
var FoodRoutes = []productRoute{
	{method: http.MethodGet, path: "/stats", operation: "food.stats", permission: permFoodStatsRead},
	{method: http.MethodGet, path: "/dashboard", operation: "food.dashboard", permission: permFoodStatsRead},

	{method: http.MethodGet, path: "/restaurants/pending", operation: "food.restaurants.pending", permission: permFoodRestaurantApprove},
	{method: http.MethodPost, path: "/restaurants/:restaurantId/approve", operation: "food.restaurant.approve", permission: permFoodRestaurantApprove, targetType: "food_restaurant"},
	{method: http.MethodPost, path: "/restaurants/:restaurantId/reject", operation: "food.restaurant.reject", permission: permFoodRestaurantApprove, targetType: "food_restaurant"},
	// Decided per request: ACTIVE/APPROVED is an approval; any other status
	// suspends, needs food:restaurant.suspend and a step-up.
	{method: http.MethodPatch, path: "/restaurants/:restaurantId/status", operation: opFoodRestaurantStatus, permission: permFoodRestaurantSuspend, targetType: "food_restaurant"},
	{method: http.MethodGet, path: "/delivery-partners/pending", operation: "food.delivery_partners.pending", permission: permFoodRiderApprove},
	{method: http.MethodPost, path: "/delivery-partners/:partnerId/approve", operation: "food.delivery_partner.approve", permission: permFoodRiderApprove, targetType: "food_delivery_partner"},
	{method: http.MethodPost, path: "/delivery-partners/:partnerId/reject", operation: "food.delivery_partner.reject", permission: permFoodRiderApprove, targetType: "food_delivery_partner"},
	{method: http.MethodPatch, path: "/delivery-partners/:partnerId/status", operation: opFoodDeliveryPartnerState, permission: permFoodRiderSuspend, targetType: "food_delivery_partner"},

	{method: http.MethodPost, path: "/restaurants/:restaurantId/documents/:docId/decide", operation: "food.restaurant.document.decide", permission: permFoodDocumentsReview, stepUp: true, targetType: "food_document"},
	{method: http.MethodGet, path: "/delivery-partners/:partnerId/kyc", operation: "food.delivery_partner.kyc.read", permission: permFoodDocumentsReview, stepUp: true, targetType: "food_delivery_partner"},
	{method: http.MethodPost, path: "/delivery-partners/:partnerId/documents/:docId/decide", operation: "food.delivery_partner.document.decide", permission: permFoodDocumentsReview, stepUp: true, targetType: "food_document"},
	{method: http.MethodGet, path: "/payout-accounts", operation: "food.payout_accounts.list", permission: permFoodPayoutAccountsRead, stepUp: true},

	{method: http.MethodGet, path: "/orders", operation: "food.orders.list", permission: permFoodOrdersRead},
	{method: http.MethodGet, path: "/orders/:orderId", operation: "food.order.read", permission: permFoodOrdersRead, targetType: "food_order"},
	{method: http.MethodPost, path: "/orders/:orderId/cancel", operation: "food.order.cancel", permission: permFoodOrdersCancel, stepUp: true, targetType: "food_order"},
	// Step-up and two-person, always: every refund needs a second person.
	{method: http.MethodPost, path: "/orders/:orderId/refund", operation: opFoodRefundIssue, permission: permFoodRefundIssue, stepUp: true, twoPerson: true, targetType: "food_order", idempotent: true},
	{method: http.MethodGet, path: "/refunds", operation: "food.refunds.list", permission: permFoodRefundsRead},
	// Step-up always; approving is two-person (decided per request), rejecting moves no money.
	{method: http.MethodPost, path: "/refunds/:refundId/decide", operation: opFoodRefundDecide, permission: permFoodRefundIssue, stepUp: true, mayTwoPerson: true, targetType: "food_refund_request"},

	{method: http.MethodPost, path: "/settlements/generate", operation: "food.settlements.generate", permission: permFoodSettlementGenerate, stepUp: true, idempotent: true},
	{method: http.MethodGet, path: "/settlements/restaurants", operation: "food.settlements.restaurants", permission: permFoodSettlementRead},
	{method: http.MethodPost, path: "/settlements/restaurants/:settlementId/mark-paid", operation: opFoodRestaurantMarkPaid, permission: permFoodSettlementMarkPaid, stepUp: true, twoPerson: true, targetType: "food_restaurant_settlement"},
	{method: http.MethodGet, path: "/settlements/delivery-partners", operation: "food.settlements.delivery_partners", permission: permFoodSettlementRead},
	{method: http.MethodPost, path: "/settlements/delivery-partners/:settlementId/mark-paid", operation: opFoodDeliveryMarkPaid, permission: permFoodSettlementMarkPaid, stepUp: true, twoPerson: true, targetType: "food_delivery_settlement"},
	{method: http.MethodPost, path: "/settlements/files", operation: "food.settlement_file.create", permission: permFoodSettlementGenerate, stepUp: true},
	{method: http.MethodGet, path: "/settlements/files", operation: "food.settlement_files.list", permission: permFoodSettlementRead},
	{method: http.MethodGet, path: "/settlements/files/:id/download", operation: "food.settlement_file.download", permission: permFoodSettlementRead, stepUp: true, targetType: "food_settlement_file"},

	{method: http.MethodGet, path: "/moderation/queue", operation: "food.moderation.queue", permission: permFoodMenuModerate},
	{method: http.MethodPost, path: "/moderation/menu-items/:itemId", operation: "food.menu_item.moderate", permission: permFoodMenuModerate, targetType: "food_menu_item"},
	{method: http.MethodDelete, path: "/item-reviews/:reviewId", operation: "food.item_review.hide", permission: permFoodReviewsModerate, targetType: "food_item_review"},
	{method: http.MethodGet, path: "/support/tickets", operation: "food.tickets.list", permission: permFoodTicketsAct},
	{method: http.MethodPost, path: "/support/tickets/:ticketId/status", operation: "food.ticket.status", permission: permFoodTicketsAct, targetType: "food_ticket"},

	{method: http.MethodGet, path: "/coupons", operation: "food.coupons.list", permission: permFoodCouponsManage},
	{method: http.MethodPost, path: "/coupons", operation: "food.coupon.create", permission: permFoodCouponsManage},
	{method: http.MethodPatch, path: "/coupons/:couponId", operation: "food.coupon.update", permission: permFoodCouponsManage, stepUp: true, targetType: "food_coupon"},
	{method: http.MethodGet, path: "/service-areas", operation: "food.service_areas.list", permission: permFoodServiceAreasManage},
	{method: http.MethodPost, path: "/service-areas", operation: "food.service_area.create", permission: permFoodServiceAreasManage},
	{method: http.MethodPatch, path: "/service-areas/:areaId", operation: "food.service_area.update", permission: permFoodServiceAreasManage, targetType: "food_service_area"},

	{method: http.MethodGet, path: "/reports/restaurant-sla", operation: "food.report.restaurant_sla", permission: permFoodReportsRead},
	{method: http.MethodGet, path: "/reports/delivery-sla", operation: "food.report.delivery_sla", permission: permFoodReportsRead},
	{method: http.MethodGet, path: "/reports/payment-recon", operation: "food.report.payment_recon", permission: permFoodReportsRead},
	{method: http.MethodGet, path: "/reports/refunds", operation: "food.report.refunds", permission: permFoodReportsRead},
	{method: http.MethodGet, path: "/reports/coupon-abuse", operation: "food.report.coupon_abuse", permission: permFoodReportsRead},
	{method: http.MethodGet, path: "/reports/compliance", operation: "food.report.compliance", permission: permFoodReportsRead},
	{method: http.MethodGet, path: "/reports/orders", operation: "food.report.orders", permission: permFoodReportsRead},
	{method: http.MethodGet, path: "/reports/revenue", operation: "food.report.revenue", permission: permFoodReportsRead},
	{method: http.MethodGet, path: "/fraud/top", operation: "food.fraud.top", permission: permFoodFraudRead},
	{method: http.MethodGet, path: "/audit-logs", operation: "food.audit.list", permission: permFoodAuditRead},
}

// foodStatusPermission mirrors food's RestaurantStatusPermission and
// DeliveryPartnerStatusPermission: making one ACTIVE or APPROVED is an
// approval; every other status takes it off the platform.
func foodStatusPermission(status, approve, suspend string) (perm string, suspends bool) {
	switch normaliseFoodStatus(status) {
	case "ACTIVE", "APPROVED":
		return approve, false
	}
	return suspend, true
}

func normaliseFoodStatus(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

type foodStatusBody struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// RegisterFoodRoutes adds the Feast dashboard under /v1/admin/food.
//
//	two-person   settlement mark-paid (both kinds), refund issue and
//	             refund-request approval, always (no amount threshold)
//	step-up      restaurant/rider suspension, document decisions, rider KYC,
//	             payout accounts, order cancel, refunds, settlement generate,
//	             settlement file create and download, coupon update
func (h *Handler) RegisterFoodRoutes(r *gin.Engine) {
	p := product{app: "food", label: "Feast", prefix: "/v1/admin/food", client: h.food}

	routes := make([]productRoute, len(FoodRoutes))
	copy(routes, FoodRoutes)
	for i := range routes {
		switch routes[i].operation {
		case opFoodRestaurantStatus:
			routes[i].decide = foodStatusDecision(permFoodRestaurantApprove, permFoodRestaurantSuspend)
		case opFoodDeliveryPartnerState:
			routes[i].decide = foodStatusDecision(permFoodRiderApprove, permFoodRiderSuspend)
		case opFoodRefundIssue:
			routes[i].decide = foodRefundIssueDecision
		case opFoodRefundDecide:
			routes[i].decide = h.foodRefundDecideDecision
		}
	}

	exec := func(perm string, call func(ctx context.Context, actor string, payload json.RawMessage) (service.ProductRequest, error)) approvals.Executor {
		return func(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
			pr, err := call(ctx, actor, payload)
			if err != nil {
				return approvals.Result{Err: err}
			}
			pr.Permission, pr.Actor = perm, actor
			resp, err := h.food.Do(ctx, pr)
			return approvals.Result{Data: resp.Body, Status: resp.Status, Err: err}
		}
	}
	h.approvals.Register("food", opFoodRefundIssue, exec(permFoodRefundIssue, refundIssueRequest))
	h.approvals.Register("food", opFoodRefundDecide, exec(permFoodRefundIssue, refundDecideRequest))
	h.approvals.Register("food", opFoodRestaurantMarkPaid, exec(permFoodSettlementMarkPaid, markPaidRequest("/settlements/restaurants/")))
	h.approvals.Register("food", opFoodDeliveryMarkPaid, exec(permFoodSettlementMarkPaid, markPaidRequest("/settlements/delivery-partners/")))

	special := map[string]gin.HandlerFunc{
		opFoodRestaurantStatus:     h.foodStatus(p, "/restaurants/", "restaurantId"),
		opFoodDeliveryPartnerState: h.foodStatus(p, "/delivery-partners/", "partnerId"),
		opFoodRefundIssue:          h.foodRefundIssue(p),
		opFoodRefundDecide:         h.foodRefundDecide(p),
		opFoodRestaurantMarkPaid:   h.foodMarkPaid("food_restaurant_settlement"),
		opFoodDeliveryMarkPaid:     h.foodMarkPaid("food_delivery_settlement"),
	}
	h.registerProduct(r, p, routes, special)
}

// --- status (approve vs suspend, decided from the body) ---

func foodStatusDecision(approve, suspend string) func(*gin.Context, adminauth.Permissions) (Decision, error) {
	return func(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
		var body foodStatusBody
		raw, _, err := jsonBody(c)
		if err != nil {
			return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON")
		}
		_ = json.Unmarshal(raw, &body)
		if normaliseFoodStatus(body.Status) == "" {
			return Decision{}, badRequest(CodeInvalidAction, "A status is required")
		}
		perm, suspends := foodStatusPermission(body.Status, approve, suspend)
		return Decision{Permission: perm, StepUp: suspends, Audit: map[string]any{"status": normaliseFoodStatus(body.Status)}}, nil
	}
}

func (h *Handler) foodStatus(p product, prefix, param string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param(param))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid id", nil)
			return
		}
		raw, _, _ := jsonBody(c)
		var body foodStatusBody
		_ = json.Unmarshal(raw, &body)
		info := auditFrom(c)
		info.targetID, info.reason = id.String(), body.Reason
		// Forward exactly the status the gate judged.
		h.productCall(c, p, service.ProductRequest{
			Method: http.MethodPatch, Path: prefix + id.String() + "/status",
			Body: foodStatusBody{Status: normaliseFoodStatus(body.Status), Reason: body.Reason},
		}, false)
	}
}

// --- refunds ---

type foodRefundBody struct {
	Reason      string       `json:"reason"`
	AmountPaise *json.Number `json:"amount_paise"`
	Amount      *json.Number `json:"amount"` // rupees, as food takes it
}

// maxRefundPaise bounds a parsed amount (₹1 crore) against overflow games.
const maxRefundPaise int64 = 1_000_000_000

// parseRefundAmount returns the refund in paise, or known=false for a full
// refund (no amount sent). amount_paise is preferred; amount (rupees) is
// accepted; both must agree.
func parseRefundAmount(b foodRefundBody) (paise int64, known bool, err error) {
	var fromPaise, fromRupees int64 = -1, -1
	if b.AmountPaise != nil {
		n, perr := strconv.ParseInt(b.AmountPaise.String(), 10, 64)
		if perr != nil || n <= 0 || n > maxRefundPaise {
			return 0, false, badRequest(CodeInvalidBody, "amount_paise must be a positive whole number of paise")
		}
		fromPaise = n
	}
	if b.Amount != nil {
		f, perr := strconv.ParseFloat(b.Amount.String(), 64)
		if perr != nil || f <= 0 || math.IsInf(f, 0) || f*100 > float64(maxRefundPaise) {
			return 0, false, badRequest(CodeInvalidBody, "amount must be a positive number of rupees")
		}
		fromRupees = int64(math.Round(f * 100))
	}
	switch {
	case fromPaise > 0 && fromRupees > 0 && fromPaise != fromRupees:
		return 0, false, badRequest(CodeInvalidBody, "amount and amount_paise disagree")
	case fromPaise > 0:
		return fromPaise, true, nil
	case fromRupees > 0:
		return fromRupees, true, nil
	}
	return 0, false, nil
}

func readRefundBody(c *gin.Context) (foodRefundBody, error) {
	raw, _, err := jsonBody(c)
	if err != nil {
		return foodRefundBody{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	var b foodRefundBody
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &b); err != nil {
			return foodRefundBody{}, badRequest(CodeInvalidBody, "The refund body is malformed")
		}
	}
	return b, nil
}

// foodRefundIssueDecision validates the refund body before anything is
// called and records the amount for the audit row. The route is two-person:
// a stated amount and a full refund (no amount) are both sent for approval,
// so the order total is never read here.
func foodRefundIssueDecision(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
	if !perms.Has(permFoodRefundIssue) {
		return Decision{}, nil // the gate refuses; nothing is parsed
	}
	if _, err := uuid.Parse(c.Param("orderId")); err != nil {
		return Decision{}, badRequest(CodeInvalidID, "Invalid order id")
	}
	b, err := readRefundBody(c)
	if err != nil {
		return Decision{}, err
	}
	paise, known, err := parseRefundAmount(b)
	if err != nil {
		return Decision{}, err
	}
	audit := map[string]any{"amount_basis": "full_refund"}
	if known {
		audit["amount_basis"], audit["amount_paise"] = "stated", paise
	}
	return Decision{TwoPerson: true, Audit: audit}, nil
}

type refundIssuePayload struct {
	OrderID        string `json:"order_id"`
	AmountPaise    int64  `json:"amount_paise,omitempty"` // 0 = full refund
	Reason         string `json:"reason,omitempty"`
	IdempotencyKey string `json:"idempotency_key"`
}

func refundIssueRequest(_ context.Context, _ string, payload json.RawMessage) (service.ProductRequest, error) {
	var p refundIssuePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return service.ProductRequest{}, err
	}
	id, err := uuid.Parse(p.OrderID)
	if err != nil {
		return service.ProductRequest{}, err
	}
	body := map[string]any{}
	if p.Reason != "" {
		body["reason"] = p.Reason
	}
	if p.AmountPaise > 0 {
		body["amount"] = float64(p.AmountPaise) / 100
	}
	return service.ProductRequest{
		Method: http.MethodPost, Path: "/orders/" + id.String() + "/refund", Body: body, IdempotencyKey: p.IdempotencyKey,
	}, nil
}

func (h *Handler) foodRefundIssue(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		orderID, err := uuid.Parse(c.Param("orderId"))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid order id", nil)
			return
		}
		key, ok := idempotencyKey(c, true)
		if !ok {
			return
		}
		b, _ := readRefundBody(c)
		paise, _, _ := parseRefundAmount(b) // validated by the decision
		payload := refundIssuePayload{OrderID: orderID.String(), AmountPaise: paise, Reason: b.Reason, IdempotencyKey: key}
		h.foodRunOrSubmit(c, p, "food_order", orderID.String(), b.Reason, payload, refundIssueRequest)
	}
}

// foodRunOrSubmit executes directly when the gate did not require two-person,
// or submits for approval when it did — the same stored request either way.
func (h *Handler) foodRunOrSubmit(c *gin.Context, p product, targetType, targetID, reason string, payload any,
	build func(context.Context, string, json.RawMessage) (service.ProductRequest, error)) {
	req, _ := effectiveRequirement(c)
	info := auditFrom(c)
	if req.TwoPerson {
		h.submitTwoPerson(c, targetType, targetID, reason, payload)
		return
	}
	info.targetType, info.targetID, info.reason = targetType, targetID, reason
	raw, err := json.Marshal(payload)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Could not encode the request", nil)
		return
	}
	pr, err := build(c.Request.Context(), actorFrom(c), raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidBody, "Invalid request", nil)
		return
	}
	h.productCall(c, p, pr, false)
}

type foodRefundDecideBody struct {
	Status string `json:"status"`
	Reason string `json:"reason"`
}

func readRefundDecideBody(c *gin.Context) (foodRefundDecideBody, error) {
	raw, _, err := jsonBody(c)
	if err != nil {
		return foodRefundDecideBody{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	var b foodRefundDecideBody
	_ = json.Unmarshal(raw, &b)
	b.Status = strings.ToLower(strings.TrimSpace(b.Status))
	if b.Status != "approved" && b.Status != "rejected" {
		return b, badRequest(CodeInvalidAction, "status must be approved or rejected")
	}
	return b, nil
}

// foodRefundDecideDecision: rejecting moves no money (step-up only);
// approving is two-person, always. The stored request's amount is read from
// food with a food:refunds.read token when the admin holds it, for the audit
// row and the approval summary only: it never changes the decision, and when
// it cannot be read the summary says the amount is not stated.
func (h *Handler) foodRefundDecideDecision(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
	if !perms.Has(permFoodRefundIssue) {
		return Decision{}, nil
	}
	refundID, err := uuid.Parse(c.Param("refundId"))
	if err != nil {
		return Decision{}, badRequest(CodeInvalidID, "Invalid refund id")
	}
	b, err := readRefundDecideBody(c)
	if err != nil {
		return Decision{}, err
	}
	audit := map[string]any{"status": b.Status}
	if b.Status == "rejected" {
		return Decision{Audit: audit}, nil
	}
	if paise, ok := h.foodRefundRequestPaise(c, perms, refundID); ok {
		audit["amount_paise"] = paise
		c.Set(ctxFoodRefundPaise, paise)
	} else {
		audit["amount_basis"] = "unknown"
	}
	return Decision{TwoPerson: true, Audit: audit}, nil
}

// foodRefundRequestPaise is a best-effort read of the request's amount for
// the audit row and the approval summary; ok is false when the admin may not
// list refunds or the request is not among the ones awaiting a decision.
func (h *Handler) foodRefundRequestPaise(c *gin.Context, perms adminauth.Permissions, refundID uuid.UUID) (int64, bool) {
	if !perms.Has(permFoodRefundsRead) {
		return 0, false
	}
	resp, err := h.food.Do(productContext(c), service.ProductRequest{
		Method: http.MethodGet, Path: "/refunds", Query: url.Values{"status": {"requested"}, "limit": {"500"}},
		Permission: permFoodRefundsRead, Actor: actorFrom(c),
	})
	if err != nil || resp.Status != http.StatusOK {
		return 0, false
	}
	var list struct {
		Refunds []struct {
			ID     string  `json:"id"`
			Amount float64 `json:"amount"`
		} `json:"refunds"`
	}
	if json.Unmarshal(envelopeData(resp.Body), &list) != nil {
		return 0, false
	}
	for _, r := range list.Refunds {
		if r.ID == refundID.String() && r.Amount > 0 {
			return int64(math.Round(r.Amount * 100)), true
		}
	}
	return 0, false
}

type refundDecidePayload struct {
	RefundID string `json:"refund_id"`
	Status   string `json:"status"`
	Reason   string `json:"reason,omitempty"`
	// AmountPaise is the request's stored amount when it could be read
	// (approval summary only; food decides from its own record).
	AmountPaise int64 `json:"amount_paise,omitempty"`
}

func refundDecideRequest(_ context.Context, _ string, payload json.RawMessage) (service.ProductRequest, error) {
	var p refundDecidePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return service.ProductRequest{}, err
	}
	id, err := uuid.Parse(p.RefundID)
	if err != nil {
		return service.ProductRequest{}, err
	}
	return service.ProductRequest{
		Method: http.MethodPost, Path: "/refunds/" + id.String() + "/decide",
		Body: foodRefundDecideBody{Status: p.Status, Reason: p.Reason},
	}, nil
}

func (h *Handler) foodRefundDecide(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		refundID, err := uuid.Parse(c.Param("refundId"))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid refund id", nil)
			return
		}
		b, _ := readRefundDecideBody(c) // validated by the decision
		payload := refundDecidePayload{RefundID: refundID.String(), Status: b.Status, Reason: b.Reason}
		if v, ok := c.Get(ctxFoodRefundPaise); ok {
			payload.AmountPaise, _ = v.(int64)
		}
		h.foodRunOrSubmit(c, p, "food_refund_request", refundID.String(), b.Reason, payload, refundDecideRequest)
	}
}

// --- settlement mark-paid (always two-person) ---

type markPaidPayload struct {
	SettlementID string `json:"settlement_id"`
	Reference    string `json:"reference,omitempty"`
}

func markPaidRequest(prefix string) func(context.Context, string, json.RawMessage) (service.ProductRequest, error) {
	return func(_ context.Context, _ string, payload json.RawMessage) (service.ProductRequest, error) {
		var p markPaidPayload
		if err := json.Unmarshal(payload, &p); err != nil {
			return service.ProductRequest{}, err
		}
		id, err := uuid.Parse(p.SettlementID)
		if err != nil {
			return service.ProductRequest{}, err
		}
		return service.ProductRequest{
			Method: http.MethodPost, Path: prefix + id.String() + "/mark-paid",
			Body: map[string]string{"reference": p.Reference},
		}, nil
	}
}

func (h *Handler) foodMarkPaid(targetType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := uuid.Parse(c.Param("settlementId"))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidID, "Invalid settlement id", nil)
			return
		}
		raw, _, err := jsonBody(c)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be JSON", nil)
			return
		}
		var body struct {
			Reference string `json:"reference"`
			Reason    string `json:"reason"`
		}
		_ = json.Unmarshal(raw, &body)
		h.submitTwoPerson(c, targetType, id.String(), body.Reason,
			markPaidPayload{SettlementID: id.String(), Reference: body.Reference})
	}
}

// envelopeData unwraps {"data": ...} when present.
func envelopeData(b []byte) []byte {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(b, &env) == nil && len(env.Data) > 0 && string(env.Data) != "null" {
		return env.Data
	}
	return b
}
