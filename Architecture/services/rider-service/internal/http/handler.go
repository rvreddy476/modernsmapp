// Package http wires gin routes to the rider-service.
package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/rider-service/internal/http/middleware"
	"github.com/atpost/rider-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler is the rider-service HTTP layer.
type Handler struct {
	svc         *service.Service
	internalKey string
	// verifier admits admin-service tokens on the token-only admin family
	// (admin_token.go); nil refuses every token.
	verifier *servicetoken.Verifier
	// audit overrides the admin audit sink (tests); nil means the store.
	audit middleware.AuditWriter
}

// New constructs a Handler.
func New(svc *service.Service, internalKey string) *Handler {
	return &Handler{svc: svc, internalKey: internalKey}
}

// CheckInternalKey refuses a production process with no internal service
// key. Every /v1/rider handler trusts X-User-Id and X-Scopes, so without the
// key anything that can reach the pod can be any rider, partner or admin.
func CheckInternalKey(production bool, key string) error {
	if production && strings.TrimSpace(key) == "" {
		return errors.New("INTERNAL_SERVICE_KEY is required in production: /v1/rider trusts gateway identity headers")
	}
	return nil
}

// RegisterRoutes registers all /v1/rider routes on the provided engine.
//
// Every route, admin included, sits behind X-Internal-Service-Key when a key
// is configured: the handlers trust X-User-Id and X-Scopes, which only the
// gateway may set. The check is on the group, not the engine, so /healthz and
// /metrics stay open whatever order main registers them in. main refuses to
// start in production without a key (CheckInternalKey).
//
// The service never verifies a bearer token itself. The gateway is the only
// party that verifies JWTs, applies the dormant-product gate
// (RIDER_PUBLIC_ENABLED) and the session-revocation check; a bearer path here
// would let a caller who can reach the pod skip all three.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	rider := r.Group("/v1/rider")
	if h.internalKey != "" {
		rider.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	{
		// --- Public Routes (no identity required) -------------------------
		rider.GET("/cities", h.GetCities)
		rider.GET("/serviceability", h.GetServiceability)
		rider.POST("/estimate", h.PostEstimate)
		rider.GET("/share/:token", h.GetSharedRide)

		// --- Protected Routes (gateway identity required) -----------------
		protected := rider.Group("")
		protected.Use(middleware.GatewayIdentity())
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

	// --- Admin (LEGACY: gated by AdminGuard + AuditAdmin middleware) -----
	// Nested under rider so the internal-key check runs before AdminGuard.
	// The route table is adminRoutes (admin_token.go); this family ignores
	// the per-route permission and keeps today's all-or-nothing admin scope.
	admin := rider.Group("/admin")
	admin.Use(middleware.AdminGuard())
	admin.Use(middleware.AuditAdmin(h.auditWriter()))
	for _, rt := range h.adminRoutes() {
		admin.Handle(rt.method, rt.path, rt.handler)
	}

	// --- Admin console (admin-service tokens only) ----------------------
	// On the engine root, outside the internal-key group: the key is neither
	// required nor evidence there. See admin_token.go.
	h.registerInternalAdminRoutes(r)
}

// --- helpers --------------------------------------------------------------

// getUserID extracts the verified user UUID from JWT middleware context.
func getUserID(c *gin.Context) (uuid.UUID, bool) {
	if uid, ok := middleware.GetAuthenticatedUserID(c); ok && uid != uuid.Nil {
		return uid, true
	}

	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "authentication required", nil)
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
