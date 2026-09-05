package gateway

// Cashfree adapter — built, tested, and DARK.
//
// D10 and the review's §5-D10 ruling: Razorpay is the only provider enabled
// at launch, and shipping a second live PSP would double the reconciliation
// surface on day one for no launch benefit. But a port with one
// implementation is not an abstraction, it is a wrapper with aspirations, so
// this adapter exists to prove the seam.
//
// It earns its keep immediately, because Cashfree's webhook contract is
// materially different from Razorpay's and the ORIGINAL interface could not
// have expressed it at all:
//
//	Razorpay   HMAC-SHA256(rawBody),              hex,    X-Razorpay-Signature
//	           event id in x-razorpay-event-id,   no timestamp header
//	Cashfree   HMAC-SHA256(timestamp + rawBody),  base64, x-webhook-signature
//	           timestamp in x-webhook-timestamp,  x-idempotency-header
//
// The old `VerifySignature(orderID, paymentID, signature string) bool`
// literally has nowhere to put a raw body or a timestamp. Writing this file
// is what demonstrated the port change was necessary rather than tidy.
//
// Because Cashfree DOES sign a timestamp, this adapter can enforce a replay
// window at the transport layer — something the Razorpay adapter cannot do,
// which is exactly why the inbox dedupe has to be the load-bearing control
// for Razorpay rather than a nicety.

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const cashfreeProdBaseURL = "https://api.cashfree.com/pg"

// CashfreeProvider implements Provider. Not wired into main; constructed
// only by the contract tests until a founder decision enables it.
type CashfreeProvider struct {
	appID         string
	secretKey     string
	webhookSecret string
	baseURL       string
	apiVersion    string
	client        *http.Client
	now           func() time.Time
}

func NewCashfreeProvider(appID, secretKey, webhookSecret string) *CashfreeProvider {
	return &CashfreeProvider{
		appID:         appID,
		secretKey:     secretKey,
		webhookSecret: webhookSecret,
		baseURL:       cashfreeProdBaseURL,
		apiVersion:    "2023-08-01",
		client:        &http.Client{Timeout: 30 * time.Second},
		now:           time.Now,
	}
}

// ClientSession is NOT derivable for Cashfree.
//
// Its SDK opens on a `payment_session_id` that the provider mints when the
// order is created and which cannot be recomputed from the order id. Making
// Cashfree launchable from the app therefore requires persisting that value
// at CreateOrder time — a schema change and a migration.
//
// Returning nil rather than a half-populated map is deliberate: the caller
// treats nil as "this provider cannot be launched from the client" and says
// so, instead of handing the app a session that will fail to open. Cashfree
// is a compile-tested adapter seam and is not an enabled runtime path for
// launch (Decision 3), so this is a stated limitation, not a gap.
func (g *CashfreeProvider) ClientSession(_ string) map[string]string { return nil }

func (g *CashfreeProvider) Name() string { return "cashfree" }

func (g *CashfreeProvider) Capabilities() Capabilities {
	return Capabilities{
		// Cashfree captures on authorisation for standard collections.
		ManualCapture: false,
		// The signature covers a timestamp, so a captured body cannot be
		// replayed indefinitely.
		WebhookTimestamped:      true,
		RefundIdempotencyHeader: "x-idempotency-key",
	}
}

func (g *CashfreeProvider) CreateOrder(ctx context.Context, amount Money, idempotencyKey string, meta map[string]string) (ProviderOrder, error) {
	if idempotencyKey == "" {
		return ProviderOrder{}, fmt.Errorf("cashfree: idempotency key is required")
	}
	// Cashfree amounts are MAJOR units with two decimals on the wire. This
	// is the single place the conversion happens, and it is exact integer
	// arithmetic rendered as text — never a float.
	body := map[string]any{
		"order_id":       idempotencyKey,
		"order_amount":   majorFromMinor(amount.Minor),
		"order_currency": amount.Currency,
		"customer_details": map[string]string{
			"customer_id":    meta["customer_id"],
			"customer_phone": meta["customer_phone"],
		},
	}
	var out struct {
		CFOrderID        json.RawMessage `json:"cf_order_id"`
		OrderID          string          `json:"order_id"`
		OrderAmount      json.Number     `json:"order_amount"`
		OrderCurrency    string          `json:"order_currency"`
		PaymentSessionID string          `json:"payment_session_id"`
	}
	if err := g.do(ctx, http.MethodPost, "/orders", body, nil, &out); err != nil {
		return ProviderOrder{}, err
	}
	minor, err := minorFromMajorString(out.OrderAmount.String())
	if err != nil {
		return ProviderOrder{}, err
	}
	return ProviderOrder{
		ProviderOrderID: out.OrderID,
		Amount:          Money{Minor: minor, Currency: out.OrderCurrency},
		ClientSession: map[string]string{
			"provider":           "cashfree",
			"payment_session_id": out.PaymentSessionID,
			"order_id":           out.OrderID,
		},
	}, nil
}

