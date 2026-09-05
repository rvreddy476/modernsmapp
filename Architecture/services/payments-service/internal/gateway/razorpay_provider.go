package gateway

// Razorpay adapter for the provider-neutral port.
//
// Facts this encodes, verified against Razorpay's official documentation
// rather than recalled:
//
//   - Webhook signature is HMAC-SHA256, hex, over the RAW request body,
//     keyed by the webhook secret, in `X-Razorpay-Signature`. The body must
//     not be parsed or re-serialised before signing.
//   - The unique event identifier is the `x-razorpay-event-id` HEADER.
//     Razorpay does NOT put an event id in the payment webhook body. This is
//     review R-5: the previous code read a body `id` that does not exist, so
//     the inbox key was almost always empty.
//   - Razorpay publishes no webhook timestamp header. There is therefore no
//     replay window we can enforce at the transport layer, which makes the
//     inbox table the ONLY replay defence and its fail-closed behaviour
//     load-bearing rather than belt-and-braces.
//   - The client checkout callback signs `order_id|payment_id` with the API
//     key secret. That is a DIFFERENT scheme from the webhook, and Razorpay's
//     own guidance is to use the callback for immediate client-side feedback
//     and webhooks for server-side verification.
//   - Refund creation honours an idempotency header, so a retry after an
//     ambiguous timeout returns the original refund (A6).

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
	"net/url"
	"strings"
	"time"
)

// RazorpayProvider implements Provider.
type RazorpayProvider struct {
	keyID         string
	keySecret     string
	webhookSecret string
	baseURL       string
	client        *http.Client
}

// NewRazorpayProvider builds the adapter. An empty webhookSecret is
// permitted only so tests can construct one; VerifyWebhook refuses to
// verify without it, and the service refuses to boot without it.
func NewRazorpayProvider(keyID, keySecret, webhookSecret string) *RazorpayProvider {
	return &RazorpayProvider{
		keyID:         keyID,
		keySecret:     keySecret,
		webhookSecret: webhookSecret,
		baseURL:       razorpayBaseURL,
		client:        &http.Client{Timeout: 30 * time.Second},
	}
}

// WithEndpoint points the adapter at a different base URL and HTTP client.
//
// MRC-1's required proof is that the REAL adapter decodes `currency` from
// recorded response bytes — the previous pass's reconciliation test used a
// hand-built struct that supplied the very field the production adapter
// dropped, so it was green while production was wrong. Driving the real
// decoder needs a seam, and this is the smallest one: no behaviour changes,
// only where the bytes come from.
//
// Production never calls this; NewRazorpayProvider already points at
// api.razorpay.com.
func (g *RazorpayProvider) WithEndpoint(baseURL string, client *http.Client) *RazorpayProvider {
	if baseURL != "" {
		g.baseURL = baseURL
	}
	if client != nil {
		g.client = client
	}
	return g
}

// ClientSession is derivable for Razorpay: the checkout sheet needs the
// publishable key and the order id, and both are known here.
func (g *RazorpayProvider) ClientSession(providerOrderID string) map[string]string {
	if providerOrderID == "" || g.keyID == "" {
		return nil
	}
	return map[string]string{
		"provider": "razorpay",
		"order_id": providerOrderID,
		"key_id":   g.keyID,
	}
}

func (g *RazorpayProvider) Name() string { return "razorpay" }

func (g *RazorpayProvider) Capabilities() Capabilities {
	return Capabilities{
		ManualCapture: true,
		// No timestamp header exists, so a captured body replays forever
		// unless the inbox stops it.
		WebhookTimestamped:      false,
		RefundIdempotencyHeader: "X-Refund-Idempotency",
	}
}

