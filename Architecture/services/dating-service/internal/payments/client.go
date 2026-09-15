package payments

// dating-service's binding of the shared payments-service client.
//
// Everything about talking to payments-service lives in shared/paymentsclient:
// the service token (audience payments, one operation and the dating_premium
// reference type per token, two-minute TTL), the routes, the {"data": …}
// envelope, the error classes and the echo checks. What stays here is
// dating's side of it:
//
//   - its env var names, and its rule that there is NO legacy fallback: without
//     DATING_SERVICE_TOKEN_KEY and DATING_SERVICE_TOKEN_KID there is no client,
//     and the purchase route answers 503 PREMIUM_UNAVAILABLE (boot is not
//     refused);
//   - its payments application id (dating), reference type (dating_premium)
//     and deterministic idempotency keys;
//   - its stricter local refusals: a launch payment method, a positive refund,
//     a reference echo that must be this purchase, a 1 MiB response cap.
//
// Money on the wire is integer paise (`amount_minor`). No float amount is ever
// sent, and echoes are checked rather than trusted.

import (
	"context"
	"errors"
	"strings"

	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/paymentsclient"
	"github.com/google/uuid"
)

const (
	serviceName = "dating-service"
	// ApplicationID is the payments application every dating intent and refund
	// belongs to (payments migration 011).
	ApplicationID = "dating"
	// DefaultPaymentsURL matches the compose service name.
	DefaultPaymentsURL = "http://payments-service:8102"
	// maxResponseBytes caps how much of a payments response is read.
	maxResponseBytes = 1 << 20

	// EnvTokenKey / EnvTokenKID name dating-service's service-token key.
	EnvTokenKey = "DATING_SERVICE_TOKEN_KEY"
	EnvTokenKID = "DATING_SERVICE_TOKEN_KID"
	// EnvPayeeID optionally overrides the payee recorded on every intent.
	EnvPayeeID = "DATING_PAYMENTS_PAYEE_ID"
)

// DefaultPayeeID is the payee recorded on a dating intent: the platform itself
// (there is no seller). A fixed name-based UUID so every replica and every
// retry names the same payee. Override with DATING_PAYMENTS_PAYEE_ID.
var DefaultPayeeID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/payee/dating-premium"))

var (
	// ErrPaymentsUnavailable is a transport failure or 5xx. The caller must
	// treat it as "unknown", never as "did not happen".
	ErrPaymentsUnavailable = paymentsclient.ErrUnavailable
	// ErrRefused is a 4xx from payments.
	ErrRefused = paymentsclient.ErrRefused
	// ErrNotConfigured: DATING_SERVICE_TOKEN_KEY or DATING_SERVICE_TOKEN_KID is
	// unset. Not a boot error: the purchase route answers 503.
	ErrNotConfigured = errors.New("DATING_SERVICE_TOKEN_KEY and DATING_SERVICE_TOKEN_KID are required for premium purchases")
)

// Client talks to payments-service.
type Client struct {
	c       *paymentsclient.Client
	payeeID uuid.UUID
}

// NewTokenClient authenticates with dating-service's service token. Both the
// key and the kid are required; there is no default kid and no legacy key.
func NewTokenClient(baseURL, kid, signingKeyB64 string, payeeID uuid.UUID) (*Client, error) {
	if strings.TrimSpace(kid) == "" || strings.TrimSpace(signingKeyB64) == "" {
		return nil, ErrNotConfigured
	}
	if payeeID == uuid.Nil {
		payeeID = DefaultPayeeID
	}
	c, err := paymentsclient.New(paymentsclient.Config{
		BaseURL:               baseURL,
		Service:               serviceName,
		ReferenceType:         RefTypeDatingPremium,
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

// ClientFromEnv builds the client from PAYMENTS_SERVICE_URL,
// DATING_SERVICE_TOKEN_KEY, DATING_SERVICE_TOKEN_KID and
// DATING_PAYMENTS_PAYEE_ID. It returns ErrNotConfigured (never a boot error)
// when the key or kid is unset, and any other error for a key that does not
// load or a malformed payee.
func ClientFromEnv(getenv func(string) string) (*Client, error) {
	baseURL := strings.TrimSpace(getenv("PAYMENTS_SERVICE_URL"))
	if baseURL == "" {
		baseURL = DefaultPaymentsURL
	}
	payee := DefaultPayeeID
	if raw := strings.TrimSpace(getenv(EnvPayeeID)); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return nil, errors.New(EnvPayeeID + " must be a non-nil UUID")
		}
		payee = id
	}
	return NewTokenClient(baseURL, getenv(EnvTokenKID), getenv(EnvTokenKey), payee)
}

// IsLocalEnv reports whether ENV names a local or dev stack. A blank ENV is
// NOT local. Outside local/dev an intent without a usable checkout session
// (the payments stub gateway) is refused rather than presented as payable.
func IsLocalEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return true
	}
	return false
}

// Intent is the payments-side intent as dating sees it.
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

// CreateIntentInput is server-authored: the amount comes from the catalogue
// price stored on the purchase, never from the client.
type CreateIntentInput struct {
	PurchaseID  uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	Currency    string
	// Method is the client-chosen launch instrument (upi|card).
	Method string
}

// IntentIdempotencyKey is the deterministic payments key for a purchase: a
// retry for the same purchase collapses to one intent.
func IntentIdempotencyKey(purchaseID uuid.UUID) string {
	return RefTypeDatingPremium + ":" + purchaseID.String()
}

// CreateIntent opens a payment for a premium purchase. The shared client
// refuses a non-positive amount or a non-launch method before calling, and an
// echoed amount or reference that is not this purchase's.
func (c *Client) CreateIntent(ctx context.Context, in CreateIntentInput) (*Intent, error) {
	out, err := c.c.CreateIntent(ctx, paymentsclient.CreateIntentRequest{
		ApplicationID:  ApplicationID,
		ReferenceID:    in.PurchaseID,
		PayerID:        in.PayerID,
		PayeeID:        c.payeeID,
		AmountMinor:    in.AmountMinor,
		Currency:       in.Currency,
		Method:         in.Method,
		IdempotencyKey: IntentIdempotencyKey(in.PurchaseID),
	})
	if err != nil {
		return nil, err
	}
	return intentFrom(out), nil
}

// RefundAccepted acknowledges a durable refund command, not moved money.
type RefundAccepted = paymentsclient.RefundAccepted

// Refund requests a refund with an explicit amount and a deterministic key.
// Nothing in dating calls it yet (refunds are operator-initiated in payments);
// the payment.refunded event is what revokes the entitlement.
func (c *Client) Refund(ctx context.Context, intentID uuid.UUID, amountMinor int64, reason, idempotencyKey string) (*RefundAccepted, error) {
	return c.c.Refund(ctx, intentID, paymentsclient.RefundRequest{
		ApplicationID: ApplicationID, AmountMinor: amountMinor, Reason: reason, IdempotencyKey: idempotencyKey,
	})
}
