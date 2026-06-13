package http

import (
	"net/http"

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
	body, ctype, num, err := h.svc.GenerateOrderInvoice(c.Request.Context(), uid, orderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "INVOICE_FAILED", err.Error(), nil)
		return
	}
	c.Writer.Header().Set("Content-Type", ctype)
	c.Writer.Header().Set("X-Invoice-Number", num)
	c.Writer.Header().Set("Content-Disposition",
		`inline; filename="`+num+`.html"`)
	_, _ = c.Writer.Write(body)
}
