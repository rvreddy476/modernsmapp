// Package payments is a small HTTP client over payments-service used by
// commerce-service for refund initiation. The full payments-service surface
// (intents, holds, etc.) is not modelled here — only the bits commerce
// actually calls.
package payments

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// PaymentIntent mirrors the subset payments-service returns.
//
// Audit P7-deep: AmountMinor (paise-minor int64) is the source of
// truth; Amount (rupees-major float64) is kept as a deprecated mirror
// for one release cycle. New consumers that arithmetic on the value
// should use AmountMinor and divide by 100 only at the display
// boundary. The two fields are always written together by payments-
// service.
type PaymentIntent struct {
	ID     uuid.UUID `json:"id"`
	Status string    `json:"status"`
	// Deprecated: use AmountMinor. Kept for one release cycle to
	// support analytics-style readers; arithmetic / comparisons must
	// switch to AmountMinor.
	Amount        float64   `json:"amount"`
	AmountMinor   int64     `json:"amount_minor"`
	Currency      string    `json:"currency"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   uuid.UUID `json:"reference_id"`
	ProviderRef   string    `json:"provider_ref,omitempty"`
}

// Client speaks to payments-service via the internal-key header.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func New(baseURL, internalKey string) *Client {
	// Phase F3.2 — otelhttp auto-instruments outbound HTTP so payments
	// calls appear as a child client span under the originating
	// checkout / confirm-payment server span in Jaeger.
	return &Client{
		baseURL:     baseURL,
		internalKey: internalKey,
		http: &http.Client{
			Timeout:   8 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// envelope is the shared API response wrapper used by all atpost services.
type envelope[T any] struct {
	Data  T `json:"data"`
	Error *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// FindOrderIntent returns the most recent succeeded payment intent for an
// order, or nil if none. Used by ApproveReturn to pick which intent to
// refund. We pass actorID so the upstream call has a sensible X-User-Id.
func (c *Client) FindOrderIntent(ctx context.Context, orderID, actorID uuid.UUID) (*PaymentIntent, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/v1/payments/intents?ref_type=order&ref_id=%s", c.baseURL, orderID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, actorID)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("payments find: status %d", resp.StatusCode)
	}
	var e envelope[[]PaymentIntent]
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		return nil, err
	}
	// Walk newest-first looking for a succeeded intent. payments-service
	// orders by created_at DESC so index 0 is most recent.
	for i := range e.Data {
		if e.Data[i].Status == "succeeded" {
			return &e.Data[i], nil
		}
	}
	return nil, nil
}

// VerifyResult mirrors payments-service's verify endpoint response.
// Verified is only true when the Razorpay signature, the provider order
// id, and (when supplied) the amount all match the stored intent.
type VerifyResult struct {
	Verified    bool      `json:"verified"`
	IntentID    uuid.UUID `json:"intent_id"`
	Status      string    `json:"status"`
	AmountMinor int64     `json:"amount_minor"`
	ProviderRef string    `json:"provider_ref"`
}

// VerifyIntent asks payments-service to validate the Razorpay signature
// and amount for the supplied intent. Returns a non-nil result with
// Verified=true only on a successful verification; any error means
// commerce-service must NOT mark the order paid.
//
// Server-to-server call — only the internal-service-key gate authenticates;
// no X-User-Id is required by the verify endpoint.
func (c *Client) VerifyIntent(ctx context.Context, intentID uuid.UUID, rzpOrderID, rzpPaymentID, rzpSignature string, amountMinor int64) (*VerifyResult, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("payments client not configured")
	}
	url := fmt.Sprintf("%s/v1/payments/intents/%s/verify", c.baseURL, intentID)
	body, _ := json.Marshal(map[string]interface{}{
		"razorpay_order_id":   rzpOrderID,
		"razorpay_payment_id": rzpPaymentID,
		"razorpay_signature":  rzpSignature,
		"amount_minor":        amountMinor,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req, uuid.Nil)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		var e envelope[any]
		_ = json.NewDecoder(resp.Body).Decode(&e)
		msg := ""
		if e.Error != nil {
			msg = e.Error.Message
		}
		return nil, fmt.Errorf("payments verify: status %d: %s", resp.StatusCode, msg)
	}
	var e envelope[VerifyResult]
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		return nil, err
	}
	return &e.Data, nil
}

// InitiateRefund kicks payments-service's refund pipeline for the intent.
// Idempotent at the gateway: a second call returns the existing refund.
//
// amountMinor is paise-minor int64. Pass 0 for a full refund of the
// remaining refundable balance (matches the historical no-amount
// semantics); pass >0 for a partial refund (commerce-service's return
// flow uses this with the per-line item value).
func (c *Client) InitiateRefund(ctx context.Context, intentID, actorID uuid.UUID, amountMinor int64, reason string) (*PaymentIntent, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("payments client not configured")
	}
	url := fmt.Sprintf("%s/v1/payments/intents/%s/refund", c.baseURL, intentID)
	payload := map[string]interface{}{"reason": reason}
	if amountMinor > 0 {
		payload["amount_minor"] = amountMinor
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req, actorID)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("payments refund: status %d", resp.StatusCode)
	}
	var e envelope[PaymentIntent]
	if err := json.NewDecoder(resp.Body).Decode(&e); err != nil {
		return nil, err
	}
	return &e.Data, nil
}

func (c *Client) setHeaders(req *http.Request, actorID uuid.UUID) {
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	if actorID != uuid.Nil {
		req.Header.Set("X-User-Id", actorID.String())
	}
}
