//go:build integration

package service

// C3-LB-1 — exact provider money truth on the two paths review 3 found open.
//
// The reconciliation and blank-reference paths already had proofs
// (reconcile_n2_integration_test.go). These are the two that did not:
//
//	A-LB-1  IMMEDIATE ambiguous-create recovery accepted the first non-blank
//	        recovered order id without comparing amount or currency;
//	A-LB-2  webhook normalization ran currency through defaultINR, so an
//	        authentic-but-incomplete payload arrived carrying a currency the
//	        provider never sent — and the downstream comparison, which exists
//	        precisely to catch that, then found it equal.
//
// Both are driven through the REAL RazorpayProvider against recorded Razorpay
// bytes and a live PostgreSQL. Nothing here hand-builds a normalized provider
// struct, because a fake that supplies the field production drops is how the
// previous pass's currency check came to be reported closed while being a
// no-op.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -v

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/atpost/payments-service/internal/gateway"
	"github.com/google/uuid"
)

// ─── A Razorpay that distinguishes create from lookup ────────────────

// createStub separates POST /orders (open an order) from GET /orders?receipt=
// (recover one). The reconciler harness's stub answers both on one path,
// which cannot express "the create failed and the lookup succeeded" — the
// exact shape of an ambiguous timeout.
type createStub struct {
	srv *httptest.Server

	mu          sync.Mutex
	createCode  int
	createBody  string
	lookupBody  string
	createCalls int
	lookupCalls int
}

func newCreateStub(t *testing.T) *createStub {
	t.Helper()
	s := &createStub{createCode: http.StatusGatewayTimeout, createBody: `{"error":"timeout"}`}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/orders":
			s.createCalls++
			w.WriteHeader(s.createCode)
			_, _ = w.Write([]byte(s.createBody))
		case r.Method == http.MethodGet && r.URL.Path == "/orders":
			s.lookupCalls++
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(s.lookupBody))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *createStub) setLookup(body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookupBody = body
}

func (s *createStub) calls() (create, lookup int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createCalls, s.lookupCalls
}

func (s *createStub) provider() *gateway.RazorpayProvider {
	return gateway.NewRazorpayProvider("rzp_test", "secret", c3WebhookSecret).
		WithEndpoint(s.srv.URL, s.srv.Client())
}

const c3WebhookSecret = "whsec_c3"

// initiate drives the REAL InitiatePayment path — the one a checkout takes.
func initiate(t *testing.T, s *Service, amountMinor int64, currency string) (string, error) {
	t.Helper()
	key := "c3-" + uuid.NewString()
	_, err := s.InitiatePayment(context.Background(), InitiateInput{
		PayerID:        uuid.New(),
		PayeeID:        uuid.New(),
		ReferenceType:  "order",
		ReferenceID:    uuid.New(),
		AmountMinor:    amountMinor,
		Currency:       currency,
		Method:         "upi",
		IdempotencyKey: key,
		OwnerDomain:    "commerce",
	})
	return key, err
}

// refByKey reads the stored provider reference for an idempotency key.
//
// The intent row exists whether or not a reference was attached — that is the
// point of creating it before the PSP is contacted (B6) — so this
// distinguishes "no reference" from "no intent".
func refByKey(t *testing.T, key string) string {
	t.Helper()
	var ref string
	err := recPool.QueryRow(context.Background(),
		`SELECT COALESCE(provider_order_id, COALESCE(provider_ref,''))
		   FROM payments.payment_intents WHERE idempotency_key = $1`, key).Scan(&ref)
	if err != nil {
		t.Fatalf("reading provider ref for %s: %v", key, err)
	}
	return ref
}

// ─── A-LB-1: immediate ambiguous-create recovery ─────────────────────

