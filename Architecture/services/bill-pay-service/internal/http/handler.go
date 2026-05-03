// Package http wires gin routes to the bill-pay service.
package http

import (
	"net/http"
	"strings"

	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Handler is the bill-pay-service HTTP layer.
type Handler struct {
	svc         *service.Service
	internalKey string
}

// New constructs a Handler. internalKey wires RequireInternalKey onto
// /internal/* routes (incl. the Setu webhook receiver). Empty key = internal
// routes are not registered.
func New(svc *service.Service, internalKey string) *Handler {
	return &Handler{svc: svc, internalKey: internalKey}
}

// RegisterRoutes registers all /v1/billpay routes on the provided engine.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	g := r.Group("/v1/billpay")
	{
		g.GET("/categories", h.GetCategories)
		g.GET("/providers", h.ListProviders)
		g.GET("/providers/:id", h.GetProvider)

		g.GET("/accounts", h.ListAccounts)
		g.POST("/accounts", h.CreateAccount)
		g.PATCH("/accounts/:id", h.PatchAccount)
		g.DELETE("/accounts/:id", h.DeleteAccount)
		g.GET("/accounts/:id/bill", h.GetAccountBill)

		g.POST("/pay", h.PostPay)
		g.GET("/payments", h.ListPayments)
		g.GET("/payments/:id", h.GetPayment)

		g.POST("/recharge/mobile", h.PostRechargeMobile)
		g.GET("/recharge/operator-circle", h.GetOperatorCircle)
		g.GET("/recharge/plans", h.GetMobilePlans)

		g.GET("/reminders", h.ListReminders)
		g.POST("/reminders", h.CreateReminder)
		g.DELETE("/reminders/:id", h.DeleteReminder)

		g.GET("/scheduled", h.ListScheduled)
		g.POST("/scheduled", h.CreateScheduled)
		g.PATCH("/scheduled/:id", h.PatchScheduled)
		g.DELETE("/scheduled/:id", h.DeleteScheduled)
	}

	// Internal-only — Setu webhook + any service-to-service routes. Setu's
	// signature verification adds a second auth layer; the internal-key
	// gate keeps random callers from probing the endpoint at all.
	if h.internalKey != "" {
		internal := r.Group("/v1/billpay/internal")
		internal.Use(middleware.RequireInternalKey(h.internalKey))
		{
			internal.POST("/setu-webhook", h.SetuWebhook)
		}
	}
}

// --- helpers -------------------------------------------------------------

// getUserID extracts the X-User-ID (or X-User-Id) header.
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
// into HTTP status codes. Mirrors wallet-service.
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
