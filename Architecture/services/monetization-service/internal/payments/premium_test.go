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

	"github.com/atpost/monetization-service/internal/premium"
	"github.com/atpost/shared/events"
	sharedkafka "github.com/atpost/shared/kafka"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

type recPremium struct {
	got []premium.Event
	err error
}

func (r *recPremium) ApplyPremiumPaymentEvent(_ context.Context, ev premium.Event) (premium.Outcome, error) {
	r.got = append(r.got, ev)
	return premium.OutcomeActivated, r.err
}

func premiumPayload(ref uuid.UUID, app string) map[string]any {
	return map[string]any{"id": uuid.NewString(), "payer_id": uuid.NewString(), "reference_type": premium.RefType,
		"reference_id": ref.String(), "amount_minor": 14900, "currency": "INR", "status": "succeeded", "application_id": app}
}

// Only a signed payments event for application premium and reference type
// platform_premium reaches the store; memberships' events never do, and
// premium's never reach the membership consumer.
func TestPremiumConsumerAppliesOnlyPremiumEvents(t *testing.T) {
	ref := uuid.New()
	with := func(mut func(map[string]any)) map[string]any {
		p := premiumPayload(ref, "premium")
		mut(p)
		return p
	}
	cases := []struct {
		name    string
		env     *events.EventEnvelope
		applied bool
		perm    bool
	}{
		{"ours", envelope(t, events.EventPaymentSucceeded, "e1", premiumPayload(ref, "premium")), true, false},
		{"failed", envelope(t, events.EventPaymentFailed, "e1f", premiumPayload(ref, "premium")), true, false},
		{"refunded", envelope(t, events.EventPaymentRefunded, "e1r", map[string]any{"id": uuid.NewString(),
			"reference_type": premium.RefType, "reference_id": ref.String(), "amount_minor": 14900, "status": "succeeded",
			"application_id": "premium"}), true, false},
		{"memberships application", envelope(t, events.EventPaymentSucceeded, "e2", premiumPayload(ref, "memberships")), false, false},
		{"no application stated", envelope(t, events.EventPaymentSucceeded, "e3", premiumPayload(ref, "")), false, false},
		{"membership reference type", envelope(t, events.EventPaymentSucceeded, "e4", with(func(p map[string]any) {
			p["reference_type"] = "creator_membership"
		})), false, false},
		{"refund failed is not consumed", envelope(t, events.EventPaymentRefundFailed, "e5", premiumPayload(ref, "premium")), false, false},
		{"no event id", envelope(t, events.EventPaymentSucceeded, "", premiumPayload(ref, "premium")), false, true},
		{"bad reference", envelope(t, events.EventPaymentSucceeded, "e6", with(func(p map[string]any) { p["reference_id"] = "x" })), false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := &recPremium{}
			err := NewPremiumHandlerForTest(a).Handle(context.Background(), tc.env)
			if tc.perm != (err != nil && sharedkafka.IsPermanent(err)) {
				t.Fatalf("err = %v, want permanent=%v", err, tc.perm)
			}
			if tc.applied != (len(a.got) == 1) {
				t.Fatalf("applied %d events, want applied=%v", len(a.got), tc.applied)
			}
			if tc.applied && a.got[0].PeriodID != ref {
				t.Fatalf("period id %s", a.got[0].PeriodID)
			}
		})
	}
	// The membership consumer ignores a premium event.
	m := &recApplier{}
	if err := NewHandlerForTest(m).Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "x", premiumPayload(ref, "premium"))); err != nil || len(m.got) != 0 {
		t.Fatalf("membership consumer took a premium event: %v %d", err, len(m.got))
	}
}

func TestPremiumConsumerRetriesATransientError(t *testing.T) {
	a := &recPremium{err: errors.New("db down")}
	err := NewPremiumHandlerForTest(a).Handle(context.Background(), envelope(t, events.EventPaymentSucceeded, "e1", premiumPayload(uuid.New(), "premium")))
	if err == nil || sharedkafka.IsPermanent(err) {
		t.Fatalf("a transient error must be retried: %v", err)
	}
}

func TestPremiumClientFromEnvNeedsTheTokenKey(t *testing.T) {
	if _, err := PremiumClientFromEnv(func(string) string { return "" }); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("got %v", err)
	}
}

// The wire: application premium, reference platform_premium:<id>, the row's
// amount in paise, a service token scoped to platform_premium.
func TestPremiumClientOpensTheIntentForThePremiumApplication(t *testing.T) {
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	verifier := servicetoken.NewVerifier(servicetoken.AudiencePayments)
	if err := verifier.RegisterBase64("monetization-service", "m1", pub, []string{servicetoken.OpIntentCreate}, []string{servicetoken.RefPlatformPremium}); err != nil {
		t.Fatal(err)
	}
	periodID, payer := uuid.New(), uuid.New()
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok := strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer ")
		if _, err := verifier.Verify(tok, servicetoken.OpIntentCreate, servicetoken.RefPlatformPremium); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"` + uuid.NewString() + `","status":"created","amount_minor":14900,"currency":"INR",
			"reference_type":"platform_premium","reference_id":"` + periodID.String() + `","provider_ref":"order_9",
			"client_session":{"provider":"razorpay","order_id":"order_9","key_id":"rzp_test"}}}`))
	}))
	defer srv.Close()
	c, err := NewPremiumTokenClient(srv.URL, "m1", priv)
	if err != nil {
		t.Fatal(err)
	}
	in, err := c.CreatePremiumIntent(context.Background(), PremiumIntentInput{PeriodID: periodID, PayerID: payer, AmountMinor: 14_900, Currency: "INR"})
	if err != nil {
		t.Fatal(err)
	}
	if body["application_id"] != "premium" || body["reference_type"] != "platform_premium" || body["reference_id"] != periodID.String() ||
		body["idempotency_key"] != "platform_premium:"+periodID.String() || body["amount_minor"] != float64(14900) ||
		body["payer_id"] != payer.String() || body["payee_id"] != PremiumPayeeID.String() {
		t.Fatalf("intent body = %v", body)
	}
	if in.ClientSession == nil || in.ClientSession.OrderID != "order_9" {
		t.Fatalf("session %+v", in.ClientSession)
	}
}
