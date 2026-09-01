package gateway

// MRC-1.6 / MRC-2.4 — the Razorpay adapter's own decoding contract.
//
// These drive the REAL adapter against recorded Razorpay response bytes over
// an httptest.Server. No struct is hand-populated, because that is exactly
// how the previous pass's reconciliation proof went green while production
// was wrong: the fake supplied a `Currency` the production adapter never
// decoded.
//
// The two properties that matter here:
//
//	1. currency is decoded from the bytes, and a payload that omits it yields
//	   an EMPTY currency rather than a defaulted "INR" — callers must be able
//	   to tell "the provider said nothing" from "the provider said INR";
//	2. one deterministic receipt matching more than one order is an
//	   ambiguity error, not Items[0].

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func stubbed(t *testing.T, handler http.HandlerFunc) *RazorpayProvider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return NewRazorpayProvider("rzp_test", "secret", "whsec").
		WithEndpoint(srv.URL, srv.Client())
}

func jsonHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}

// ─── FetchPayment decodes the money tuple ────────────────────────────

func TestFetchPaymentDecodesCurrencyFromTheResponseBytes(t *testing.T) {
	p := stubbed(t, jsonHandler(
		`{"id":"pay_ABC","order_id":"order_ABC","amount":118000,"currency":"INR","status":"captured"}`))

	st, err := p.FetchPayment(context.Background(), "pay_ABC")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if st.Amount.Currency != "INR" {
		t.Fatalf("currency = %q, want INR — the adapter must decode it, not drop it", st.Amount.Currency)
	}
	if st.Amount.Minor != 118000 {
		t.Fatalf("amount = %d, want 118000", st.Amount.Minor)
	}
	if st.ProviderPaymentID != "pay_ABC" || st.ProviderOrderID != "order_ABC" {
		t.Fatalf("identifiers not decoded: %+v", st)
	}
	if st.State != StateCaptured {
		t.Fatalf("state = %q, want captured", st.State)
	}
}

// A non-INR capture must surface AS non-INR. If this ever returns INR the
// reconciler's currency comparison is comparing against an invention.
func TestFetchPaymentPreservesANonINRCurrency(t *testing.T) {
	p := stubbed(t, jsonHandler(
		`{"id":"pay_USD","order_id":"order_USD","amount":118000,"currency":"USD","status":"captured"}`))

	st, err := p.FetchPayment(context.Background(), "pay_USD")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if st.Amount.Currency != "USD" {
		t.Fatalf("currency = %q, want USD", st.Amount.Currency)
	}
}

// THE MRC-1 property: an omitted currency stays empty. It used to be run
// through defaultINR, which turned an absent field into a confident "INR" and
// made the downstream comparison pass on a value nobody had asserted.
func TestFetchPaymentDoesNotInventACurrency(t *testing.T) {
	p := stubbed(t, jsonHandler(
		`{"id":"pay_NONE","order_id":"order_NONE","amount":118000,"status":"captured"}`))

	st, err := p.FetchPayment(context.Background(), "pay_NONE")
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if st.Amount.Currency != "" {
		t.Fatalf("currency = %q, want empty — a missing field must not be defaulted to INR, "+
			"because the caller refuses blank and cannot refuse an invention", st.Amount.Currency)
	}
}

// ─── FetchByIdempotencyKey ───────────────────────────────────────────

func TestFetchByIdempotencyKeyReturnsTheSingleMatch(t *testing.T) {
	p := stubbed(t, jsonHandler(
		`{"count":1,"items":[{"id":"order_ONE","amount":118000,"currency":"INR","status":"created"}]}`))

	st, err := p.FetchByIdempotencyKey(context.Background(), "idem-1")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if st.ProviderOrderID != "order_ONE" {
		t.Fatalf("order id = %q, want order_ONE", st.ProviderOrderID)
	}
	if st.Amount.Minor != 118000 || st.Amount.Currency != "INR" {
		t.Fatalf("amount = %+v, want 118000 INR", st.Amount)
	}
}

func TestFetchByIdempotencyKeyReportsNoMatch(t *testing.T) {
	p := stubbed(t, jsonHandler(`{"count":0,"items":[]}`))

	st, err := p.FetchByIdempotencyKey(context.Background(), "idem-none")
	if err != nil {
		t.Fatalf("a zero-result lookup is not an error: %v", err)
	}
	if st.ProviderOrderID != "" {
		t.Fatalf("order id = %q, want empty", st.ProviderOrderID)
	}
	if st.State != StateUnknown {
		t.Fatalf("state = %q, want unknown", st.State)
	}
}

