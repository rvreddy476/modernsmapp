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

	"github.com/atpost/monetization-service/internal/membership"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

type recApplier struct {
	got []membership.Event
	err error
}

func (r *recApplier) ApplyMembershipPaymentEvent(_ context.Context, ev membership.Event) (membership.Applied, error) {
	r.got = append(r.got, ev)
	return membership.Applied{Decision: membership.Decision{Outcome: membership.OutcomeActivated}}, r.err
}

func envelope(t *testing.T, typ, eventID string, payload map[string]any) *events.EventEnvelope {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &events.EventEnvelope{EventID: eventID, EventType: typ, Payload: raw}
}

func succeededPayload(ref uuid.UUID, app string) map[string]any {
	return map[string]any{"id": uuid.NewString(), "payer_id": uuid.NewString(), "reference_type": membership.RefType,
		"reference_id": ref.String(), "amount_minor": 4900, "currency": "INR", "status": "succeeded", "application_id": app}
}

func TestConsumerAppliesOnlyMembershipEvents(t *testing.T) {
	ref := uuid.New()
	cases := []struct {
		name    string
		env     *events.EventEnvelope
		applied bool
		perm    bool
	}{
		{"ours", envelope(t, events.EventPaymentSucceeded, "e1", succeededPayload(ref, "memberships")), true, false},
		{"another application", envelope(t, events.EventPaymentSucceeded, "e2", succeededPayload(ref, "dating")), false, false},
		{"no application stated", envelope(t, events.EventPaymentSucceeded, "e3", succeededPayload(ref, "")), false, false},
		{"another reference type", envelope(t, events.EventPaymentSucceeded, "e4", func() map[string]any {
			p := succeededPayload(ref, "memberships")
			p["reference_type"] = "dating_premium"
			return p
		}()), false, false},
		{"unknown event type", envelope(t, "post.created", "e5", map[string]any{}), false, false},
		{"no event id", envelope(t, events.EventPaymentSucceeded, "", succeededPayload(ref, "memberships")), false, true},
		{"bad reference", envelope(t, events.EventPaymentSucceeded, "e6", func() map[string]any {
			p := succeededPayload(ref, "memberships")
			p["reference_id"] = "not-a-uuid"
			return p
		}()), false, true},
		{"malformed payload", &events.EventEnvelope{EventID: "e7", EventType: events.EventPaymentSucceeded, Payload: json.RawMessage(`{"amount_minor":"x"`)}, false, true},
		{"refund failed", envelope(t, events.EventPaymentRefundFailed, "e8", map[string]any{"id": uuid.NewString(), "intent_id": uuid.NewString(),
			"reference_type": membership.RefType, "reference_id": ref.String(), "amount_minor": 4900, "reason_code": "provider_error",
			"status": "needs_attention", "application_id": "memberships"}), true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &recApplier{}
			err := NewHandlerForTest(a).Handle(context.Background(), tc.env)
			if tc.perm != (err != nil && sharedkafka.IsPermanent(err)) {
				t.Fatalf("err = %v, want permanent=%v", err, tc.perm)
			}
			if tc.applied != (len(a.got) == 1) {
				t.Fatalf("applied %d events, want applied=%v", len(a.got), tc.applied)
			}
			if tc.applied && a.got[0].PaymentID != ref {
				t.Fatalf("payment id %s", a.got[0].PaymentID)
			}
		})
	}
}

func TestConsumerRetriesATransientApplyError(t *testing.T) {
	a := &recApplier{err: errors.New("db down")}
	err := NewHandlerForTest(a).Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", succeededPayload(uuid.New(), "memberships")))
	if err == nil || sharedkafka.IsPermanent(err) {
		t.Fatalf("a transient error must be retried, not parked: %v", err)
	}
}

func TestClientFromEnvNeedsTheTokenKey(t *testing.T) {
	if _, err := ClientFromEnv(func(string) string { return "" }); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
	_, priv, _ := servicetoken.GenerateKeypair()
	env := map[string]string{EnvTokenKey: priv, EnvTokenKID: "m1", EnvPayeeID: "not-a-uuid"}
	if _, err := ClientFromEnv(func(k string) string { return env[k] }); err == nil {
		t.Fatal("a malformed payee must be refused")
	}
}

// The wire: application memberships, reference creator_membership:<id>, the
// row's amount, a service token for payments — never a float, never the key.
func TestClientOpensTheIntentForTheMembershipApplication(t *testing.T) {
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := verifier.RegisterBase64("monetization-service", "m1", pub, []string{servicetoken.OpIntentCreate, servicetoken.OpRefundCreate}, []string{membership.RefType}); err != nil {
		t.Fatal(err)
	}
	paymentID, payer := uuid.New(), uuid.New()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer ")
		op := servicetoken.OpIntentCreate
		if strings.HasSuffix(r.URL.Path, "/refund") {
			op = servicetoken.OpRefundCreate
		}
		if _, err := verifier.Verify(tok, op, membership.RefType); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		if r.Header.Get("X-Internal-Service-Key") != "" {
			http.Error(w, "legacy key sent", http.StatusBadRequest)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/refund") {
			_, _ = w.Write([]byte(`{"data":{"command_id":"` + uuid.NewString() + `","amount_minor":4900,"status":"requested"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"id":"` + uuid.NewString() + `","status":"created","amount_minor":4900,"currency":"INR",
			"reference_type":"creator_membership","reference_id":"` + paymentID.String() + `","provider_ref":"order_1",
			"client_session":{"provider":"razorpay","order_id":"order_1","key_id":"rzp_test","key_secret":"MUST-NOT-PASS","merchant_display_name":"Momentum Merchant"}}}`))
	}))
	defer srv.Close()
	c, err := NewTokenClient(srv.URL, "m1", priv, uuid.Nil)
	if err != nil {
		t.Fatal(err)
	}
	in, err := c.CreateIntent(context.Background(), IntentInput{PaymentID: paymentID, PayerID: payer, AmountMinor: 4_900, Currency: "INR"})
	if err != nil {
		t.Fatal(err)
	}
	if body["application_id"] != "memberships" || body["reference_type"] != "creator_membership" || body["reference_id"] != paymentID.String() ||
		body["idempotency_key"] != "creator_membership:"+paymentID.String() || body["amount_minor"] != float64(4900) || body["payer_id"] != payer.String() {
		t.Fatalf("intent body = %v", body)
	}
	if _, ok := body["amount"]; ok {
		t.Fatal("a float amount was sent")
	}
	if in.ClientSession == nil || in.ClientSession.OrderID != "order_1" || in.ClientSession.MerchantDisplayName != "Momentum Merchant" {
		t.Fatalf("session = %+v", in.ClientSession)
	}
	if raw, _ := json.Marshal(in.ClientSession); strings.Contains(string(raw), "MUST-NOT-PASS") {
		t.Fatal("a key secret was relayed")
	}
	if _, err := c.Refund(context.Background(), uuid.New(), 4_900, "late", "membership-late:"+paymentID.String()); err != nil {
		t.Fatal(err)
	}
	if body["application_id"] != "memberships" || body["idempotency_key"] != "membership-late:"+paymentID.String() || body["amount_minor"] != float64(4900) {
		t.Fatalf("refund body = %v", body)
	}
	// A mismatched echo is refused.
	if _, err := c.CreateIntent(context.Background(), IntentInput{PaymentID: uuid.New(), PayerID: payer, AmountMinor: 4_900}); err == nil {
		t.Fatal("an intent echoing another reference was accepted")
	}
}
