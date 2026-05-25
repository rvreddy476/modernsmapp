package http

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"

	"github.com/atpost/payments-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc           *service.Service
	internalKey   string
	webhookSecret string
}

func New(svc *service.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) WithInternalKey(key string) *Handler {
	h.internalKey = key
	return h
}

// WithWebhookSecret enables HMAC-SHA256 verification of Razorpay webhooks.
// When set, requests with a missing or mismatched X-Razorpay-Signature are
// rejected with 401. Empty secret keeps signature checks off (dev/test).
func (h *Handler) WithWebhookSecret(secret string) *Handler {
	h.webhookSecret = secret
	return h
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	// Audit P5: /webhook must NOT live behind the internal-service-key
	// gate — Razorpay's webhook delivery has no way to send that header,
	// so a configuration with both internalKey and webhookSecret set
	// (the production target) would lock Razorpay out and break every
	// payment status update. The webhook is authenticated solely by
	// its HMAC-SHA256 X-Razorpay-Signature; that check happens inside
	// HandleWebhook itself. Register the webhook route on the bare
	// engine before applying the internal-key middleware to /v1/payments.
	r.POST("/v1/payments/webhook", h.HandleWebhook)

	v1 := r.Group("/v1/payments")
	if h.internalKey != "" {
		v1.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	{
		v1.POST("/intents", h.InitiatePayment)
		v1.GET("/intents/:id", h.GetIntent)
		v1.PATCH("/intents/:id/status", h.UpdateStatus)
		v1.POST("/intents/:id/verify", h.VerifyIntent) // Phase 0.1b — synchronous signature verify for commerce-service
		v1.POST("/intents/:id/refund", h.InitiateRefund)
		v1.GET("/intents", h.ListByReference)
		v1.POST("/holds/:intentId/release", h.ReleaseHold)
	}
}

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	str := c.GetHeader("X-User-Id")
	if str == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(str)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil)
		return uuid.Nil, false
	}
	return id, true
}

// InitiatePayment POST /v1/payments/intents
func (h *Handler) InitiatePayment(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body struct {
		PayeeID        string  `json:"payee_id" binding:"required"`
		ReferenceType  string  `json:"reference_type" binding:"required"`
		ReferenceID    string  `json:"reference_id" binding:"required"`
		Amount         float64 `json:"amount" binding:"required"`
		Currency       string  `json:"currency"`
		Method         string  `json:"method" binding:"required"`
		IdempotencyKey string  `json:"idempotency_key"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if body.IdempotencyKey == "" {
		body.IdempotencyKey = uuid.New().String()
	}
	payeeID, _ := uuid.Parse(body.PayeeID)
	refID, _ := uuid.Parse(body.ReferenceID)

	intent, err := h.svc.InitiatePayment(c.Request.Context(), service.InitiateInput{
		PayerID:        userID,
		PayeeID:        payeeID,
		ReferenceType:  body.ReferenceType,
		ReferenceID:    refID,
		Amount:         body.Amount,
		Currency:       body.Currency,
		Method:         body.Method,
		IdempotencyKey: body.IdempotencyKey,
	})
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INITIATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, intent, nil)
}

// GetIntent GET /v1/payments/intents/:id
func (h *Handler) GetIntent(c *gin.Context) {
	_, ok := getUserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil)
		return
	}
	intent, err := h.svc.GetIntent(c.Request.Context(), id)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "intent not found", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, intent, nil)
}

// UpdateStatus PATCH /v1/payments/intents/:id/status
func (h *Handler) UpdateStatus(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil)
		return
	}
	var body struct {
		OldStatus   string `json:"old_status" binding:"required"`
		NewStatus   string `json:"new_status" binding:"required"`
		ProviderRef string `json:"provider_ref"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	intent, err := h.svc.UpdateStatus(c.Request.Context(), id, body.OldStatus, body.NewStatus, body.ProviderRef, userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, intent, nil)
}

// InitiateRefund POST /v1/payments/intents/:id/refund
func (h *Handler) InitiateRefund(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil)
		return
	}
	var body struct {
		Reason      string `json:"reason"`
		AmountMinor int64  `json:"amount_minor"`
	}
	c.ShouldBindJSON(&body) //nolint:errcheck
	// Audit P6 + P7: amount_minor is paise-minor int64. 0 means "full
	// refund of the remaining refundable balance"; >0 means partial.
	// Validation (sign + cap) lives in the service.
	intent, err := h.svc.InitiateRefund(c.Request.Context(), id, userID, body.AmountMinor, body.Reason)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "REFUND_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, intent, nil)
}

