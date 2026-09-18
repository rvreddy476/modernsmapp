package http

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/approvals"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Mopedu permissions, exactly as rider-service checks them
// (rider-service/internal/http/admin_token.go). Those marked NEW are not yet
// in identity's catalogue; see the report.
const (
	permRiderStatsRead       = "rider:stats.read"    // NEW
	permRiderPartnersRead    = "rider:partners.read" // NEW
	permRiderPartnersApprove = "rider:partners.approve"
	permRiderPartnersSuspend = "rider:partners.suspend" // NEW
	permRiderDocumentsReview = "rider:documents.review"
	permRiderVehiclesReview  = "rider:vehicles.review" // NEW
	permRiderPaymentsRead    = "rider:payments.read"   // NEW
	permRiderPaymentsSettle  = "rider:payments.settle"
	permRiderPaymentsReject  = "rider:payments.reject" // NEW
	permRiderRidesRead       = "rider:rides.read"
	permRiderRidesCancel     = "rider:rides.cancel"     // NEW
	permRiderRatingsModerate = "rider:ratings.moderate" // NEW
	permRiderComplaintsAct   = "rider:complaints.act"
	permRiderIncidentsRead   = "rider:incidents.read"
	permRiderIncidentsAct    = "rider:incidents.act" // NEW
	permRiderIncidentsReveal = "rider:incidents.reveal"
	permRiderCitiesManage    = "rider:cities.manage" // NEW
	permRiderFaresManage     = "rider:fares.manage"
	permRiderReportsRead     = "rider:reports.read" // NEW
	permRiderAuditRead       = "rider:audit.read"
)

const (
	riderAuditApp = "rider"
	riderPrefix   = "/v1/admin/rider"
)

// Mopedu operations that run through two-person approval: both give money
// back to a customer (a ride refund returns it, a waiver forgives a fee owed).
const (
	opRiderOutstandingWaive = "rider.outstanding.waive"
	opRiderRefundIssue      = "rider.ride.refund"
)

