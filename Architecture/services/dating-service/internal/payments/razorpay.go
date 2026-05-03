// Package payments implements the Razorpay/UPI client used by the premium
// service. Two implementations are provided:
//
//   - HTTPClient — production: hits api.razorpay.com with the configured
//     RAZORPAY_KEY_ID/RAZORPAY_KEY_SECRET, verifies webhook signatures using
//     RAZORPAY_WEBHOOK_SECRET (HMAC-SHA256).
//   - MockClient — tests: returns deterministic order ids and a Verify hook.
//
// Selection happens in main.go via env RAZORPAY_MODE=mock|http (default mock
// — production must explicitly opt in to http, mirroring the DigiLocker
// pattern from Sprint 4).
//
// CRITICAL RULES #4: webhook signature verification is the only guard
// between Razorpay's outbound retries and our payment_event idempotency log.
// If verification fails the caller MUST NOT mutate state.
package payments

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Order represents the response from Razorpay's POST /v1/orders endpoint.
type Order struct {
	ID         string `json:"id"`
	Amount     int64  `json:"amount"`
	Currency   string `json:"currency"`
	Receipt    string `json:"receipt,omitempty"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
}

// Subscription represents the response from POST /v1/subscriptions.
type Subscription struct {
	ID         string `json:"id"`
	PlanID     string `json:"plan_id"`
	Status     string `json:"status"`
	TotalCount int    `json:"total_count"`
	CreatedAt  int64  `json:"created_at"`
}

// Payment represents the GET /v1/payments/:id response (subset).
type Payment struct {
	ID       string `json:"id"`
	OrderID  string `json:"order_id"`
	Status   string `json:"status"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Method   string `json:"method"`
}

// Client is the contract the premium service depends on. Both HTTPClient and
// MockClient implement it.
type Client interface {
	CreateOrder(ctx context.Context, amountPaise int64, receipt string, notes map[string]string) (*Order, error)
	CreateSubscription(ctx context.Context, planID string, totalCount int, notes map[string]string) (*Subscription, error)
	VerifyWebhookSignature(payload []byte, signature string) error
	FetchPayment(ctx context.Context, paymentID string) (*Payment, error)
	KeyID() string
}

// --- HTTP client -----------------------------------------------------------

// HTTPClient is the production Razorpay client.
type HTTPClient struct {
	keyID         string
	keySecret     string
	webhookSecret string
	baseURL       string
	httpClient    *http.Client
}

// NewHTTPClient builds an HTTPClient from the explicit credentials.
// baseURL defaults to https://api.razorpay.com/v1 when empty.
func NewHTTPClient(keyID, keySecret, webhookSecret, baseURL string) *HTTPClient {
	if baseURL == "" {
		baseURL = "https://api.razorpay.com/v1"
	}
	return &HTTPClient{
		keyID:         keyID,
		keySecret:     keySecret,
		webhookSecret: webhookSecret,
		baseURL:       strings.TrimRight(baseURL, "/"),
		httpClient:    &http.Client{Timeout: 8 * time.Second},
	}
}

// KeyID returns the public key id (safe to surface to clients).
func (c *HTTPClient) KeyID() string { return c.keyID }

// CreateOrder hits POST /orders.
func (c *HTTPClient) CreateOrder(ctx context.Context, amountPaise int64, receipt string, notes map[string]string) (*Order, error) {
	if amountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	body := map[string]any{
		"amount":   amountPaise,
		"currency": "INR",
		"receipt":  receipt,
		"notes":    notes,
	}
	var out Order
	if err := c.do(ctx, http.MethodPost, "/orders", body, &out); err != nil {
		return nil, fmt.Errorf("razorpay create order: %w", err)
	}
	return &out, nil
}

// CreateSubscription hits POST /subscriptions.
func (c *HTTPClient) CreateSubscription(ctx context.Context, planID string, totalCount int, notes map[string]string) (*Subscription, error) {
	if planID == "" {
		return nil, fmt.Errorf("invalid: plan_id required")
	}
	if totalCount <= 0 {
		totalCount = 12
	}
	body := map[string]any{
		"plan_id":     planID,
		"total_count": totalCount,
		"notes":       notes,
	}
	var out Subscription
	if err := c.do(ctx, http.MethodPost, "/subscriptions", body, &out); err != nil {
		return nil, fmt.Errorf("razorpay create subscription: %w", err)
	}
	return &out, nil
}

