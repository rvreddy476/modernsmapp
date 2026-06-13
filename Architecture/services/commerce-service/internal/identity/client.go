// Package identity is a small HTTP client over auth-service's internal contact lookup.
package identity

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// Contact is the subset of a user's identity commerce needs to send emails.
type Contact struct {
	UserID uuid.UUID `json:"user_id"`
	Email  string    `json:"email"`
	Phone  string    `json:"phone"`
}

// Client calls auth-service internal endpoints.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

func New(baseURL, internalKey string) *Client {
	// Phase F3.2 — otelhttp.NewTransport auto-injects W3C traceparent
	// onto every outbound request and emits an HTTP client span that
	// nests under the caller's server span. One-line wiring per client.
	return &Client{
		baseURL:     baseURL,
		internalKey: internalKey,
		http: &http.Client{
			Timeout:   5 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// GetContact resolves a user's email/phone. Returns nil if not configured.
func (c *Client) GetContact(ctx context.Context, userID uuid.UUID) (*Contact, error) {
	if c == nil || c.baseURL == "" {
		return nil, nil
	}
	url := fmt.Sprintf("%s/v1/auth/internal/users/%s", c.baseURL, userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("identity lookup: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("identity lookup status %d", resp.StatusCode)
	}
	var envelope struct {
		Data Contact `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, err
	}
	return &envelope.Data, nil
}
