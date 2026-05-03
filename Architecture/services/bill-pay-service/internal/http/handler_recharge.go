package http

import (
	"net/http"

	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type rechargeMobileWire struct {
	Phone          string `json:"phone"`
	Operator       string `json:"operator"`
	Circle         string `json:"circle"`
	AmountPaise    int64  `json:"amount_paise"`
	PlanID         string `json:"plan_id,omitempty"`
	PaymentMethod  string `json:"payment_method"`
	IdempotencyKey string `json:"idempotency_key"`
}

// PostRechargeMobile handles POST /v1/billpay/recharge/mobile.
func (h *Handler) PostRechargeMobile(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var req rechargeMobileWire
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	in := service.RechargeMobileRequest{
		Phone:          req.Phone,
		Operator:       req.Operator,
		Circle:         req.Circle,
		AmountPaise:    req.AmountPaise,
		PaymentMethod:  req.PaymentMethod,
		IdempotencyKey: req.IdempotencyKey,
	}
	if req.PlanID != "" {
		id, err := uuid.Parse(req.PlanID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid plan_id", nil)
			return
		}
		in.PlanID = &id
	}
	res, err := h.svc.RechargeMobile(c.Request.Context(), uid, in)
	if err != nil {
		respondServiceError(c, err, http.StatusBadGateway, "RECHARGE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, res)
}

// GetOperatorCircle handles GET /v1/billpay/recharge/operator-circle?phone=
func (h *Handler) GetOperatorCircle(c *gin.Context) {
	phone := c.Query("phone")
	op, cir, err := h.svc.DetectOperatorCircle(c.Request.Context(), phone)
	if err != nil {
		respondServiceError(c, err, http.StatusBadGateway, "DETECT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{
		"operator": op, "circle": cir,
	})
}

// GetMobilePlans handles GET /v1/billpay/recharge/plans?operator=&circle=
func (h *Handler) GetMobilePlans(c *gin.Context) {
	op := c.Query("operator")
	cir := c.Query("circle")
	plans, err := h.svc.ListMobilePlans(c.Request.Context(), op, cir)
	if err != nil {
		respondServiceError(c, err, http.StatusBadGateway, "PLANS_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, plans)
}
