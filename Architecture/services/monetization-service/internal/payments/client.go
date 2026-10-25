// Package payments is monetization-service's binding of payments-service for
// paid creator memberships (application `memberships`, reference type
// creator_membership, one intent per membership_payments row).
//
// Everything about talking to payments-service lives in shared/paymentsclient:
// the service token (audience payments, one operation and the
// creator_membership reference type per token, two-minute TTL), the routes,
// the {"data": …} envelope, the error classes and the echo checks. What stays
// here is monetization's side of it:
//
//   - its env var names (PAYMENTS_SERVICE_URL, MONETIZATION_SERVICE_TOKEN_KEY,
//     MONETIZATION_SERVICE_TOKEN_KID, optional MONETIZATION_PAYMENTS_PAYEE_ID),
//     and its rule that there is NO legacy key fallback: without the token key
//     and kid there is no client, and the membership payment routes answer 503
//     MEMBERSHIP_PAYMENTS_UNAVAILABLE (boot is not refused);
//   - the application id and the deterministic keys (internal/membership);
//   - the stricter local refusals: a launch payment method, a positive
//     refund, a reference echo that must be this payment, a 1 MiB cap.
//
// Money on the wire is integer paise (`amount_minor`). A response from this
// client never applies money: only the signed payment.succeeded event does
// (consumer.go). VerifyCallback is advisory.
package payments

import (
	"context"
	"errors"
	"strings"

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

const (
	serviceName = "monetization-service"
	// DefaultPaymentsURL matches the compose service name.
	DefaultPaymentsURL = "http://payments-service:8102"
	maxResponseBytes   = 1 << 20

	EnvURL      = "PAYMENTS_SERVICE_URL"
	EnvTokenKey = "MONETIZATION_SERVICE_TOKEN_KEY" //nolint:gosec // G101: name of the env variable, not its value
	EnvTokenKID = "MONETIZATION_SERVICE_TOKEN_KID" //nolint:gosec // G101: name of the env variable, not its value
	EnvPayeeID  = "MONETIZATION_PAYMENTS_PAYEE_ID"

	// DefaultMethod is the instrument an intent is opened for when the
	// client names none. Checkout lets the member pick within the
	// application's enabled methods.
	DefaultMethod = "upi"
)

// DefaultPayeeID is the payee recorded on a membership intent: the platform,
// which collects and owes the creator their share (creator ledger). A fixed
// name-based UUID so every replica and retry names the same payee.
var DefaultPayeeID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/payee/creator-memberships"))

var (
	// ErrUnavailable is a transport failure or 5xx: "unknown", never "did
	// not happen".
	ErrUnavailable = paymentsclient.ErrUnavailable
	// ErrRefused is a 4xx from payments.
	ErrRefused = paymentsclient.ErrRefused
	// ErrNotConfigured: the token key or kid is unset.
	ErrNotConfigured = errors.New(EnvTokenKey + " and " + EnvTokenKID + " are required for membership payments")
)

// ClientSession is what the app needs to open checkout. Public values only.
type ClientSession struct {
	Provider            string `json:"provider"`
	OrderID             string `json:"order_id"`
	KeyID               string `json:"key_id"`
	MerchantDisplayName string `json:"merchant_display_name,omitempty"`
}

// Intent is the payments-side intent as monetization sees it.
type Intent struct {
	ID            uuid.UUID
	Status        string
	AmountMinor   int64
	Currency      string
	ProviderRef   string
	ReferenceID   uuid.UUID
	ClientSession *ClientSession
}

// IntentInput is server-authored: the amount is the membership_payments row's.
type IntentInput struct {
	PaymentID   uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
	Method      string
}

// Callback is a client-returned checkout payload, checked ADVISORILY.
type Callback struct {
	OrderID             string
	PaymentID           string
	Signature           string
	ExpectedAmountMinor int64
}

// Verdict is the advisory verdict with the echoes to refuse a foreign one.
type Verdict struct {
	Verified      bool
	AmountMinor   int64
	PayerID       uuid.UUID
	ReferenceType string
	ReferenceID   uuid.UUID
	ApplicationID string
}

// RefundAccepted acknowledges a durable refund command; money has NOT moved.
type RefundAccepted struct {
	CommandID   uuid.UUID
	IntentID    uuid.UUID
	AmountMinor int64
	Status      string
}

// API is what the membership service calls. *Client satisfies it; tests fake it.
type API interface {
	CreateIntent(ctx context.Context, in IntentInput) (*Intent, error)
	VerifyCallback(ctx context.Context, intentID uuid.UUID, in Callback) (*Verdict, error)
	Refund(ctx context.Context, intentID uuid.UUID, amountMinor int64, reason, key string) (*RefundAccepted, error)
}

// Client talks to payments-service.
type Client struct {
	c       *paymentsclient.Client
	payeeID uuid.UUID
}

var _ API = (*Client)(nil)

// NewTokenClient authenticates with monetization-service's service token.
// Both the key and the kid are required; there is no legacy key.
func NewTokenClient(baseURL, kid, signingKeyB64 string, payeeID uuid.UUID) (*Client, error) {
	if strings.TrimSpace(kid) == "" || strings.TrimSpace(signingKeyB64) == "" {
		return nil, ErrNotConfigured
	}
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultPaymentsURL
	}
	if payeeID == uuid.Nil {
		payeeID = DefaultPayeeID
	}
	c, err := paymentsclient.New(paymentsclient.Config{
		BaseURL:               baseURL,
		Service:               serviceName,
		ReferenceType:         membership.RefType,
		Auth:                  paymentsclient.Auth{TokenKey: strings.TrimSpace(signingKeyB64), TokenKID: strings.TrimSpace(kid)},
		MaxResponseBytes:      maxResponseBytes,
		ValidateMethod:        paymentmethod.Validate,
		VerifyReferenceEcho:   true,
		RequirePositiveRefund: true,
	})
	if err != nil {
		return nil, err
	}
	return &Client{c: c, payeeID: payeeID}, nil
}

