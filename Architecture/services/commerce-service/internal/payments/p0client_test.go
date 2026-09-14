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

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/paymentsclient"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// TestP0Client_WireContract pins what commerce sends through the shared
// client: a commerce-service token for the `order` reference type, the
// order-derived idempotency key, integer paise, the application id, and a
// client_session relayed as exactly the three public keys.
func TestP0Client_WireContract(t *testing.T) {
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := verifier.RegisterBase64("commerce-service", "c1", pub,
		[]string{servicetoken.OpIntentCreate, servicetoken.OpIntentRead, servicetoken.OpRefundCreate},
		[]string{servicetoken.RefOrder}); err != nil {
		t.Fatal(err)
	}
	orderID, intentID := uuid.New(), uuid.New()
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		op := servicetoken.OpIntentRead
		if strings.HasSuffix(r.URL.Path, "/refund") {
			op = servicetoken.OpRefundCreate
		} else if r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/intents") {
			op = servicetoken.OpIntentCreate
		}
		if _, err := verifier.Verify(strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer "), op, servicetoken.RefOrder); err != nil {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if r.Header.Get("X-Internal-Service-Key") != "" {
			t.Error("the internal key rode alongside a token")
		}
		raw, _ := io.ReadAll(r.Body)
		body := map[string]any{}
		_ = json.Unmarshal(raw, &body)
		bodies = append(bodies, body)
		out := map[string]any{"id": intentID, "status": "pending", "amount_minor": 90000, "reference_type": "order",
			"reference_id": orderID, "provider_ref": "order_RZP1",
			"client_session": map[string]string{"provider": "razorpay", "order_id": "order_RZP1", "key_id": "rzp_test_pub", "key_secret": "SECRET"}}
		if op == servicetoken.OpRefundCreate {
			out = map[string]any{"command_id": uuid.New(), "intent_id": intentID, "amount_minor": 90000, "status": "pending"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": out})
	}))
	defer srv.Close()

	c, err := NewP0Client(srv.URL, DefaultApplicationID, "c1", priv)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := c.CreateIntent(context.Background(), CreateIntentInput{OrderID: orderID, PayerID: uuid.New(), PayeeID: uuid.New(), AmountMinor: money.Paise(90000), Method: "upi"})
	if err != nil {
		t.Fatalf("CreateIntent: %v", err)
	}
	if intent.AmountMinor != money.Paise(90000) || len(intent.ClientSession) != 3 || intent.ClientSession["key_id"] != "rzp_test_pub" {
		t.Fatalf("intent = %+v", intent)
	}
	if _, ok := intent.ClientSession["key_secret"]; ok {
		t.Fatal("a non-public session key was relayed")
	}
	if _, err := c.Refund(context.Background(), intentID, money.Paise(90000), "cancel:x", "cancel:"+orderID.String()); err != nil {
		t.Fatalf("Refund: %v", err)
	}

	create, refund := bodies[0], bodies[1]
	for k, want := range map[string]any{"reference_type": "order", "reference_id": orderID.String(), "currency": "INR",
		"idempotency_key": "order:" + orderID.String(), "amount_minor": float64(90000), "application_id": "mstore"} {
		if create[k] != want {
			t.Errorf("create %s = %v, want %v", k, create[k], want)
		}
	}
	for k, want := range map[string]any{"idempotency_key": "cancel:" + orderID.String(), "amount_minor": float64(90000), "application_id": "mstore"} {
		if refund[k] != want {
			t.Errorf("refund %s = %v, want %v", k, refund[k], want)
		}
	}
}

func TestP0Client_LegacyOnlyWhenAllowedAndApplicationIDValidated(t *testing.T) {
	if _, err := NewInternalKeyClient("http://payments", DefaultApplicationID, "k", false); !errors.Is(err, paymentsclient.ErrServiceTokenRequired) {
		t.Fatalf("legacy outside local: err = %v", err)
	}
	c, err := NewInternalKeyClient("http://payments", DefaultApplicationID, "k", true)
	if err != nil || !c.LegacyAuth() {
		t.Fatalf("legacy in local: client=%v err=%v", c, err)
	}
	if _, err := NewInternalKeyClient("http://payments", "MStore", "k", true); !errors.Is(err, paymentsclient.ErrInvalidApplicationID) {
		t.Fatalf("bad application id: err = %v", err)
	}
	get := func(v string) func(string) string { return func(string) string { return v } }
	if ApplicationIDFromEnv(get("")) != "mstore" || ApplicationIDFromEnv(get(" mstore_b2b ")) != "mstore_b2b" {
		t.Fatal("ApplicationIDFromEnv default/trim wrong")
	}
}
