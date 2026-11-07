package postgres

import (
	"errors"
	"strings"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// A payment.succeeded for an order whose own payment already landed is never
// a second confirmation and never a second refund of the order. The same
// payment again is a no-op; a different payment is owed back but not booked
// against the order. The bug this pins: a partially refunded order took the
// late-capture branch and refunded its whole total again.
func TestDecideCapture(t *testing.T) {
	const intentA, intentB = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	ev := func(intent string) PaymentEvent { return PaymentEvent{IntentID: intent} }

	for _, tc := range []struct {
		name  string
		order captureOrder
		event PaymentEvent
		want  captureVerdict
	}{
		{"a payable order is paid",
			captureOrder{Status: "payment_pending", PaymentStatus: "pending", BoundIntent: intentA}, ev(intentA), captureApplies},
		{"a COD order confirmed at checkout is still payable",
			captureOrder{Status: "confirmed", PaymentStatus: "pending"}, ev(intentA), captureApplies},

		{"paid, same intent: redelivery",
			captureOrder{Status: "confirmed", PaymentStatus: "paid", BoundIntent: intentA}, ev(intentA), captureRedelivered},
		{"partially refunded, same intent: redelivery, not a late capture",
			captureOrder{Status: "delivered", PaymentStatus: "partially_refunded", BoundIntent: intentA}, ev(intentA), captureRedelivered},
		{"partially refunded, intent not stated: cannot be told apart, so redelivery",
			captureOrder{Status: "delivered", PaymentStatus: "partially_refunded", BoundIntent: intentA}, ev(""), captureRedelivered},
		{"paid, no intent bound: cannot be told apart, so redelivery",
			captureOrder{Status: "confirmed", PaymentStatus: "paid"}, ev(intentB), captureRedelivered},
		{"intent ids are compared trimmed and case-insensitively",
			captureOrder{Status: "confirmed", PaymentStatus: "paid", BoundIntent: "aaaaaaaa-1111-4111-8111-111111111111"},
			ev(" AAAAAAAA-1111-4111-8111-111111111111 "), captureRedelivered},
		{"paid, cancelled and refunding: redelivery, not a second refund",
			captureOrder{Status: "cancelled", PaymentStatus: "refund_pending", BoundIntent: intentA}, ev(intentA), captureRedelivered},
		{"paid then fully refunded: redelivery",
			captureOrder{Status: "cancelled", PaymentStatus: "refunded", BoundIntent: intentA}, ev(intentA), captureRedelivered},

		{"paid, another intent: duplicate",
			captureOrder{Status: "confirmed", PaymentStatus: "paid", BoundIntent: intentA}, ev(intentB), captureDuplicate},
		{"partially refunded, another intent: duplicate",
			captureOrder{Status: "delivered", PaymentStatus: "partially_refunded", BoundIntent: intentA}, ev(intentB), captureDuplicate},
		{"refunding after a cancel, another intent: duplicate",
			captureOrder{Status: "cancelled", PaymentStatus: "refund_pending", BoundIntent: intentA}, ev(intentB), captureDuplicate},

		{"expired before payment: late capture",
			captureOrder{Status: "expired", PaymentStatus: "pending", Expired: true, BoundIntent: intentA}, ev(intentA), captureLate},
		{"reservation expired, status not yet moved: late capture",
			captureOrder{Status: "payment_pending", PaymentStatus: "pending", Expired: true}, ev(intentA), captureLate},
		{"cancelled before payment: late capture",
			captureOrder{Status: "cancelled", PaymentStatus: "pending", BoundIntent: intentA}, ev(intentA), captureLate},
		// A recorded late capture is money that landed on the order. These two
		// rows used to say captureLate, which re-ran the late branch: its key
		// dropped the second payment, and the status update moved a refunded
		// order back to refund_pending.
		{"a late capture already refunding, no intent bound: cannot be told apart, so redelivery",
			captureOrder{Status: "expired", PaymentStatus: "refund_pending", Expired: true, LateCaptureRefunded: true}, ev(intentA), captureRedelivered},
		{"a late capture refunded, no intent bound: redelivery, never back to refund_pending",
			captureOrder{Status: "expired", PaymentStatus: "refunded", Expired: true, LateCaptureRefunded: true}, ev(intentB), captureRedelivered},
		{"a late capture refunded, the same intent again: redelivery",
			captureOrder{Status: "expired", PaymentStatus: "refunded", Expired: true, LateCaptureRefunded: true, BoundIntent: intentA},
			ev(intentA), captureRedelivered},
		{"a late capture refunding, another intent: duplicate, refunded by payments",
			captureOrder{Status: "expired", PaymentStatus: "refund_pending", Expired: true, LateCaptureRefunded: true, BoundIntent: intentA},
			ev(intentB), captureDuplicate},
		{"a late capture refunded, another intent: duplicate",
			captureOrder{Status: "cancelled", PaymentStatus: "refunded", LateCaptureRefunded: true, BoundIntent: intentA},
			ev(intentB), captureDuplicate},

		// The first capture to land is the order's payment, whichever attempt
		// it came from. These went to the mismatch dead-letter queue.
		{"payable, another attempt's intent lands first: it applies",
			captureOrder{Status: "payment_pending", PaymentStatus: "pending", BoundIntent: intentB}, ev(intentA), captureApplies},
		{"expired, another attempt's intent lands first: late capture on the order",
			captureOrder{Status: "expired", PaymentStatus: "pending", Expired: true, BoundIntent: intentB}, ev(intentA), captureLate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideCapture(tc.order, tc.event); got != tc.want {
				t.Fatalf("decideCapture(%+v, intent %q) = %d, want %d", tc.order, tc.event.IntentID, got, tc.want)
			}
		})
	}
}

