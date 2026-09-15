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

type recorded struct {
	method, path string
	header       http.Header
	body         map[string]json.RawMessage
}

// paymentsStub is a payments-service that verifies the dating token exactly
// the way payments does: registered caller dating-service, kid d1, one op per
// route, and the dating_premium reference type.
func paymentsStub(t *testing.T, respond func(r recorded) (int, any)) (*httptest.Server, *[]recorded, string) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := verifier.RegisterBase64("dating-service", "d1", pub,
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate},
		[]string{servicetoken.RefDatingPremium}); err != nil {
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
		if _, err := verifier.Verify(strings.TrimPrefix(auth, "Bearer "), op, servicetoken.RefDatingPremium); err != nil {
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

func TestPremiumClient_CreateIntentSendsDatingTokenApplicationAndKey(t *testing.T) {
	srv, calls, priv := paymentsStub(t, echoIntent)
	c, err := NewTokenClient(srv.URL, "d1", priv, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := c.CreateIntent(context.Background(), CreateIntentInput{
		PurchaseID: tPurchaseID, PayerID: tUserID, AmountMinor: 39900, Currency: "INR", Method: "upi",
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if intent.ID.String() != tIntentID || intent.AmountMinor != 39900 {
		t.Fatalf("intent = %+v", intent)
	}
	if cs := intent.PublicClientSession(); cs == nil || cs.KeyID != "rzp_test_pub" {
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
		"application_id":  "dating",
		"reference_type":  "dating_premium",
		"reference_id":    tPurchaseID.String(),
		"payer_id":        tUserID.String(),
		"payee_id":        DefaultPayeeID.String(),
		"idempotency_key": "dating_premium:" + tPurchaseID.String(),
		"method":          "upi",
		"currency":        "INR",
	}
	for k, want := range wantStr {
		var v string
		if err := json.Unmarshal(got.body[k], &v); err != nil || v != want {
			t.Fatalf("%s = %s, want %q", k, got.body[k], want)
		}
	}
	if string(got.body["amount_minor"]) != "39900" {
		t.Fatalf("amount_minor = %s", got.body["amount_minor"])
	}
	if got.header.Get("X-Internal-Service-Key") != "" {
		t.Fatalf("the legacy key must never be sent")
	}
}

func TestPremiumClient_RefusesBadMethodBeforeCalling(t *testing.T) {
	srv, calls, priv := paymentsStub(t, echoIntent)
	c, err := NewTokenClient(srv.URL, "d1", priv, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"", "cod", "UPI", "wallet"} {
		if _, err := c.CreateIntent(context.Background(), CreateIntentInput{PurchaseID: tPurchaseID, PayerID: tUserID, AmountMinor: 4900, Method: m}); err == nil {
			t.Fatalf("method %q accepted", m)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("a refused method still called payments")
	}
}

func TestPremiumClient_ErrorClasses(t *testing.T) {
	srv, _, priv := paymentsStub(t, func(recorded) (int, any) { return http.StatusServiceUnavailable, nil })
	c, _ := NewTokenClient(srv.URL, "d1", priv, uuid.Nil)
	_, err := c.CreateIntent(context.Background(), CreateIntentInput{PurchaseID: tPurchaseID, PayerID: tUserID, AmountMinor: 4900, Method: "upi"})
	if !errors.Is(err, ErrPaymentsUnavailable) {
		t.Fatalf("5xx: %v", err)
	}
	srv2, _, priv2 := paymentsStub(t, func(recorded) (int, any) { return http.StatusUnprocessableEntity, map[string]string{"code": "X"} })
	c2, _ := NewTokenClient(srv2.URL, "d1", priv2, uuid.Nil)
	_, err = c2.CreateIntent(context.Background(), CreateIntentInput{PurchaseID: tPurchaseID, PayerID: tUserID, AmountMinor: 4900, Method: "upi"})
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("4xx: %v", err)
	}
}

func TestPremiumClientFromEnv_NeedsKeyAndKid(t *testing.T) {
	_, priv, _ := servicetoken.GenerateKeypair()
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	for name, m := range map[string]map[string]string{
		"nothing":     {},
		"no kid":      {EnvTokenKey: priv},
		"no key":      {EnvTokenKID: "d1"},
		"blank key":   {EnvTokenKey: "  ", EnvTokenKID: "d1"},
		"legacy only": {"INTERNAL_SERVICE_KEY": "k", "ENV": "local"},
	} {
		if _, err := ClientFromEnv(env(m)); !errors.Is(err, ErrNotConfigured) {
			t.Fatalf("%s: err = %v, want ErrNotConfigured", name, err)
		}
	}
	if _, err := ClientFromEnv(env(map[string]string{EnvTokenKey: priv, EnvTokenKID: "d1"})); err != nil {
		t.Fatalf("configured: %v", err)
	}
	if _, err := ClientFromEnv(env(map[string]string{EnvTokenKey: "not base64 !!", EnvTokenKID: "d1"})); err == nil || errors.Is(err, ErrNotConfigured) {
		t.Fatalf("a bad key must be a real error, got %v", err)
	}
	if _, err := ClientFromEnv(env(map[string]string{EnvTokenKey: priv, EnvTokenKID: "d1", EnvPayeeID: "nope"})); err == nil {
		t.Fatalf("a malformed payee must be refused")
	}
}

func TestPremiumIsLocalEnv(t *testing.T) {
	for env, want := range map[string]bool{"local": true, "dev": true, "Development": true, "": false, "staging": false, "prod": false} {
		if IsLocalEnv(env) != want {
			t.Fatalf("IsLocalEnv(%q) != %v", env, want)
		}
	}
}
