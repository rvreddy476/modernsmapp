package http

import (
	"net/http"

	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// payRequestWire is the JSON binding for POST /v1/billpay/pay. We split it
// from service.PayRequest so we can accept "account_id" / "bill_id" as
// strings on the wire and parse to uuid.UUID here.
type payRequestWire struct {
	AccountID      string            `json:"account_id,omitempty"`
	ProviderID     string            `json:"provider_id"`
	Identifier     string            `json:"identifier"`
	AmountPaise    int64             `json:"amount_paise"`
	PaymentMethod  string            `json:"payment_method"`
	IdempotencyKey string            `json:"idempotency_key"`
	BillID         string            `json:"bill_id,omitempty"`
	ExtraParams    map[string]string `json:"extra_params,omitempty"`
}

// PostPay handles POST /v1/billpay/pay.
func (h *Handler) PostPay(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var req payRequestWire
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	provID, err := uuid.Parse(req.ProviderID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid provider_id", nil)
		return
	}
	in := service.PayRequest{
		ProviderID:     provID,
		Identifier:     req.Identifier,
		AmountPaise:    req.AmountPaise,
		PaymentMethod:  req.PaymentMethod,
		IdempotencyKey: req.IdempotencyKey,
		ExtraParams:    req.ExtraParams,
	}
	if req.AccountID != "" {
		id, err := uuid.Parse(req.AccountID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid account_id", nil)
			return
		}
		in.AccountID = &id
	}
	if req.BillID != "" {
		id, err := uuid.Parse(req.BillID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid bill_id", nil)
			return
		}
		in.BillID = &id
	}
	res, err := h.svc.Pay(c.Request.Context(), uid, in)
	if err != nil {
		respondServiceError(c, err, http.StatusBadGateway, "PAY_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, res)
}