// RiderRoutes is the Mopedu route table under /v1/admin/rider: one console
// route per rider-service admin route, at the same path, forwarded with a
// token scoped to its permission.
//
// Application confinement: rider-service holds Mopedu's rows and nothing
// else, so its payments reads and settles are the rider app's own
// permissions (rider:payments.read / rider:payments.settle) and need no
// <app>:payments_* confinement. The confined form is for the Payments
// dashboard (payments-service, every application): a rider-scoped admin
// holding rider:payments_<x> sees application "mopedu" there, exactly as
// food:payments_<x> sees "feast" (paymentsApplications).
//
//	step-up      partner suspend and block; the document queue and both
//	             document decisions (a document row carries the plaintext
//	             document number: a KYC reveal); payment verify and reject;
//	             ride cancel; a safety incident's contact alerts (trusted
//	             contacts' phone numbers); every pricing write: fare rules,
//	             fare windows and coupons (create, update, deactivate)
//	two-person   ride refund and outstanding-fee waiver, always (the console's
//	             refund rule: every refund needs a second approver, whatever
//	             the amount)
var RiderRoutes = []productRoute{
	{method: http.MethodGet, path: "/stats", operation: "rider.stats", permission: permRiderStatsRead},
	{method: http.MethodGet, path: "/dashboard", operation: "rider.dashboard", permission: permRiderStatsRead},

	// Partners: approve/reject is one severity, suspend/block another.
	{method: http.MethodGet, path: "/partners", operation: "rider.partners.list", permission: permRiderPartnersRead},
	{method: http.MethodGet, path: "/partners/:id", operation: "rider.partner.read", permission: permRiderPartnersRead, targetType: "rider_partner"},
	{method: http.MethodPost, path: "/partners/:id/approve", operation: "rider.partner.approve", permission: permRiderPartnersApprove, targetType: "rider_partner"},
	{method: http.MethodPost, path: "/partners/:id/reject", operation: "rider.partner.reject", permission: permRiderPartnersApprove, targetType: "rider_partner"},
	{method: http.MethodPost, path: "/partners/:id/suspend", operation: "rider.partner.suspend", permission: permRiderPartnersSuspend, stepUp: true, targetType: "rider_partner"},
	{method: http.MethodPost, path: "/partners/:id/block", operation: "rider.partner.block", permission: permRiderPartnersSuspend, stepUp: true, targetType: "rider_partner"},

	// Documents: the list is a KYC reveal (document numbers in the clear).
	{method: http.MethodGet, path: "/documents", operation: "rider.documents.list", permission: permRiderDocumentsReview, stepUp: true},
	{method: http.MethodPost, path: "/documents/:id/verify", operation: "rider.document.verify", permission: permRiderDocumentsReview, stepUp: true, targetType: "rider_document"},
	{method: http.MethodPost, path: "/documents/:id/reject", operation: "rider.document.reject", permission: permRiderDocumentsReview, stepUp: true, targetType: "rider_document"},

	{method: http.MethodGet, path: "/vehicles", operation: "rider.vehicles.list", permission: permRiderVehiclesReview},
	{method: http.MethodPost, path: "/vehicles/:id/verify", operation: "rider.vehicle.verify", permission: permRiderVehiclesReview, targetType: "rider_vehicle"},
	{method: http.MethodPost, path: "/vehicles/:id/reject", operation: "rider.vehicle.reject", permission: permRiderVehiclesReview, targetType: "rider_vehicle"},

	// Subscription payments: verify settles the partner's payment, reject
	// does not; both are step-up.
	{method: http.MethodGet, path: "/payments", operation: "rider.payments.list", permission: permRiderPaymentsRead},
	{method: http.MethodPost, path: "/payments/:id/verify", operation: "rider.payment.verify", permission: permRiderPaymentsSettle, stepUp: true, targetType: "rider_payment"},
	{method: http.MethodPost, path: "/payments/:id/reject", operation: "rider.payment.reject", permission: permRiderPaymentsReject, stepUp: true, targetType: "rider_payment"},

	{method: http.MethodGet, path: "/rides", operation: "rider.rides.list", permission: permRiderRidesRead},
	{method: http.MethodGet, path: "/rides/live", operation: "rider.rides.live", permission: permRiderRidesRead},
	{method: http.MethodPost, path: "/rides/:id/cancel", operation: "rider.ride.cancel", permission: permRiderRidesCancel, stepUp: true, targetType: "rider_ride"},
	{method: http.MethodPost, path: "/rides/:id/rating/visibility", operation: "rider.ride.rating.visibility", permission: permRiderRatingsModerate, targetType: "rider_ride"},

	// Money: ride fares collected through payments-service (application
	// mopedu) and their refunds. A refund returns money to the customer:
	// step-up and two-person, always; an omitted amount_paise refunds what
	// the payment still holds.
	{method: http.MethodGet, path: "/ride-payments", operation: "rider.ride_payments.list", permission: permRiderPaymentsRead},
	{method: http.MethodGet, path: "/refunds", operation: "rider.refunds.list", permission: permRiderPaymentsRead},
	{method: http.MethodPost, path: "/rides/:id/refund", operation: opRiderRefundIssue, permission: permRiderPaymentsSettle, stepUp: true, twoPerson: true, targetType: "rider_ride"},
	// Outstanding cancellation fees: reading is a payments read; waiving
	// forgives money owed, so the settle permission, step-up and two-person.
	{method: http.MethodGet, path: "/outstanding", operation: "rider.outstanding.list", permission: permRiderPaymentsRead},
	{method: http.MethodPost, path: "/outstanding/:id/waive", operation: opRiderOutstandingWaive, permission: permRiderPaymentsSettle, stepUp: true, twoPerson: true, targetType: "rider_customer_outstanding"},

	{method: http.MethodGet, path: "/complaints", operation: "rider.complaints.list", permission: permRiderComplaintsAct},
	{method: http.MethodPost, path: "/complaints/:id/update-status", operation: "rider.complaint.status", permission: permRiderComplaintsAct, targetType: "rider_complaint"},

	// Safety incidents; the contact alerts carry trusted contacts' phone
	// numbers, so reading them is a reveal.
	{method: http.MethodGet, path: "/safety-incidents", operation: "rider.incidents.list", permission: permRiderIncidentsRead},
	{method: http.MethodPost, path: "/safety-incidents/:id/acknowledge", operation: "rider.incident.acknowledge", permission: permRiderIncidentsAct, targetType: "rider_safety_incident"},
	{method: http.MethodPost, path: "/safety-incidents/:id/resolve", operation: "rider.incident.resolve", permission: permRiderIncidentsAct, targetType: "rider_safety_incident"},
	{method: http.MethodGet, path: "/safety/incidents/:id/alerts", operation: "rider.incident.alerts.reveal", permission: permRiderIncidentsReveal, stepUp: true, targetType: "rider_safety_incident"},

	{method: http.MethodPost, path: "/cities", operation: "rider.city.create", permission: permRiderCitiesManage},
	{method: http.MethodPatch, path: "/cities/:id", operation: "rider.city.update", permission: permRiderCitiesManage, targetType: "rider_city"},
	{method: http.MethodPost, path: "/zones", operation: "rider.zone.create", permission: permRiderCitiesManage},
	{method: http.MethodPatch, path: "/zones/:id", operation: "rider.zone.update", permission: permRiderCitiesManage, targetType: "rider_zone"},
	// Fare rules change what every rider is charged: step-up.
	{method: http.MethodPost, path: "/fare-rules", operation: "rider.fare_rule.create", permission: permRiderFaresManage, stepUp: true},
	{method: http.MethodPatch, path: "/fare-rules/:id", operation: "rider.fare_rule.update", permission: permRiderFaresManage, stepUp: true, targetType: "rider_fare_rule"},
	// Fare windows (peak multipliers by weekday and local time) and coupons
	// are pricing too: every write is step-up like a fare rule. Surge is a
	// read of the live demand ratio per vehicle type.
	{method: http.MethodGet, path: "/fare-windows", operation: "rider.fare_windows.list", permission: permRiderFaresManage},
	{method: http.MethodPost, path: "/fare-windows", operation: "rider.fare_window.create", permission: permRiderFaresManage, stepUp: true},
	{method: http.MethodPatch, path: "/fare-windows/:id", operation: "rider.fare_window.update", permission: permRiderFaresManage, stepUp: true, targetType: "rider_fare_window"},
	{method: http.MethodPost, path: "/fare-windows/:id/deactivate", operation: "rider.fare_window.deactivate", permission: permRiderFaresManage, stepUp: true, targetType: "rider_fare_window"},
	{method: http.MethodGet, path: "/surge", operation: "rider.surge.read", permission: permRiderFaresManage},
	// Coupons: the redemptions list is declared before /coupons/:id, as
	// rider-service declares it.
	{method: http.MethodGet, path: "/coupons", operation: "rider.coupons.list", permission: permRiderFaresManage},
	{method: http.MethodGet, path: "/coupons/redemptions", operation: "rider.coupon.redemptions.list", permission: permRiderFaresManage},
	{method: http.MethodPost, path: "/coupons", operation: "rider.coupon.create", permission: permRiderFaresManage, stepUp: true},
	{method: http.MethodPatch, path: "/coupons/:id", operation: "rider.coupon.update", permission: permRiderFaresManage, stepUp: true, targetType: "rider_coupon"},
	{method: http.MethodPost, path: "/coupons/:id/deactivate", operation: "rider.coupon.deactivate", permission: permRiderFaresManage, stepUp: true, targetType: "rider_coupon"},

	{method: http.MethodGet, path: "/reports/matching-health", operation: "rider.report.matching_health", permission: permRiderReportsRead},
	{method: http.MethodGet, path: "/reports/partner-quality", operation: "rider.report.partner_quality", permission: permRiderReportsRead},
	{method: http.MethodGet, path: "/reports/supply-demand", operation: "rider.report.supply_demand", permission: permRiderReportsRead},
	{method: http.MethodGet, path: "/reports/safety", operation: "rider.report.safety", permission: permRiderReportsRead},
	{method: http.MethodGet, path: "/reports/compliance", operation: "rider.report.compliance", permission: permRiderReportsRead},
	{method: http.MethodGet, path: "/reports/revenue", operation: "rider.report.revenue", permission: permRiderReportsRead},
	{method: http.MethodGet, path: "/reports/cohort-retention", operation: "rider.report.cohort_retention", permission: permRiderReportsRead},
	{method: http.MethodGet, path: "/reports/customer-cohort", operation: "rider.report.customer_cohort", permission: permRiderReportsRead},
	{method: http.MethodGet, path: "/reports/cron-runs", operation: "rider.report.cron_runs", permission: permRiderReportsRead},

	{method: http.MethodGet, path: "/audit-logs", operation: "rider.audit.list", permission: permRiderAuditRead},
}