func (g *RazorpayProvider) CreateOrder(ctx context.Context, amount Money, idempotencyKey string, meta map[string]string) (ProviderOrder, error) {
	if idempotencyKey == "" {
		return ProviderOrder{}, fmt.Errorf("razorpay: idempotency key is required")
	}
	body := map[string]any{
		"amount":   amount.Minor,
		"currency": amount.Currency,
		// `receipt` is our deterministic identity for this order. It is
		// what FetchByIdempotencyKey looks up after an ambiguous timeout,
		// so a retry recovers the original order instead of opening a
		// second one against the same cart.
		"receipt": idempotencyKey,
	}
	if len(meta) > 0 {
		notes := map[string]string{}
		for k, v := range meta {
			notes[k] = v
		}
		body["notes"] = notes
	}
	var out struct {
		ID       string `json:"id"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := g.do(ctx, http.MethodPost, "/orders", body, nil, &out); err != nil {
		return ProviderOrder{}, err
	}
	return ProviderOrder{
		ProviderOrderID: out.ID,
		Amount:          Money{Minor: out.Amount, Currency: out.Currency},
		ClientSession: map[string]string{
			"provider": "razorpay",
			"order_id": out.ID,
			"key_id":   g.keyID,
		},
	}, nil
}

// VerifyWebhook authenticates the raw body and normalizes the event.
func (g *RazorpayProvider) VerifyWebhook(ctx context.Context, headers http.Header, rawBody []byte) (WebhookEvent, error) {
	if g.webhookSecret == "" {
		// Fail CLOSED. LB-6: the old handler verified only when a secret
		// happened to be configured, so a missing secret accepted every
		// unsigned webhook — an attacker could mark any order paid.
		return WebhookEvent{}, ErrSignatureInvalid
	}
	sig := headers.Get("X-Razorpay-Signature")
	if sig == "" {
		return WebhookEvent{}, ErrSignatureInvalid
	}
	mac := hmac.New(sha256.New, []byte(g.webhookSecret))
	mac.Write(rawBody)
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return WebhookEvent{}, ErrSignatureInvalid
	}

	// R-5: the event id is a header, and an empty one is not usable as a
	// dedupe key.
	eventID := headers.Get("X-Razorpay-Event-Id")
	if eventID == "" {
		return WebhookEvent{}, ErrMissingEventID
	}

	var env struct {
		Event   string `json:"event"`
		Payload struct {
			Payment struct {
				Entity struct {
					ID       string `json:"id"`
					OrderID  string `json:"order_id"`
					Amount   int64  `json:"amount"`
					Currency string `json:"currency"`
					Status   string `json:"status"`
				} `json:"entity"`
			} `json:"payment"`
			Refund struct {
				Entity struct {
					ID        string `json:"id"`
					PaymentID string `json:"payment_id"`
					Amount    int64  `json:"amount"`
					Currency  string `json:"currency"`
					Status    string `json:"status"`
					// B3: Razorpay's refund entity does not carry the order
					// id as a first-class field, but `notes` and `acquirer_data`
					// sometimes echo it, and the API version matters. Decoding
					// it when present costs nothing; the resolution path no
					// longer DEPENDS on it, because the store falls back to
					// provider_payment_id.
					OrderID string `json:"order_id"`
				} `json:"entity"`
			} `json:"refund"`
			Order struct {
				Entity struct {
					ID       string `json:"id"`
					Amount   int64  `json:"amount"`
					Currency string `json:"currency"`
				} `json:"entity"`
			} `json:"order"`
		} `json:"payload"`
		CreatedAt int64 `json:"created_at"`
	}
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return WebhookEvent{}, fmt.Errorf("razorpay: decode webhook: %w", err)
	}

	ev := WebhookEvent{
		EventID: eventID,
		Type:    env.Event,
	}
	if env.CreatedAt > 0 {
		ev.OccurredAt = time.Unix(env.CreatedAt, 0).UTC()
	}

	p := env.Payload.Payment.Entity
	r := env.Payload.Refund.Entity
	switch {
	case r.ID != "":
		ev.ProviderRefundID = r.ID
		ev.ProviderPaymentID = r.PaymentID
		// B3. This used to read ONLY `p.OrderID` — the order id off the
		// PAYMENT entity. A refund-only payload does not contain a payment
		// entity, so a legitimate `refund.processed` yielded an empty order
		// id, the intent lookup failed, and the refund was lost while its
		// inbox row stayed committed.
		//
		// Prefer the refund entity's own order id, fall back to the payment
		// entity when the payload happens to carry one, and otherwise leave
		// it empty on purpose: the store resolves by provider_payment_id in
		// that case, which a refund entity always carries.
		ev.ProviderOrderID = firstNonEmpty(r.OrderID, p.OrderID)
		ev.Amount = Money{Minor: r.Amount, Currency: r.Currency}
		ev.State = StateRefunded
	case p.ID != "":
		ev.ProviderPaymentID = p.ID
		ev.ProviderOrderID = p.OrderID
		ev.Amount = Money{Minor: p.Amount, Currency: p.Currency}
		ev.State = normalizeRazorpayState(p.Status)
	case env.Payload.Order.Entity.ID != "":
		ev.ProviderOrderID = env.Payload.Order.Entity.ID
		ev.Amount = Money{
			Minor:    env.Payload.Order.Entity.Amount,
			Currency: env.Payload.Order.Entity.Currency,
		}
		ev.State = StateCaptured
	}
	return ev, nil
}

// VerifyClientCallback checks the browser-returned triple. ADVISORY only.
func (g *RazorpayProvider) VerifyClientCallback(ctx context.Context, payload map[string]string) (CallbackVerdict, error) {
	orderID := payload["razorpay_order_id"]
	paymentID := payload["razorpay_payment_id"]
	sig := payload["razorpay_signature"]
	if orderID == "" || paymentID == "" || sig == "" {
		return CallbackVerdict{}, ErrSignatureInvalid
	}
	mac := hmac.New(sha256.New, []byte(g.keySecret))
	mac.Write([]byte(orderID + "|" + paymentID))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return CallbackVerdict{}, ErrSignatureInvalid
	}
	return CallbackVerdict{
		Genuine:           true,
		ProviderOrderID:   orderID,
		ProviderPaymentID: paymentID,
	}, nil
}

func (g *RazorpayProvider) Capture(ctx context.Context, providerPaymentID string, amount Money, idempotencyKey string) (ProviderPaymentState, error) {
	var out razorpayPayment
	err := g.do(ctx, http.MethodPost, "/payments/"+url.PathEscape(providerPaymentID)+"/capture",
		map[string]any{"amount": amount.Minor, "currency": amount.Currency},
		map[string]string{"X-Payment-Idempotency": idempotencyKey}, &out)
	if err != nil {
		return ProviderPaymentState{}, err
	}
	return out.normalize(), nil
}

func (g *RazorpayProvider) Refund(ctx context.Context, providerPaymentID string, amount Money, idempotencyKey string) (ProviderRefund, error) {
	if idempotencyKey == "" {
		return ProviderRefund{}, fmt.Errorf("razorpay: refund idempotency key is required")
	}
	var out struct {
		ID        string `json:"id"`
		PaymentID string `json:"payment_id"`
		Amount    int64  `json:"amount"`
		Currency  string `json:"currency"`
		Status    string `json:"status"`
	}
	// A6: the provider's own idempotency header. Two attempts with the same
	// key produce one refund at Razorpay, so an ambiguous timeout is safe
	// to retry.
	err := g.do(ctx, http.MethodPost, "/payments/"+url.PathEscape(providerPaymentID)+"/refund",
		map[string]any{"amount": amount.Minor},
		map[string]string{"X-Refund-Idempotency": idempotencyKey}, &out)
	if err != nil {
		return ProviderRefund{}, err
	}
	return ProviderRefund{
		ProviderRefundID:  out.ID,
		ProviderPaymentID: out.PaymentID,
		Amount:            Money{Minor: out.Amount, Currency: out.Currency},
		State:             StateRefunded,
	}, nil
}

func (g *RazorpayProvider) FetchPayment(ctx context.Context, providerPaymentID string) (ProviderPaymentState, error) {
	var out razorpayPayment
	if err := g.do(ctx, http.MethodGet, "/payments/"+url.PathEscape(providerPaymentID), nil, nil, &out); err != nil {
		return ProviderPaymentState{}, err
	}
	return out.normalize(), nil
}

// FetchByIdempotencyKey recovers an order created by a call whose response
// we never saw, by looking it up on the deterministic `receipt`.
func (g *RazorpayProvider) FetchByIdempotencyKey(ctx context.Context, key string) (ProviderPaymentState, error) {
	if key == "" {
		return ProviderPaymentState{}, ErrLookupNotSupported
	}
	var out struct {
		Count int `json:"count"`
		Items []struct {
			ID       string `json:"id"`
			Amount   int64  `json:"amount"`
			Currency string `json:"currency"`
			Status   string `json:"status"`
		} `json:"items"`
	}
	if err := g.do(ctx, http.MethodGet, "/orders?receipt="+url.QueryEscape(key), nil, nil, &out); err != nil {
		return ProviderPaymentState{}, err
	}
	if len(out.Items) == 0 {
		return ProviderPaymentState{State: StateUnknown}, nil
	}
	// MRC-2.4 — more than one order under ONE deterministic receipt is an
	// ambiguity, not a list to index into.
	//
	// This used to take `Items[0]`. The receipt IS the idempotency key, so
	// two orders sharing it means either the key stopped being deterministic
	// or the provider genuinely created a duplicate — both of which are the
	// reconciliation break this lookup exists to detect. Picking the first
	// would attach whichever Razorpay happened to return first and bury the
	// duplicate.
	if len(out.Items) > 1 {
		ids := make([]string, 0, len(out.Items))
		for _, it := range out.Items {
			ids = append(ids, it.ID)
		}
		return ProviderPaymentState{}, fmt.Errorf(
			"%w: receipt %q matches %d Razorpay orders (%s)",
			ErrAmbiguousLookup, key, len(out.Items), strings.Join(ids, ", "))
	}
	it := out.Items[0]
	st := StatePending
	if it.Status == "paid" {
		st = StateCaptured
	}
	// MRC-1: the currency is reported as the provider stated it. It is NOT
	// defaulted to INR here — see the note on normalize().
	return ProviderPaymentState{
		ProviderOrderID: it.ID,
		Amount:          Money{Minor: it.Amount, Currency: it.Currency},
		State:           st,
	}, nil
}

// ─── plumbing ────────────────────────────────────────────────────────

type razorpayPayment struct {
	ID       string `json:"id"`
	OrderID  string `json:"order_id"`
	Amount   int64  `json:"amount"`
	Currency string `json:"currency"`
	Status   string `json:"status"`
}

// normalize maps a fetched Razorpay payment onto the port's shape.
//
// MRC-1: the currency is passed through EXACTLY as the provider stated it,
// including empty. It used to be run through defaultINR, which turned "the
// provider told us nothing" into "the provider said INR" — and the caller
// then compared that invented value against the intent and found it equal.
// A default is indistinguishable from a fact, which is the whole failure
// mode this pass exists to close. Callers refuse a blank currency
// (verifyProviderTuple); they must be able to see that it is blank.
//
// C3-LB-1: the webhook paths no longer default either, and `defaultINR` is
// gone from this file entirely. A signature authenticates the BYTES; it does
// not make an omitted field mean INR. An incomplete signed payload is
// incomplete, and VerifyProviderMoney refuses it rather than letting a
// manufactured "INR" satisfy the comparison meant to catch it.
func (p razorpayPayment) normalize() ProviderPaymentState {
	st := ProviderPaymentState{
		ProviderPaymentID: p.ID,
		ProviderOrderID:   p.OrderID,
		Amount:            Money{Minor: p.Amount, Currency: p.Currency},
		State:             normalizeRazorpayState(p.Status),
	}
	if st.State == StateCaptured {
		now := time.Now().UTC()
		st.CapturedAt = &now
	}
	return st
}

func normalizeRazorpayState(s string) State {
	switch s {
	case "captured":
		return StateCaptured
	case "authorized":
		return StateAuthorized
	case "failed":
		return StateFailed
	case "refunded":
		return StateRefunded
	case "created", "pending":
		return StatePending
	default:
		return StateUnknown
	}
}

func (g *RazorpayProvider) do(ctx context.Context, method, path string, body any, headers map[string]string, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.baseURL+path, rdr)
	if err != nil {
		return err
	}
	req.SetBasicAuth(g.keyID, g.keySecret)
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("razorpay: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// The body is returned so an operator can see the provider's own
		// error, but it is never logged at the call sites that carry
		// secrets — see the redaction rules in the handler.
		return fmt.Errorf("razorpay: %s %s returned %d: %s", method, path, resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("razorpay: decode %s response: %w", path, err)
		}
	}
	return nil
}

// InitiateRefundIdempotent lets the legacy PaymentGateway-shaped refund
// worker use the provider's native idempotency, bridging until every caller
// moves to the Provider port.
func (g *RazorpayGateway) InitiateRefundIdempotent(ctx context.Context, paymentID string, amountMinor int64, idempotencyKey string) (GatewayRefund, error) {
	p := NewRazorpayProvider(g.keyID, g.keySecret, "")
	r, err := p.Refund(ctx, paymentID, Money{Minor: amountMinor, Currency: "INR"}, idempotencyKey)
	if err != nil {
		return GatewayRefund{}, err
	}
	return GatewayRefund{
		ID:        r.ProviderRefundID,
		PaymentID: r.ProviderPaymentID,
		Amount:    r.Amount.Minor,
		Status:    string(r.State),
	}, nil
}

var _ Provider = (*RazorpayProvider)(nil)
var _ IdempotentRefunder = (*RazorpayGateway)(nil)
