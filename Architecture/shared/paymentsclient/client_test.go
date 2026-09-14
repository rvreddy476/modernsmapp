package paymentsclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const tApp = "demo_app"

var (
	tRef    = uuid.MustParse("aaaaaaaa-0000-0000-0000-000000000001")
	tPayer  = uuid.MustParse("bbbbbbbb-0000-0000-0000-000000000002")
	tIntent = uuid.MustParse("cccccccc-0000-0000-0000-000000000003")
)

type call struct {
	method, path string
	header       http.Header
	body         map[string]json.RawMessage
}

// fakePayments is an httptest payments-service. It verifies a presented
// service token exactly as payments does (registered caller, one op per
// route, the caller's reference type) and answers with respond.
type fakePayments struct {
	mu    sync.Mutex
	calls []call
	srv   *httptest.Server
	priv  string
}

func newFake(t *testing.T, service, refType string, respond func(call) (int, any)) *fakePayments {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := verifier.RegisterBase64(service, "k1", pub,
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate},
		[]string{refType}); err != nil {
		t.Fatal(err)
	}
	f := &fakePayments{priv: priv}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c := call{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: map[string]json.RawMessage{}}
		_ = json.Unmarshal(raw, &c.body)
		f.mu.Lock()
		f.calls = append(f.calls, c)
		f.mu.Unlock()

		if auth := r.Header.Get(HeaderServiceAuthorization); auth != "" {
			op := servicetoken.OpIntentRead
			switch {
			case strings.HasSuffix(r.URL.Path, "/refund"):
				op = servicetoken.OpRefundCreate
			case strings.HasSuffix(r.URL.Path, "/intents") && r.Method == http.MethodPost:
				op = servicetoken.OpIntentCreate
			}
			if _, err := verifier.Verify(strings.TrimPrefix(auth, "Bearer "), op, refType); err != nil {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN"}}`))
				return
			}
		}
		status, body := respond(c)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": body})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePayments) recorded() []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]call(nil), f.calls...)
}

