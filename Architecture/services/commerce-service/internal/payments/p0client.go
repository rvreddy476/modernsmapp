package payments

// The service-to-service payments client: commerce-service's binding of
// shared/paymentsclient.
//
// Amendments A1, A2 and LB-4 all land here.
//
// What changed and why:
//
//   - Authentication. The old client sent `X-Internal-Service-Key`, the same
//     cluster-wide header the API gateway injected into every proxied
//     request. Anything that could reach payments could therefore act as a
//     service. This client signs a short-lived Ed25519 token, scoped to one
//     audience, one operation and one reference type, with a key only
//     commerce holds. The internal key survives ONLY as a legacy credential
//     (NewInternalKeyClient) for a deployment that has not issued commerce a
//     signing key yet — the dev stack — and main.go refuses it in production.
//
//   - Routes. Every call lands on the /v1/payments/internal family: the
//     service-authority half of payments' route split. The user-facing
//     family (/v1/payments/*) authorises against a forwarded X-User-Id and
//     has nothing a service acting on its own order needs.
//
//   - Authorship. Commerce now CREATES the payment intent, from the order
//     total it owns. Previously the client chose the amount and the browser
//     could call the same endpoint with any number it liked.
//
//   - Refunds. `Refund` returns "accepted", not "refunded". The provider has
//     not been contacted when it returns; payments has persisted a durable
//     command with a deterministic idempotency key. Reporting a refund as
//     complete before the money moved is what produced the ledger that lied.
//
// The mechanics (token minting, the legacy header, the {"data": …} envelope,
// the unavailable/refused error classes, the amount echo check) live in
// shared/paymentsclient. What stays here is commerce's side: its reference
// type and idempotency keys, its payments application id, its traced HTTP
// client, and the money.Paise-typed shapes the service and its tests use.

import (
	"context"
	"net/http"
	"strings"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/paymentsclient"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	serviceName = "commerce-service"

	// applicationIDEnv and DefaultApplicationID name the payments application
	// every commerce intent and refund belongs to. PROVISIONAL until the
	// application registry's keys are confirmed; this is the only place
	// commerce names either.
	applicationIDEnv     = "PAYMENTS_APPLICATION_ID"
	DefaultApplicationID = "mstore"
)

// ApplicationIDFromEnv reads PAYMENTS_APPLICATION_ID, defaulting to
// DefaultApplicationID. The constructors validate the result.
func ApplicationIDFromEnv(getenv func(string) string) string {
	if id := strings.TrimSpace(getenv(applicationIDEnv)); id != "" {
		return id
	}
	return DefaultApplicationID
}

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
		BaseURL:       baseURL,
		Service:       serviceName,
		ReferenceType: servicetoken.RefOrder,
		Auth:          auth,
		HTTPClient:    newHTTPClient(),
	})
	if err != nil {
		return nil, err
	}
	return &Client{c: c, applicationID: applicationID}, nil
}

// NewP0Client builds a client that authenticates with a service token.
//
// `signingKeyB64` is commerce's OWN Ed25519 private key. payments holds only
// the matching public key, so a compromise of payments cannot forge a
// commerce call.
func NewP0Client(baseURL, applicationID, kid, signingKeyB64 string) (*Client, error) {
	return newClient(baseURL, applicationID, paymentsclient.Auth{TokenKey: signingKeyB64, TokenKID: kid})
}

// NewInternalKeyClient builds the LEGACY client: same routes, same
// contracts, but authenticated with the shared X-Internal-Service-Key
// instead of a per-service token. payments-service admits it only while its
// own legacy fallback is on, and both sides log that they are running this
// way. It exists so a deployment without a commerce signing key (the dev
// stack) still checks out; it is not a production configuration.
//
// legacyAllowed is main.go's verdict that ENV is local. Without it no client
// is built, whatever key is supplied.
func NewInternalKeyClient(baseURL, applicationID, internalKey string, legacyAllowed bool) (*Client, error) {
	return newClient(baseURL, applicationID, paymentsclient.Auth{InternalKey: internalKey, LegacyAllowed: legacyAllowed})
}

// LegacyAuth reports whether this client presents the shared internal key
// rather than a service token.
func (c *Client) LegacyAuth() bool { return c != nil && c.c.LegacyAuth() }

// ApplicationID is the payments application this client files under.
func (c *Client) ApplicationID() string { return c.applicationID }