// The positive case. A recovered order that verifies IS adopted — otherwise
// the refusals below would be indistinguishable from a path that simply never
// recovers anything.
func TestC3ImmediateRecoveryAdoptsAnExactlyMatchingOrder(t *testing.T) {
	stub := newCreateStub(t)
	orderID := "order_exact_" + uuid.NewString()[:8]
	stub.setLookup(orderList(rzpOrder(orderID, 118000, "INR", "created")))

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err != nil {
		t.Fatalf("initiate: %v", err)
	}
	if got := refByKey(t, key); got != orderID {
		t.Fatalf("provider ref = %q, want %q — a verifying recovery must be adopted", got, orderID)
	}
	if create, lookup := stub.calls(); create != 1 || lookup != 1 {
		t.Fatalf("calls: create=%d lookup=%d, want 1/1", create, lookup)
	}
}

// THE defect. A recovered order for a different amount must not be attached.
//
// This is the one the local unique index cannot catch: the wrong reference is
// still internally unique, so nothing downstream can tell that this intent now
// points at money that was never ours.
func TestC3ImmediateRecoveryRefusesAnOrderWithTheWrongAmount(t *testing.T) {
	stub := newCreateStub(t)
	stub.setLookup(orderList(rzpOrder("order_else_"+uuid.NewString()[:8], 999900, "INR", "created")))

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("initiate succeeded; a recovered order for a different amount must be refused")
	}
	if !errors.Is(err, gateway.ErrProviderMoneyUnverified) {
		t.Fatalf("error = %v, want ErrProviderMoneyUnverified", err)
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty — the WRONG provider order was attached, "+
			"so this intent now points at somebody else's money", got)
	}
}

func TestC3ImmediateRecoveryRefusesAnOrderInAnotherCurrency(t *testing.T) {
	stub := newCreateStub(t)
	// The same NUMBER in a different denomination: 118000 minor is ₹1,180
	// or $1,180, and only the currency field separates them.
	stub.setLookup(orderList(rzpOrder("order_usd_"+uuid.NewString()[:8], 118000, "USD", "created")))

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("initiate succeeded; a recovered order in another currency must be refused")
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty", got)
	}
}

// An order the provider returned with no currency at all. The adapter must
// report it blank and the policy must refuse it; a default would make this
// indistinguishable from a matching INR order.
func TestC3ImmediateRecoveryRefusesAnOrderWithNoCurrency(t *testing.T) {
	stub := newCreateStub(t)
	stub.setLookup(orderList(map[string]any{
		"id": "order_nocur_" + uuid.NewString()[:8], "amount": 118000, "status": "created",
	}))

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("initiate succeeded; an order with no currency is not a verified fact")
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty", got)
	}
}

// NC-1C's target: one deterministic receipt matching two provider orders.
func TestC3ImmediateRecoveryRefusesAnAmbiguousLookup(t *testing.T) {
	stub := newCreateStub(t)
	stub.setLookup(orderList(
		rzpOrder("order_first_"+uuid.NewString()[:8], 118000, "INR", "created"),
		rzpOrder("order_second_"+uuid.NewString()[:8], 118000, "INR", "created"),
	))

	key, err := initiate(t, svcWith(t, stub.provider()), 118000, "INR")
	if err == nil {
		t.Fatal("initiate succeeded; one key matching two provider orders has no correct answer")
	}
	if !errors.Is(err, gateway.ErrAmbiguousLookup) {
		t.Fatalf("error = %v, want ErrAmbiguousLookup", err)
	}
	if got := refByKey(t, key); got != "" {
		t.Fatalf("provider ref = %q, want empty — an arbitrary one of the two was attached", got)
	}
}

// ─── A-LB-2: a signed webhook with a missing money field ─────────────

// signedWebhook renders a Razorpay payment webhook and signs it with the
// adapter's own secret, so the signature is genuinely valid.
//
// That is the entire point of these cases: authenticity and completeness are
// different properties, and only one of them was being checked.
func signedWebhook(t *testing.T, eventID, event string, payment map[string]any) (http.Header, []byte) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"event":   event,
		"payload": map[string]any{"payment": map[string]any{"entity": payment}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mac := hmac.New(sha256.New, []byte(c3WebhookSecret))
	mac.Write(body)

	h := http.Header{}
	h.Set("X-Razorpay-Event-Id", eventID)
	h.Set("X-Razorpay-Signature", hex.EncodeToString(mac.Sum(nil)))
	return h, body
}

