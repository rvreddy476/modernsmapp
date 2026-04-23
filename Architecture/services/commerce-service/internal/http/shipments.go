package http

import (
	"io"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// RegisterShipmentRoutes registers fulfillment endpoints. Called from RegisterRoutes.
func (h *Handler) RegisterShipmentRoutes(v1 *gin.RouterGroup) {
	v1.POST("/orders/:orderId/shipment", h.CreateShipment)
	v1.GET("/orders/:orderId/shipment", h.GetShipment)
	v1.POST("/orders/:orderId/invoice", h.IssueInvoice)
	v1.GET("/orders/:orderId/invoice", h.GetInvoice)
	v1.POST("/shipments/webhooks/:courier", h.ShipmentWebhook)
}

func (h *Handler) CreateShipment(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	sh, err := h.svc.CreateShipmentForOrder(c.Request.Context(), orderID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "SHIPMENT_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, sh, nil)
}

func (h *Handler) GetShipment(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	sh, evts, err := h.svc.GetShipmentForOrder(c.Request.Context(), orderID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "shipment not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"shipment": sh, "events": evts}, nil)
}

func (h *Handler) IssueInvoice(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	inv, err := h.svc.IssueInvoice(c.Request.Context(), orderID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVOICE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, inv, nil)
}

func (h *Handler) GetInvoice(c *gin.Context) {
	orderID, ok := parseUUID(c, "orderId")
	if !ok {
		return
	}
	inv, url, err := h.svc.GetInvoiceDownloadURL(c.Request.Context(), orderID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "invoice not found", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusUnauthorized, "WEBHOOK_FAILED", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}
