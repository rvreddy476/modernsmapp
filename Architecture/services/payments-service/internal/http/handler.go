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
	if h.internalKey != "" {
		r.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	v1 := r.Group("/v1/payments")
	{
		v1.POST("/intents", h.InitiatePayment)
		v1.GET("/intents/:id", h.GetIntent)
		v1.PATCH("/intents/:id/status", h.UpdateStatus)
		v1.POST("/intents/:id/refund", h.InitiateRefund)
		v1.GET("/intents", h.ListByReference)
		v1.POST("/webhook", h.HandleWebhook)
		v1.POST("/holds/:intentId/release", h.ReleaseHold)
	}
}

func getUserID(c *gin.Context) (uuid.UUID, bool) {
	str := c.GetHeader("X-User-Id")
	if str == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(str)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "invalid user id", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INITIATE_FAILED", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil, nil)
		return
	}
	intent, err := h.svc.GetIntent(c.Request.Context(), id)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "intent not found", nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil, nil)
		return
	}
	var body struct {
		OldStatus   string `json:"old_status" binding:"required"`
		NewStatus   string `json:"new_status" binding:"required"`
		ProviderRef string `json:"provider_ref"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	intent, err := h.svc.UpdateStatus(c.Request.Context(), id, body.OldStatus, body.NewStatus, body.ProviderRef, userID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil, nil)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	c.ShouldBindJSON(&body) //nolint:errcheck
	intent, err := h.svc.InitiateRefund(c.Request.Context(), id, userID, body.Reason)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "REFUND_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, intent, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "MISSING_PARAMS", "ref_type and ref_id required", nil, nil)
		return
	}
	refID, err := uuid.Parse(refIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REF_ID", "invalid ref_id", nil, nil)
		return
	}
	intents, err := h.svc.ListByReference(c.Request.Context(), refType, refID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
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
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid intent id", nil, nil)
		return
	}
	actor := c.GetHeader("X-User-Id")
	if err := h.svc.ReleaseHold(c.Request.Context(), intentID, actor); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "HOLD_RELEASE_FAILED", err.Error(), nil, nil)
		return
	}
	c.Status(http.StatusNoContent)
}
