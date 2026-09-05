package gateway

// The provider-neutral port (D10, review §4).
//
// The existing PaymentGateway interface is Razorpay's shape wearing a
// generic name. `VerifySignature(orderID, paymentID, signature)` encodes one
// provider's client-callback scheme — Cashfree signs `timestamp + rawBody`
// with headers, which this signature literally cannot express. An
// "abstraction" that only one implementation can satisfy is a wrapper.
//
// So the port is defined here, and it is frozen and contract-tested in P0
// against BOTH providers' recorded fixtures even though only Razorpay is
// enabled at launch. Proving the seam with a second adapter is the only way
// to know it is a seam.
//
// Five corrections the review required over the first draft:
//
//	1. Currency travels with every amount. A normalized event without it
//	   forces every consumer to assume INR, which is exactly the kind of
//	   implicit assumption that breaks on the first non-INR settlement.
//	2. Provider state is normalized to our own enum, not passed through.
//	3. Create returns provider-specific client-session data as an opaque
//	   blob: Razorpay hands the client an order id plus a key, Cashfree a
//	   payment session id. Neither shape belongs in the domain.
//	4. Capture is a CAPABILITY, not an assumption. Auto-capture providers
//	   have nothing to call.
//	5. Webhook verification takes raw bytes and headers, so a scheme that
//	   signs a timestamp alongside the body can be implemented at all.

import (
	"context"
	"net/http"
	"time"
)

// State is the normalized provider payment state. Providers report a dozen
// different vocabularies; the domain understands only these.
type State string

const (
	StateUnknown    State = "unknown"
	StatePending    State = "pending"
	StateAuthorized State = "authorized"
	StateCaptured   State = "captured"
	StateFailed     State = "failed"
	StateRefunded   State = "refunded"
)

// Money is an amount in the currency's minor unit. Currency is carried with
// it so the two can never drift apart.
type Money struct {
	Minor    int64  `json:"minor"`
	Currency string `json:"currency"`
}

// ProviderOrder is the result of opening a payment at the provider.
type ProviderOrder struct {
	// ProviderOrderID is the PSP's handle for the order.
	ProviderOrderID string
	Amount          Money
	// ClientSession is whatever the client SDK needs to open checkout,
	// opaque to the domain. Razorpay: {"order_id":…,"key_id":…}.
	// Cashfree: {"payment_session_id":…}.
	ClientSession map[string]string
}

// ProviderPaymentState is a normalized view of one payment.
type ProviderPaymentState struct {
	ProviderPaymentID string
	ProviderOrderID   string
	Amount            Money
	State             State
	// CapturedAt is set once the money is actually taken.
	CapturedAt *time.Time
}

// ProviderRefund is a normalized refund result.
type ProviderRefund struct {
	ProviderRefundID  string
	ProviderPaymentID string
	Amount            Money
	State             State
}

// WebhookEvent is a verified, normalized provider event.
type WebhookEvent struct {
	// EventID is the provider's unique event identifier, used as the inbox
	// key. It must be non-empty: an empty key lets one event occupy the
	// slot and mask every later payment (review R-5).
	EventID           string
	Type              string
	ProviderOrderID   string
	ProviderPaymentID string
	ProviderRefundID  string
	Amount            Money
	State             State
	OccurredAt        time.Time
}

// CallbackVerdict is the result of checking a CLIENT-supplied callback.
//
// Genuine reports only that the payload is authentically from the provider.
// It is never sufficient to mark a payment captured — A1/R-3. The field is
// named `Genuine` rather than `Verified` precisely so a call site cannot
// read it as authorisation.
type CallbackVerdict struct {
	Genuine           bool
	ProviderOrderID   string
	ProviderPaymentID string
}

// Capabilities describes what a provider supports, so the domain can branch
// on a fact rather than on a provider name.
type Capabilities struct {
	// ManualCapture is false for providers that always auto-capture.
	ManualCapture bool
	// WebhookTimestamped is true when the signature covers a timestamp, so
	// a replay window can be enforced at the transport layer. Razorpay does
	// not; Cashfree does. Where it is false, the inbox table is the ONLY
	// replay defence, which is why its dedupe must fail closed.
	WebhookTimestamped bool
	// RefundIdempotencyHeader names the provider's idempotency header, or
	// is empty when the provider has none.
	RefundIdempotencyHeader string
}

