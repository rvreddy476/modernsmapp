// HTTP handlers for /v1/dating/premium/*. Sprint 5 — see PULSE_DATING_SPEC.md
// §14 (premium tier).
//
// CRITICAL RULES #4 — the webhook handler is unauthenticated by user; it is
// authenticated by the Razorpay signature in the X-Razorpay-Signature
// header. Idempotency is enforced inside the service layer via
// dating_payment_events.razorpay_event_id (UNIQUE).
package http

import (
	"errors"
	"io"
	"net/http"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// GetPlans — GET /v1/dating/premium/plans.
func (h *Handler) GetPlans(c *gin.Context) {
	plans, err := h.svc.ListPlans(c.Request.Context())
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, plans, nil)
}

// PostCheckout — POST /v1/dating/premium/checkout.
func (h *Handler) PostCheckout(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	var body service.CheckoutRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	out, err := h.svc.Checkout(c.Request.Context(), userID, body)
	if err != nil {
		if errors.Is(err, store.ErrPlanNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "plan not found", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "CHECKOUT_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
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

// PostCancelPremium — POST /v1/dating/premium/cancel.
func (h *Handler) PostCancelPremium(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	if err := h.svc.CancelSubscription(c.Request.Context(), userID); err != nil {
		if errors.Is(err, store.ErrSubscriptionNotFound) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "NOT_FOUND", "no active subscription", nil)
			return
		}
		respondServiceError(c, err, http.StatusInternalServerError, "CANCEL_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"cancelled": true}, nil)
}

// PostWebhook — POST /v1/dating/premium/webhook.
//
// CRITICAL: this handler intentionally does NOT call getUserID. Razorpay
// webhooks are verified solely by the HMAC-SHA256 signature in the
// X-Razorpay-Signature header. A missing/wrong signature returns 401; a
// retried delivery (already-seen razorpay event id) returns 200 no-op.
func (h *Handler) PostWebhook(c *gin.Context) {
	signature := c.GetHeader("X-Razorpay-Signature")
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", "cannot read body", nil)
		return
	}
	res, err := h.svc.HandleWebhook(c.Request.Context(), signature, body)
	if err != nil {
		// Signature failure → 401 so Razorpay knows we rejected it.
		// Other errors → 500 so Razorpay retries.
		if hasPrefix(err.Error(), "forbidden:") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "AUTH_REQUIRED", "invalid signature", nil)
			return
		}
		if hasPrefix(err.Error(), "invalid:") {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
			return
		}
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "WEBHOOK_FAILED", err.Error(), nil)
		return
	}
	if res.Idempotent {
		api.JSON(c.Writer, http.StatusOK, gin.H{"idempotent": true, "event_id": res.EventID}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"processed": true, "event_id": res.EventID}, nil)
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// PostBoost — POST /v1/dating/pulse/boost.
//
// Premium daily boost OR consume a one-shot boost token. Service applies
// the rate limit and returns a 403 envelope when blocked.
func (h *Handler) PostBoost(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	out, err := h.svc.RequestBoost(c.Request.Context(), userID)
	if err != nil {
		// Forbidden (rate-limited) — surface the next-boost-at hint via the
		// service result if available.
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