// FetchPayment hits GET /payments/:id.
func (c *HTTPClient) FetchPayment(ctx context.Context, paymentID string) (*Payment, error) {
	if paymentID == "" {
		return nil, fmt.Errorf("invalid: payment id required")
	}
	var out Payment
	if err := c.do(ctx, http.MethodGet, "/payments/"+paymentID, nil, &out); err != nil {
		return nil, fmt.Errorf("razorpay fetch payment: %w", err)
	}
	return &out, nil
}

// VerifyWebhookSignature validates the X-Razorpay-Signature header against
// HMAC-SHA256(payload, webhookSecret). Returns an error on mismatch.
func (c *HTTPClient) VerifyWebhookSignature(payload []byte, signature string) error {
	if c.webhookSecret == "" {
		return fmt.Errorf("razorpay webhook secret not configured")
	}
	if signature == "" {
		return fmt.Errorf("razorpay signature missing")
	}
	mac := hmac.New(sha256.New, []byte(c.webhookSecret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("razorpay signature mismatch")
	}
	return nil
}

// do is the shared HTTP plumbing.
func (c *HTTPClient) do(ctx context.Context, method, path string, body, out any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reqBody)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.keyID, c.keySecret)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("razorpay request: %w", err)
	}
	defer resp.Body.Close()
	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("razorpay status %d: %s", resp.StatusCode, string(respBytes))
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(respBytes, out); err != nil {
		return fmt.Errorf("razorpay decode: %w", err)
	}
	return nil
}

// --- Mock client -----------------------------------------------------------

// MockClient is a deterministic in-memory client used by tests and the
// default boot mode.
type MockClient struct {
	mu             sync.Mutex
	keyIDValue     string
	webhookSecret  string
	orderCounter   int
	subCounter     int
	// VerifyHook lets tests override signature verification.
	VerifyHook func(payload []byte, signature string) error
	// Payments stores fetched payment records keyed by payment id.
	Payments map[string]*Payment
}

// NewMockClient returns a default MockClient with key id "rzp_test_mock".
func NewMockClient() *MockClient {
	return &MockClient{
		keyIDValue:    "rzp_test_mock",
		webhookSecret: "whsec_mock",
		Payments:      map[string]*Payment{},
	}
}

// KeyID exposes the mock key id.
func (m *MockClient) KeyID() string { return m.keyIDValue }

// CreateOrder returns a deterministic order id.
func (m *MockClient) CreateOrder(ctx context.Context, amountPaise int64, receipt string, notes map[string]string) (*Order, error) {
	if amountPaise <= 0 {
		return nil, fmt.Errorf("invalid: amount must be positive")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.orderCounter++
	return &Order{
		ID:        fmt.Sprintf("order_mock_%06d", m.orderCounter),
		Amount:    amountPaise,
		Currency:  "INR",
		Receipt:   receipt,
		Status:    "created",
		CreatedAt: time.Now().Unix(),
	}, nil
}

// CreateSubscription returns a deterministic subscription id.
func (m *MockClient) CreateSubscription(ctx context.Context, planID string, totalCount int, notes map[string]string) (*Subscription, error) {
	if planID == "" {
		return nil, fmt.Errorf("invalid: plan_id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subCounter++
	if totalCount <= 0 {
		totalCount = 12
	}
	return &Subscription{
		ID:         fmt.Sprintf("sub_mock_%06d", m.subCounter),
		PlanID:     planID,
		Status:     "created",
		TotalCount: totalCount,
		CreatedAt:  time.Now().Unix(),
	}, nil
}

// VerifyWebhookSignature delegates to VerifyHook when set; otherwise it does
// the standard HMAC-SHA256 against the configured webhookSecret. Tests can
// flip VerifyHook to a no-op for happy-path coverage or to a failing func to
// exercise the signature-mismatch branch.
func (m *MockClient) VerifyWebhookSignature(payload []byte, signature string) error {
	if m.VerifyHook != nil {
		return m.VerifyHook(payload, signature)
	}
	if signature == "" {
		return fmt.Errorf("razorpay signature missing")
	}
	mac := hmac.New(sha256.New, []byte(m.webhookSecret))
	mac.Write(payload)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("razorpay signature mismatch")
	}
	return nil
}

// FetchPayment returns a stored mock payment if present.
func (m *MockClient) FetchPayment(ctx context.Context, paymentID string) (*Payment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p, ok := m.Payments[paymentID]; ok {
		return p, nil
	}
	return &Payment{
		ID:       paymentID,
		Status:   "captured",
		Currency: "INR",
		Method:   "upi",
	}, nil
}

// SignPayload is a test helper that returns the HMAC-SHA256 signature for a
// payload using the mock's webhook secret.
func (m *MockClient) SignPayload(payload []byte) string {
	mac := hmac.New(sha256.New, []byte(m.webhookSecret))
	mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}
