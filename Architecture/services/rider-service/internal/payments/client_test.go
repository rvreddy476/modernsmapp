package payments

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

var (
	tCustomerID = uuid.MustParse("11111111-1111-4111-8111-111111111111")
	tRideID     = uuid.MustParse("22222222-2222-4222-8222-222222222222")
)

const tIntentID = "33333333-3333-4333-8333-333333333333"

type recorded struct {
	method, path string
	header       http.Header
	body         map[string]json.RawMessage
}

// paymentsStub is a payments-service that verifies the rider token exactly
// the way payments does: registered caller rider-service, kid r1, one op
// per route, and the mopedu_ride reference type.
func paymentsStub(t *testing.T, respond func(r recorded) (int, any)) (*httptest.Server, *[]recorded, string) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := verifier.RegisterBase64("rider-service", "r1", pub,
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate},
		[]string{servicetoken.RefMopeduRide}); err != nil {
		t.Fatal(err)
	}
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec := recorded{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: map[string]json.RawMessage{}}
		_ = json.Unmarshal(raw, &rec.body)
		calls = append(calls, rec)
		op := servicetoken.OpIntentRead
		switch {
		case strings.HasSuffix(r.URL.Path, "/refund"):
			op = servicetoken.OpRefundCreate
		case strings.HasSuffix(r.URL.Path, "/intents") && r.Method == http.MethodPost:
			op = servicetoken.OpIntentCreate
		}
		auth := r.Header.Get("X-Service-Authorization")
		if _, err := verifier.Verify(strings.TrimPrefix(auth, "Bearer "), op, servicetoken.RefMopeduRide); err != nil {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN"}}`))
			return
		}
		status, body := respond(rec)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"data": body})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls, priv
}

func echoIntent(r recorded) (int, any) {
	var out map[string]any
	b, _ := json.Marshal(r.body)
	_ = json.Unmarshal(b, &out)
	out["id"] = tIntentID
	out["status"] = "pending"
	out["provider_ref"] = "order_rzp_1"
	out["client_session"] = map[string]string{"provider": "razorpay", "order_id": "order_rzp_1", "key_id": "rzp_test_pub", "key_secret": "dropped"}
	return http.StatusCreated, out
}

