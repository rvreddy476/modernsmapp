// Package payments is rider-service's binding of payments-service: the
// client that opens intents and files refunds for Mopedu rides, and the
// consumer that applies the signed payment events.
//
// Everything about talking to payments-service lives in
// shared/paymentsclient (the service token with audience payments, the
// routes, the {"data": …} envelope, the error classes and the echo checks).
// What stays here is rider's side of it (dating-service's pattern):
//
//   - its env var names, and the rule that there is NO legacy fallback:
//     without RIDER_SERVICE_TOKEN_KEY and RIDER_SERVICE_TOKEN_KID there is no
//     client, and the online payment routes answer 503 PAYMENTS_UNAVAILABLE
//     (boot is not refused);
//   - its payments application id (mopedu), reference type (mopedu_ride)
//     and deterministic idempotency keys;
//   - its stricter local refusals: a launch payment method, a positive
//     refund, a reference echo that must be this ride's, a 1 MiB cap.
//
// Money on the wire is integer paise (`amount_minor`). Nothing here marks a
// payment paid: the intent echo and the callback verdict are advisory, and
// only the consumer's signed events change a payment row.
package payments

import (
	"context"
	"errors"
	"strings"

	"github.com/atpost/shared/paymentmethod"
	"github.com/atpost/shared/paymentsclient"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const (
	serviceName = "rider-service"
	// ApplicationID is the payments application every Mopedu intent and
	// refund belongs to (payments migration 012).
	ApplicationID = "mopedu"
	// RefTypeMopeduRide is the payments reference type rider owns.
	RefTypeMopeduRide = servicetoken.RefMopeduRide
	// CurrencyINR is the only currency Mopedu prices in.
	CurrencyINR = "INR"
	// DefaultPaymentsURL matches the compose service name.
	DefaultPaymentsURL = "http://payments-service:8102"
	// maxResponseBytes caps how much of a payments response is read.
	maxResponseBytes = 1 << 20

	// EnvTokenKey / EnvTokenKID name rider-service's service-token key.
	EnvTokenKey = "RIDER_SERVICE_TOKEN_KEY"
	EnvTokenKID = "RIDER_SERVICE_TOKEN_KID"
	// EnvPayeeID optionally overrides the payee recorded on every intent.
	EnvPayeeID = "MOPEDU_PAYMENTS_PAYEE_ID"
)

// DefaultPayeeID is the payee recorded on a Mopedu intent: the platform,
// which collects the fare as the e-commerce operator and settles the captain
// separately. A fixed name-based UUID so every replica names the same payee.
var DefaultPayeeID = uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/payee/mopedu-rides"))

var (
	// ErrPaymentsUnavailable is a transport failure or 5xx. The caller must
	// treat it as "unknown", never as "did not happen".
	ErrPaymentsUnavailable = paymentsclient.ErrUnavailable
	// ErrRefused is a 4xx from payments.
	ErrRefused = paymentsclient.ErrRefused
	// ErrNotConfigured: RIDER_SERVICE_TOKEN_KEY or RIDER_SERVICE_TOKEN_KID is
	// unset. Not a boot error: the online payment routes answer 503.
	ErrNotConfigured = errors.New("RIDER_SERVICE_TOKEN_KEY and RIDER_SERVICE_TOKEN_KID are required for online ride payments")
)

// Client talks to payments-service.
type Client struct {
	c       *paymentsclient.Client
	payeeID uuid.UUID
}

// NewTokenClient authenticates with rider-service's service token. Both the
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
		ReferenceType:         RefTypeMopeduRide,
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
// RIDER_SERVICE_TOKEN_KEY, RIDER_SERVICE_TOKEN_KID and
// MOPEDU_PAYMENTS_PAYEE_ID. It returns ErrNotConfigured (never a boot error)
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

// Intent is the payments-side intent as rider sees it.
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

// CreateIntentInput is server-authored: the amount is the ride's final
// total (fare_breakdown.total_paise) or the outstanding row's amount, never
// the client's.
type CreateIntentInput struct {
	// ReferenceID is the ride id, or the outstanding row id (the wire
	// reference is a UUID; IdempotencyKey tells the two apart).
	ReferenceID uuid.UUID
	PayerID     uuid.UUID
	AmountMinor int64
	// Method is the client-chosen launch instrument (upi|card).
	Method string
	// IdempotencyKey is RideIntentKey or OutstandingIntentKey.
	IdempotencyKey string
}

// RideIntentKey is the deterministic payments key for a ride and method: a
// retry for the same ride and method collapses to one intent.
func RideIntentKey(rideID uuid.UUID, method string) string {
	return "ride:" + rideID.String() + ":" + method
}

// OutstandingIntentKey is the deterministic key for paying an outstanding
// fee directly.
func OutstandingIntentKey(outstandingID uuid.UUID, method string) string {
	return "outstanding:" + outstandingID.String() + ":" + method
}

// RefundKey is the deterministic key for one rider_ride_refunds row, so an
// ambiguous timeout followed by a retry produces one refund at the PSP.
func RefundKey(refundID uuid.UUID) string {
	return "refund:" + refundID.String()
}

// CreateIntent opens a payment. The shared client refuses a non-positive
// amount or a non-launch method before calling, and an echoed amount or
// reference that is not this record's.
func (c *Client) CreateIntent(ctx context.Context, in CreateIntentInput) (*Intent, error) {
	out, err := c.c.CreateIntent(ctx, paymentsclient.CreateIntentRequest{
		ApplicationID:  ApplicationID,
		ReferenceID:    in.ReferenceID,
		PayerID:        in.PayerID,
		PayeeID:        c.payeeID,
		AmountMinor:    in.AmountMinor,
		Currency:       CurrencyINR,
		Method:         in.Method,
		IdempotencyKey: in.IdempotencyKey,
	})
	if err != nil {
		return nil, err
	}
	return intentFrom(out), nil
}

// CallbackRequest is the client-returned checkout payload.
type CallbackRequest = paymentsclient.CallbackRequest

// CallbackVerdict is ADVISORY: a positive verdict never means "paid". The
// parties and the reference are echoed so a genuine signature for another
// ride or another payer can be refused.
type CallbackVerdict = paymentsclient.CallbackVerdict

// VerifyCallback checks a client checkout callback against an intent.
func (c *Client) VerifyCallback(ctx context.Context, intentID uuid.UUID, in CallbackRequest) (*CallbackVerdict, error) {
	return c.c.VerifyCallback(ctx, intentID, in)
}

// RefundAccepted acknowledges a durable refund command, not moved money.
type RefundAccepted = paymentsclient.RefundAccepted

// Refund requests a refund of an explicit amount with a deterministic key
// (RefundKey). The payment.refunded event is what records the money moved.
func (c *Client) Refund(ctx context.Context, intentID uuid.UUID, amountMinor int64, reason, idempotencyKey string) (*RefundAccepted, error) {
	return c.c.Refund(ctx, intentID, paymentsclient.RefundRequest{
		ApplicationID: ApplicationID, AmountMinor: amountMinor, Reason: reason, IdempotencyKey: idempotencyKey,
	})
}
