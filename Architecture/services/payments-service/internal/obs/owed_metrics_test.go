package obs

import "testing"

// The owed-capture instruments are registered, move when called, and keep a
// bounded label set (com-2, com-15).
func TestOwedCaptureInstruments(t *testing.T) {
	SetOwedCaptures(4, 1, 7200)
	if open, parked, age := OwedCaptures(); open != 4 || parked != 1 || age != 7200 {
		t.Fatalf("gauges = %v/%v/%v, want 4/1/7200", open, parked, age)
	}
	SetOwedCaptures(0, 0, -3) // clock skew between the pod and the database
	if _, _, age := OwedCaptures(); age != 0 {
		t.Fatalf("oldest age = %v for a negative reading, want 0", age)
	}

	for _, reason := range []string{"retryable", "provider_rejected", "no_payment_id", "refund_list_failed"} {
		before := OwedRefundFailedCount(reason)
		OwedRefundFailed(reason)
		if d := OwedRefundFailedCount(reason) - before; d != 1 {
			t.Fatalf("failures{%s} moved by %v, want 1", reason, d)
		}
	}
	other := OwedRefundFailedCount("other")
	OwedRefundFailed("pay_ABC123 refused for buyer 42") // never a free-form label
	if d := OwedRefundFailedCount("other") - other; d != 1 {
		t.Fatalf("an unknown reason was not folded into other (moved by %v)", d)
	}

	for _, outcome := range OwedRefundOutcomes {
		before := OwedRefundIssuedCount(outcome)
		OwedRefundIssued(outcome)
		if d := OwedRefundIssuedCount(outcome) - before; d != 1 {
			t.Fatalf("issued{%s} moved by %v, want 1", outcome, d)
		}
	}
	otherIssued := OwedRefundIssuedCount("other")
	OwedRefundIssued("rfnd_XYZ")
	if d := OwedRefundIssuedCount("other") - otherIssued; d != 1 {
		t.Fatalf("an unknown outcome was not folded into other (moved by %v)", d)
	}
}
