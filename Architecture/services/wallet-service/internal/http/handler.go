// Package http wires gin routes to the wallet service.
package http

import (
	"net/http"
	"strings"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/middleware"
	"github.com/atpost/wallet-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler is the wallet-service HTTP layer.
type Handler struct {
	svc          *service.Service
	internalKey  string // when non-empty, internal endpoints require X-Internal-Service-Key
	digilockerBaseURL string
	digilockerRedirect string
	aadhaarVerifier service.AadhaarVerifier
}

// New constructs a Handler. internalKey wires the RequireInternalKey middleware
// onto /internal/* routes. aadhaarVerifier may be nil (Aadhaar endpoints will
// then return 503).
func New(svc *service.Service, internalKey, digilockerBaseURL, digilockerRedirect string, aadhaarVerifier service.AadhaarVerifier) *Handler {
	return &Handler{
		svc:                svc,
		internalKey:        internalKey,
		digilockerBaseURL:  digilockerBaseURL,
		digilockerRedirect: digilockerRedirect,
		aadhaarVerifier:    aadhaarVerifier,
	}
}

// RegisterRoutes registers all /v1/wallet routes on the provided engine.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	wallet := r.Group("/v1/wallet")
	{
		// Public (user) endpoints.
		wallet.GET("/balance", h.GetBalance)

		wallet.POST("/top-up", h.PostTopUp)
		wallet.POST("/top-up/:id/confirm", h.PostConfirmTopUp)
		wallet.GET("/top-up/:id", h.GetTopUp)

		wallet.POST("/send", h.PostSend)
		wallet.GET("/transactions", h.ListTransactions)
		wallet.GET("/transactions/:id", h.GetTransactionDetail)
		wallet.GET("/recipients", h.ListRecipients)

		wallet.GET("/kyc", h.GetKYC)
		wallet.POST("/kyc/aadhaar/start", h.StartAadhaar)
		wallet.POST("/kyc/aadhaar/callback", h.AadhaarCallback)
		wallet.POST("/kyc/pan", h.SubmitPAN)
	}

	// Internal-only routes for other services. RequireInternalKey panics if
	// secret is empty, so we gate registration on a non-empty key.
	if h.internalKey != "" {
		internal := r.Group("/v1/wallet/internal")
		internal.Use(middleware.RequireInternalKey(h.internalKey))
		{
			internal.POST("/debit", h.InternalDebit)
			internal.POST("/refund", h.InternalRefund)
			internal.GET("/balance/:user_id", h.InternalBalance)
		}
	}
}

// --- helpers --------------------------------------------------------------

// getUserID extracts the X-User-ID header (or X-User-Id; matching dating-service).
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

// parseUUIDParam parses a route param uuid.
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
// into HTTP status codes.
//   - "invalid: …" → 400
//   - "forbidden: …" → 403
//   - "not_found: …" → 404
//   - everything else → defaultStatus.
//
// Mirrors the dating-service pattern so handlers stay one-liners.
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