func newHTTPClient() *http.Client {
	// Phase F3.2 — otelhttp auto-instruments outbound HTTP so payments
	// calls appear as a child client span under the originating
	// checkout / confirm-payment server span in Jaeger.
	return &http.Client{
		Timeout:   paymentsclient.DefaultTimeout,
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
}

var (
	// ErrPaymentsUnavailable is a transport-level failure. The caller must
	// treat it as "unknown", never as "did not happen".
	ErrPaymentsUnavailable = paymentsclient.ErrUnavailable
	// ErrRefused is a 4xx from payments — a real rejection.
	ErrRefused = paymentsclient.ErrRefused
)

// Intent is the payments-side intent as commerce sees it.
type Intent struct {
	ID          uuid.UUID   `json:"id"`
	Status      string      `json:"status"`
	AmountMinor money.Paise `json:"amount_minor"`
	Currency    string      `json:"currency"`
	ProviderRef string      `json:"provider_ref"`
	ReferenceID uuid.UUID   `json:"reference_id"`
	// ClientSession is what the client SDK needs to open checkout: the
	// provider name, its order handle and the PUBLISHABLE key. It comes from
	// payments so the key always matches the one the provider order was
	// created against; an app-compiled key could silently disagree. Only
	// those three keys are ever present, plus merchant_display_name when
	// payments-service names the application's merchant (trimmed, at most 64
	// runes; the key is omitted rather than sent empty).
	//
	// Absent when the provider cannot derive a session from the order id
	// alone (Cashfree). The app then reports that it cannot open a sheet
	// rather than opening one that will fail.
	ClientSession map[string]string `json:"client_session,omitempty"`
	ReferenceType string            `json:"reference_type"`
	PayerID       uuid.UUID         `json:"payer_id"`
	PayeeID       uuid.UUID         `json:"payee_id"`
}

func intentFrom(i *paymentsclient.Intent) *Intent {
	return &Intent{
		ID: i.ID, Status: i.Status, AmountMinor: money.Paise(i.AmountMinor), Currency: i.Currency,
		ProviderRef: i.ProviderRef, ReferenceID: i.ReferenceID, ClientSession: i.ClientSession.AsMap(),
		ReferenceType: i.ReferenceType, PayerID: i.PayerID, PayeeID: i.PayeeID,
	}
}

// CreateIntentInput is a server-authored payment request.
//
// There is no field a client could influence: the amount comes from the
// order, the reference is the order, and the idempotency key is derived
// from the order rather than supplied.
type CreateIntentInput struct {
	OrderID     uuid.UUID
	PayerID     uuid.UUID
	PayeeID     uuid.UUID
	AmountMinor money.Paise
	Method      string
}

// CreateIntent opens a payment for an order (LB-4).
//
// LB-5: never trust the echo. The shared client refuses an intent whose
// amount differs from the order total, so this order can never proceed to a
// payable state on a number commerce did not author.
func (c *Client) CreateIntent(ctx context.Context, in CreateIntentInput) (*Intent, error) {
	out, err := c.c.CreateIntent(ctx, paymentsclient.CreateIntentRequest{
		ApplicationID: c.applicationID,
		ReferenceID:   in.OrderID,
		PayerID:       in.PayerID,
		PayeeID:       in.PayeeID,
		AmountMinor:   in.AmountMinor.Int64(),
		Method:        in.Method,
		// Deterministic: a retry for the same order collapses to one intent
		// rather than opening a second payable for the same goods.
		IdempotencyKey: "order:" + in.OrderID.String(),
	})
	if err != nil {
		return nil, err
	}
	return intentFrom(out), nil
}

// GetIntent reads current payment state. This is what the order's
// payment/status endpoint reports — the app polls it rather than trusting a
// redirect (A1).
func (c *Client) GetIntent(ctx context.Context, id uuid.UUID) (*Intent, error) {
	out, err := c.c.GetIntent(ctx, id)
	if err != nil {
		return nil, err
	}
	return intentFrom(out), nil
}

// CallbackVerdict is the ADVISORY result of checking a client callback.
//
// The parties and reference are echoed so the caller can REFUSE a genuine
// signature that belongs to another order or another user; they never make
// a positive verdict mean "paid".
type CallbackVerdict = paymentsclient.CallbackVerdict

// VerifyCallback checks a browser-returned payment payload.
//
// A1/R-3: this is EVIDENCE, not authority. A true verdict means "the
// callback looks genuine, stop the spinner and keep polling". It does not
// mark anything paid, and commerce must never treat it as if it did — the
// order becomes paid only when the payment event arrives through the inbox.
func (c *Client) VerifyCallback(ctx context.Context, intentID uuid.UUID, orderID, paymentID, signature string, expected money.Paise) (*CallbackVerdict, error) {
	return c.c.VerifyCallback(ctx, intentID, paymentsclient.CallbackRequest{
		ProviderOrderID:     orderID,
		ProviderPaymentID:   paymentID,
		Signature:           signature,
		ExpectedAmountMinor: expected.Int64(),
	})
}

// RefundAccepted is what a refund request returns: an acknowledgement that
// the refund is durable, NOT that money has moved.
type RefundAccepted = paymentsclient.RefundAccepted

// Refund asks payments to refund, with a deterministic idempotency key.
//
// A6: the SAME key on every retry, so an ambiguous timeout followed by a
// retry produces one refund at the PSP.
func (c *Client) Refund(ctx context.Context, intentID uuid.UUID, amount money.Paise, reason, idempotencyKey string) (*RefundAccepted, error) {
	return c.c.Refund(ctx, intentID, paymentsclient.RefundRequest{
		ApplicationID:  c.applicationID,
		AmountMinor:    amount.Int64(),
		Reason:         reason,
		IdempotencyKey: idempotencyKey,
	})
}
