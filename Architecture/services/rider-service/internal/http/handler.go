// Package http wires gin routes to the rider-service.
package http

import (
	"net/http"
	"strings"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler is the rider-service HTTP layer.
type Handler struct {
	svc         *service.Service
	internalKey string
}

// New constructs a Handler.
func New(svc *service.Service, internalKey string) *Handler {
	return &Handler{svc: svc, internalKey: internalKey}
}

// RegisterRoutes registers all /v1/rider routes on the provided engine.
//
// Surface (Sprint 1 scope per mopedu/IMPLEMENTATION_PLAN.md §3):
//   - public: GET /cities, POST /estimate
//   - customer: POST /rides, GET /rides/:id, GET /rides/me
//   - partner: profile / KYC / Aadhaar / vehicles / vehicle docs
//   - subscription: list plans, subscribe, payment-proof, GET me
//
// Admin routes are stubbed-out in S3.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	rider := r.Group("/v1/rider")
	{
		// --- Public -------------------------------------------------------
		rider.GET("/cities", h.GetCities)
		rider.POST("/estimate", h.PostEstimate)

		// --- Customer rides ----------------------------------------------
		rider.POST("/rides", h.PostRide)
		rider.GET("/rides/me", h.GetMyRides)
		rider.GET("/rides/:id", h.GetRide)
		rider.POST("/rides/:id/cancel", h.PostCancelRide)
		rider.POST("/rides/:id/rate", h.PostRateRide)

		// --- Partner ride lifecycle (S2) ----------------------------------
		rider.POST("/rides/:id/arriving", h.PostMarkArriving)
		rider.POST("/rides/:id/arrived", h.PostMarkArrived)
		rider.POST("/rides/:id/start", h.PostStartRide)
		rider.POST("/rides/:id/complete", h.PostCompleteRide)

		// --- Partner ops (online/offline/location/dashboard) (S2) ---------
		rider.POST("/partners/me/online", h.PostGoOnline)
		rider.POST("/partners/me/offline", h.PostGoOffline)
		rider.POST("/partners/me/location", h.PostUpdateLocation)
		rider.GET("/partners/me/dashboard", h.GetPartnerDashboard)
		rider.GET("/partners/me/earnings", h.GetPartnerEarnings)

		// --- Offers (S2) -------------------------------------------------
		rider.GET("/offers/incoming", h.GetIncomingOffers)
		rider.POST("/offers/:id/accept", h.PostAcceptOffer)
		rider.POST("/offers/:id/reject", h.PostRejectOffer)

		// --- Partner profile ----------------------------------------------
		rider.POST("/partners", h.PostPartner)
		rider.GET("/partners/me", h.GetMyPartner)
		rider.PATCH("/partners/me", h.PatchMyPartner)

		rider.POST("/partners/me/documents", h.PostMyDocument)
		rider.GET("/partners/me/documents", h.GetMyDocuments)

		rider.POST("/partners/me/aadhaar/start", h.PostAadhaarStart)
		rider.POST("/partners/me/aadhaar/callback", h.PostAadhaarCallback)

		rider.POST("/partners/me/vehicles", h.PostVehicle)
		rider.GET("/partners/me/vehicles", h.GetMyVehicles)

		rider.POST("/vehicles/:id/documents", h.PostVehicleDocument)
		rider.GET("/vehicles/:id/documents", h.GetVehicleDocuments)

		// --- Subscription -------------------------------------------------
		rider.GET("/subscriptions/plans", h.GetPlans)
		rider.POST("/subscriptions/subscribe", h.PostSubscribe)
		rider.POST("/subscriptions/payment-proof", h.PostPaymentProof)
		rider.GET("/subscriptions/me", h.GetMySubscription)

		// --- S3 customer safety + complaints -----------------------------
		rider.POST("/rides/:id/sos", h.PostSOS)
		rider.POST("/rides/:id/share", h.PostShareToken)
		rider.POST("/rides/:id/complain", h.PostComplaint)
		rider.GET("/complaints/me", h.GetMyComplaints)
		rider.GET("/trusted-contact", h.GetTrustedContact)
		rider.PUT("/trusted-contact", h.PutTrustedContact)

		// --- S3 public share view (no auth) ------------------------------
		rider.GET("/share/:token", h.GetSharedRide)
	}

	// --- Admin (gated by AdminGuard + AuditAdmin middleware) -------------
	admin := r.Group("/v1/rider/admin")
	admin.Use(middleware.AdminGuard())
	admin.Use(middleware.AuditAdmin(h.svc.Store()))
	{
		admin.GET("/dashboard", h.AdminDashboard)

		admin.GET("/partners", h.AdminListPartners)
		admin.GET("/partners/:id", h.AdminGetPartner)
		admin.POST("/partners/:id/approve", h.AdminApprovePartner)
		admin.POST("/partners/:id/reject", h.AdminRejectPartner)
		admin.POST("/partners/:id/suspend", h.AdminSuspendPartner)
		admin.POST("/partners/:id/block", h.AdminBlockPartner)

		admin.GET("/documents", h.AdminListDocuments)
		admin.POST("/documents/:id/verify", h.AdminVerifyDocument)
		admin.POST("/documents/:id/reject", h.AdminRejectDocument)

		admin.GET("/vehicles", h.AdminListVehicles)
		admin.POST("/vehicles/:id/verify", h.AdminVerifyVehicle)
		admin.POST("/vehicles/:id/reject", h.AdminRejectVehicle)

		admin.GET("/payments", h.AdminListPayments)
		admin.POST("/payments/:id/verify", h.AdminVerifyPayment)
		admin.POST("/payments/:id/reject", h.AdminRejectPayment)

		admin.GET("/rides", h.AdminListRides)
		admin.GET("/rides/live", h.AdminListLiveRides)
		admin.POST("/rides/:id/cancel", h.AdminCancelRide)

		admin.GET("/complaints", h.AdminListComplaints)
		admin.POST("/complaints/:id/update-status", h.AdminUpdateComplaint)

		admin.GET("/safety-incidents", h.AdminListSafetyIncidents)
		admin.POST("/safety-incidents/:id/acknowledge", h.AdminAcknowledgeIncident)
		admin.POST("/safety-incidents/:id/resolve", h.AdminResolveIncident)

		admin.POST("/cities", h.AdminCreateCity)
		admin.PATCH("/cities/:id", h.AdminUpdateCity)
		admin.POST("/zones", h.AdminCreateZone)
		admin.PATCH("/zones/:id", h.AdminUpdateZone)
		admin.POST("/fare-rules", h.AdminCreateFareRule)
		admin.PATCH("/fare-rules/:id", h.AdminUpdateFareRule)

		admin.GET("/audit-logs", h.AdminListAuditLogs)

		// --- S4 reports ----------------------------------------------
		admin.GET("/reports/revenue", h.AdminRevenueReport)
		admin.GET("/reports/cohort-retention", h.AdminCohortRetention)
		admin.GET("/reports/customer-cohort", h.AdminCustomerCohort)
		admin.GET("/reports/cron-runs", h.AdminCronRuns)
	}
}

