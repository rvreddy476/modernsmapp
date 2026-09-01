package payments

// The service-to-service payments client.
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
//     commerce holds.
//
//   - Authorship. Commerce now CREATES the payment intent, from the order
//     total it owns. Previously the client chose the amount and the browser
//     could call the same endpoint with any number it liked.
//
//   - Refunds. `Refund` returns "accepted", not "refunded". The provider has
//     not been contacted when it returns; payments has persisted a durable
//     command with a deterministic idempotency key. Reporting a refund as
//     complete before the money moved is what produced the ledger that lied.

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

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Client talks to payments-service.
type Client struct {
	baseURL string
	signer  *servicetoken.Signer
	http    *http.Client
}

// NewP0Client builds a client that authenticates with a service token.
//
// `signingKeyB64` is commerce's OWN Ed25519 private key. payments holds only
// the matching public key, so a compromise of payments cannot forge a
// commerce call.
func NewP0Client(baseURL, kid, signingKeyB64 string) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("payments client: base URL is required")
	}
	signer, err := servicetoken.NewSignerFromBase64("commerce-service", kid, signingKeyB64)
	if err != nil {
		return nil, fmt.Errorf("payments client: signing key: %w", err)
	}
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		signer:  signer,
		http:    &http.Client{Timeout: 15 * time.Second},
	}, nil
}

var (
	// ErrPaymentsUnavailable is a transport-level failure. The caller must
	// treat it as "unknown", never as "did not happen".
	ErrPaymentsUnavailable = errors.New("payments service unavailable")
	// ErrRefused is a 4xx from payments — a real rejection.
	ErrRefused = errors.New("payments refused the request")
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
	// created against; an app-compiled key could silently disagree.
	//
	// Absent when the provider cannot derive a session from the order id
	// alone (Cashfree). The app then reports that it cannot open a sheet
	// rather than opening one that will fail.
	ClientSession map[string]string `json:"client_session,omitempty"`
	ReferenceType string            `json:"reference_type"`
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
func (c *Client) CreateIntent(ctx context.Context, in CreateIntentInput) (*Intent, error) {
	if in.AmountMinor <= 0 {
		return nil, fmt.Errorf("payments client: refusing to create an intent for %s", in.AmountMinor)
	}
	// Deterministic: a retry for the same order collapses to one intent
	// rather than opening a second payable for the same goods.
	idem := "order:" + in.OrderID.String()

	body := map[string]any{
		"payer_id":        in.PayerID,
		"payee_id":        in.PayeeID,
		"reference_type":  servicetoken.RefOrder,
		"reference_id":    in.OrderID,
		"amount_minor":    in.AmountMinor.Int64(),
		"currency":        "INR",
		"method":          in.Method,
		"idempotency_key": idem,
	}
	var out Intent
	err := c.do(ctx, http.MethodPost, "/v1/payments/intents",
		servicetoken.OpIntentCreate, servicetoken.RefOrder, body, &out)
	if err != nil {
		return nil, err
	}
	// LB-5: never trust the echo. If payments came back with a different
	// amount than we authored, something is wrong upstream and this order
	// must not proceed to a payable state.
	if out.AmountMinor != in.AmountMinor {
		return nil, fmt.Errorf(
			"payments client: intent amount %s does not match the order total %s",
			out.AmountMinor, in.AmountMinor)
	}
	return &out, nil
}

// GetIntent reads current payment state. This is what the order's
// payment/status endpoint reports — the app polls it rather than trusting a
// redirect (A1).
func (c *Client) GetIntent(ctx context.Context, id uuid.UUID) (*Intent, error) {
	var out Intent
	err := c.do(ctx, http.MethodGet, "/v1/payments/intents/"+id.String(),
		servicetoken.OpIntentRead, servicetoken.RefOrder, nil, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// CallbackVerdict is the ADVISORY result of checking a client callback.
type CallbackVerdict struct {
	Verified bool   `json:"verified"`
	Advisory bool   `json:"advisory"`
	Status   string `json:"status"`
}

// VerifyCallback checks a browser-returned payment payload.
//
// A1/R-3: this is EVIDENCE, not authority. A true verdict means "the
// callback looks genuine, stop the spinner and keep polling". It does not
// mark anything paid, and commerce must never treat it as if it did — the
// order becomes paid only when the payment event arrives through the inbox.
func (c *Client) VerifyCallback(ctx context.Context, intentID uuid.UUID, orderID, paymentID, signature string, expected money.Paise) (*CallbackVerdict, error) {
	body := map[string]any{
		"razorpay_order_id":   orderID,
		"razorpay_payment_id": paymentID,
		"razorpay_signature":  signature,
		"amount_minor":        expected.Int64(),
	}
	var out CallbackVerdict
	err := c.do(ctx, http.MethodPost, "/v1/payments/intents/"+intentID.String()+"/verify",
		servicetoken.OpIntentRead, servicetoken.RefOrder, body, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// RefundAccepted is what a refund request returns: an acknowledgement that
// the refund is durable, NOT that money has moved.
type RefundAccepted struct {
	CommandID   uuid.UUID   `json:"command_id"`
	IntentID    uuid.UUID   `json:"intent_id"`
	AmountMinor money.Paise `json:"amount_minor"`
	Status      string      `json:"status"`
}

// Refund asks payments to refund, with a deterministic idempotency key.
//
// A6: the SAME key on every retry, so an ambiguous timeout followed by a
// retry produces one refund at the PSP.
func (c *Client) Refund(ctx context.Context, intentID uuid.UUID, amount money.Paise, reason, idempotencyKey string) (*RefundAccepted, error) {
	if idempotencyKey == "" {
		return nil, fmt.Errorf("payments client: a refund idempotency key is required")
	}
	body := map[string]any{
		"amount_minor":    amount.Int64(),
		"reason":          reason,
		"idempotency_key": idempotencyKey,
	}
	var out RefundAccepted
	err := c.do(ctx, http.MethodPost, "/v1/payments/intents/"+intentID.String()+"/refund",
		servicetoken.OpRefundCreate, servicetoken.RefOrder, body, &out)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

// do performs one authenticated call.
func (c *Client) do(ctx context.Context, method, path, op, refType string, body any, out any) error {
	// A2: a fresh, narrowly-scoped token per call. Minting per request
	// rather than per process keeps the TTL short, which is the only
	// control there is against replay of a stolen token.
	token, err := c.signer.Mint(
		servicetoken.AudiencePayments, "commerce-service",
		[]string{op}, []string{refType}, 2*time.Minute)
	if err != nil {
		return fmt.Errorf("payments client: mint token: %w", err)
	}

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
	req.Header.Set("X-Service-Authorization", "Bearer "+token)

	resp, err := c.http.Do(req)
	if err != nil {
		// Transport failure. The caller must NOT conclude the request did
		// not happen — it may have been applied and the response lost,
		// which is exactly why every mutating call carries a deterministic
		// idempotency key.
		return fmt.Errorf("%w: %v", ErrPaymentsUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	if resp.StatusCode >= 500 {
		return fmt.Errorf("%w: status %d", ErrPaymentsUnavailable, resp.StatusCode)
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("%w: status %d: %s", ErrRefused, resp.StatusCode, truncate(string(raw), 300))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	// payments wraps responses in the shared envelope {"data": …}.
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