// RegisterRiderRoutes adds the Mopedu dashboard under /v1/admin/rider. Stats
// come from rider's own /stats route (one source, forwarded as-is). The two
// two-person routes store the console's call and replay it as the approver
// (riderStoredCall / riderExecutor).
func (h *Handler) RegisterRiderRoutes(r *gin.Engine) {
	p := product{app: riderAuditApp, label: "Mopedu", prefix: riderPrefix, client: h.rider}
	routes := make([]productRoute, len(RiderRoutes))
	copy(routes, RiderRoutes)
	special := map[string]gin.HandlerFunc{}
	for i := range routes {
		if routes[i].operation == opRiderRefundIssue {
			routes[i].decide = riderRefundDecision
		}
		rt := routes[i]
		if rt.twoPerson {
			h.approvals.Register(riderAuditApp, rt.operation, h.riderExecutor(rt))
			special[rt.operation] = h.riderStoredCall(rt)
		}
	}
	h.registerProduct(r, p, routes, special)
}

// --- two-person routes: refund and waive ---

type riderRefundBody struct {
	AmountPaise *json.Number `json:"amount_paise"`
	Reason      string       `json:"reason"`
}

// riderRefundAmount returns the stated refund in paise, or known=false when
// no amount is sent (rider-service then refunds what the payment still
// holds). A stated amount must be a positive whole number of paise.
func riderRefundAmount(raw []byte) (paise int64, known bool, err error) {
	var b riderRefundBody
	if len(raw) > 0 {
		if json.Unmarshal(raw, &b) != nil {
			return 0, false, badRequest(CodeInvalidBody, "The refund body is malformed")
		}
	}
	if b.AmountPaise == nil {
		return 0, false, nil
	}
	n, perr := strconv.ParseInt(b.AmountPaise.String(), 10, 64)
	if perr != nil || n <= 0 || n > maxRefundPaise {
		return 0, false, badRequest(CodeInvalidBody, "amount_paise must be a positive whole number of paise")
	}
	return n, true, nil
}