// MRC-2.4: two orders under one deterministic receipt. Items[0] would have
// attached whichever Razorpay listed first and buried the duplicate.
func TestFetchByIdempotencyKeyRefusesMultipleMatches(t *testing.T) {
	p := stubbed(t, jsonHandler(`{"count":2,"items":[
		{"id":"order_A","amount":118000,"currency":"INR","status":"created"},
		{"id":"order_B","amount":118000,"currency":"INR","status":"created"}]}`))

	st, err := p.FetchByIdempotencyKey(context.Background(), "idem-dup")
	if !errors.Is(err, ErrAmbiguousLookup) {
		t.Fatalf("got %v, want ErrAmbiguousLookup", err)
	}
	if st.ProviderOrderID != "" {
		t.Fatalf("order id = %q; an ambiguous lookup must name nothing", st.ProviderOrderID)
	}
	// The message must name both, so an operator can reconcile by hand.
	for _, want := range []string{"order_A", "order_B"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
}

func TestFetchByIdempotencyKeyRefusesAnEmptyKey(t *testing.T) {
	p := stubbed(t, jsonHandler(`{"count":0,"items":[]}`))
	if _, err := p.FetchByIdempotencyKey(context.Background(), ""); !errors.Is(err, ErrLookupNotSupported) {
		t.Fatalf("got %v, want ErrLookupNotSupported for an empty key", err)
	}
}

func TestFetchByIdempotencyKeyDoesNotInventACurrency(t *testing.T) {
	p := stubbed(t, jsonHandler(
		`{"count":1,"items":[{"id":"order_NC","amount":118000,"status":"created"}]}`))

	st, err := p.FetchByIdempotencyKey(context.Background(), "idem-nc")
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if st.Amount.Currency != "" {
		t.Fatalf("currency = %q, want empty", st.Amount.Currency)
	}
}

// Cashfree must keep satisfying the same port (MRC-1.7). This is a
// compile-time assertion; it costs nothing and catches a drifted signature.
var (
	_ Provider = (*RazorpayProvider)(nil)
	_ Provider = (*CashfreeProvider)(nil)
)

// ─── ClientSession (the app's checkout handoff) ──────────────────────
//
// The publishable key must reach the client from the SERVER that created the
// order. An app-compiled key can disagree with the server's environment — a
// test-key build cannot open a sheet for a live-key order — and sourcing it
// here makes that disagreement impossible rather than merely unlikely.

func TestClientSessionCarriesKeyAndOrder(t *testing.T) {
	p := NewRazorpayProvider("rzp_test_abc", "secret", "whsec")

	session := p.ClientSession("order_XYZ")
	if session["key_id"] != "rzp_test_abc" {
		t.Fatalf("key_id = %q, want rzp_test_abc", session["key_id"])
	}
	if session["order_id"] != "order_XYZ" {
		t.Fatalf("order_id = %q, want order_XYZ", session["order_id"])
	}
	if session["provider"] != "razorpay" {
		t.Fatalf("provider = %q, want razorpay", session["provider"])
	}
}

// No amount. LB-4: the client never names what it is paying — that was the
// exact shape of the zero-rupee exploit — and the provider order already
// fixes the amount.
func TestClientSessionCarriesNoAmount(t *testing.T) {
	session := NewRazorpayProvider("k", "s", "w").ClientSession("order_1")
	for _, forbidden := range []string{"amount", "amount_minor", "currency"} {
		if _, present := session[forbidden]; present {
			t.Fatalf("client session carries %q; the client must not be handed an amount", forbidden)
		}
	}
}

// No session without an order, and none without a key. Returning a
// half-populated map would hand the app something that fails to open.
func TestClientSessionRefusesIncompleteInput(t *testing.T) {
	if s := NewRazorpayProvider("k", "s", "w").ClientSession(""); s != nil {
		t.Fatalf("session for an empty order id = %v, want nil", s)
	}
	if s := NewRazorpayProvider("", "s", "w").ClientSession("order_1"); s != nil {
		t.Fatalf("session with no key id = %v, want nil", s)
	}
}

// Cashfree cannot derive one: its SDK opens on a payment_session_id minted at
// order creation, which is not recomputable from the order id. nil is the
// honest answer, and the caller reports "cannot open" rather than opening a
// sheet that will fail.
func TestCashfreeClientSessionIsNilByDesign(t *testing.T) {
	if s := NewCashfreeProvider("app", "secret", "whsec").ClientSession("order_1"); s != nil {
		t.Fatalf("cashfree session = %v, want nil until payment_session_id is persisted", s)
	}
}
