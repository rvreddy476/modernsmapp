package payments

// food-service's binding of the shared payments-service client.
//
// Everything about talking to payments-service lives in shared/paymentsclient:
// the service token (audience payments, one operation and the food_order
// reference type per token, two-minute TTL), the legacy key, the routes, the
// {"data": …} envelope, the error classes and the echo checks. What stays here
// is food's side of it:
//
//   - its env var names and its legacy rule: the shared
//     X-Internal-Service-Key only when ENV is local/dev (ClientFromEnv);
//   - its payments application id, reference type and deterministic
//     idempotency keys;
//   - its stricter local refusals: a launch payment method, a positive
//     refund, a reference echo that must be this order, a 1 MiB response cap;
//   - its own Intent shape, which the service and its tests read.
//
// Money on the wire is integer paise (`amount_minor`). No float amount is
// ever sent, and echoes are checked rather than trusted.

import (
	"context"
	"errors"
	"strings"

	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

const (
	serviceName = "food-service"
	// DefaultTokenKID is used when FOOD_SERVICE_TOKEN_KID is unset.
	DefaultTokenKID = "f1"
	// DefaultPaymentsURL matches the compose service name.
	DefaultPaymentsURL = "http://payments-service:8102"
	// maxResponseBytes caps how much of a payments response is read.
	maxResponseBytes = 1 << 20

	// applicationIDEnv and DefaultApplicationID name the payments application
	// every food intent and refund belongs to. PROVISIONAL until the
	// application registry's keys are confirmed; this is the only place food
	// names either.
	applicationIDEnv     = "PAYMENTS_APPLICATION_ID"
	DefaultApplicationID = "feast"
)

var (
	// ErrPaymentsUnavailable is a transport failure or 5xx. The caller must
	// treat it as "unknown", never as "did not happen".
	ErrPaymentsUnavailable = paymentsclient.ErrUnavailable
	// ErrRefused is a 4xx from payments.
	ErrRefused = paymentsclient.ErrRefused
	// ErrServiceTokenRequired: FOOD_SERVICE_TOKEN_KEY is unset outside local/dev.
	ErrServiceTokenRequired = errors.New("FOOD_SERVICE_TOKEN_KEY is required outside ENV=local/dev")
)

// Client talks to payments-service.
type Client struct {
	c             *paymentsclient.Client
	applicationID string
}

func newClient(baseURL, applicationID string, auth paymentsclient.Auth) (*Client, error) {
	if err := paymentsclient.ValidateApplicationID(applicationID); err != nil {
		return nil, err
	}
	c, err := paymentsclient.New(paymentsclient.Config{
		BaseURL:               baseURL,
		Service:               serviceName,
		ReferenceType:         RefTypeFoodOrder,
		Auth:                  auth,
		MaxResponseBytes:      maxResponseBytes,
		ValidateMethod:        paymentmethod.Validate,
		VerifyReferenceEcho:   true,
		RequirePositiveRefund: true,
	})
	if errors.Is(err, paymentsclient.ErrServiceTokenRequired) {
		return nil, ErrServiceTokenRequired
	}
	if err != nil {
		return nil, err
	}
	return &Client{c: c, applicationID: applicationID}, nil
}

// NewTokenClient authenticates with food-service's service token, under the
// default application id.
func NewTokenClient(baseURL, kid, signingKeyB64 string) (*Client, error) {
	if strings.TrimSpace(kid) == "" {
		kid = DefaultTokenKID
	}
	return newClient(baseURL, DefaultApplicationID, paymentsclient.Auth{TokenKey: signingKeyB64, TokenKID: kid})
}

// NewInternalKeyClient is the LEGACY client (local/dev only; ClientFromEnv
// enforces that), under the default application id.
func NewInternalKeyClient(baseURL, internalKey string) (*Client, error) {
	return newClient(baseURL, DefaultApplicationID, paymentsclient.Auth{InternalKey: internalKey, LegacyAllowed: true})
}

// ClientFromEnv builds the client from PAYMENTS_SERVICE_URL,
// FOOD_SERVICE_TOKEN_KEY, FOOD_SERVICE_TOKEN_KID, ENV, INTERNAL_SERVICE_KEY
// and PAYMENTS_APPLICATION_ID. Without a token key it falls back to the
// internal key only when ENV is local/dev; anything else (including a blank
// ENV) fails closed.
func ClientFromEnv(getenv func(string) string) (*Client, error) {
	baseURL := strings.TrimSpace(getenv("PAYMENTS_SERVICE_URL"))
	if baseURL == "" {
		baseURL = DefaultPaymentsURL
	}
	kid := getenv("FOOD_SERVICE_TOKEN_KID")
	if strings.TrimSpace(kid) == "" {
		kid = DefaultTokenKID
	}
	applicationID := strings.TrimSpace(getenv(applicationIDEnv))
	if applicationID == "" {
		applicationID = DefaultApplicationID
	}
	return newClient(baseURL, applicationID, paymentsclient.Auth{
		TokenKey:      strings.TrimSpace(getenv("FOOD_SERVICE_TOKEN_KEY")),
		TokenKID:      kid,
		InternalKey:   getenv("INTERNAL_SERVICE_KEY"),
		LegacyAllowed: legacyAllowed(getenv("ENV")),
	})
}

func legacyAllowed(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return true
	}
	return false
}

