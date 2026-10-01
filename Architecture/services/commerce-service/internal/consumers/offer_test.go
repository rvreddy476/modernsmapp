package consumers

import (
	"context"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/atpost/shared/paymentevents"
)

// sampleOfferSucceeded is payment.succeeded for an offer capture exactly as
// payments-service writes it (store/postgres/offers.go offerSucceededPayload:
// the intent's own keys, amount_minor = the ORDER value, plus the five offer
// keys).
const sampleOfferSucceeded = `{
  "id":"7d0d3b8e-3f5a-4a31-9a66-0f3c5d2b9e11","payer_id":"5b8f8c55-4c7e-4ad4-9a0e-6f2c0f6b3a21",
  "payee_id":"0a0e7c55-2c3e-4ad4-9a0e-6f2c0f6b3a22","reference_type":"order",
  "reference_id":"9c1d2e3f-4a5b-4c6d-8e7f-001122334455","amount_minor":134800,"currency":"INR",
  "method":"card","status":"succeeded","provider_ref":"order_RZP001","application_id":"mstore",
  "captured_minor":121320,"offer_id":"3f2a1b0c-9d8e-4f7a-8b6c-5d4e3f2a1b0c",
  "offer_title":"10% off with HDFC credit cards","offer_discount_minor":13480,"offer_funded_by":"bank"
}`

func TestSucceededOffer_ReadsPaymentsOfferKeys(t *testing.T) {
	env := &events.EventEnvelope{EventID: "e1", EventType: events.EventPaymentSucceeded, Payload: []byte(sampleOfferSucceeded)}
	o := SucceededOffer(env)
	if o == nil {
		t.Fatal("no offer read from an offer capture")
	}
	if o.OfferID != "3f2a1b0c-9d8e-4f7a-8b6c-5d4e3f2a1b0c" || o.Title != "10% off with HDFC credit cards" ||
		o.DiscountMinor != 13480 || o.CapturedMinor != 121320 || o.FundedBy != "bank" {
		t.Fatalf("read %+v", o)
	}
	// The shared decoder still reads amount_minor as the order value — the
	// capture check commerce runs is unchanged.
	var ev paymentevents.Succeeded
	if err := paymentevents.Dispatch(context.Background(), env, captureSucceeded{out: &ev}); err != nil {
		t.Fatal(err)
	}
	if ev.AmountMinor != 134800 {
		t.Fatalf("amount_minor decoded as %d, want the order value 134800", ev.AmountMinor)
	}
}

type captureSucceeded struct {
	paymentevents.NopHandler
	out *paymentevents.Succeeded
}

func (c captureSucceeded) OnSucceeded(_ context.Context, _ *events.EventEnvelope, ev paymentevents.Succeeded) error {
	*c.out = ev
	return nil
}

func TestSucceededOffer_AbsentWithoutAnOffer(t *testing.T) {
	for _, payload := range []string{
		`{"id":"x","reference_type":"order","reference_id":"y","amount_minor":100,"currency":"INR"}`,
		`{"amount_minor":100,"offer_id":"","captured_minor":90,"offer_discount_minor":10}`,
		`{"amount_minor":100,"offer_id":"abc","offer_discount_minor":10}`,
		`{"amount_minor":100,"offer_id":"abc","captured_minor":"ninety","offer_discount_minor":10}`,
		``,
	} {
		env := &events.EventEnvelope{EventID: "e", EventType: events.EventPaymentSucceeded, Payload: []byte(payload)}
		if o := SucceededOffer(env); o != nil {
			t.Fatalf("payload %s: read an offer %+v", payload, o)
		}
	}
	if SucceededOffer(nil) != nil {
		t.Fatal("nil envelope")
	}
}
