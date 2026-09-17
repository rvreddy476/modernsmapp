package http

import (
	"net/http"

	"github.com/gin-gonic/gin"
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

// RiderRoutes is the Mopedu route table under /v1/admin/rider: one console
// route per rider-service admin route, at the same path, forwarded with a
// token scoped to its permission. Rider moves no money out (subscription
// payments are verified, not paid), so nothing here is two-person.
//
//	step-up   partner suspend and block; the document queue and both document
//	          decisions (a document row carries the plaintext document number:
//	          a KYC reveal); payment verify and reject; ride cancel; a safety
//	          incident's contact alerts (trusted contacts' phone numbers);
//	          fare-rule create and update
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
// come from rider's own /stats route (one source, forwarded as-is).
func (h *Handler) RegisterRiderRoutes(r *gin.Engine) {
	p := product{app: riderAuditApp, label: "Mopedu", prefix: riderPrefix, client: h.rider}
	routes := make([]productRoute, len(RiderRoutes))
	copy(routes, RiderRoutes)
	h.registerProduct(r, p, routes, nil)
}
