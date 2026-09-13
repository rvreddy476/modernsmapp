package payments

// The service-to-service payments client (mirrors commerce p0client.go).
//
// Every call is authenticated with food-service's OWN Ed25519 service token:
// audience payments, one operation and the food_order reference type per
// token, two-minute TTL. payments holds only the public key. The shared
// X-Internal-Service-Key survives solely as a legacy credential for a local
// or dev stack that has not issued food a signing key (ClientFromEnv refuses
// it anywhere else), and it is never sent alongside a token.
//
// Money on the wire is integer paise (`amount_minor`). No float amount is
// ever sent, and echoes are checked rather than trusted.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const (
	internalBase = "/v1/payments/internal"
	serviceName  = "food-service"
	// DefaultTokenKID is used when FOOD_SERVICE_TOKEN_KID is unset.
	DefaultTokenKID = "f1"
	// DefaultPaymentsURL matches the compose service name.
	DefaultPaymentsURL = "http://payments-service:8102"
)

var (
	// ErrPaymentsUnavailable is a transport failure or 5xx. The caller must
	// treat it as "unknown", never as "did not happen".
	ErrPaymentsUnavailable = errors.New("payments service unavailable")
	// ErrRefused is a 4xx from payments.
	ErrRefused = errors.New("payments refused the request")
	// ErrServiceTokenRequired: FOOD_SERVICE_TOKEN_KEY is unset outside local/dev.
	ErrServiceTokenRequired = errors.New("FOOD_SERVICE_TOKEN_KEY is required outside ENV=local/dev")
)

// Client talks to payments-service.
type Client struct {
	baseURL     string
	signer      *servicetoken.Signer
	internalKey string
	http        *http.Client
}

// NewTokenClient authenticates with food-service's service token.
func NewTokenClient(baseURL, kid, signingKeyB64 string) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("payments client: base URL is required")
	}
	if strings.TrimSpace(kid) == "" {
		kid = DefaultTokenKID
	}
	signer, err := servicetoken.NewSignerFromBase64(serviceName, kid, signingKeyB64)
	if err != nil {
		return nil, fmt.Errorf("payments client: signing key: %w", err)
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), signer: signer, http: newHTTPClient()}, nil
}

// NewInternalKeyClient is the LEGACY client (local/dev only).
func NewInternalKeyClient(baseURL, internalKey string) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("payments client: base URL is required")
	}
	if strings.TrimSpace(internalKey) == "" {
		return nil, fmt.Errorf("payments client: INTERNAL_SERVICE_KEY is required in legacy mode")
	}
	return &Client{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, http: newHTTPClient()}, nil
}

// ClientFromEnv builds the client from PAYMENTS_SERVICE_URL,
// FOOD_SERVICE_TOKEN_KEY, FOOD_SERVICE_TOKEN_KID, ENV and INTERNAL_SERVICE_KEY.
// Without a token key it falls back to the internal key only when ENV is
// local/dev; anything else (including a blank ENV) fails closed.
func ClientFromEnv(getenv func(string) string) (*Client, error) {
	baseURL := strings.TrimSpace(getenv("PAYMENTS_SERVICE_URL"))
	if baseURL == "" {
		baseURL = DefaultPaymentsURL
	}
	if key := strings.TrimSpace(getenv("FOOD_SERVICE_TOKEN_KEY")); key != "" {
		return NewTokenClient(baseURL, getenv("FOOD_SERVICE_TOKEN_KID"), key)
	}
	if !legacyAllowed(getenv("ENV")) {
		return nil, ErrServiceTokenRequired
	}
	return NewInternalKeyClient(baseURL, getenv("INTERNAL_SERVICE_KEY"))
}

func legacyAllowed(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return true
	}
	return false
}

// LegacyAuth reports whether this client presents the shared internal key.
func (c *Client) LegacyAuth() bool { return c != nil && c.signer == nil }

func newHTTPClient() *http.Client { return &http.Client{Timeout: 15 * time.Second} }

// Intent is the payments-side intent as food sees it.
type Intent struct {
	ID            uuid.UUID         `json:"id"`
	Status        string            `json:"status"`
	AmountMinor   int64             `json:"amount_minor"`
	Currency      string            `json:"currency"`
	Method        string            `json:"method"`
	ProviderRef   string            `json:"provider_ref"`
	ReferenceType string            `json:"reference_type"`
	ReferenceID   uuid.UUID         `json:"reference_id"`
	PayerID       uuid.UUID         `json:"payer_id"`
	PayeeID       uuid.UUID         `json:"payee_id"`
	ClientSession map[string]string `json:"client_session,omitempty"`
}