func TestClient_CreateIntentSendsRiderTokenApplicationAndKey(t *testing.T) {
	srv, calls, priv := paymentsStub(t, echoIntent)
	c, err := NewTokenClient(srv.URL, "r1", priv, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := c.CreateIntent(context.Background(), CreateIntentInput{
		ReferenceID: tRideID, PayerID: tCustomerID, AmountMinor: 12451, Method: "upi", IdempotencyKey: RideIntentKey(tRideID, "upi"),
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if intent.ID.String() != tIntentID || intent.AmountMinor != 12451 {
		t.Fatalf("intent = %+v", intent)
	}
	if cs := intent.PublicClientSession(); cs == nil || cs.KeyID != "rzp_test_pub" || cs.OrderID != "order_rzp_1" {
		t.Fatalf("client session = %+v", intent.ClientSession)
	}
	if _, leaked := intent.ClientSession["key_secret"]; leaked {
		t.Fatalf("a non-public session field survived decode")
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d", len(*calls))
	}
	got := (*calls)[0]
	wantStr := map[string]string{
		"application_id":  "mopedu",
		"reference_type":  "mopedu_ride",
		"reference_id":    tRideID.String(),
		"payer_id":        tCustomerID.String(),
		"payee_id":        DefaultPayeeID.String(),
		"idempotency_key": "ride:" + tRideID.String() + ":upi",
		"method":          "upi",
		"currency":        "INR",
	}
	for k, want := range wantStr {
		var v string
		if err := json.Unmarshal(got.body[k], &v); err != nil || v != want {
			t.Fatalf("%s = %s, want %q", k, got.body[k], want)
		}
	}
	if string(got.body["amount_minor"]) != "12451" {
		t.Fatalf("amount_minor = %s", got.body["amount_minor"])
	}
	if got.header.Get("X-Internal-Service-Key") != "" {
		t.Fatalf("the legacy key must never be sent")
	}
}

func TestClient_RefusesBadMethodAndNonPositiveAmountBeforeCalling(t *testing.T) {
	srv, calls, priv := paymentsStub(t, echoIntent)
	c, err := NewTokenClient(srv.URL, "r1", priv, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range []CreateIntentInput{
		{ReferenceID: tRideID, PayerID: tCustomerID, AmountMinor: 100, Method: "wallet", IdempotencyKey: "k"},
		{ReferenceID: tRideID, PayerID: tCustomerID, AmountMinor: 100, Method: "cash", IdempotencyKey: "k"},
		{ReferenceID: tRideID, PayerID: tCustomerID, AmountMinor: 0, Method: "upi", IdempotencyKey: "k"},
	} {
		if _, err := c.CreateIntent(context.Background(), in); err == nil {
			t.Fatalf("%+v accepted", in)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("payments was called %d times", len(*calls))
	}
}

func TestClient_RefundSendsApplicationAmountAndKey(t *testing.T) {
	srv, calls, priv := paymentsStub(t, func(r recorded) (int, any) {
		return http.StatusAccepted, map[string]any{"command_id": uuid.NewString(), "intent_id": tIntentID, "amount_minor": 5000, "status": "requested", "application_id": "mopedu"}
	})
	c, err := NewTokenClient(srv.URL, "r1", priv, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	refundID := uuid.New()
	acc, err := c.Refund(context.Background(), uuid.MustParse(tIntentID), 5000, "captain no-show", RefundKey(refundID))
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if acc.AmountMinor != 5000 {
		t.Fatalf("accepted = %+v", acc)
	}
	got := (*calls)[0]
	if !strings.HasSuffix(got.path, "/intents/"+tIntentID+"/refund") {
		t.Fatalf("path = %s", got.path)
	}
	var key, app string
	_ = json.Unmarshal(got.body["idempotency_key"], &key)
	_ = json.Unmarshal(got.body["application_id"], &app)
	if key != "refund:"+refundID.String() || app != "mopedu" || string(got.body["amount_minor"]) != "5000" {
		t.Fatalf("refund body = %v", got.body)
	}
	if _, err := c.Refund(context.Background(), uuid.MustParse(tIntentID), 0, "x", RefundKey(refundID)); err == nil {
		t.Fatal("a zero refund must be refused locally")
	}
}

func TestClient_ErrorClasses(t *testing.T) {
	srv, _, priv := paymentsStub(t, func(r recorded) (int, any) { return http.StatusUnprocessableEntity, map[string]any{"error": "no"} })
	c, _ := NewTokenClient(srv.URL, "r1", priv, uuid.Nil)
	_, err := c.CreateIntent(context.Background(), CreateIntentInput{ReferenceID: tRideID, PayerID: tCustomerID, AmountMinor: 1, Method: "upi", IdempotencyKey: "k"})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("4xx: %v", err)
	}
	down, _ := NewTokenClient("http://127.0.0.1:1", "r1", priv, uuid.Nil)
	_, err = down.CreateIntent(context.Background(), CreateIntentInput{ReferenceID: tRideID, PayerID: tCustomerID, AmountMinor: 1, Method: "upi", IdempotencyKey: "k"})
	if !errors.Is(err, ErrPaymentsUnavailable) {
		t.Fatalf("down: %v", err)
	}
}

func TestClientFromEnv_NeedsKeyAndKid(t *testing.T) {
	_, _, priv := paymentsStub(t, echoIntent)
	cases := []map[string]string{
		{},
		{EnvTokenKey: priv},
		{EnvTokenKID: "r1"},
	}
	for _, env := range cases {
		_, err := ClientFromEnv(func(k string) string { return env[k] })
		if !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("%v: err = %v, want ErrNotConfigured", env, err)
		}
	}
	c, err := ClientFromEnv(func(k string) string {
		return map[string]string{EnvTokenKey: priv, EnvTokenKID: "r1", "PAYMENTS_SERVICE_URL": "http://payments"}[k]
	})
	if err != nil || c == nil {
		t.Fatalf("configured: %v", err)
	}
	if _, err := ClientFromEnv(func(k string) string {
		return map[string]string{EnvTokenKey: priv, EnvTokenKID: "r1", EnvPayeeID: "nope"}[k]
	}); err == nil {
		t.Fatal("a malformed payee must be refused")
	}
}

func TestIsLocalEnv(t *testing.T) {
	for env, want := range map[string]bool{"local": true, "dev": true, "development": true, "": false, "staging": false, "production": false} {
		if IsLocalEnv(env) != want {
			t.Fatalf("IsLocalEnv(%q) = %v", env, !want)
		}
	}
}
