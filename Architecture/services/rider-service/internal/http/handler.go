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
	jwtKeys     middleware.JWTKeySet
}

// New constructs a Handler.
func New(svc *service.Service, internalKey string) *Handler {
	keys, err := middleware.LoadJWTKeySet()
	if err != nil {
		keys = middleware.JWTKeySet{}
	}
	keys.InternalKey = internalKey
	return &Handler{
		svc:         svc,
		internalKey: internalKey,
		jwtKeys:     keys,
	}
}

// RegisterRoutes registers all /v1/rider routes on the provided engine.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	rider := r.Group("/v1/rider")
	{
		// --- Public Routes (Unauthenticated) ------------------------------
		rider.GET("/cities", h.GetCities)
		rider.GET("/serviceability", h.GetServiceability)
		rider.POST("/estimate", h.PostEstimate)
		rider.GET("/share/:token", h.GetSharedRide)

		// --- Protected Routes (AuthRequired strictly enforced) ------------
		protected := rider.Group("")
		protected.Use(middleware.AuthRequired(h.jwtKeys))
		{
			// --- Realtime token (issued for SSE subscription) ----------------
			protected.POST("/realtime/token", h.IssueRealtimeToken)

			// --- Customer rides ----------------------------------------------
			protected.POST("/rides", h.PostRide)
			protected.GET("/rides/active", h.GetActiveRide)
			protected.GET("/rides/me", h.GetMyRides)
			protected.GET("/rides/:id", h.GetRide)
			protected.GET("/rides/:id/receipt", h.GetRideReceipt)
			protected.POST("/rides/:id/cancel", h.PostCancelRide)
			protected.POST("/rides/:id/rate", h.PostRateRide)

			// --- Partner ride lifecycle (S2) ----------------------------------
			protected.POST("/rides/:id/arriving", h.PostMarkArriving)
			protected.POST("/rides/:id/arrived", h.PostMarkArrived)
			protected.POST("/rides/:id/start", h.PostStartRide)
			protected.POST("/rides/:id/complete", h.PostCompleteRide)
			protected.POST("/rides/:id/payment/cash-confirm", h.PostConfirmCashPayment)
			protected.POST("/rides/:id/no-show", h.PostMarkNoShow)
			protected.POST("/safety/masked-call", h.PostInitiateMaskedCall)
			protected.POST("/rides/:id/rating/response", h.PostPartnerRespondRating)
			protected.GET("/rides/:id/messages", h.ListRideMessages)
			protected.POST("/rides/:id/messages", h.PostRideMessage)
			protected.POST("/rides/:id/messages/:msgId/read", h.MarkRideMessageRead)

			// --- Partner ops (online/offline/location/dashboard) (S2) ---------
			protected.POST("/partners/me/online", h.PostGoOnline)
			protected.POST("/partners/me/offline", h.PostGoOffline)
			protected.POST("/partners/me/location", h.PostUpdateLocation)
			protected.GET("/partners/me/dashboard", h.GetPartnerDashboard)
			protected.GET("/partners/me/earnings", h.GetPartnerEarnings)

			// --- Offers (S2) -------------------------------------------------
			protected.GET("/offers/incoming", h.GetIncomingOffers)
			protected.POST("/offers/:id/accept", h.PostAcceptOffer)
			protected.POST("/offers/:id/reject", h.PostRejectOffer)

			// --- Partner profile ----------------------------------------------
			protected.POST("/partners", h.PostPartner)
			protected.GET("/partners/me", h.GetMyPartner)
			protected.PATCH("/partners/me", h.PatchMyPartner)

			protected.POST("/partners/me/documents", h.PostMyDocument)
			protected.GET("/partners/me/documents", h.GetMyDocuments)

			protected.POST("/partners/me/aadhaar/start", h.PostAadhaarStart)
			protected.POST("/partners/me/aadhaar/callback", h.PostAadhaarCallback)

			protected.POST("/partners/me/vehicles", h.PostVehicle)
			protected.GET("/partners/me/vehicles", h.GetMyVehicles)

			protected.POST("/vehicles/:id/documents", h.PostVehicleDocument)
			protected.GET("/vehicles/:id/documents", h.GetVehicleDocuments)

			// --- Subscription -------------------------------------------------
			protected.GET("/subscriptions/plans", h.GetPlans)
			protected.POST("/subscriptions/subscribe", h.PostSubscribe)
			protected.POST("/subscriptions/payment-proof", h.PostPaymentProof)
			protected.GET("/subscriptions/me", h.GetMySubscription)

			// --- S3 customer safety + complaints -----------------------------
			protected.POST("/rides/:id/sos", h.PostSOS)
			protected.POST("/rides/:id/share", h.PostShareToken)
			protected.DELETE("/rides/:id/share", h.DeleteShareToken)
			protected.POST("/rides/:id/complain", h.PostComplaint)
			protected.GET("/complaints/me", h.GetMyComplaints)
			protected.GET("/trusted-contact", h.GetTrustedContact)
			protected.PUT("/trusted-contact", h.PutTrustedContact)
		}
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
		admin.GET("/safety/incidents/:id/alerts", h.AdminListSafetyContactAlerts)
		admin.POST("/rides/:id/rating/visibility", h.AdminHideRideRating)
		admin.GET("/reports/matching-health", h.AdminMatchingHealthReport)
		admin.GET("/reports/partner-quality", h.AdminPartnerQualityReport)
		admin.GET("/reports/supply-demand", h.AdminSupplyDemandReport)
		admin.GET("/reports/safety", h.AdminSafetyIncidentReport)
		admin.GET("/reports/compliance", h.AdminPartnerComplianceReport)
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

// getUserID extracts the verified user UUID from JWT middleware context.
func getUserID(c *gin.Context) (uuid.UUID, bool) {
	if uid, ok := middleware.GetAuthenticatedUserID(c); ok && uid != uuid.Nil {
		return uid, true
	}

	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "Authorization Bearer token or trusted edge required", nil)
	return uuid.Nil, false
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
