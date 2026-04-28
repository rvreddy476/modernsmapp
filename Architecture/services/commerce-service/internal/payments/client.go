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
)

// PaymentIntent mirrors the subset payments-service returns.
type PaymentIntent struct {
	ID            uuid.UUID `json:"id"`
	Status        string    `json:"status"`
	Amount        float64   `json:"amount"`
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
	return &Client{
		baseURL:     baseURL,
		internalKey: internalKey,
		http:        &http.Client{Timeout: 8 * time.Second},
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

// InitiateRefund kicks payments-service's refund pipeline for the intent.
// Idempotent at the gateway: a second call returns the existing refund.
func (c *Client) InitiateRefund(ctx context.Context, intentID, actorID uuid.UUID, reason string) (*PaymentIntent, error) {
	if c == nil || c.baseURL == "" {
		return nil, fmt.Errorf("payments client not configured")
	}
	url := fmt.Sprintf("%s/v1/payments/intents/%s/refund", c.baseURL, intentID)
	body, _ := json.Marshal(map[string]string{"reason": reason})
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