// VerifyIntent POST /v1/payments/intents/:id/verify — Phase 0.1b.
// Internal-only (gated by the X-Internal-Service-Key middleware that wraps
// the /v1/payments group) so commerce-service can synchronously verify a
// Razorpay signature + amount and confirm the customer's order without
// waiting for webhook delivery. Returns 400 on signature / amount / order
// mismatch so the caller can refuse to mark the order paid.
func (h *Handler) VerifyIntent(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil)
		return
	}
	var body struct {
		RazorpayOrderID   string `json:"razorpay_order_id" binding:"required"`
		RazorpayPaymentID string `json:"razorpay_payment_id" binding:"required"`
		RazorpaySignature string `json:"razorpay_signature" binding:"required"`
		AmountMinor       int64  `json:"amount_minor,omitempty"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	result, err := h.svc.VerifyIntent(c.Request.Context(), id, body.RazorpayOrderID, body.RazorpayPaymentID, body.RazorpaySignature, body.AmountMinor)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "VERIFY_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, result, nil)
}

// ListByReference GET /v1/payments/intents?ref_type=order&ref_id=uuid
func (h *Handler) ListByReference(c *gin.Context) {
	_, ok := getUserID(c)
	if !ok {
		return
	}
	refType := c.Query("ref_type")
	refIDStr := c.Query("ref_id")
	if refType == "" || refIDStr == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "MISSING_PARAMS", "ref_type and ref_id required", nil)
		return
	}
	refID, err := uuid.Parse(refIDStr)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REF_ID", "invalid ref_id", nil)
		return
	}
	intents, err := h.svc.ListByReference(c.Request.Context(), refType, refID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, intents, nil)
}

// HandleWebhook POST /v1/payments/webhook
//
// Razorpay sends a JSON event with an X-Razorpay-Signature header that is the
// HMAC-SHA256 of the raw body keyed by the webhook secret. When the secret is
// configured (production), we reject unsigned/mismatched calls with 401. When
// no secret is set (dev/test), we accept all calls so local stub flows work.
func (h *Handler) HandleWebhook(c *gin.Context) {
	signature := c.GetHeader("X-Razorpay-Signature")
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	if h.webhookSecret != "" {
		if !verifyRazorpaySignature(body, signature, h.webhookSecret) {
			slog.Warn("razorpay webhook signature mismatch", "have_sig", signature != "")
			c.Status(http.StatusUnauthorized)
			return
		}
	}

	var event struct {
		Event   string          `json:"event"`
		EventID string          `json:"id"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(body, &event); err != nil {
		c.Status(http.StatusBadRequest)
		return
	}

	// Parse payment entity
	var payloadData struct {
		Payment struct {
			Entity struct {
				ID      string `json:"id"`
				OrderID string `json:"order_id"`
			} `json:"entity"`
		} `json:"payment"`
	}
	json.Unmarshal(event.Payload, &payloadData) //nolint:errcheck

	paymentID := payloadData.Payment.Entity.ID
	orderID := payloadData.Payment.Entity.OrderID

	// Audit P3: dedup by Razorpay event_id. A duplicate retry returns
	// 200 without re-running side effects. The check fails open (treats
	// as new) if event_id is empty or Redis/Postgres is briefly down;
	// the state-machine in UpdateStatusByProviderRef (audit P2) is the
	// second line of defense.
	fresh, err := h.svc.MarkWebhookSeen(c.Request.Context(), event.EventID, event.Event, orderID)
	if err != nil {
		slog.Warn("razorpay webhook dedup check failed; processing anyway", "err", err)
	}
	if !fresh {
		c.Status(http.StatusOK)
		return
	}

	switch event.Event {
	case "payment.captured":
		h.svc.UpdateStatusByProviderRef(c.Request.Context(), orderID, "succeeded", paymentID)
	case "payment.failed":
		h.svc.UpdateStatusByProviderRef(c.Request.Context(), orderID, "failed", paymentID)
	case "refund.processed":
		h.svc.UpdateStatusByProviderRef(c.Request.Context(), orderID, "refunded", paymentID)
	}
	c.Status(http.StatusOK)
}

// verifyRazorpaySignature checks the HMAC-SHA256 of body against signature
// using secret. Constant-time compare so a mismatch doesn't leak length info
// via timing.
func verifyRazorpaySignature(body []byte, signature, secret string) bool {
	if signature == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// ReleaseHold POST /v1/payments/holds/:intentId/release
func (h *Handler) ReleaseHold(c *gin.Context) {
	intentID, err := uuid.Parse(c.Param("intentId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil)
		return
	}
	actor := c.GetHeader("X-User-Id")
	if err := h.svc.ReleaseHold(c.Request.Context(), intentID, actor); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "HOLD_RELEASE_FAILED", err.Error(), nil)
		return
	}
	c.Status(http.StatusNoContent)
}