// applySigned runs the whole real path: verify the signature with the real
// adapter, then apply the normalized event through the real service.
func applySigned(t *testing.T, svc *Service, hdr http.Header, body []byte) (gateway.WebhookEvent, error) {
	t.Helper()
	prov := newCreateStub(t).provider()
	ev, err := prov.VerifyWebhook(context.Background(), hdr, body)
	if err != nil {
		t.Fatalf("the signature must be valid for this case to mean anything: %v", err)
	}
	return ev, svc.ApplyWebhook(context.Background(), WebhookInput{
		Provider:          "razorpay",
		EventID:           ev.EventID,
		EventType:         ev.Type,
		ProviderOrderID:   ev.ProviderOrderID,
		ProviderPaymentID: ev.ProviderPaymentID,
		AmountMinor:       ev.Amount.Minor,
		Currency:          ev.Amount.Currency,
	})
}

// The positive control: a complete signed capture still settles. Without it
// the refusals below could all pass against a webhook path that is simply
// broken.
func TestC3SignedWebhookWithACompleteTupleSettles(t *testing.T) {
	si := seedStale(t, 118000, "INR", "order_wh_ok_"+uuid.NewString()[:8])
	hdr, body := signedWebhook(t, "evt_ok_"+uuid.NewString(), "payment.captured", map[string]any{
		"id": "pay_ok", "order_id": si.providerOrder, "amount": 118000,
		"currency": "INR", "status": "captured",
	})
	if _, err := applySigned(t, svcWith(t, newCreateStub(t).provider()), hdr, body); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("intent status = %q, want succeeded", s)
	}
}

// THE defect. An authentic payload that omits `currency` used to arrive here
// carrying "INR" — manufactured by defaultINR — and the guard that exists to
// compare currencies then compared INR against INR and let it through.
//
// A signature proves the bytes came from Razorpay and were not altered. It
// says nothing whatsoever about a field Razorpay never sent.
func TestC3SignedWebhookWithNoCurrencyCannotSettle(t *testing.T) {
	si := seedStale(t, 118000, "INR", "order_wh_nocur_"+uuid.NewString()[:8])
	hdr, body := signedWebhook(t, "evt_nocur_"+uuid.NewString(), "payment.captured", map[string]any{
		"id": "pay_nocur", "order_id": si.providerOrder, "amount": 118000,
		"status": "captured", // no "currency" key at all
	})

	ev, err := applySigned(t, svcWith(t, newCreateStub(t).provider()), hdr, body)

	// The adapter must report the field as the provider left it.
	if ev.Amount.Currency != "" {
		t.Fatalf("the adapter reported currency %q for a payload that carried none — "+
			"a default is indistinguishable from a fact", ev.Amount.Currency)
	}
	if err == nil {
		t.Fatal("a signed capture with no currency settled the intent")
	}
	requireNoTerminalEffect(t, si, "signed webhook with no currency")
}

func TestC3SignedWebhookInAnotherCurrencyCannotSettle(t *testing.T) {
	si := seedStale(t, 118000, "INR", "order_wh_usd_"+uuid.NewString()[:8])
	hdr, body := signedWebhook(t, "evt_usd_"+uuid.NewString(), "payment.captured", map[string]any{
		"id": "pay_usd", "order_id": si.providerOrder, "amount": 118000,
		"currency": "USD", "status": "captured",
	})
	if _, err := applySigned(t, svcWith(t, newCreateStub(t).provider()), hdr, body); err == nil {
		t.Fatal("a capture denominated in USD settled an INR intent")
	}
	requireNoTerminalEffect(t, si, "signed webhook in another currency")
}

func TestC3SignedWebhookWithNoAmountCannotSettle(t *testing.T) {
	si := seedStale(t, 118000, "INR", "order_wh_noamt_"+uuid.NewString()[:8])
	hdr, body := signedWebhook(t, "evt_noamt_"+uuid.NewString(), "payment.captured", map[string]any{
		"id": "pay_noamt", "order_id": si.providerOrder,
		"currency": "INR", "status": "captured", // no amount
	})
	if _, err := applySigned(t, svcWith(t, newCreateStub(t).provider()), hdr, body); err == nil {
		t.Fatal("a capture with no amount settled the intent")
	}
	requireNoTerminalEffect(t, si, "signed webhook with no amount")
}

