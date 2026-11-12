package http

import (
	"net/http"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The buyer's invoice e-mail (notification-service, 13 Nov 2026).
//
//	GET /v1/monetization/internal/premium/invoices/:invoiceId?user_id=<uuid>
//
// notification-service reads the facts its e-mail prints (number, date,
// plan, amount) from here, never from the event (which carries ids only).
// A signed notification-service token carrying
// monetization:premium-invoice-mail, plus the internal key and the premium
// switch like every premium route; never reachable through the gateway (an
// /internal/ path). user_id is the event's buyer: a document that is not
// theirs is 404 NOT_FOUND, exactly as one that does not exist.

const (
	// OpPremiumInvoiceMail is the operation an invoice-mail token carries.
	OpPremiumInvoiceMail = "monetization:premium-invoice-mail" //nolint:gosec // G101: service-token operation name, not a credential
	// IssuerNotificationService is the buyer e-mail's sender.
	IssuerNotificationService = "notification-service"
)

// premiumInvoiceMailCallers: notification-service only.
var premiumInvoiceMailCallers = []string{IssuerNotificationService}

// PremiumInvoiceForMail — GET /v1/monetization/internal/premium/invoices/:invoiceId?user_id=
//
//	200 {"data": {"id","user_id","number","kind","date","pass_id","description","total_paise","currency","original_number"?}}
//	404 NOT_FOUND — no such document, another buyer's, or no valid user_id.
func (h *Handler) PremiumInvoiceForMail(c *gin.Context) {
	userID, err := uuid.Parse(c.Query("user_id"))
	if err != nil || userID == uuid.Nil {
		writePremiumError(c, service.ErrPremiumInvoiceNotFound)
		return
	}
	id, ok := h.premiumInvoiceID(c)
	if !ok {
		return
	}
	out, err := h.premium.InvoiceForMail(c.Request.Context(), userID, id)
	if err != nil {
		writePremiumError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