// ClientFromEnv builds the client from the environment. ErrNotConfigured
// (never a boot error) when the key or kid is unset; any other error for a
// key that does not load or a malformed payee.
func ClientFromEnv(getenv func(string) string) (*Client, error) {
	payee := DefaultPayeeID
	if raw := strings.TrimSpace(getenv(EnvPayeeID)); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return nil, errors.New(EnvPayeeID + " must be a non-nil UUID")
		}
		payee = id
	}
	return NewTokenClient(strings.TrimSpace(getenv(EnvURL)), getenv(EnvTokenKID), getenv(EnvTokenKey), payee)
}

// CreateIntent opens (or, under the same key, re-reads) the intent for one
// membership payment.
func (c *Client) CreateIntent(ctx context.Context, in IntentInput) (*Intent, error) {
	method := in.Method
	if method == "" {
		method = DefaultMethod
	}
	out, err := c.c.CreateIntent(ctx, paymentsclient.CreateIntentRequest{
		ApplicationID:  membership.ApplicationID,
		ReferenceID:    in.PaymentID,
		PayerID:        in.PayerID,
		PayeeID:        c.payeeID,
		AmountMinor:    in.AmountMinor,
		Currency:       in.Currency,
		Method:         method,
		IdempotencyKey: membership.IntentKey(in.PaymentID),
	})
	if err != nil {
		return nil, err
	}
	i := &Intent{ID: out.ID, Status: out.Status, AmountMinor: out.AmountMinor, Currency: out.Currency,
		ProviderRef: out.ProviderRef, ReferenceID: out.ReferenceID}
	if out.ClientSession != nil {
		i.ClientSession = &ClientSession{Provider: out.ClientSession.Provider, OrderID: out.ClientSession.OrderID,
			KeyID: out.ClientSession.KeyID, MerchantDisplayName: out.ClientSession.MerchantDisplayName}
	}
	return i, nil
}

// VerifyCallback checks a checkout callback. Advisory: it never applies money.
func (c *Client) VerifyCallback(ctx context.Context, intentID uuid.UUID, in Callback) (*Verdict, error) {
	v, err := c.c.VerifyCallback(ctx, intentID, paymentsclient.CallbackRequest{
		ProviderOrderID: in.OrderID, ProviderPaymentID: in.PaymentID, Signature: in.Signature,
		ExpectedAmountMinor: in.ExpectedAmountMinor,
	})
	if err != nil {
		return nil, err
	}
	return &Verdict{Verified: v.Verified, AmountMinor: v.AmountMinor, PayerID: v.PayerID,
		ReferenceType: v.ReferenceType, ReferenceID: v.ReferenceID, ApplicationID: v.ApplicationID}, nil
}

// Refund files a durable refund command with an explicit amount and key.
func (c *Client) Refund(ctx context.Context, intentID uuid.UUID, amountMinor int64, reason, key string) (*RefundAccepted, error) {
	out, err := c.c.Refund(ctx, intentID, paymentsclient.RefundRequest{
		ApplicationID: membership.ApplicationID, AmountMinor: amountMinor, Reason: reason, IdempotencyKey: key,
	})
	if err != nil {
		return nil, err
	}
	return &RefundAccepted{CommandID: out.CommandID, IntentID: out.IntentID, AmountMinor: out.AmountMinor, Status: out.Status}, nil
}

// IsLocalEnv reports whether ENV names a local or dev stack. Outside it an
// intent without a usable checkout session (the stub gateway) is refused
// rather than presented as payable.
func IsLocalEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return true
	}
	return false
}