// VerifyWebhook implements Cashfree's timestamp+rawBody scheme.
func (g *CashfreeProvider) VerifyWebhook(ctx context.Context, headers http.Header, rawBody []byte) (WebhookEvent, error) {
	if g.webhookSecret == "" {
		return WebhookEvent{}, ErrSignatureInvalid
	}
	sig := headers.Get("x-webhook-signature")
	ts := headers.Get("x-webhook-timestamp")
	if sig == "" || ts == "" {
		return WebhookEvent{}, ErrSignatureInvalid
	}

	mac := hmac.New(sha256.New, []byte(g.secretKeyOrWebhook()))
	mac.Write([]byte(ts))
	mac.Write(rawBody)
	expected := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(sig)) {
		return WebhookEvent{}, ErrSignatureInvalid
	}

	// The timestamp is inside the signature, so this window is genuinely
	// enforceable — an attacker cannot move it without breaking the MAC.
	if epoch, err := strconv.ParseInt(ts, 10, 64); err == nil {
		age := g.now().Sub(time.Unix(epoch, 0))
		if age > ReplayWindow || age < -ReplayWindow {
			return WebhookEvent{}, ErrReplayWindowExpired
		}
	}

	eventID := headers.Get("x-idempotency-header")

	var env struct {
		Type string `json:"type"`
		Data struct {
			Order struct {
				OrderID       string      `json:"order_id"`
				OrderAmount   json.Number `json:"order_amount"`
				OrderCurrency string      `json:"order_currency"`
			} `json:"order"`
			Payment struct {
				CFPaymentID   json.RawMessage `json:"cf_payment_id"`
				PaymentStatus string          `json:"payment_status"`
				PaymentAmount json.Number     `json:"payment_amount"`
			} `json:"payment"`
			Refund struct {
				CFRefundID   json.RawMessage `json:"cf_refund_id"`
				RefundID     string          `json:"refund_id"`
				RefundAmount json.Number     `json:"refund_amount"`
				RefundStatus string          `json:"refund_status"`
			} `json:"refund"`
		} `json:"data"`
		EventTime string `json:"event_time"`
	}
	if err := json.Unmarshal(rawBody, &env); err != nil {
		return WebhookEvent{}, fmt.Errorf("cashfree: decode webhook: %w", err)
	}

	if eventID == "" {
		// Fall back to a deterministic key derived from the signed
		// material. Still never empty — an empty inbox key is the R-5
		// defect and must not be reachable from any provider.
		sum := sha256.Sum256(append([]byte(ts), rawBody...))
		eventID = "cf-" + base64.RawURLEncoding.EncodeToString(sum[:16])
	}

	ev := WebhookEvent{
		EventID:         eventID,
		Type:            env.Type,
		ProviderOrderID: env.Data.Order.OrderID,
	}
	if t, err := time.Parse(time.RFC3339, env.EventTime); err == nil {
		ev.OccurredAt = t.UTC()
	}
	cur := env.Data.Order.OrderCurrency

	switch {
	case env.Data.Refund.RefundID != "" || len(env.Data.Refund.CFRefundID) > 0:
		ev.ProviderRefundID = firstNonEmpty(env.Data.Refund.RefundID, rawString(env.Data.Refund.CFRefundID))
		if m, err := minorFromMajorString(env.Data.Refund.RefundAmount.String()); err == nil {
			ev.Amount = Money{Minor: m, Currency: cur}
		}
		ev.State = StateRefunded
	default:
		ev.ProviderPaymentID = rawString(env.Data.Payment.CFPaymentID)
		if m, err := minorFromMajorString(env.Data.Payment.PaymentAmount.String()); err == nil {
			ev.Amount = Money{Minor: m, Currency: cur}
		}
		ev.State = normalizeCashfreeState(env.Data.Payment.PaymentStatus)
	}
	return ev, nil
}

func (g *CashfreeProvider) VerifyClientCallback(ctx context.Context, payload map[string]string) (CallbackVerdict, error) {
	// Cashfree's return URL carries no signature; the documented pattern is
	// to fetch the order server-side. Reporting "not genuine" here rather
	// than inventing a check keeps the ADVISORY contract honest — a caller
	// that cannot verify must not claim it did.
	return CallbackVerdict{Genuine: false, ProviderOrderID: payload["order_id"]}, nil
}

func (g *CashfreeProvider) Capture(ctx context.Context, providerPaymentID string, amount Money, idempotencyKey string) (ProviderPaymentState, error) {
	return ProviderPaymentState{}, ErrCaptureNotSupported
}

