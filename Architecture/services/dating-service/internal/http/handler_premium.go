// HTTP handlers for /v1/dating/premium/* (lane P2): one-off passes and Boost
// paid through payments-service. The Razorpay checkout, cancel and webhook
// routes are gone; checkout and cancel answer 410 so an old client learns why.
package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/atpost/dating-service/internal/payments"
	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/paymentmethod"
	"github.com/gin-gonic/gin"
)

// Premium error codes.
const (
	CodePremiumUnavailable         = "PREMIUM_UNAVAILABLE"
	CodePremiumPaymentsUnavailable = "PREMIUM_PAYMENTS_UNAVAILABLE"
	CodePremiumPaymentsRefused     = "PREMIUM_PAYMENTS_REFUSED"
	CodeClientPriceRefused         = "CLIENT_PRICE_REFUSED"
	CodeInvalidProduct             = "INVALID_PRODUCT"
	CodePaymentMethodInvalid       = "PAYMENT_METHOD_INVALID"
	CodeIdempotencyKeyRequired     = "IDEMPOTENCY_KEY_REQUIRED"
	CodeIdempotencyKeyReused       = "IDEMPOTENCY_KEY_REUSED"
	CodePurchaseIntentConflict     = "PURCHASE_INTENT_CONFLICT"
	CodePurchaseNotFound           = "PURCHASE_NOT_FOUND"
	CodeSubscriptionsRemoved       = "PREMIUM_SUBSCRIPTIONS_REMOVED"
	CodePlansMoved                 = "PREMIUM_PLANS_MOVED"
)

// maxPurchaseBody bounds the purchase request body.
const maxPurchaseBody = 4 << 10

// GetPremiumCatalogue — GET /v1/dating/premium/catalogue.
func (h *Handler) GetPremiumCatalogue(c *gin.Context) {
	api.JSON(c.Writer, http.StatusOK, gin.H{"products": h.svc.PremiumCatalogue()}, nil)
}

// purchaseBody is the purchase request. The price fields are declared ONLY so
// their presence can be refused: a price comes from the catalogue, never from
// the client.
type purchaseBody struct {
	Product        string `json:"product"`
	IdempotencyKey string `json:"idempotency_key"`
	Method         string `json:"method"`

	AmountMinor json.RawMessage `json:"amount_minor"`
	Amount      json.RawMessage `json:"amount"`
	Price       json.RawMessage `json:"price"`
	PriceMinor  json.RawMessage `json:"price_minor"`
	Currency    json.RawMessage `json:"currency"`
}

func (b purchaseBody) carriesPrice() bool {
	for _, v := range []json.RawMessage{b.AmountMinor, b.Amount, b.Price, b.PriceMinor, b.Currency} {
		if len(v) > 0 {
			return true
		}
	}
	return false
}

// PostPremiumPurchase — POST /v1/dating/premium/purchases
// {product, idempotency_key, method?}. 201 with {purchase, client_session}
// for a new purchase, 200 for a repeat of the same key.
func (h *Handler) PostPremiumPurchase(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, maxPurchaseBody+1))
	if err != nil || len(raw) > maxPurchaseBody {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "request body is unreadable or too large", nil)
		return
	}
	var body purchaseBody
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY",
			"body must be JSON with product, idempotency_key and optionally method", nil)
		return
	}
	if body.carriesPrice() {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeClientPriceRefused,
			"prices come from the premium catalogue; do not send amount, price or currency", nil)
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = c.GetHeader("Idempotency-Key")
	}
	out, created, err := h.svc.CreatePremiumPurchase(c.Request.Context(), userID, service.PremiumPurchaseInput{
		Product: body.Product, IdempotencyKey: body.IdempotencyKey, Method: body.Method,
	})
	if err != nil {
		writePremiumError(c, err, "PREMIUM_PURCHASE_FAILED")
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	api.JSON(c.Writer, status, out, nil)
}

// GetPremiumPurchasePayment — GET /v1/dating/premium/purchases/:id/payment.
// {purchase_id, product, status: confirming|paid|failed, amount_minor,
// currency, refund_status, updated_at}. Another user's purchase is the same
// 404 as a missing one.
func (h *Handler) GetPremiumPurchasePayment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	purchaseID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	out, err := h.svc.PremiumPurchasePayment(c.Request.Context(), userID, purchaseID)
	if err != nil {
		writePremiumError(c, err, "PREMIUM_PAYMENT_STATUS_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// GetMyPremium — GET /v1/dating/premium/me.
func (h *Handler) GetMyPremium(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.MyPremium(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// premiumSubscriptionsRemoved answers the retired Razorpay subscription
// routes (checkout, cancel): 410 with a stable code.
func premiumSubscriptionsRemoved(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, CodeSubscriptionsRemoved,
		"Premium subscriptions were removed; buy a one-off pass or Boost with POST /v1/dating/premium/purchases",
		gin.H{"moved_to": "/v1/dating/premium/purchases", "catalogue": "/v1/dating/premium/catalogue"})
	c.Abort()
}

// premiumPlansMoved answers the retired plan list.
func premiumPlansMoved(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusGone, CodePlansMoved,
		"the plan list moved to GET /v1/dating/premium/catalogue",
		gin.H{"moved_to": "/v1/dating/premium/catalogue"})
	c.Abort()
}

func writePremiumError(c *gin.Context, err error, defaultCode string) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrPremiumUnavailable), errors.Is(err, service.ErrPremiumCheckoutUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodePremiumUnavailable,
			"premium purchases are unavailable right now", nil)
	case errors.Is(err, payments.ErrPaymentsUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodePremiumPaymentsUnavailable,
			"payments are unavailable; retry with the same idempotency_key", nil)
	case errors.Is(err, payments.ErrRefused):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway, CodePremiumPaymentsRefused,
			"payments refused this purchase", nil)
	case errors.Is(err, service.ErrPremiumProductUnknown):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidProduct,
			"product must be one of the catalogue products", gin.H{"allowed": payments.ProductIDs()})
	case errors.Is(err, service.ErrPremiumMethodInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodePaymentMethodInvalid,
			"method must be a launch payment method", gin.H{"allowed": paymentmethod.Allowed()})
	case errors.Is(err, service.ErrPremiumIdempotencyKeyRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeIdempotencyKeyRequired, err.Error(), nil)
	case errors.Is(err, store.ErrIdempotencyKeyReused):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeIdempotencyKeyReused,
			"this idempotency_key already names a different purchase", nil)
	case errors.Is(err, store.ErrPurchaseIntentConflict):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodePurchaseIntentConflict, err.Error(), nil)
	case errors.Is(err, store.ErrPurchaseNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodePurchaseNotFound, "purchase not found", nil)
	default:
		respondServiceError(c, err, http.StatusInternalServerError, defaultCode)
	}
}

// PostBoost — POST /v1/dating/pulse/boost.
//
// A purchased Boost token, or a pass holder's daily boost. Service applies
// the rate limit and returns a 403 envelope when blocked.
func (h *Handler) PostBoost(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.RequestBoost(c.Request.Context(), userID)
	if err != nil {
		if hasPrefix(err.Error(), "forbidden:") {
			if out != nil {
				api.JSON(c.Writer, http.StatusForbidden, out, nil)
				return
			}
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, "FORBIDDEN", err.Error()[len("forbidden: "):], nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "BOOST_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
