package http

import (
	"net/http"
	"strings"

	"github.com/atpost/food-service/internal/foodinvoice"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CustomerGetInvoice — GET /v1/food/orders/:orderId/invoice
//
// Returns the rendered HTML tax invoice. Customer-only (the order
// must belong to X-User-Id). Browsers render the HTML and can save
// as PDF via Cmd+P; a future Renderer can swap to true PDF via
// wkhtmltopdf without touching this handler.
func (h *Handler) CustomerGetInvoice(c *gin.Context) {
	uid, ok := h.requireUser(c)
	if !ok {
		return
	}
	orderID, err := uuid.Parse(c.Param("orderId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ORDER_ID", err.Error(), nil)
		return
	}
	doc, err := h.svc.GetOrderInvoice(c.Request.Context(), uid, orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "INVOICE_FAILED", err.Error(), nil)
		return
	}
	// Wave 1 B3: additive headers for the per-party invoice numbers and the
	// adviser marker; X-Invoice-Number keeps its meaning (the primary number).
	num := service.PrimaryInvoiceNumber(doc)
	header := c.Writer.Header()
	header.Set("X-Invoice-Number", num)
	if n := service.SectionInvoiceNumber(doc, foodinvoice.IssuerPlatform); n != "" {
		header.Set("X-Platform-Invoice-Number", n)
	}
	if n := service.SectionInvoiceNumber(doc, foodinvoice.IssuerRestaurant); n != "" {
		header.Set("X-Restaurant-Invoice-Number", n)
	}
	if doc.NeedsAdviserConfirmation {
		header.Set("X-Tax-Adviser-Confirmation", "pending")
	}
	// ?format=json (or Accept: application/json) returns the invoice document
	// the HTML is rendered from.
	if strings.EqualFold(c.Query("format"), "json") || strings.Contains(c.GetHeader("Accept"), "application/json") {
		api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, doc)
		return
	}
	body, err := foodinvoice.RenderHTML(doc)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INVOICE_FAILED", "invoice could not be rendered", nil)
		return
	}
	header.Set("Content-Type", "text/html; charset=utf-8")
	header.Set("Content-Disposition", `inline; filename="`+strings.ReplaceAll(num, "/", "_")+`.html"`)
	_, _ = c.Writer.Write(body)
}