// CreateIntentInput is server-authored: the amount comes from the order.
type CreateIntentInput struct {
	OrderID     uuid.UUID
	PayerID     uuid.UUID
	PayeeID     uuid.UUID
	AmountMinor int64
	// Method is the client-chosen launch instrument (upi|card).
	Method string
}

// CreateIntent opens a payment for a food order.
func (c *Client) CreateIntent(ctx context.Context, in CreateIntentInput) (*Intent, error) {
	if in.AmountMinor <= 0 {
		return nil, fmt.Errorf("payments client: refusing an intent for %d paise", in.AmountMinor)
	}
	if err := paymentmethod.Validate(in.Method); err != nil {
		return nil, fmt.Errorf("payments client: %w", err)
	}
	body := map[string]any{
		"payer_id":       in.PayerID,
		"payee_id":       in.PayeeID,
		"reference_type": servicetoken.RefFoodOrder,
		"reference_id":   in.OrderID,
		"amount_minor":   in.AmountMinor,
		"currency":       "INR",
		"method":         in.Method,
		// Deterministic: a retry for the same order collapses to one intent.
		"idempotency_key": "food_order:" + in.OrderID.String(),
	}
	var out Intent
	if err := c.do(ctx, http.MethodPost, internalBase+"/intents", servicetoken.OpIntentCreate, body, &out); err != nil {
		return nil, err
	}
	// Never trust the echo.
	if out.AmountMinor != in.AmountMinor {
		return nil, fmt.Errorf("payments client: intent amount %d does not match the order total %d", out.AmountMinor, in.AmountMinor)
	}
	if out.ReferenceType != servicetoken.RefFoodOrder || out.ReferenceID != in.OrderID {
		return nil, fmt.Errorf("payments client: intent reference %s/%s is not food_order/%s", out.ReferenceType, out.ReferenceID, in.OrderID)
	}
	return &out, nil
}

// CallbackVerdict is ADVISORY: it lets food refuse a callback, never accept one
// as payment.
type CallbackVerdict struct {
	Verified      bool      `json:"verified"`
	Advisory      bool      `json:"advisory"`
	Status        string    `json:"status"`
	AmountMinor   int64     `json:"amount_minor"`
	PayerID       uuid.UUID `json:"payer_id"`
	PayeeID       uuid.UUID `json:"payee_id"`
	ReferenceType string    `json:"reference_type"`
	ReferenceID   uuid.UUID `json:"reference_id"`
}

// VerifyCallback checks a client-returned checkout payload. expectedMinor is
// the server's order total.
func (c *Client) VerifyCallback(ctx context.Context, intentID uuid.UUID, providerOrderID, providerPaymentID, signature string, expectedMinor int64) (*CallbackVerdict, error) {
	body := map[string]any{
		"razorpay_order_id":   providerOrderID,
		"razorpay_payment_id": providerPaymentID,
		"razorpay_signature":  signature,
		"amount_minor":        expectedMinor,
	}
	var out CallbackVerdict
	if err := c.do(ctx, http.MethodPost, internalBase+"/intents/"+intentID.String()+"/verify", servicetoken.OpIntentRead, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// RefundAccepted acknowledges a durable refund command, not moved money.
type RefundAccepted struct {
	CommandID   uuid.UUID `json:"command_id"`
	IntentID    uuid.UUID `json:"intent_id"`
	AmountMinor int64     `json:"amount_minor"`
	Status      string    `json:"status"`
}

// Refund requests a refund with an explicit amount and a deterministic key.
func (c *Client) Refund(ctx context.Context, intentID uuid.UUID, amountMinor int64, reason, idempotencyKey string) (*RefundAccepted, error) {
	if strings.TrimSpace(idempotencyKey) == "" {
		return nil, fmt.Errorf("payments client: a refund idempotency key is required")
	}
	if amountMinor <= 0 {
		return nil, fmt.Errorf("payments client: refusing a refund of %d paise", amountMinor)
	}
	body := map[string]any{
		"amount_minor":    amountMinor,
		"reason":          reason,
		"idempotency_key": idempotencyKey,
	}
	var out RefundAccepted
	if err := c.do(ctx, http.MethodPost, internalBase+"/intents/"+intentID.String()+"/refund", servicetoken.OpRefundCreate, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) do(ctx context.Context, method, path, op string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.signer == nil {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	} else {
		token, err := c.signer.Mint(servicetoken.AudiencePayments, serviceName,
			[]string{op}, []string{servicetoken.RefFoodOrder}, 2*time.Minute)
		if err != nil {
			return fmt.Errorf("payments client: mint token: %w", err)
		}
		req.Header.Set("X-Service-Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrPaymentsUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: status %d", ErrPaymentsUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d: %s", ErrRefused, resp.StatusCode, truncate(string(raw), 300))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return json.Unmarshal(raw, out)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