func TestC3SignedWebhookWithTheWrongAmountCannotSettle(t *testing.T) {
	si := seedStale(t, 118000, "INR", "order_wh_wrongamt_"+uuid.NewString()[:8])
	hdr, body := signedWebhook(t, "evt_wrongamt_"+uuid.NewString(), "payment.captured", map[string]any{
		"id": "pay_wrongamt", "order_id": si.providerOrder, "amount": 1,
		"currency": "INR", "status": "captured",
	})
	if _, err := applySigned(t, svcWith(t, newCreateStub(t).provider()), hdr, body); err == nil {
		t.Fatal("a ₹0.01 capture settled a ₹1,180 intent")
	}
	requireNoTerminalEffect(t, si, "signed webhook with the wrong amount")
}

// ─── The policy is one policy ────────────────────────────────────────

// Every money path must refuse the same tuple. This is the guard against the
// rule drifting back apart into four copies, which is how the immediate
// recovery path came to have no copy of it at all.
func TestC3OneRefusalRuleAcrossEveryOperation(t *testing.T) {
	cases := []struct {
		name     string
		provider gateway.Money
		expected gateway.Money
		id       string
	}{
		{"blank identifier", gateway.Money{Minor: 100, Currency: "INR"}, gateway.Money{Minor: 100, Currency: "INR"}, ""},
		{"whitespace identifier", gateway.Money{Minor: 100, Currency: "INR"}, gateway.Money{Minor: 100, Currency: "INR"}, "  "},
		{"zero amount", gateway.Money{Minor: 0, Currency: "INR"}, gateway.Money{Minor: 100, Currency: "INR"}, "x"},
		{"negative amount", gateway.Money{Minor: -100, Currency: "INR"}, gateway.Money{Minor: -100, Currency: "INR"}, "x"},
		{"amount mismatch", gateway.Money{Minor: 101, Currency: "INR"}, gateway.Money{Minor: 100, Currency: "INR"}, "x"},
		{"provider currency blank", gateway.Money{Minor: 100}, gateway.Money{Minor: 100, Currency: "INR"}, "x"},
		{"our currency blank", gateway.Money{Minor: 100, Currency: "INR"}, gateway.Money{Minor: 100}, "x"},
		{"currency mismatch", gateway.Money{Minor: 100, Currency: "USD"}, gateway.Money{Minor: 100, Currency: "INR"}, "x"},
		{"whitespace currency", gateway.Money{Minor: 100, Currency: "   "}, gateway.Money{Minor: 100, Currency: "INR"}, "x"},
	}
	ops := []string{
		"immediate ambiguous-create recovery",
		"blank-provider-reference repair",
		"stale-intent reconciliation",
		"webhook application",
	}
	for _, c := range cases {
		for _, op := range ops {
			err := gateway.VerifyProviderMoney(gateway.MoneyCheck{
				Operation: op, IdentifierKind: "id", Identifier: c.id,
				Provider: c.provider, Expected: c.expected,
			})
			if err == nil {
				t.Fatalf("%s accepted %q", op, c.name)
			}
			if !errors.Is(err, gateway.ErrProviderMoneyUnverified) {
				t.Fatalf("%s/%s: error = %v, want ErrProviderMoneyUnverified", op, c.name, err)
			}
		}
	}

	// The exact tuple is accepted, and currency comparison folds case —
	// but nothing else is forgiven.
	for _, cur := range []string{"INR", "inr", "Inr"} {
		if err := gateway.VerifyProviderMoney(gateway.MoneyCheck{
			Operation: "canonical", IdentifierKind: "id", Identifier: "x",
			Provider: gateway.Money{Minor: 100, Currency: cur},
			Expected: gateway.Money{Minor: 100, Currency: "INR"},
		}); err != nil {
			t.Fatalf("exact tuple with currency %q refused: %v", cur, err)
		}
	}
}