// --- helpers --------------------------------------------------------------

// getUserID extracts X-User-ID (or X-User-Id) from headers.
func getUserID(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-ID")
	if raw == "" {
		raw = c.GetHeader("X-User-Id")
	}
	if raw == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "missing user id", nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid user id", nil)
		return uuid.Nil, false
	}
	return id, true
}

// parseUUIDParam parses a route param as a uuid.
func parseUUIDParam(c *gin.Context, param string) (uuid.UUID, bool) {
	raw := c.Param(param)
	id, err := uuid.Parse(raw)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid "+param, nil)
		return uuid.Nil, false
	}
	return id, true
}

// respondServiceError translates the service-layer error-string convention
// into HTTP status codes:
//   - "invalid: …"   -> 400
//   - "forbidden: …" -> 403
//   - "not_found: …" -> 404
//   - everything else -> defaultStatus.
func respondServiceError(c *gin.Context, err error, defaultStatus int, defaultCode string) {
	if err == nil {
		return
	}
	msg := err.Error()
	if detail, ok := strings.CutPrefix(msg, "invalid: "); ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", detail, nil)
		return
	}
	if detail, ok := strings.CutPrefix(msg, "forbidden: "); ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", detail, nil)
		return
	}
	if detail, ok := strings.CutPrefix(msg, "not_found: "); ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", detail, nil)
		return
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, defaultStatus, defaultCode, msg, nil)
}
