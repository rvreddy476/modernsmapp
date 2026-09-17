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
// Surface (Sprint 1 scope per mopedu/IMPLEMENTATION_PLAN.md §3):
//   - public: GET /cities, POST /estimate
//   - customer: POST /rides, GET /rides/:id, GET /rides/me
//   - partner: profile / KYC / Aadhaar / vehicles / vehicle docs
//   - subscription: list plans, subscribe, payment-proof, GET me
//
// Admin routes are stubbed-out in S3.
//
// Every route, admin included, sits behind X-Internal-Service-Key when a key
// is configured: the handlers trust X-User-Id and X-Scopes, which only the
// gateway may set. The check is on the group, not the engine, so /healthz and
// /metrics stay open whatever order main registers them in. main refuses to
// start in production without a key (CheckInternalKey).
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	rider := r.Group("/v1/rider")
	if h.internalKey != "" {
		rider.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	{
		// --- Public -------------------------------------------------------
		rider.GET("/cities", h.GetCities)
		rider.POST("/estimate", h.PostEstimate)

		// --- Realtime token (issued for SSE subscription) ----------------
		rider.POST("/realtime/token", h.IssueRealtimeToken)

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
		rider.POST("/rides/:id/no-show", h.PostMarkNoShow)
		rider.POST("/safety/masked-call", h.PostInitiateMaskedCall)
		rider.POST("/rides/:id/rating/response", h.PostPartnerRespondRating)
		rider.GET("/rides/:id/messages", h.ListRideMessages)
		rider.POST("/rides/:id/messages", h.PostRideMessage)
		rider.POST("/rides/:id/messages/:msgId/read", h.MarkRideMessageRead)

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
