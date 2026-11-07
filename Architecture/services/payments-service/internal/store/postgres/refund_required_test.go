package postgres

import (
	"regexp"
	"strings"
	"testing"
)

// A refund books against the intent only when it refunds the payment that
// settled the intent. A refund of a recorded owed capture settles that row; a
// refund of any other payment is refused. The DB half is proven in
// service/refundrequired_rr_integration_test.go.
func TestClassifyRefundTarget(t *testing.T) {
	const none, dup, late = "", reasonDuplicateCapture, reasonLateCaptureAfterFailed
	for _, tc := range []struct {
		name            string
		settled, refund string
		owed            string // the owed row's reason, "" for none
		want            refundTarget
	}{
		{"the settling payment", "pay_A", "pay_A", none, refundOfIntent},
		{"the settling payment, surrounding blanks", " pay_A ", "pay_A", none, refundOfIntent},
		{"the settling payment even if an owed row names it", "pay_A", "pay_A", dup, refundOfIntent},
		{"the settling payment even if a late-capture row names it", "pay_A", "pay_A", late, refundOfIntent},
		{"no payment id on the event (stub refunds)", "pay_A", "", none, refundOfIntent},
		{"a recorded duplicate capture", "pay_A", "pay_B", dup, refundOfOwedCapture},
		{"a late capture on a failed intent with no payment", "", "pay_B", late, refundOfOwedCapture},
		{"a payment nobody recorded", "pay_A", "pay_B", none, refundOfUnknownPayment},
		{"legacy intent with no payment id: cannot tell", "", "pay_B", none, refundOfIntent},
		// The whole intent is owed back because the order kept another: its
		// own payment's refund settles the row, never the intent.
		{"a duplicate order capture: the intent's own payment, owed back", "pay_A", "pay_A", reasonDuplicateOrderCapture, refundOfOwedCapture},
		{"a duplicate order capture, surrounding blanks", " pay_A ", "pay_A", reasonDuplicateOrderCapture, refundOfOwedCapture},
		{"a duplicate order capture, no payment id on the event", "pay_A", "", reasonDuplicateOrderCapture, refundOfIntent},
		{"an owed row with no reason still counts as recorded", "pay_A", "pay_B", owedReason(&OwedCapture{}), refundOfOwedCapture},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyRefundTarget(tc.settled, tc.refund, tc.owed); got != tc.want {
				t.Fatalf("classifyRefundTarget(%q, %q, %q) = %v, want %v", tc.settled, tc.refund, tc.owed, got, tc.want)
			}
		})
	}
	if owedReason(nil) != "" {
		t.Fatal("no owed row must read as no reason")
	}
}

func TestSettleOwedCapture(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		owed, before, refund    int64
		wantRefunded            int64
		wantResolved, wantError bool
	}{
		{"full refund resolves", 118000, 0, 118000, 118000, true, false},
		{"a partial leaves it open", 118000, 0, 18000, 18000, false, false},
		{"the remainder resolves", 118000, 18000, 100000, 118000, true, false},
		{"over-refund is refused, never clamped", 118000, 18000, 118000, 0, false, true},
		{"a zero refund is refused", 118000, 0, 0, 0, false, true},
		{"no recorded amount: counted, never resolved", 0, 0, 5000, 5000, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, resolved, err := settleOwedCapture(tc.owed, tc.before, tc.refund)
			if (err != nil) != tc.wantError {
				t.Fatalf("err = %v, want error %v", err, tc.wantError)
			}
			if tc.wantError {
				return
			}
			if got != tc.wantRefunded || resolved != tc.wantResolved {
				t.Fatalf("settleOwedCapture(%d, %d, %d) = %d/%v, want %d/%v",
					tc.owed, tc.before, tc.refund, got, resolved, tc.wantRefunded, tc.wantResolved)
			}
		})
	}
}

func TestOwedCaptureKeyAndRemaining(t *testing.T) {
	o := OwedCapture{ID: 42, AmountMinor: 118000, RefundedMinor: 18000}
	if k := o.IdempotencyKey(); k != "refund-required-42" {
		t.Fatalf("idempotency key = %q, want refund-required-42 (derived from the row id only)", k)
	}
	if again := (OwedCapture{ID: 42, AmountMinor: 1, Attempts: 7}).IdempotencyKey(); again != o.IdempotencyKey() {
		t.Fatalf("the key changed with the row's state: %q vs %q", again, o.IdempotencyKey())
	}
	if r := o.RemainingMinor(); r != 100000 {
		t.Fatalf("remaining = %d, want 100000", r)
	}
	if r := (OwedCapture{AmountMinor: 100, RefundedMinor: 150}).RemainingMinor(); r != 0 {
		t.Fatalf("remaining = %d, want 0 (never negative)", r)
	}
}

// The claim is one statement whose shape is the queue's correctness: only
// unresolved, unparked, due rows; SKIP LOCKED so replicas never share a row;
// the lease (next_attempt_at pushed out) taken in the same UPDATE. The
// behaviour against PostgreSQL is in the integration test.
func TestClaimRefundRequiredSQLShape(t *testing.T) {
	sql := strings.Join(strings.Fields(claimRefundRequiredSQL), " ")
	for _, want := range []*regexp.Regexp{
		regexp.MustCompile(`^UPDATE payments\.refund_required SET refund_attempts = refund_attempts \+ 1, next_attempt_at = NOW\(\) \+ \(LEAST\(refund_attempts \+ 1, 8\) \* INTERVAL '30 seconds'\) WHERE id IN \(`),
		regexp.MustCompile(`WHERE resolved_at IS NULL AND parked_at IS NULL AND next_attempt_at <= NOW\(\)`),
		regexp.MustCompile(`ORDER BY next_attempt_at, id FOR UPDATE SKIP LOCKED LIMIT \$1\)`),
	} {
		if !want.MatchString(sql) {
			t.Fatalf("claim SQL lost %q:\n%s", want, sql)
		}
	}
}
