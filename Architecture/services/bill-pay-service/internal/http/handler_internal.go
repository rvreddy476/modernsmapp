package http

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"

	"github.com/atpost/bill-pay-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// SetuWebhook handles POST /v1/billpay/internal/setu-webhook.
//
// Two layers of auth:
//  1. The internal-key middleware (configured in handler.go) ensures only
//     the api-gateway / explicit forwarders can hit this URL.
//  2. The Setu HMAC-SHA256 signature in X-Setu-Signature is verified here
//     against the configured SETU_WEBHOOK_SECRET. Mismatch -> 401.
//
// The body MUST be read once into a buffer because we need it BOTH for the
// HMAC verification and for JSON unmarshalling.
func (h *Handler) SetuWebhook(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "READ_BODY_FAILED", err.Error(), nil)
		return
	}
	// Reset body for any downstream middleware that may want to re-read.
	c.Request.Body = io.NopCloser(bytes.NewReader(body))

	if err := h.svc.Setu().VerifyWebhookSignature(c.Request, body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "INVALID_SIGNATURE", err.Error(), nil)
		return
	}

	var evt service.SetuWebhookEvent
	if err := json.Unmarshal(body, &evt); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	if err := h.svc.HandleSetuWebhook(c.Request.Context(), evt); err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "WEBHOOK_PROCESS_FAILED")
		return
	}
	api.JSONWithContext(c.Request.Context(), c.Writer, http.StatusOK, gin.H{"ok": true})
}