// riderRefundDecision validates the refund body before anything is stored
// and records the amount for the audit row. The route is two-person in the
// table: a stated amount and a full refund (no amount) are both sent for
// approval, so the payment is never read here.
func riderRefundDecision(c *gin.Context, perms adminauth.Permissions) (Decision, error) {
	if !perms.Has(permRiderPaymentsSettle) {
		return Decision{}, nil // the gate refuses; nothing is parsed
	}
	if _, err := uuid.Parse(c.Param("id")); err != nil {
		return Decision{}, badRequest(CodeInvalidID, "Invalid ride id")
	}
	raw, _, err := jsonBody(c)
	if err != nil {
		return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON")
	}
	paise, known, err := riderRefundAmount(raw)
	if err != nil {
		return Decision{}, err
	}
	audit := map[string]any{"amount_basis": "full_refund"}
	if known {
		audit["amount_basis"], audit["amount_paise"] = "stated", paise
	}
	return Decision{TwoPerson: true, Audit: audit}, nil
}

// riderStoredCall submits a two-person write for approval: the filled
// product path and the console's body, exactly as sent (storedCall, shared
// with monetization). Every :param must be a uuid, as rider-service's ids
// are; the reason is the body's.
func (h *Handler) riderStoredCall(rt productRoute) gin.HandlerFunc {
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
		call := storedCall{Path: path, Body: raw}
		if rt.operation == opRiderRefundIssue {
			call.AmountPaise, _, _ = riderRefundAmount(raw) // validated by the decision
		}
		h.submitTwoPerson(c, rt.targetType, last, stringField(fields, "reason"), call)
	}
}

// riderExecutor replays a stored call as the approver, with a token scoped
// to the route's permission; a path that is not the route's own is refused.
func (h *Handler) riderExecutor(rt productRoute) approvals.Executor {
	return func(ctx context.Context, actor string, payload json.RawMessage) approvals.Result {
		var sc storedCall
		if err := json.Unmarshal(payload, &sc); err != nil {
			return approvals.Result{Err: err}
		}
		if !storedPathMatches(rt.path, sc.Path) {
			return approvals.Result{Err: errStoredPath}
		}
		pr := service.ProductRequest{Method: rt.method, Path: sc.Path, Permission: rt.permission, Actor: actor}
		if len(sc.Body) > 0 && string(sc.Body) != "null" {
			pr.RawBody = sc.Body
		}
		resp, err := h.rider.Do(ctx, pr)
		return approvals.Result{Data: resp.Body, Status: resp.Status, Err: err}
	}
}
