package http

import (
	"net/http"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// PlaceOrderRequest is the body of POST /v1/food/orders.
type PlaceOrderRequest struct {
	AddressID           string `json:"address_id"`
	PaymentMethod       string `json:"payment_method"`
	CouponCode          string `json:"coupon_code"`
	CustomerInstruction string `json:"customer_instruction"`
	// ExpectedTotalPaise is optional (food server lane, 9 Nov 2026): the
	// total the app showed. A different server total is refused with 409
	// FOOD_ORDER_TOTAL_CHANGED (error.details.server_total_paise) and no order
	// is created. Absent, placement behaves exactly as before.
	ExpectedTotalPaise *int64 `json:"expected_total_paise"`
}

// PlaceOrderChecked — POST /v1/food/orders. PlaceOrder (handler.go) plus the
// optional expected_total_paise. It replaces PlaceOrder on the route once
// handler.go is free to edit (the founder's admin-scope change is pending
// there); until then this handler is wired only in tests.
func (h *Handler) PlaceOrderChecked(c *gin.Context) {
	userID, ok := h.currentUserID(c)
	if !ok {
		return
	}
	idempotencyKey := c.GetHeader("Idempotency-Key")
	if idempotencyKey == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "IDEMPOTENCY_KEY_REQUIRED", "Idempotency-Key header is required", nil)
		return
	}
	var body PlaceOrderRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if body.ExpectedTotalPaise != nil && *body.ExpectedTotalPaise < 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "expected_total_paise must not be negative", nil)
		return
	}
	addressID, err := uuid.Parse(body.AddressID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ADDRESS", "invalid address id", nil)
		return
	}
	order, err := h.svc.PlaceOrder(c.Request.Context(), userID, postgres.PlaceOrderInput{
		AddressID:           addressID,
		PaymentMethod:       body.PaymentMethod,
		CouponCode:          body.CouponCode,
		CustomerInstruction: body.CustomerInstruction,
		ExpectedTotalPaise:  body.ExpectedTotalPaise,
	}, idempotencyKey)
	if err != nil {
		if writeKnownError(c, err) {
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "FOOD_ORDER_PLACE_FAILED", err.Error(), nil)
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusCreated, order)
}
