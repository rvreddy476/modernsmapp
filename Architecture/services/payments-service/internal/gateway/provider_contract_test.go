package gateway

// Provider-port contract tests.
//
// D10 / review §5-D10: Razorpay is the only provider enabled at launch, so
// the ONLY thing standing between "we have an abstraction" and "we have a
// Razorpay wrapper with a generic name" is this file. Both adapters are
// driven through the same port with each provider's own recorded signature
// scheme, and the assertions are written against the port's contract rather
// than against either implementation.
//
// The fixtures are hand-built to each provider's documented scheme rather
// than captured from a live account, so no secret or real payment identifier
// enters the repository.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"
)

const (
	testRZPWebhookSecret = "rzp_webhook_secret_for_tests"
	testRZPKeySecret     = "rzp_key_secret_for_tests"
	testCFWebhookSecret  = "cf_webhook_secret_for_tests"
)

// ─── Razorpay: HMAC-SHA256 hex over the RAW body ─────────────────────

func razorpaySigned(t *testing.T, body string, eventID string) (http.Header, []byte) {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(testRZPWebhookSecret))
	mac.Write([]byte(body))
	h := http.Header{}
	h.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
	if eventID != "" {
		h.Set("X-Razorpay-Event-Id", eventID)
	}
	return h, []byte(body)
}

const rzpCapturedBody = `{"event":"payment.captured","created_at":1756200000,"payload":{"payment":{"entity":{"id":"pay_TEST123","order_id":"order_TEST123","amount":118000,"currency":"INR","status":"captured"}}}}`

func TestRazorpay_WebhookVerifiesAndNormalizes(t *testing.T) {
	p := NewRazorpayProvider("rzp_test_key", testRZPKeySecret, testRZPWebhookSecret)
	h, body := razorpaySigned(t, rzpCapturedBody, "evt_TEST_1")

	ev, err := p.VerifyWebhook(context.Background(), h, body)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.EventID != "evt_TEST_1" {
		t.Fatalf("event id = %q; it must come from the x-razorpay-event-id HEADER", ev.EventID)
	}
	if ev.State != StateCaptured {
		t.Fatalf("state = %q, want captured", ev.State)
	}
	if ev.Amount.Minor != 118000 || ev.Amount.Currency != "INR" {
		t.Fatalf("amount = %+v, want 118000 INR", ev.Amount)
	}
	if ev.ProviderOrderID != "order_TEST123" || ev.ProviderPaymentID != "pay_TEST123" {
		t.Fatalf("identifiers not normalized: %+v", ev)
	}
}

// R-5. The defect: the old handler read the event id from a body field that
// Razorpay does not send, so the inbox key was empty, the first event took
// that key, and every later payment was suppressed as a duplicate.
func TestRazorpay_MissingEventIDHeaderIsRejected(t *testing.T) {
	p := NewRazorpayProvider("k", testRZPKeySecret, testRZPWebhookSecret)
	h, body := razorpaySigned(t, rzpCapturedBody, "")
	if _, err := p.VerifyWebhook(context.Background(), h, body); err != ErrMissingEventID {
		t.Fatalf("got %v, want ErrMissingEventID — an empty dedupe key must never be accepted", err)
	}
}

func TestRazorpay_BodyIsNotInTheEventID(t *testing.T) {
	// Guard against a regression that reintroduces a body-derived id: the
	// body here carries a plausible `"id"` and it must NOT be used.
	body := `{"id":"body_id_that_must_be_ignored","event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_X","order_id":"order_X","amount":100,"currency":"INR","status":"captured"}}}}`
	p := NewRazorpayProvider("k", testRZPKeySecret, testRZPWebhookSecret)
	h, raw := razorpaySigned(t, body, "evt_HEADER")
	ev, err := p.VerifyWebhook(context.Background(), h, raw)
	if err != nil {
		t.Fatal(err)
	}
	if ev.EventID != "evt_HEADER" {
		t.Fatalf("event id = %q; the header must win over any body field", ev.EventID)
	}
}