func (g *CashfreeProvider) Refund(ctx context.Context, providerOrderID string, amount Money, idempotencyKey string) (ProviderRefund, error) {
	if idempotencyKey == "" {
		return ProviderRefund{}, fmt.Errorf("cashfree: refund idempotency key is required")
	}
	body := map[string]any{
		"refund_amount": majorFromMinor(amount.Minor),
		"refund_id":     idempotencyKey,
	}
	var out struct {
		RefundID     string      `json:"refund_id"`
		RefundAmount json.Number `json:"refund_amount"`
		RefundStatus string      `json:"refund_status"`
	}
	err := g.do(ctx, http.MethodPost, "/orders/"+url.PathEscape(providerOrderID)+"/refunds", body,
		map[string]string{"x-idempotency-key": idempotencyKey}, &out)
	if err != nil {
		return ProviderRefund{}, err
	}
	minor, _ := minorFromMajorString(out.RefundAmount.String())
	return ProviderRefund{
		ProviderRefundID: out.RefundID,
		Amount:           Money{Minor: minor, Currency: amount.Currency},
		State:            StateRefunded,
	}, nil
}

func (g *CashfreeProvider) FetchPayment(ctx context.Context, providerOrderID string) (ProviderPaymentState, error) {
	var out struct {
		OrderID       string      `json:"order_id"`
		OrderAmount   json.Number `json:"order_amount"`
		OrderCurrency string      `json:"order_currency"`
		OrderStatus   string      `json:"order_status"`
	}
	if err := g.do(ctx, http.MethodGet, "/orders/"+url.PathEscape(providerOrderID), nil, nil, &out); err != nil {
		return ProviderPaymentState{}, err
	}
	minor, _ := minorFromMajorString(out.OrderAmount.String())
	return ProviderPaymentState{
		ProviderOrderID: out.OrderID,
		Amount:          Money{Minor: minor, Currency: out.OrderCurrency},
		State:           normalizeCashfreeState(out.OrderStatus),
	}, nil
}

func (g *CashfreeProvider) FetchByIdempotencyKey(ctx context.Context, key string) (ProviderPaymentState, error) {
	// Cashfree's order_id IS our idempotency key, so the lookup is the
	// ordinary fetch.
	return g.FetchPayment(ctx, key)
}

// ─── plumbing ────────────────────────────────────────────────────────

func (g *CashfreeProvider) secretKeyOrWebhook() string {
	if g.webhookSecret != "" {
		return g.webhookSecret
	}
	return g.secretKey
}

func normalizeCashfreeState(s string) State {
	switch s {
	case "SUCCESS", "PAID":
		return StateCaptured
	case "FAILED", "USER_DROPPED":
		return StateFailed
	case "PENDING", "ACTIVE", "NOT_ATTEMPTED":
		return StatePending
	case "REFUNDED", "SUCCESS_REFUND":
		return StateRefunded
	default:
		return StateUnknown
	}
}

// majorFromMinor renders paise as a two-decimal major string without ever
// touching a float. 118000 -> "1180.00".
func majorFromMinor(minor int64) json.Number {
	neg := minor < 0
	if neg {
		minor = -minor
	}
	s := fmt.Sprintf("%d.%02d", minor/100, minor%100)
	if neg {
		s = "-" + s
	}
	return json.Number(s)
}

// minorFromMajorString parses "1180.00" into 118000 with integer arithmetic.
// Parsing through float64 would reintroduce exactly the precision loss the
// paise migration exists to remove.
func minorFromMajorString(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}
	neg := false
	if s[0] == '-' {
		neg, s = true, s[1:]
	} else if s[0] == '+' {
		s = s[1:]
	}
	whole, frac := s, ""
	if i := bytes.IndexByte([]byte(s), '.'); i >= 0 {
		whole, frac = s[:i], s[i+1:]
	}
	for len(frac) < 2 {
		frac += "0"
	}
	if len(frac) > 2 {
		// More precision than paise. Refuse rather than round silently:
		// a provider sending sub-paise means our assumptions are wrong.
		return 0, fmt.Errorf("cashfree: amount %q has sub-paise precision", s)
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cashfree: parse amount %q: %w", s, err)
	}
	f, err := strconv.ParseInt(frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("cashfree: parse amount %q: %w", s, err)
	}
	v := w*100 + f
	if neg {
		v = -v
	}
	return v, nil
}

func rawString(b json.RawMessage) string {
	s := string(b)
	s = trimQuotes(s)
	if s == "null" {
		return ""
	}
	return s
}

func trimQuotes(s string) string {
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return s[1 : len(s)-1]
	}
	return s
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func (g *CashfreeProvider) do(ctx context.Context, method, path string, body any, headers map[string]string, out any) error {
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
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-version", g.apiVersion)
	req.Header.Set("x-client-id", g.appID)
	req.Header.Set("x-client-secret", g.secretKey)
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return fmt.Errorf("cashfree: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("cashfree: %s %s returned %d: %s", method, path, resp.StatusCode, string(raw))
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("cashfree: decode %s response: %w", path, err)
		}
	}
	return nil
}

var _ Provider = (*CashfreeProvider)(nil)
