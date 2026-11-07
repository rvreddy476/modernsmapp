package postgres

import "testing"

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
		{"a late capture already refunding: the late branch again (its key dedupes)",
			captureOrder{Status: "expired", PaymentStatus: "refund_pending", Expired: true, LateCaptureRefunded: true}, ev(intentA), captureLate},
		{"a late capture refunded: not the order's own payment, so not a redelivery",
			captureOrder{Status: "expired", PaymentStatus: "refunded", Expired: true, LateCaptureRefunded: true}, ev(intentB), captureLate},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideCapture(tc.order, tc.event); got != tc.want {
				t.Fatalf("decideCapture(%+v, intent %q) = %d, want %d", tc.order, tc.event.IntentID, got, tc.want)
			}
		})
	}
}

func TestOwnCaptureApplied(t *testing.T) {
	for _, tc := range []struct {
		status string
		late   bool
		want   bool
	}{
		{"paid", false, true},
		{"partially_refunded", false, true},
		{"refund_pending", false, true},
		{"refunded", false, true},
		{"refund_pending", true, false},
		{"refunded", true, false},
		// A late-capture marker never hides a payment that did land.
		{"paid", true, true},
		{"pending", false, false},
		{"processing", false, false},
		{"failed", false, false},
		{"", false, false},
	} {
		if got := ownCaptureApplied(tc.status, tc.late); got != tc.want {
			t.Errorf("ownCaptureApplied(%q, late=%v) = %v, want %v", tc.status, tc.late, got, tc.want)
		}
	}
}