// Provider is the port every PSP adapter implements.
type Provider interface {
	Name() string
	Capabilities() Capabilities

	// CreateOrder opens a payment. `idempotencyKey` is deterministic and
	// caller-derived; the adapter maps it onto the provider's native
	// mechanism (Razorpay's `receipt`, Cashfree's `order_id`) so a retry
	// returns the original order rather than creating a second one.
	CreateOrder(ctx context.Context, amount Money, idempotencyKey string, meta map[string]string) (ProviderOrder, error)

	// VerifyWebhook authenticates raw bytes plus headers and returns a
	// normalized event. The adapter owns the scheme AND any replay window
	// its provider supports.
	VerifyWebhook(ctx context.Context, headers http.Header, rawBody []byte) (WebhookEvent, error)

	// VerifyClientCallback checks a browser-returned payload. ADVISORY.
	VerifyClientCallback(ctx context.Context, payload map[string]string) (CallbackVerdict, error)

	// Capture takes an authorized payment. Providers whose Capabilities
	// report ManualCapture=false return ErrCaptureNotSupported.
	Capture(ctx context.Context, providerPaymentID string, amount Money, idempotencyKey string) (ProviderPaymentState, error)

	// Refund places a refund. `idempotencyKey` MUST be honoured natively
	// where the provider supports it, so a retry after an ambiguous
	// timeout cannot produce a second refund.
	Refund(ctx context.Context, providerPaymentID string, amount Money, idempotencyKey string) (ProviderRefund, error)

	// FetchPayment is the server-side source of truth used by
	// reconciliation and by ambiguous-timeout recovery.
	FetchPayment(ctx context.Context, providerPaymentID string) (ProviderPaymentState, error)

	// FetchByIdempotencyKey recovers the provider-side object created by a
	// call whose response we never saw (A6). Providers that cannot look up
	// by key return ErrLookupNotSupported.
	FetchByIdempotencyKey(ctx context.Context, key string) (ProviderPaymentState, error)

	// ClientSession is what the CLIENT SDK needs to open checkout for an
	// already-created provider order.
	//
	// It exists so the publishable key travels from the server that created
	// the order rather than being compiled into the app. Those two must
	// agree — an app built against a test key cannot open a sheet for an
	// order the server created against a live key — and sourcing it here
	// makes disagreement impossible rather than merely unlikely.
	//
	// Nothing secret goes in here. It carries the publishable key and the
	// provider's own order handle, both of which the client necessarily
	// learns anyway. The amount is deliberately absent: the client does not
	// name what it is paying (LB-4), the provider order already fixes it.
	//
	// Returns nil when the provider cannot derive a session from the order
	// id alone; the caller treats that as "this provider is not launchable
	// from the app" rather than guessing.
	ClientSession(providerOrderID string) map[string]string
}

// IdempotentRefunder is the optional interface an adapter implements when
// its provider supports a native refund-idempotency header. The refund
// worker prefers it, so a retry cannot double-refund.
type IdempotentRefunder interface {
	InitiateRefundIdempotent(ctx context.Context, providerRef string, amountMinor int64, idempotencyKey string) (GatewayRefund, error)
}

// Sentinel errors so callers branch on capability rather than string match.
var (
	ErrCaptureNotSupported = errorString("gateway: provider auto-captures; manual capture is not available")
	ErrLookupNotSupported  = errorString("gateway: provider cannot look up an object by idempotency key")
	// ErrAmbiguousLookup means one deterministic idempotency key matched more
	// than one provider object (MRC-2.4). The key is supposed to identify
	// exactly one, so this is a reconciliation break: either the key stopped
	// being deterministic or the provider created a duplicate. Adapters
	// return it instead of selecting one of the matches.
	ErrAmbiguousLookup     = errorString("gateway: idempotency key matches more than one provider object")
	ErrSignatureInvalid    = errorString("gateway: webhook signature verification failed")
	ErrMissingEventID      = errorString("gateway: provider event id is missing")
	ErrReplayWindowExpired = errorString("gateway: webhook timestamp is outside the accepted replay window")
)

type errorString string

func (e errorString) Error() string { return string(e) }

// ReplayWindow bounds how old a timestamped webhook may be. Only providers
// whose Capabilities report WebhookTimestamped can enforce it.
const ReplayWindow = 5 * time.Minute
