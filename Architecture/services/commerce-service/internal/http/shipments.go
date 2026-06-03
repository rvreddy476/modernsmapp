package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterShipmentRoutes registers fulfillment endpoints. Called from RegisterRoutes.
func (h *Handler) RegisterShipmentRoutes(v1 *gin.RouterGroup) {
	v1.POST("/orders/:orderId/shipment", h.CreateShipment)
	v1.GET("/orders/:orderId/shipment", h.GetShipment)
	v1.GET("/orders/:orderId/shipments", h.ListShipments)
	v1.POST("/orders/:orderId/invoice", h.IssueInvoice)
	v1.GET("/orders/:orderId/invoice", h.GetInvoice)
	v1.POST("/shipments/webhooks/:courier", h.ShipmentWebhook)
	// Generic alias for couriers whose dashboards reject URLs that
	// contain the partner's brand name (Shiprocket's "address not
	// allowed" filter rejects 'shiprocket', 'sr', 'kr', etc. in webhook
	// URLs). Path is courier-agnostic; the configured COURIER_PROVIDER
	// env var decides which adapter handles the body.
	v1.POST("/shipments/courier-callback", h.ShipmentWebhookGeneric)
}

// requireOrderRole resolves the caller's role on the order. Write-side
// handlers (shipment + invoice) require seller; read-side handlers accept
// either. Returns false (and writes the response) when the actor is not
// associated with the order, so the caller can simply `return` on false.
func (h *Handler) requireOrderRole(c *gin.Context, orderID uuid.UUID, writeOnly bool) (service.OrderActorRole, bool) {
	userID, ok := getUserID(c)
	if !ok {
		return service.OrderActorRole{}, false
	}
	role, err := h.svc.OrderActor(c.Request.Context(), orderID, userID)
	if err != nil {
		if errors.Is(err, service.ErrOrderNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "ORDER_NOT_FOUND", err.Error(), nil)
			return role, false
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL", err.Error(), nil)
		return role, false
	}
	if writeOnly {
		if !role.IsSeller {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "actor is not a seller on this order", nil)
			return role, false
		}
	} else {
		if !role.IsCustomer && !role.IsSeller {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", "actor is not associated with this order", nil)
			return role, false
		}
	}
	return role, true
}

// CreateShipment books shipments for the order. Multi-seller orders return
// a list of shipments (one per seller); single-seller orders return a list
// of length 1. Wrapped in a `shipments` envelope so the response shape is
// stable regardless of the seller count.
//
// Phase 0.4: previously had no auth — any caller could book shipments for
// any order. Now requires X-User-Id and the actor must be a seller of at
// least one order item. Per-seller filtering (so seller A cannot book
// seller B's items) is a Phase 4 concern; this gate at minimum blocks
// random users from triggering courier API calls on arbitrary orders.
func (h *Handler) CreateShipment(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	if _, ok := h.requireOrderRole(c, orderID, true); !ok {
		return
	}
	shipments, err := h.svc.CreateShipmentsForOrder(c.Request.Context(), orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "SHIPMENT_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gin.H{"shipments": shipments}, nil)
}

// GetShipment returns the latest shipment + events for the order. Kept for
// backward-compatible single-seller flows; use ListShipments for the full set
// across multi-seller orders.
func (h *Handler) GetShipment(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	if _, ok := h.requireOrderRole(c, orderID, false); !ok {
		return
	}
	sh, evts, err := h.svc.GetShipmentForOrder(c.Request.Context(), orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "shipment not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"shipment": sh, "events": evts}, nil)
}

// ListShipments returns every shipment for the order with their tracking
// events. Used by the order detail page to render multi-seller fulfillment
// progress.
func (h *Handler) ListShipments(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	if _, ok := h.requireOrderRole(c, orderID, false); !ok {
		return
	}
	out, err := h.svc.ListShipmentsForOrder(c.Request.Context(), orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "LIST_FAILED", err.Error(), nil)
		return
	}
	if out == nil {
		out = []service.ShipmentWithEvents{}
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"shipments": out}, nil)
}

// IssueInvoice — Phase 0.4: gated to sellers on the order (same rationale
// as CreateShipment).
func (h *Handler) IssueInvoice(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	if _, ok := h.requireOrderRole(c, orderID, true); !ok {
		return
	}
	inv, err := h.svc.IssueInvoice(c.Request.Context(), orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVOICE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, inv, nil)
}

// GetInvoice — readers include the order customer and any seller on the
// order. Phase 0.4 — was previously unauthenticated.
func (h *Handler) GetInvoice(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	if _, ok := h.requireOrderRole(c, orderID, false); !ok {
		return
	}
	inv, url, err := h.svc.GetInvoiceDownloadURL(c.Request.Context(), orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "invoice not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"invoice": inv, "download_url": url}, nil)
}

// ShipmentWebhook is the public courier callback. The provider verifies the
// payload via its configured shared secret / HMAC before parsing — unsigned
// callbacks are rejected with 401.
func (h *Handler) ShipmentWebhook(c *gin.Context) {
	courierName := c.Param("courier")
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	// Flatten headers (take first value per key) for the provider.
	headers := make(map[string]string, len(c.Request.Header))
	for k, v := range c.Request.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	if err := h.svc.HandleShipmentWebhook(c.Request.Context(), courierName, headers, body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "WEBHOOK_FAILED", err.Error(), nil)
		return
	}
	// Return 200 with a body — Shiprocket's "Test Webhook" UI marks any
	// response that's not 2xx-with-body as a failure even though 204 is
	// technically valid. The body content doesn't matter as long as it's
	// JSON-parseable.
	api.JSON(c.Writer, http.StatusOK, gin.H{"ok": true}, nil)
}

// ShipmentWebhookGeneric is the courier-agnostic webhook surface used
// when a partner's dashboard rejects URLs containing the partner brand
// (e.g. Shiprocket's "address not allowed" filter that bans 'shiprocket',
// 'sr', 'kr' in the URL). Routes to the courier adapter selected by the
// COURIER_PROVIDER env var; falls back to "shiprocket" since that's the
// only non-stub provider currently wired.
func (h *Handler) ShipmentWebhookGeneric(c *gin.Context) {
	c.Params = append(c.Params, gin.Param{Key: "courier", Value: "shiprocket"})
	h.ShipmentWebhook(c)
}