func TestCaptureLanded(t *testing.T) {
	for _, tc := range []struct {
		status string
		late   bool
		want   bool
	}{
		{"paid", false, true},
		{"partially_refunded", false, true},
		{"refund_pending", false, true},
		{"refunded", false, true},
		// A recorded late capture landed too (these were false: its repeat
		// re-ran the late branch).
		{"refund_pending", true, true},
		{"refunded", true, true},
		{"pending", true, true},
		// A late-capture marker never hides a payment that did land.
		{"paid", true, true},
		{"pending", false, false},
		{"processing", false, false},
		{"failed", false, false},
		{"", false, false},
	} {
		if got := captureLanded(tc.status, tc.late); got != tc.want {
			t.Errorf("captureLanded(%q, late=%v) = %v, want %v", tc.status, tc.late, got, tc.want)
		}
	}
}

// One late-capture refund per order AND intent: two late captures on one
// order were one key, and the second payment was never refunded.
func TestLateCaptureKey(t *testing.T) {
	order := uuid.MustParse("99999999-9999-4999-8999-999999999999")
	a := lateCaptureKey(order, "11111111-1111-4111-8111-111111111111")
	b := lateCaptureKey(order, "22222222-2222-4222-8222-222222222222")
	if a == b {
		t.Fatalf("two intents share the late-capture key %q", a)
	}
	if a != "late-capture:"+order.String()+":11111111-1111-4111-8111-111111111111" {
		t.Fatalf("key = %q", a)
	}
	if again := lateCaptureKey(order, " 11111111-1111-4111-8111-111111111111 "); again != a {
		t.Fatalf("the same intent gave two keys: %q, %q", a, again)
	}
	if upper := lateCaptureKey(order, "AAAAAAAA-1111-4111-8111-111111111111"); upper != lateCaptureKey(order, "aaaaaaaa-1111-4111-8111-111111111111") {
		t.Fatalf("intent case changed the key: %q", upper)
	}
	if none := lateCaptureKey(order, ""); none != "late-capture:"+order.String() {
		t.Fatalf("no intent: key = %q, want the order-wide key", none)
	}
}

func TestCaptureIntentToBind(t *testing.T) {
	const a, b = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		name            string
		bound, incoming string
		wantBind        bool
		wantErr         bool
	}{
		{"nothing bound: bind the paying intent", "", a, true, false},
		{"another attempt's intent: rebind to the one that paid", b, a, true, false},
		{"already bound", a, a, false, false},
		{"already bound, surrounding blanks", a, " 11111111-1111-4111-8111-111111111111 ", false, false},
		{"already bound, other case", "aaaaaaaa-1111-4111-8111-111111111111", "AAAAAAAA-1111-4111-8111-111111111111", false, false},
		{"the event states no intent: keep the binding", a, "", false, false},
		{"not a payment intent id", "", "pay_123", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, ok, err := captureIntentToBind(tc.bound, tc.incoming)
			if (err != nil) != tc.wantErr || ok != tc.wantBind {
				t.Fatalf("captureIntentToBind(%q, %q) = %v, %v, %v; want bind %v, error %v",
					tc.bound, tc.incoming, id, ok, err, tc.wantBind, tc.wantErr)
			}
			if tc.wantErr && !errors.Is(err, ErrAmountMismatch) {
				t.Fatalf("err = %v, want ErrAmountMismatch (poison, not a retry)", err)
			}
			if ok && id.String() != strings.TrimSpace(tc.incoming) {
				t.Fatalf("bound %s, want %s", id, tc.incoming)
			}
		})
	}
}

// What payments-service needs to refund the duplicate: the duplicate intent,
// the order's own intent, the order, the amount and currency.
func TestDuplicateCapturePayload(t *testing.T) {
	order, payer := uuid.New(), uuid.New()
	p := duplicateCapturePayload(PaymentEvent{
		OrderID: order, IntentID: " 22222222-2222-4222-8222-222222222222 ", AmountMinor: money.Paise(118000),
		Currency: "INR", PayerID: payer,
	}, "11111111-1111-4111-8111-111111111111")
	want := events.CommerceOrderDuplicateCapturePayload{
		OrderID: order.String(), IntentID: "22222222-2222-4222-8222-222222222222",
		BoundIntentID: "11111111-1111-4111-8111-111111111111", PayerID: payer.String(),
		AmountMinor: 118000, Currency: "INR", RefundDueMinor: 118000,
		Reason: "a second payment for an order that is already paid",
	}
	if p != want {
		t.Fatalf("payload = %+v, want %+v", p, want)
	}
	if p := duplicateCapturePayload(PaymentEvent{OrderID: order}, ""); p.PayerID != "" {
		t.Fatalf("no payer: payer_id = %q, want empty", p.PayerID)
	}
}
