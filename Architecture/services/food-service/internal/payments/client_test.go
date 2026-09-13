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

// paymentsStub is a payments-service that verifies the food token exactly the
// way payments does: registered caller food-service, one op per route, and
// the food_order reference type.
func paymentsStub(t *testing.T, respond func(r recorded) (int, any)) (*httptest.Server, *[]recorded, string) {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := verifier.RegisterBase64("food-service", "f1", pub,
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate},
		[]string{servicetoken.RefFoodOrder}); err != nil {
		t.Fatal(err)
	}
	var calls []recorded
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		rec := recorded{method: r.Method, path: r.URL.Path, header: r.Header.Clone(), body: map[string]json.RawMessage{}}
		_ = json.Unmarshal(raw, &rec.body)
		calls = append(calls, rec)

		if auth := r.Header.Get("X-Service-Authorization"); auth != "" {
			op := servicetoken.OpIntentRead
			switch {
			case strings.HasSuffix(r.URL.Path, "/refund"):
				op = servicetoken.OpRefundCreate
			case strings.HasSuffix(r.URL.Path, "/intents") && r.Method == http.MethodPost:
				op = servicetoken.OpIntentCreate
			}
			if _, err := verifier.Verify(strings.TrimPrefix(auth, "Bearer "), op, servicetoken.RefFoodOrder); err != nil {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"error":{"code":"FORBIDDEN"}}`))
				return
			}
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
	return http.StatusCreated, out
}

func TestCreateIntent_TokenOpRefTypeAndIntegerAmount(t *testing.T) {
	srv, calls, priv := paymentsStub(t, echoIntent)
	c, err := NewTokenClient(srv.URL, "f1", priv)
	if err != nil {
		t.Fatal(err)
	}
	payee := uuid.New()
	intent, err := c.CreateIntent(context.Background(), CreateIntentInput{
		OrderID: tOrderID, PayerID: tUserID, PayeeID: payee, AmountMinor: 25050, Method: "upi",
	})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if intent.ID.String() != tIntentID || intent.AmountMinor != 25050 {
		t.Fatalf("intent = %+v", intent)
	}
	if len(*calls) != 1 {
		t.Fatalf("calls = %d", len(*calls))
	}
	call := (*calls)[0]
	if call.method != http.MethodPost || call.path != "/v1/payments/internal/intents" {
		t.Fatalf("route = %s %s", call.method, call.path)
	}
	if !strings.HasPrefix(call.header.Get("X-Service-Authorization"), "Bearer ") {
		t.Fatalf("no service token presented")
	}
	if call.header.Get("X-Internal-Service-Key") != "" {
		t.Fatalf("the shared internal key must never ride alongside a token")
	}
	if got := string(call.body["amount_minor"]); got != "25050" {
		t.Fatalf("amount_minor = %s, want integer 25050", got)
	}
	if _, ok := call.body["amount"]; ok {
		t.Fatalf("a float amount was sent")
	}
	wantStr := map[string]string{
		"reference_type":  "food_order",
		"reference_id":    tOrderID.String(),
		"payer_id":        tUserID.String(),
		"payee_id":        payee.String(),
		"currency":        "INR",
		"method":          "upi",
		"idempotency_key": "food_order:" + tOrderID.String(),
	}
	for k, want := range wantStr {
		var got string
		_ = json.Unmarshal(call.body[k], &got)
		if got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
}

func TestCreateIntent_RefusesEchoMismatch(t *testing.T) {
	cases := map[string]func(map[string]any){
		"amount":         func(m map[string]any) { m["amount_minor"] = 100 },
		"reference id":   func(m map[string]any) { m["reference_id"] = uuid.NewString() },
		"reference type": func(m map[string]any) { m["reference_type"] = "order" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			srv, _, priv := paymentsStub(t, func(r recorded) (int, any) {
				status, body := echoIntent(r)
				m := body.(map[string]any)
				mutate(m)
				return status, m
			})
			c, _ := NewTokenClient(srv.URL, "f1", priv)
			if _, err := c.CreateIntent(context.Background(), CreateIntentInput{
				OrderID: tOrderID, PayerID: tUserID, PayeeID: uuid.New(), AmountMinor: 25000, Method: "card",
			}); err == nil {
				t.Fatalf("accepted an intent whose %s differs from the order", name)
			}
		})
	}
}

func TestCreateIntent_RefusesBadInputWithoutCalling(t *testing.T) {
	srv, calls, priv := paymentsStub(t, echoIntent)
	c, _ := NewTokenClient(srv.URL, "f1", priv)
	for _, in := range []CreateIntentInput{
		{OrderID: tOrderID, PayerID: tUserID, PayeeID: uuid.New(), AmountMinor: 0, Method: "upi"},
		{OrderID: tOrderID, PayerID: tUserID, PayeeID: uuid.New(), AmountMinor: 100, Method: "cod"},
		{OrderID: tOrderID, PayerID: tUserID, PayeeID: uuid.New(), AmountMinor: 100, Method: ""},
	} {
		if _, err := c.CreateIntent(context.Background(), in); err == nil {
			t.Fatalf("accepted %+v", in)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("payments was called for refused input")
	}
}

func TestVerifyCallback_ReadOpAndServerAmount(t *testing.T) {
	srv, calls, priv := paymentsStub(t, func(recorded) (int, any) {
		return http.StatusOK, map[string]any{
			"verified": true, "advisory": true, "status": "pending", "amount_minor": 25000,
			"payer_id": tUserID.String(), "reference_type": "food_order", "reference_id": tOrderID.String(),
		}
	})
	c, _ := NewTokenClient(srv.URL, "f1", priv)
	v, err := c.VerifyCallback(context.Background(), uuid.MustParse(tIntentID), "order_x", "pay_x", "sig_x", 25000)
	if err != nil {
		t.Fatalf("VerifyCallback: %v", err)
	}
	if !v.Verified || v.PayerID != tUserID || v.ReferenceID != tOrderID || v.AmountMinor != 25000 {
		t.Fatalf("verdict = %+v", v)
	}
	call := (*calls)[0]
	if call.path != "/v1/payments/internal/intents/"+tIntentID+"/verify" {
		t.Fatalf("path = %s", call.path)
	}
	if string(call.body["amount_minor"]) != "25000" {
		t.Fatalf("amount_minor = %s", call.body["amount_minor"])
	}
}

func TestRefund_OpAmountAndIdempotencyKey(t *testing.T) {
	srv, calls, priv := paymentsStub(t, func(recorded) (int, any) {
		return http.StatusAccepted, map[string]any{"command_id": uuid.NewString(), "intent_id": tIntentID, "amount_minor": 12345, "status": "pending"}
	})
	c, _ := NewTokenClient(srv.URL, "f1", priv)
	key := "food_refund:" + tOrderID.String() + ":r1"
	acc, err := c.Refund(context.Background(), uuid.MustParse(tIntentID), 12345, "spilled", key)
	if err != nil {
		t.Fatalf("Refund: %v", err)
	}
	if acc.AmountMinor != 12345 {
		t.Fatalf("accepted = %+v", acc)
	}
	call := (*calls)[0]
	if call.path != "/v1/payments/internal/intents/"+tIntentID+"/refund" {
		t.Fatalf("path = %s", call.path)
	}
	if string(call.body["amount_minor"]) != "12345" {
		t.Fatalf("amount_minor = %s", call.body["amount_minor"])
	}
	if _, ok := call.body["amount"]; ok {
		t.Fatalf("a float amount was sent")
	}
	var gotKey string
	_ = json.Unmarshal(call.body["idempotency_key"], &gotKey)
	if gotKey != key {
		t.Fatalf("idempotency_key = %q", gotKey)
	}

	if _, err := c.Refund(context.Background(), uuid.MustParse(tIntentID), 12345, "x", ""); err == nil {
		t.Fatal("refund without an idempotency key accepted")
	}
	if _, err := c.Refund(context.Background(), uuid.MustParse(tIntentID), 0, "x", key); err == nil {
		t.Fatal("zero refund accepted")
	}
	if len(*calls) != 1 {
		t.Fatalf("refused refunds still called payments: %d calls", len(*calls))
	}
}

func TestClient_StatusClassification(t *testing.T) {
	for status, want := range map[int]error{http.StatusBadGateway: ErrPaymentsUnavailable, http.StatusConflict: ErrRefused} {
		srv, _, priv := paymentsStub(t, func(recorded) (int, any) { return status, map[string]any{} })
		c, _ := NewTokenClient(srv.URL, "f1", priv)
		_, err := c.VerifyCallback(context.Background(), uuid.MustParse(tIntentID), "o", "p", "s", 1)
		if !errors.Is(err, want) {
			t.Fatalf("status %d: err = %v, want %v", status, err, want)
		}
	}
}

func TestLegacyClient_SendsOnlyTheInternalKey(t *testing.T) {
	srv, calls, _ := paymentsStub(t, echoIntent)
	c, err := NewInternalKeyClient(srv.URL, "legacy-key")
	if err != nil {
		t.Fatal(err)
	}
	if !c.LegacyAuth() {
		t.Fatal("legacy client does not report legacy auth")
	}
	if _, err := c.CreateIntent(context.Background(), CreateIntentInput{
		OrderID: tOrderID, PayerID: tUserID, PayeeID: uuid.New(), AmountMinor: 100, Method: "upi",
	}); err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	h := (*calls)[0].header
	if h.Get("X-Internal-Service-Key") != "legacy-key" || h.Get("X-Service-Authorization") != "" {
		t.Fatalf("legacy headers wrong")
	}
}

func TestClientFromEnv(t *testing.T) {
	_, priv, _ := servicetoken.GenerateKeypair()
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	c, err := ClientFromEnv(get(map[string]string{"FOOD_SERVICE_TOKEN_KEY": priv, "ENV": "production"}))
	if err != nil || c.LegacyAuth() {
		t.Fatalf("token key set: client=%v err=%v", c, err)
	}
	for _, env := range []string{"local", "dev", "development", "DEV"} {
		c, err := ClientFromEnv(get(map[string]string{"ENV": env, "INTERNAL_SERVICE_KEY": "k"}))
		if err != nil || !c.LegacyAuth() {
			t.Fatalf("ENV=%s without token key: want legacy client, got %v %v", env, c, err)
		}
	}
	for _, env := range []string{"", "prod", "production", "staging", "test", "qa"} {
		if _, err := ClientFromEnv(get(map[string]string{"ENV": env, "INTERNAL_SERVICE_KEY": "k"})); !errors.Is(err, ErrServiceTokenRequired) {
			t.Fatalf("ENV=%q without token key: want ErrServiceTokenRequired, got %v", env, err)
		}
	}
	if _, err := ClientFromEnv(get(map[string]string{"ENV": "dev"})); err == nil {
		t.Fatal("legacy mode with no internal key accepted")
	}
}
