package postgres

import "testing"

// A payment.failed fails an order only when it is the order's live attempt.
// The bug this pins: any payment.failed for the order failed it, so a retry's
// order was failed by the earlier attempt's late failure, releasing the stock
// and coupon the retry had just re-held while the retry's own intent could
// still capture.
func TestDecideFailure(t *testing.T) {
	const intentA, intentB = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	ev := func(intent string) PaymentEvent { return PaymentEvent{IntentID: intent} }
	pending := func(bound string) captureOrder {
		return captureOrder{Status: "payment_pending", PaymentStatus: "pending", BoundIntent: bound}
	}

	for _, tc := range []struct {
		name  string
		order captureOrder
		event PaymentEvent
		want  failureVerdict
	}{
		// The order's own attempt: as before.
		{"the bound intent failed", pending(intentA), ev(intentA), failureApplies},
		{"the bound intent failed while processing",
			captureOrder{Status: "payment_pending", PaymentStatus: "processing", BoundIntent: intentA}, ev(intentA), failureApplies},
		{"intent ids are compared trimmed and case-insensitively",
			pending("aaaaaaaa-1111-4111-8111-111111111111"), ev(" AAAAAAAA-1111-4111-8111-111111111111 "), failureApplies},
		{"the event states no intent: cannot be told apart, so it applies", pending(intentA), ev(""), failureApplies},
		{"nothing bound and no intent stated: cannot be told apart, so it applies", pending(""), ev(""), failureApplies},

		// Another attempt's intent: the order waits on its own.
		{"another attempt's intent failed while the bound one is open", pending(intentA), ev(intentB), failureOtherIntent},
		{"the earlier attempt's failure after a retry bound a fresh intent", pending(intentB), ev(intentA), failureOtherIntent},
		{"nothing bound (a retry is opening its intent): an earlier attempt's failure holds",
			pending(""), ev(intentA), failureUnbound},
		{"nothing bound, blank bound id", pending("   "), ev(intentA), failureUnbound},

		// A capture already landed: a failure never undoes it, on any intent.
		{"paid on the bound intent, then its failure arrives out of order",
			captureOrder{Status: "confirmed", PaymentStatus: "paid", BoundIntent: intentA}, ev(intentA), failureAfterCapture},
		{"paid on one attempt, another attempt fails",
			captureOrder{Status: "confirmed", PaymentStatus: "paid", BoundIntent: intentA}, ev(intentB), failureAfterCapture},
		{"partially refunded", captureOrder{Status: "delivered", PaymentStatus: "partially_refunded", BoundIntent: intentA},
			ev(intentA), failureAfterCapture},
		{"refunding after a cancel", captureOrder{Status: "cancelled", PaymentStatus: "refund_pending", BoundIntent: intentA},
			ev(intentA), failureAfterCapture},
		{"refunded", captureOrder{Status: "cancelled", PaymentStatus: "refunded", BoundIntent: intentA},
			ev(intentA), failureAfterCapture},
		// A late capture recorded on an order whose status never left
		// payment_pending: failing it would overwrite refund_pending and
		// release a hold the refund is accounting for.
		{"a late capture is being refunded, the status still payment_pending",
			captureOrder{Status: "payment_pending", PaymentStatus: "refund_pending", LateCaptureRefunded: true, BoundIntent: intentA},
			ev(intentA), failureAfterCapture},
		{"a late capture recorded, payment status not yet moved",
			captureOrder{Status: "payment_pending", PaymentStatus: "pending", LateCaptureRefunded: true, BoundIntent: intentA},
			ev(intentA), failureAfterCapture},

		// Not waiting on a payment.
		{"already failed: a redelivery under another event id",
			captureOrder{Status: "payment_failed", PaymentStatus: "failed", BoundIntent: intentA}, ev(intentA), failureNotPending},
		{"expired", captureOrder{Status: "expired", PaymentStatus: "pending", Expired: true, BoundIntent: intentA},
			ev(intentA), failureNotPending},
		{"cancelled before payment", captureOrder{Status: "cancelled", PaymentStatus: "pending", BoundIntent: intentA},
			ev(intentA), failureNotPending},
		{"a COD order confirmed at checkout", captureOrder{Status: "confirmed", PaymentStatus: "pending", BoundIntent: intentA},
			ev(intentA), failureNotPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := decideFailure(tc.order, tc.event); got != tc.want {
				t.Fatalf("decideFailure(%+v, intent %q) = %s, want %s", tc.order, tc.event.IntentID, got, tc.want)
			}
		})
	}
}

func TestFailureVerdictString(t *testing.T) {
	seen := map[string]failureVerdict{}
	for _, v := range []failureVerdict{failureApplies, failureNotPending, failureAfterCapture, failureOtherIntent, failureUnbound} {
		s := v.String()
		if s == "unknown" || s == "" {
			t.Fatalf("verdict %d has no name", v)
		}
		if prev, ok := seen[s]; ok {
			t.Fatalf("verdicts %d and %d share the name %q", prev, v, s)
		}
		seen[s] = v
	}
}