func TestRazorpay_TamperedBodyRejected(t *testing.T) {
	p := NewRazorpayProvider("k", testRZPKeySecret, testRZPWebhookSecret)
	h, _ := razorpaySigned(t, rzpCapturedBody, "evt_1")
	tampered := []byte(`{"event":"payment.captured","payload":{"payment":{"entity":{"id":"pay_X","order_id":"order_X","amount":999999999,"currency":"INR","status":"captured"}}}}`)
	if _, err := p.VerifyWebhook(context.Background(), h, tampered); err != ErrSignatureInvalid {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// LB-6: fail CLOSED. An unconfigured secret must reject everything, not
// wave it through.
func TestRazorpay_EmptyWebhookSecretRejectsEverything(t *testing.T) {
	p := NewRazorpayProvider("k", testRZPKeySecret, "")
	h, body := razorpaySigned(t, rzpCapturedBody, "evt_1")
	if _, err := p.VerifyWebhook(context.Background(), h, body); err != ErrSignatureInvalid {
		t.Fatalf("got %v, want ErrSignatureInvalid with no webhook secret configured", err)
	}
}

func TestRazorpay_MissingSignatureRejected(t *testing.T) {
	p := NewRazorpayProvider("k", testRZPKeySecret, testRZPWebhookSecret)
	if _, err := p.VerifyWebhook(context.Background(), http.Header{}, []byte(rzpCapturedBody)); err != ErrSignatureInvalid {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

func TestRazorpay_RefundWebhookNormalizes(t *testing.T) {
	body := `{"event":"refund.processed","payload":{"refund":{"entity":{"id":"rfnd_T1","payment_id":"pay_T1","amount":50000,"currency":"INR","status":"processed"}},"payment":{"entity":{"id":"pay_T1","order_id":"order_T1","amount":118000,"currency":"INR","status":"captured"}}}}`
	p := NewRazorpayProvider("k", testRZPKeySecret, testRZPWebhookSecret)
	h, raw := razorpaySigned(t, body, "evt_refund_1")
	ev, err := p.VerifyWebhook(context.Background(), h, raw)
	if err != nil {
		t.Fatal(err)
	}
	if ev.ProviderRefundID != "rfnd_T1" || ev.State != StateRefunded {
		t.Fatalf("refund not normalized: %+v", ev)
	}
	if ev.Amount.Minor != 50000 {
		t.Fatalf("refund amount = %d, want 50000", ev.Amount.Minor)
	}
	if ev.ProviderOrderID != "order_T1" {
		t.Fatalf("refund must carry the originating order id, got %q", ev.ProviderOrderID)
	}
}

// A1: the client callback is checked with a DIFFERENT scheme, and the
// verdict field is deliberately named Genuine, not Verified.
func TestRazorpay_ClientCallbackIsAdvisoryAndUsesOrderPipePayment(t *testing.T) {
	p := NewRazorpayProvider("k", testRZPKeySecret, testRZPWebhookSecret)
	mac := hmac.New(sha256.New, []byte(testRZPKeySecret))
	mac.Write([]byte("order_A|pay_B"))
	v, err := p.VerifyClientCallback(context.Background(), map[string]string{
		"razorpay_order_id":   "order_A",
		"razorpay_payment_id": "pay_B",
		"razorpay_signature":  hex.EncodeToString(mac.Sum(nil)),
	})
	if err != nil || !v.Genuine {
		t.Fatalf("expected a genuine callback verdict, got %+v err=%v", v, err)
	}
	// A wrong signature must not be "genuine".
	if _, err := p.VerifyClientCallback(context.Background(), map[string]string{
		"razorpay_order_id":   "order_A",
		"razorpay_payment_id": "pay_B",
		"razorpay_signature":  "deadbeef",
	}); err != ErrSignatureInvalid {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

func TestRazorpay_CapabilitiesDeclareNoWebhookTimestamp(t *testing.T) {
	c := NewRazorpayProvider("k", "s", "w").Capabilities()
	if c.WebhookTimestamped {
		// If this ever becomes true, the inbox is no longer the only
		// replay defence and the transport layer should enforce a window.
		t.Fatal("Razorpay publishes no webhook timestamp; claiming otherwise would weaken the inbox rationale")
	}
	if c.RefundIdempotencyHeader == "" {
		t.Fatal("Razorpay supports refund idempotency; A6 depends on it")
	}
}

// ─── Cashfree: HMAC-SHA256 base64 over timestamp + RAW body ──────────

func cashfreeSigned(t *testing.T, body string, ts time.Time, eventID string) (http.Header, []byte) {
	t.Helper()
	tss := strconv.FormatInt(ts.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(testCFWebhookSecret))
	mac.Write([]byte(tss))
	mac.Write([]byte(body))
	h := http.Header{}
	h.Set("x-webhook-signature", base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	h.Set("x-webhook-timestamp", tss)
	if eventID != "" {
		h.Set("x-idempotency-header", eventID)
	}
	return h, []byte(body)
}

const cfSuccessBody = `{"type":"PAYMENT_SUCCESS_WEBHOOK","event_time":"2026-08-26T10:00:00Z","data":{"order":{"order_id":"cf_order_1","order_amount":"1180.00","order_currency":"INR"},"payment":{"cf_payment_id":"111222333","payment_status":"SUCCESS","payment_amount":"1180.00"}}}`

// The point of the whole exercise: the SAME port call, a completely
// different signature construction, and the domain sees one shape.
func TestCashfree_WebhookVerifiesTimestampPlusBody(t *testing.T) {
	p := NewCashfreeProvider("app", "secret", testCFWebhookSecret)
	h, body := cashfreeSigned(t, cfSuccessBody, time.Now(), "cf_evt_1")

	ev, err := p.VerifyWebhook(context.Background(), h, body)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.EventID != "cf_evt_1" {
		t.Fatalf("event id = %q", ev.EventID)
	}
	if ev.State != StateCaptured {
		t.Fatalf("state = %q, want captured", ev.State)
	}
	if ev.Amount.Minor != 118000 || ev.Amount.Currency != "INR" {
		t.Fatalf("amount = %+v, want 118000 INR — major/minor conversion must be exact", ev.Amount)
	}
	if ev.ProviderOrderID != "cf_order_1" {
		t.Fatalf("order id = %q", ev.ProviderOrderID)
	}
}

// A Razorpay-style signature (hex, body only, no timestamp) must NOT verify
// against Cashfree. This is the assertion that proves the adapters are not
// quietly sharing one scheme.
func TestCashfree_RejectsRazorpayStyleSignature(t *testing.T) {
	p := NewCashfreeProvider("app", "secret", testCFWebhookSecret)
	mac := hmac.New(sha256.New, []byte(testCFWebhookSecret))
	mac.Write([]byte(cfSuccessBody)) // body only, no timestamp
	h := http.Header{}
	h.Set("x-webhook-signature", hex.EncodeToString(mac.Sum(nil)))
	h.Set("x-webhook-timestamp", strconv.FormatInt(time.Now().Unix(), 10))
	if _, err := p.VerifyWebhook(context.Background(), h, []byte(cfSuccessBody)); err != ErrSignatureInvalid {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// Cashfree signs the timestamp, so a replay window is genuinely enforceable.
func TestCashfree_StaleTimestampRejected(t *testing.T) {
	p := NewCashfreeProvider("app", "secret", testCFWebhookSecret)
	h, body := cashfreeSigned(t, cfSuccessBody, time.Now().Add(-2*ReplayWindow), "cf_evt_old")
	if _, err := p.VerifyWebhook(context.Background(), h, body); err != ErrReplayWindowExpired {
		t.Fatalf("got %v, want ErrReplayWindowExpired", err)
	}
}

func TestCashfree_MissingTimestampRejected(t *testing.T) {
	p := NewCashfreeProvider("app", "secret", testCFWebhookSecret)
	h, body := cashfreeSigned(t, cfSuccessBody, time.Now(), "cf_evt_1")
	h.Del("x-webhook-timestamp")
	if _, err := p.VerifyWebhook(context.Background(), h, body); err != ErrSignatureInvalid {
		t.Fatalf("got %v, want ErrSignatureInvalid", err)
	}
}

// Even without the idempotency header, the inbox key must never be empty.
func TestCashfree_EventIDNeverEmpty(t *testing.T) {
	p := NewCashfreeProvider("app", "secret", testCFWebhookSecret)
	h, body := cashfreeSigned(t, cfSuccessBody, time.Now(), "")
	ev, err := p.VerifyWebhook(context.Background(), h, body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.EventID == "" {
		t.Fatal("a derived event id is required; an empty inbox key is the R-5 defect")
	}
}

func TestCashfree_CapabilitiesDifferFromRazorpay(t *testing.T) {
	cf := NewCashfreeProvider("a", "b", "c").Capabilities()
	rz := NewRazorpayProvider("a", "b", "c").Capabilities()
	if !cf.WebhookTimestamped {
		t.Fatal("Cashfree signs a timestamp")
	}
	if cf.ManualCapture == rz.ManualCapture && cf.WebhookTimestamped == rz.WebhookTimestamped {
		t.Fatal("if the two providers' capabilities are identical, the capability struct is not doing any work")
	}
}

func TestCashfree_CaptureReportsUnsupportedRatherThanPretending(t *testing.T) {
	p := NewCashfreeProvider("a", "b", "c")
	if _, err := p.Capture(context.Background(), "pay", Money{Minor: 1, Currency: "INR"}, "k"); err != ErrCaptureNotSupported {
		t.Fatalf("got %v, want ErrCaptureNotSupported", err)
	}
}

// ─── Money conversion, exact integer arithmetic ──────────────────────

func TestCashfreeMoneyConversionIsExact(t *testing.T) {
	for _, tc := range []struct {
		minor int64
		major string
	}{
		{118000, "1180.00"}, {1, "0.01"}, {0, "0.00"},
		{99, "0.99"}, {100, "1.00"}, {123456789, "1234567.89"},
	} {
		if got := string(majorFromMinor(tc.minor)); got != tc.major {
			t.Fatalf("majorFromMinor(%d) = %q, want %q", tc.minor, got, tc.major)
		}
		back, err := minorFromMajorString(tc.major)
		if err != nil || back != tc.minor {
			t.Fatalf("minorFromMajorString(%q) = (%d, %v), want %d", tc.major, back, err, tc.minor)
		}
	}
}

// Sub-paise precision must be refused, not rounded. Silent rounding here
// would reintroduce exactly the loss the paise migration removes.
func TestCashfreeSubPaiseRefused(t *testing.T) {
	if _, err := minorFromMajorString("10.005"); err == nil {
		t.Fatal("sub-paise amounts must be refused rather than rounded")
	}
}

// ─── Both adapters satisfy the port ──────────────────────────────────

func TestBothAdaptersSatisfyThePort(t *testing.T) {
	var providers = []Provider{
		NewRazorpayProvider("k", "s", "w"),
		NewCashfreeProvider("a", "b", "c"),
	}
	for _, p := range providers {
		if p.Name() == "" {
			t.Fatal("every provider must name itself")
		}
		// A capability set with nothing set at all means the adapter has
		// not thought about its own contract.
		c := p.Capabilities()
		if !c.ManualCapture && !c.WebhookTimestamped && c.RefundIdempotencyHeader == "" {
			t.Fatalf("%s declares no capabilities", p.Name())
		}
	}
}

// ─── B3: the refund payload contract ─────────────────────────────────
//
// The normaliser read ProviderOrderID from `payload.payment.entity.order_id`
// on the REFUND branch. A refund-only payload contains no payment entity, so
// a legitimate `refund.processed` produced an empty order id; the service
// then failed to resolve the intent AFTER it had already committed the inbox
// row, and the provider's redelivery was answered as a duplicate. Money had
// left the PSP and the local ledger never recorded it.
//
// This is the fixture the review named: a real refund payload with no payment
// entity.

const rzpRefundNoPaymentEntity = `{"event":"refund.processed","created_at":1756200500,"payload":{"refund":{"entity":{"id":"rfnd_TEST999","payment_id":"pay_TEST123","amount":50000,"currency":"INR","status":"processed"}}}}`

func TestRazorpay_RefundWithoutAPaymentEntityStillCarriesItsIdentifiers(t *testing.T) {
	p := NewRazorpayProvider("k", testRZPKeySecret, testRZPWebhookSecret)
	h, body := razorpaySigned(t, rzpRefundNoPaymentEntity, "evt_RFND_1")

	ev, err := p.VerifyWebhook(context.Background(), h, body)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.State != StateRefunded {
		t.Fatalf("state = %q, want refunded", ev.State)
	}
	if ev.ProviderRefundID != "rfnd_TEST999" {
		t.Fatalf("provider refund id = %q, want rfnd_TEST999", ev.ProviderRefundID)
	}
	// THE regression: the payment id must survive, because it is the only
	// handle the store has for resolving the intent when no order id exists.
	if ev.ProviderPaymentID != "pay_TEST123" {
		t.Fatalf("provider payment id = %q, want pay_TEST123 — without it the refund cannot be "+
			"attributed to an intent and is lost", ev.ProviderPaymentID)
	}
	if ev.Amount.Minor != 50000 {
		t.Fatalf("amount = %d, want 50000", ev.Amount.Minor)
	}
	// The order id is legitimately empty here; the store falls back to the
	// payment id. Asserting it is empty pins the contract the store relies on.
	if ev.ProviderOrderID != "" {
		t.Fatalf("provider order id = %q; a refund-only payload has none, and the store's "+
			"payment-id fallback exists because of that", ev.ProviderOrderID)
	}
}

// When Razorpay DOES echo an order id on the refund entity, prefer it.
const rzpRefundWithOrderID = `{"event":"refund.processed","created_at":1756200600,"payload":{"refund":{"entity":{"id":"rfnd_TEST111","payment_id":"pay_TEST222","order_id":"order_FROM_REFUND","amount":25000,"currency":"INR","status":"processed"}}}}`

func TestRazorpay_RefundPrefersItsOwnOrderID(t *testing.T) {
	p := NewRazorpayProvider("k", testRZPKeySecret, testRZPWebhookSecret)
	h, body := razorpaySigned(t, rzpRefundWithOrderID, "evt_RFND_2")

	ev, err := p.VerifyWebhook(context.Background(), h, body)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ev.ProviderOrderID != "order_FROM_REFUND" {
		t.Fatalf("provider order id = %q, want order_FROM_REFUND", ev.ProviderOrderID)
	}
}

// Negative control (review §4): the previous normaliser, applied to the same
// legitimate fixture, yields an empty order id — the input to the loss
// sequence. If this stops reproducing, the assertions above prove nothing.
func TestNegativeControl_OldRefundNormalizerLosesTheOrderID(t *testing.T) {
	var env struct {
		Payload struct {
			Payment struct {
				Entity struct {
					OrderID string `json:"order_id"`
				} `json:"entity"`
			} `json:"payment"`
			Refund struct {
				Entity struct {
					ID string `json:"id"`
				} `json:"entity"`
			} `json:"refund"`
		} `json:"payload"`
	}
	if err := json.Unmarshal([]byte(rzpRefundNoPaymentEntity), &env); err != nil {
		t.Fatal(err)
	}
	if env.Payload.Refund.Entity.ID == "" {
		t.Fatal("fixture is not a refund payload")
	}
	// This is verbatim what the refund branch used to assign.
	oldProviderOrderID := env.Payload.Payment.Entity.OrderID
	if oldProviderOrderID != "" {
		t.Fatalf("negative control did not reproduce the defect: the old normaliser resolved an "+
			"order id (%q) from a payload with no payment entity", oldProviderOrderID)
	}
	t.Log("negative control reproduced the original defect: the previous refund normaliser " +
		"yielded an EMPTY provider order id for a legitimate refund.processed payload, which " +
		"failed intent lookup after the inbox row had already committed")
}