// LegacyAuth reports whether this client presents the shared internal key.
func (c *Client) LegacyAuth() bool { return c != nil && c.c.LegacyAuth() }

// ApplicationID is the payments application this client files under.
func (c *Client) ApplicationID() string { return c.applicationID }

// Intent is the payments-side intent as food sees it. ClientSession holds
// only the three public checkout keys and, when payments named one,
// merchant_display_name (the shared client drops anything else);
// PublicClientSession applies food's further rules.
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

func intentFrom(i *paymentsclient.Intent) *Intent {
	return &Intent{
		ID: i.ID, Status: i.Status, AmountMinor: i.AmountMinor, Currency: i.Currency, Method: i.Method,
		ProviderRef: i.ProviderRef, ReferenceType: i.ReferenceType, ReferenceID: i.ReferenceID,
		PayerID: i.PayerID, PayeeID: i.PayeeID, ClientSession: i.ClientSession.AsMap(),
	}
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

// CreateIntent opens a payment for a food order. The shared client refuses a
// non-positive amount or a non-launch method before calling, and an echoed
// amount or reference that is not this order's.
func (c *Client) CreateIntent(ctx context.Context, in CreateIntentInput) (*Intent, error) {
	out, err := c.c.CreateIntent(ctx, paymentsclient.CreateIntentRequest{
		ApplicationID: c.applicationID,
		ReferenceID:   in.OrderID,
		PayerID:       in.PayerID,
		PayeeID:       in.PayeeID,
		AmountMinor:   in.AmountMinor,
		Method:        in.Method,
		// Deterministic: a retry for the same order collapses to one intent.
		IdempotencyKey: "food_order:" + in.OrderID.String(),
	})
	if err != nil {
		return nil, err
	}
	return intentFrom(out), nil
}

// CallbackVerdict is ADVISORY: it lets food refuse a callback, never accept one
// as payment.
type CallbackVerdict = paymentsclient.CallbackVerdict

// VerifyCallback checks a client-returned checkout payload. expectedMinor is
// the server's order total.
func (c *Client) VerifyCallback(ctx context.Context, intentID uuid.UUID, providerOrderID, providerPaymentID, signature string, expectedMinor int64) (*CallbackVerdict, error) {
	return c.c.VerifyCallback(ctx, intentID, paymentsclient.CallbackRequest{
		ProviderOrderID:     providerOrderID,
		ProviderPaymentID:   providerPaymentID,
		Signature:           signature,
		ExpectedAmountMinor: expectedMinor,
	})
}

// RefundAccepted acknowledges a durable refund command, not moved money.
type RefundAccepted = paymentsclient.RefundAccepted

// Refund requests a refund with an explicit amount and a deterministic key.
func (c *Client) Refund(ctx context.Context, intentID uuid.UUID, amountMinor int64, reason, idempotencyKey string) (*RefundAccepted, error) {
	return c.c.Refund(ctx, intentID, paymentsclient.RefundRequest{
		ApplicationID: c.applicationID, AmountMinor: amountMinor, Reason: reason, IdempotencyKey: idempotencyKey,
	})
}
