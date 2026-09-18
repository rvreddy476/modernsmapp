package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GetPlans — GET /v1/rider/subscriptions/plans. Public (read-only).
func (h *Handler) GetPlans(c *gin.Context) {
	plans, err := h.svc.ListPlans(c.Request.Context())
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "PLANS_LIST_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, plans)
}

// subscribeRequest — body for POST /v1/rider/subscriptions/subscribe
// (legacy wallet route).
type subscribeRequest struct {
	PlanID         uuid.UUID `json:"plan_id"`
	PaymentMethod  string    `json:"payment_method"`
	IdempotencyKey string    `json:"idempotency_key"`
}

// PostSubscribe — POST /v1/rider/subscriptions/subscribe. Wallet only;
// upi / card answer 400 pointing at /subscriptions/checkout.
func (h *Handler) PostSubscribe(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var body subscribeRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.Subscribe(c.Request.Context(), uid, body.PlanID, body.PaymentMethod, body.IdempotencyKey)
	if err != nil {
		if respondPaymentError(c, err) {
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "SUBSCRIBE_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// checkoutRequest — body for POST /v1/rider/subscriptions/checkout.
type checkoutRequest struct {
	PlanCode string `json:"plan_code"`
	Method   string `json:"method"`
}

// PostSubscriptionCheckout — POST /v1/rider/subscriptions/checkout. Opens
// the payments intent for the plan (or activates the free trial instantly,
// once per partner). The subscription activates only from the signed
// payment.succeeded event; poll GET /subscriptions/me/payment.
func (h *Handler) PostSubscriptionCheckout(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var body checkoutRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.CheckoutSubscription(c.Request.Context(), uid, body.PlanCode, body.Method)
	if err != nil {
		if respondPaymentError(c, err) {
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "SUBSCRIPTION_CHECKOUT_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// GetMySubscriptionPayment — GET /v1/rider/subscriptions/me/payment.
func (h *Handler) GetMySubscriptionPayment(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetMySubscriptionPayment(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SUBSCRIPTION_PAYMENT_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}

// paymentProofRequest — body for the retired
// POST /v1/rider/subscriptions/payment-proof.
type paymentProofRequest struct {
	PaymentID uuid.UUID `json:"payment_id"`
	FileURL   string    `json:"file_url"`
}

// PostPaymentProof — POST /v1/rider/subscriptions/payment-proof. Retired:
// always 410 SUBSCRIPTION_PROOF_GONE pointing at /subscriptions/checkout.
func (h *Handler) PostPaymentProof(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	var body paymentProofRequest
	_ = c.ShouldBindJSON(&body)
	_, err := h.svc.SubmitPaymentProof(c.Request.Context(), uid, body.PaymentID, body.FileURL)
	if respondPaymentError(c, err) {
		return
	}
	respondServiceError(c, err, http.StatusGone, "SUBSCRIPTION_PROOF_GONE")
}

// GetMySubscription — GET /v1/rider/subscriptions/me.
func (h *Handler) GetMySubscription(c *gin.Context) {
	uid, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.GetMySubscription(c.Request.Context(), uid)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "SUBSCRIPTION_FETCH_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, out)
}