func tokenClient(t *testing.T, f *fakePayments, mutate func(*Config)) *Client {
	t.Helper()
	cfg := Config{
		BaseURL: f.srv.URL, Service: "demo-service", ReferenceType: "demo_ref",
		Auth: Auth{TokenKey: f.priv, TokenKID: "k1"},
	}
	if mutate != nil {
		mutate(&cfg)
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

// echoIntent answers a create with the request echoed back, the way
// payments-service does, plus a Razorpay session carrying values that must
// never reach a caller.
func echoIntent(c call) (int, any) {
	out := map[string]any{}
	for k, v := range c.body {
		out[k] = v
	}
	// payments-service does not store application_id yet, so it never echoes it.
	delete(out, "application_id")
	out["id"] = tIntent
	out["status"] = "pending"
	out["provider_ref"] = "order_RZP1"
	out["client_session"] = map[string]string{
		"provider": "razorpay", "order_id": "order_RZP1", "key_id": "rzp_test_pub",
		"key_secret": "sk_live_SECRET", "amount": "25000",
	}
	return http.StatusCreated, out
}

func TestCreateIntent_TokenHeaderAndBody(t *testing.T) {
	f := newFake(t, "demo-service", "demo_ref", echoIntent)
	c := tokenClient(t, f, nil)
	if c.LegacyAuth() {
		t.Fatal("a token client reports legacy auth")
	}

	payee := uuid.New()
	intent, err := c.CreateIntent(context.Background(), CreateIntentRequest{
		ApplicationID: tApp, ReferenceID: tRef, PayerID: tPayer, PayeeID: payee, AmountMinor: 25050, Method: "upi",
		IdempotencyKey: "demo_ref:" + tRef.String(),
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if intent.ID != tIntent || intent.AmountMinor != 25050 {
		t.Fatalf("intent = %+v", intent)
	}

	calls := f.recorded()
	if len(calls) != 1 {
		t.Fatalf("calls = %d", len(calls))
	}
	got := calls[0]
	if got.method != http.MethodPost || got.path != "/v1/payments/internal/intents" {
		t.Fatalf("route = %s %s", got.method, got.path)
	}
	if !strings.HasPrefix(got.header.Get(HeaderServiceAuthorization), "Bearer ") {
		t.Fatal("no service token presented")
	}
	if got.header.Get(HeaderInternalServiceKey) != "" {
		t.Fatal("the legacy key rode alongside a token")
	}
	if string(got.body["amount_minor"]) != "25050" {
		t.Fatalf("amount_minor = %s, want integer 25050", got.body["amount_minor"])
	}
	if _, ok := got.body["amount"]; ok {
		t.Fatal("a float amount was sent")
	}
	want := map[string]string{
		"reference_type": "demo_ref", "reference_id": tRef.String(), "payer_id": tPayer.String(),
		"payee_id": payee.String(), "currency": "INR", "method": "upi", "idempotency_key": "demo_ref:" + tRef.String(),
		"application_id": tApp,
	}
	for k, v := range want {
		var s string
		_ = json.Unmarshal(got.body[k], &s)
		if s != v {
			t.Errorf("%s = %q, want %q", k, s, v)
		}
	}
}

// TestApplicationID_SentOnTheWire pins the wire choice for application_id:
// it is SENT in the create-intent and refund bodies. payments-service binds
// both with gin's ShouldBindJSON and never enables DisallowUnknownFields, so
// the field is ignored there until it is stored. If payments ever starts
// rejecting unknown fields, this test is the place that decision changes.
// Reads decode application_id when payments echoes it and leave it empty
// when it does not (today).
func TestApplicationID_SentOnTheWire(t *testing.T) {
	f := newFake(t, "demo-service", "demo_ref", func(c call) (int, any) {
		if strings.HasSuffix(c.path, "/refund") {
			return http.StatusAccepted, map[string]any{"command_id": uuid.NewString(), "intent_id": tIntent,
				"amount_minor": 100, "status": "pending", "application_id": tApp}
		}
		if c.method == http.MethodGet {
			return http.StatusOK, map[string]any{"id": tIntent, "status": "succeeded", "application_id": tApp}
		}
		return echoIntent(c)
	})
	c := tokenClient(t, f, nil)
	ctx := context.Background()

	created, err := c.CreateIntent(ctx, CreateIntentRequest{ApplicationID: tApp, ReferenceID: tRef, AmountMinor: 100, Method: "upi", IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	if created.ApplicationID != "" {
		t.Fatalf("an intent payments did not stamp decoded application_id %q", created.ApplicationID)
	}
	acc, err := c.Refund(ctx, tIntent, RefundRequest{ApplicationID: tApp, AmountMinor: 100, IdempotencyKey: "r"})
	if err != nil {
		t.Fatal(err)
	}
	read, err := c.GetIntent(ctx, tIntent)
	if err != nil {
		t.Fatal(err)
	}
	if acc.ApplicationID != tApp || read.ApplicationID != tApp {
		t.Fatalf("echoed application_id not decoded: refund=%q intent=%q", acc.ApplicationID, read.ApplicationID)
	}

	calls := f.recorded()
	for i, name := range []string{"create", "refund"} {
		var got string
		if err := json.Unmarshal(calls[i].body["application_id"], &got); err != nil || got != tApp {
			t.Fatalf("%s body application_id = %s, want %q", name, calls[i].body["application_id"], tApp)
		}
	}
	if _, ok := calls[2].body["application_id"]; ok {
		t.Fatal("a GET carried a body")
	}
}

func TestValidateApplicationID(t *testing.T) {
	for _, ok := range []string{"feast", "mstore", "ab", "a1", "feast_rider", "a" + strings.Repeat("b", 31)} {
		if err := ValidateApplicationID(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"", "a", "Feast", "1feast", "_feast", "feast-app", "feast app", " feast", "a" + strings.Repeat("b", 32)} {
		if err := ValidateApplicationID(bad); !errors.Is(err, ErrInvalidApplicationID) {
			t.Errorf("%q accepted (err %v)", bad, err)
		}
	}
}

func TestClientSession_OnlyPublicFields(t *testing.T) {
	f := newFake(t, "demo-service", "demo_ref", echoIntent)
	c := tokenClient(t, f, nil)
	intent, err := c.GetIntent(context.Background(), tIntent)
	if err != nil {
		t.Fatalf("GetIntent: %v", err)
	}
	want := ClientSession{Provider: "razorpay", OrderID: "order_RZP1", KeyID: "rzp_test_pub"}
	if intent.ClientSession == nil || *intent.ClientSession != want {
		t.Fatalf("client_session = %+v", intent.ClientSession)
	}
	raw, _ := json.Marshal(intent)
	if strings.Contains(string(raw), "SECRET") || strings.Contains(string(raw), `"amount":`) {
		t.Fatalf("intent JSON carries a non-public session value: %s", raw)
	}
	m := intent.ClientSession.AsMap()
	if len(m) != 3 || m["provider"] != "razorpay" || m["order_id"] != "order_RZP1" || m["key_id"] != "rzp_test_pub" {
		t.Fatalf("AsMap = %v", m)
	}

	// No session from payments (stub gateway): nothing to relay.
	f2 := newFake(t, "demo-service", "demo_ref", func(call) (int, any) {
		return http.StatusOK, map[string]any{"id": tIntent, "status": "pending"}
	})
	bare, err := tokenClient(t, f2, nil).GetIntent(context.Background(), tIntent)
	if err != nil {
		t.Fatal(err)
	}
	if bare.ClientSession != nil || bare.ClientSession.AsMap() != nil {
		t.Fatalf("absent session decoded as %+v", bare.ClientSession)
	}
}

func TestCreateIntent_EchoChecks(t *testing.T) {
	mutated := func(mutate func(map[string]any)) func(call) (int, any) {
		return func(c call) (int, any) {
			status, body := echoIntent(c)
			m := body.(map[string]any)
			mutate(m)
			return status, m
		}
	}
	req := CreateIntentRequest{ApplicationID: tApp, ReferenceID: tRef, PayerID: tPayer, AmountMinor: 25000, Method: "card", IdempotencyKey: "k"}

	// The amount echo is always checked.
	f := newFake(t, "demo-service", "demo_ref", mutated(func(m map[string]any) { m["amount_minor"] = 100 }))
	if _, err := tokenClient(t, f, nil).CreateIntent(context.Background(), req); err == nil {
		t.Fatal("accepted an intent whose amount differs from the request")
	}

	// A reference echo is checked only when the caller asks for it.
	wrongRef := mutated(func(m map[string]any) { m["reference_id"] = uuid.NewString() })
	f = newFake(t, "demo-service", "demo_ref", wrongRef)
	if _, err := tokenClient(t, f, nil).CreateIntent(context.Background(), req); err != nil {
		t.Fatalf("without VerifyReferenceEcho the reference must not be compared: %v", err)
	}
	f = newFake(t, "demo-service", "demo_ref", wrongRef)
	strict := tokenClient(t, f, func(c *Config) { c.VerifyReferenceEcho = true })
	if _, err := strict.CreateIntent(context.Background(), req); err == nil {
		t.Fatal("VerifyReferenceEcho accepted an intent for another reference")
	}
}

func TestLocalRefusalsSendNothing(t *testing.T) {
	f := newFake(t, "demo-service", "demo_ref", echoIntent)
	c := tokenClient(t, f, func(c *Config) {
		c.ValidateMethod = func(m string) error {
			if m != "upi" {
				return errors.New("not a launch method")
			}
			return nil
		}
		c.RequirePositiveRefund = true
	})
	ctx := context.Background()
	for _, in := range []CreateIntentRequest{
		{ApplicationID: tApp, ReferenceID: tRef, AmountMinor: 0, Method: "upi", IdempotencyKey: "k"},
		{ApplicationID: tApp, ReferenceID: tRef, AmountMinor: 100, Method: "cod", IdempotencyKey: "k"},
		{ApplicationID: tApp, ReferenceID: tRef, AmountMinor: 100, Method: "upi", IdempotencyKey: " "},
		{ApplicationID: "", ReferenceID: tRef, AmountMinor: 100, Method: "upi", IdempotencyKey: "k"},
		{ApplicationID: "Feast", ReferenceID: tRef, AmountMinor: 100, Method: "upi", IdempotencyKey: "k"},
	} {
		if _, err := c.CreateIntent(ctx, in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
	for _, in := range []RefundRequest{
		{ApplicationID: tApp, AmountMinor: 100, IdempotencyKey: ""},
		{ApplicationID: tApp, AmountMinor: 0, IdempotencyKey: "k"},
		{ApplicationID: "", AmountMinor: 100, IdempotencyKey: "k"},
		{ApplicationID: "feast-app", AmountMinor: 100, IdempotencyKey: "k"},
	} {
		if _, err := c.Refund(ctx, tIntent, in); err == nil {
			t.Fatalf("refund accepted %+v", in)
		}
	}
	if n := len(f.recorded()); n != 0 {
		t.Fatalf("payments was called %d times for refused input", n)
	}

	// Without RequirePositiveRefund a zero refund is payments' to refuse.
	lenient := tokenClient(t, f, nil)
	if _, err := lenient.Refund(ctx, tIntent, RefundRequest{ApplicationID: tApp, AmountMinor: 0, IdempotencyKey: "k"}); err != nil {
		t.Fatalf("lenient zero refund: %v", err)
	}
	if n := len(f.recorded()); n != 1 {
		t.Fatalf("lenient zero refund calls = %d, want 1", n)
	}
}

func TestVerifyAndRefund_RoutesOpsAndBodies(t *testing.T) {
	f := newFake(t, "demo-service", "demo_ref", func(c call) (int, any) {
		if strings.HasSuffix(c.path, "/refund") {
			return http.StatusAccepted, map[string]any{"command_id": uuid.NewString(), "intent_id": tIntent, "amount_minor": 12345, "status": "pending"}
		}
		return http.StatusOK, map[string]any{"verified": true, "advisory": true, "amount_minor": 25000,
			"payer_id": tPayer, "reference_type": "demo_ref", "reference_id": tRef}
	})
	c := tokenClient(t, f, nil)
	ctx := context.Background()

	v, err := c.VerifyCallback(ctx, tIntent, CallbackRequest{ProviderOrderID: "order_x", ProviderPaymentID: "pay_x", Signature: "sig", ExpectedAmountMinor: 25000})
	if err != nil {
		t.Fatalf("VerifyCallback: %v", err)
	}
	if !v.Verified || v.PayerID != tPayer || v.ReferenceID != tRef {
		t.Fatalf("verdict = %+v", v)
	}
	acc, err := c.Refund(ctx, tIntent, RefundRequest{ApplicationID: tApp, AmountMinor: 12345, Reason: "r", IdempotencyKey: "demo_refund:1"})
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if acc.AmountMinor != 12345 || acc.IntentID != tIntent {
		t.Fatalf("accepted = %+v", acc)
	}

	calls := f.recorded()
	if calls[0].path != "/v1/payments/internal/intents/"+tIntent.String()+"/verify" || string(calls[0].body["amount_minor"]) != "25000" {
		t.Fatalf("verify call = %s %s", calls[0].path, calls[0].body["amount_minor"])
	}
	var sig string
	_ = json.Unmarshal(calls[0].body["razorpay_signature"], &sig)
	if sig != "sig" {
		t.Fatalf("razorpay_signature = %q", sig)
	}
	if calls[1].path != "/v1/payments/internal/intents/"+tIntent.String()+"/refund" || string(calls[1].body["amount_minor"]) != "12345" {
		t.Fatalf("refund call = %s %s", calls[1].path, calls[1].body["amount_minor"])
	}
	var key string
	_ = json.Unmarshal(calls[1].body["idempotency_key"], &key)
	if key != "demo_refund:1" {
		t.Fatalf("idempotency_key = %q", key)
	}
}

// A token minted for one reference type must not be accepted for another:
// the fake payments refuses it with 403, which surfaces as ErrRefused.
func TestToken_ScopedToTheClientReferenceType(t *testing.T) {
	f := newFake(t, "demo-service", "other_ref", echoIntent)
	c := tokenClient(t, f, nil) // bound to demo_ref
	_, err := c.GetIntent(context.Background(), tIntent)
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused for a token of the wrong reference type", err)
	}
}

func TestErrors_TypedPerStatusClass(t *testing.T) {
	for _, tc := range []struct {
		status      int
		kind        Kind
		unavailable bool
		refused     bool
		text        string
	}{
		{http.StatusBadGateway, KindServer, true, false, "payments service unavailable: status 502"},
		{http.StatusInternalServerError, KindServer, true, false, "payments service unavailable: status 500"},
		{http.StatusConflict, KindRefused, false, true, "payments refused the request: status 409: "},
		{http.StatusBadRequest, KindRefused, false, true, "payments refused the request: status 400: "},
	} {
		f := newFake(t, "demo-service", "demo_ref", func(call) (int, any) {
			return tc.status, map[string]any{"error": map[string]string{"code": "NOPE"}}
		})
		_, err := tokenClient(t, f, nil).VerifyCallback(context.Background(), tIntent, CallbackRequest{ExpectedAmountMinor: 1})
		var pe *Error
		if !errors.As(err, &pe) || pe.Kind != tc.kind || pe.StatusCode != tc.status {
			t.Fatalf("status %d: err = %#v, want kind %s", tc.status, err, tc.kind)
		}
		if errors.Is(err, ErrUnavailable) != tc.unavailable || errors.Is(err, ErrRefused) != tc.refused {
			t.Fatalf("status %d: Is(unavailable)=%v Is(refused)=%v", tc.status, errors.Is(err, ErrUnavailable), errors.Is(err, ErrRefused))
		}
		if !strings.HasPrefix(err.Error(), tc.text) {
			t.Fatalf("status %d: text = %q, want prefix %q", tc.status, err.Error(), tc.text)
		}
		if tc.refused && !strings.Contains(pe.Detail, "NOPE") {
			t.Fatalf("status %d: refused detail = %q", tc.status, pe.Detail)
		}
	}

	// A 4xx body is kept only to 300 bytes.
	long := strings.Repeat("x", 1000)
	f := newFake(t, "demo-service", "demo_ref", func(call) (int, any) { return http.StatusUnprocessableEntity, long })
	_, err := tokenClient(t, f, nil).GetIntent(context.Background(), tIntent)
	var pe *Error
	if !errors.As(err, &pe) || len(pe.Detail) > 300+len("…") {
		t.Fatalf("detail length = %d", len(pe.Detail))
	}

	// Transport failure.
	dead := httptest.NewServer(http.NotFoundHandler())
	url := dead.URL
	dead.Close()
	c, err := New(Config{BaseURL: url, Service: "demo-service", ReferenceType: "demo_ref", Auth: Auth{InternalKey: "k", LegacyAllowed: true}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.GetIntent(context.Background(), tIntent)
	if !errors.As(err, &pe) || pe.Kind != KindNetwork || !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrRefused) {
		t.Fatalf("network err = %#v", err)
	}
}

func TestLegacyKey_OnlyWhenTheCallerAllowsIt(t *testing.T) {
	f := newFake(t, "demo-service", "demo_ref", echoIntent)
	cfg := Config{BaseURL: f.srv.URL, Service: "demo-service", ReferenceType: "demo_ref"}

	// Outside local/dev (LegacyAllowed false) and without a token key, no
	// client exists to send the key.
	cfg.Auth = Auth{InternalKey: "legacy-key", LegacyAllowed: false}
	if _, err := New(cfg); !errors.Is(err, ErrServiceTokenRequired) {
		t.Fatalf("legacy key outside dev: err = %v, want ErrServiceTokenRequired", err)
	}

	// Allowed, but no key to send.
	cfg.Auth = Auth{LegacyAllowed: true}
	if _, err := New(cfg); err == nil {
		t.Fatal("legacy mode with no internal key accepted")
	}

	// Allowed: only the legacy header.
	cfg.Auth = Auth{InternalKey: "legacy-key", LegacyAllowed: true}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !c.LegacyAuth() {
		t.Fatal("legacy client does not report legacy auth")
	}
	if _, err := c.GetIntent(context.Background(), tIntent); err != nil {
		t.Fatal(err)
	}
	h := f.recorded()[0].header
	if h.Get(HeaderInternalServiceKey) != "legacy-key" || h.Get(HeaderServiceAuthorization) != "" {
		t.Fatalf("legacy headers wrong: token present=%v", h.Get(HeaderServiceAuthorization) != "")
	}

	// A token key always wins, even where legacy is allowed and a key is set:
	// the legacy key is never sent.
	cfg.Auth = Auth{TokenKey: f.priv, TokenKID: "k1", InternalKey: "legacy-key", LegacyAllowed: true}
	tc, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tc.GetIntent(context.Background(), tIntent); err != nil {
		t.Fatal(err)
	}
	h = f.recorded()[1].header
	if h.Get(HeaderInternalServiceKey) != "" || !strings.HasPrefix(h.Get(HeaderServiceAuthorization), "Bearer ") {
		t.Fatal("token client sent the legacy key or no token")
	}
}

func TestNew_RequiresIdentity(t *testing.T) {
	ok := Config{BaseURL: "http://p", Service: "s", ReferenceType: "r", Auth: Auth{InternalKey: "k", LegacyAllowed: true}}
	for name, mutate := range map[string]func(*Config){
		"base url":       func(c *Config) { c.BaseURL = " " },
		"service":        func(c *Config) { c.Service = "" },
		"reference type": func(c *Config) { c.ReferenceType = "" },
		"bad token key":  func(c *Config) { c.Auth = Auth{TokenKey: "not-a-key", TokenKID: "k1"} },
	} {
		cfg := ok
		mutate(&cfg)
		if _, err := New(cfg); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

func TestMaxResponseBytes(t *testing.T) {
	f := newFake(t, "demo-service", "demo_ref", func(call) (int, any) {
		return http.StatusOK, map[string]any{"id": tIntent, "status": strings.Repeat("s", 4096)}
	})
	capped := tokenClient(t, f, func(c *Config) { c.MaxResponseBytes = 64 })
	if _, err := capped.GetIntent(context.Background(), tIntent); err == nil {
		t.Fatal("a response past MaxResponseBytes decoded")
	}
	if _, err := tokenClient(t, f, nil).GetIntent(context.Background(), tIntent); err != nil {
		t.Fatalf("uncapped: %v", err)
	}
}
