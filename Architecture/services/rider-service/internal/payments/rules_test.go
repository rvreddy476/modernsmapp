package payments

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// Rule (a): a captain / admin / system cancellation refunds an online,
// captured payment in full; the customer's own cancellation and a cash ride
// never fire.
func TestCaptainCancelRefund(t *testing.T) {
	base := CaptainCancelFacts{CancelledByKind: "partner", PaymentMethod: MethodUPI, PaymentStatus: StatusSucceeded, AmountPaise: 12451}
	cases := []struct {
		name   string
		mutate func(*CaptainCancelFacts)
		want   int64
		fire   bool
	}{
		{"captain cancels a paid upi ride", func(f *CaptainCancelFacts) {}, 12451, true},
		{"admin cancels", func(f *CaptainCancelFacts) { f.CancelledByKind = "admin" }, 12451, true},
		{"system expiry", func(f *CaptainCancelFacts) { f.CancelledByKind = "system" }, 12451, true},
		{"card, partially refunded: the remainder", func(f *CaptainCancelFacts) {
			f.PaymentMethod, f.PaymentStatus, f.RefundedPaise = MethodCard, StatusPartiallyRefunded, 2000
		}, 10451, true},
		{"in-flight refund is subtracted", func(f *CaptainCancelFacts) { f.InFlightPaise = 12451 }, 0, false},
		// Mutation guards.
		{"customer cancels: nothing", func(f *CaptainCancelFacts) { f.CancelledByKind = "customer" }, 0, false},
		{"cash ride: never", func(f *CaptainCancelFacts) { f.PaymentMethod = MethodCash }, 0, false},
		{"unpaid (confirming): nothing captured", func(f *CaptainCancelFacts) { f.PaymentStatus = StatusConfirming }, 0, false},
		{"already refunded", func(f *CaptainCancelFacts) { f.PaymentStatus = StatusRefunded }, 0, false},
		{"unknown kind", func(f *CaptainCancelFacts) { f.CancelledByKind = "" }, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			tc.mutate(&f)
			got, fire := CaptainCancelRefund(f)
			if fire != tc.fire || got != tc.want {
				t.Fatalf("CaptainCancelRefund(%+v) = %d,%v want %d,%v", f, got, fire, tc.want, tc.fire)
			}
		})
	}
}

// Rule (b): a second capture on ANOTHER intent, for the ride's amount,
// currency and payer, on a ride already paid.
func TestDuplicateCapture(t *testing.T) {
	payer := uuid.New()
	base := DuplicateCaptureFacts{RowStatus: StatusSucceeded, RowMethod: MethodUPI, BoundIntentID: "int_a", EventIntentID: "int_b",
		RowAmountMinor: 12451, EventAmountMinor: 12451, RowCurrency: "INR", EventCurrency: "inr", RowPayerID: payer, EventPayerID: payer}
	cases := []struct {
		name   string
		mutate func(*DuplicateCaptureFacts)
		want   bool
	}{
		{"second intent, same amount and payer", func(f *DuplicateCaptureFacts) {}, true},
		{"row partially refunded still counts as paid", func(f *DuplicateCaptureFacts) { f.RowStatus = StatusPartiallyRefunded }, true},
		// Mutation guards.
		{"same intent re-delivered is not a duplicate", func(f *DuplicateCaptureFacts) { f.EventIntentID = "int_a" }, false},
		{"row not paid yet", func(f *DuplicateCaptureFacts) { f.RowStatus = StatusConfirming }, false},
		{"no bound intent", func(f *DuplicateCaptureFacts) { f.BoundIntentID = "" }, false},
		{"different amount is a mismatch, not a duplicate", func(f *DuplicateCaptureFacts) { f.EventAmountMinor = 100 }, false},
		{"different payer", func(f *DuplicateCaptureFacts) { f.EventPayerID = uuid.New() }, false},
		{"unstated payer", func(f *DuplicateCaptureFacts) { f.EventPayerID = uuid.Nil }, false},
		{"unstated currency", func(f *DuplicateCaptureFacts) { f.EventCurrency = "" }, false},
		{"other currency", func(f *DuplicateCaptureFacts) { f.EventCurrency = "USD" }, false},
		{"cash row", func(f *DuplicateCaptureFacts) { f.RowMethod = MethodCash }, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			tc.mutate(&f)
			if got := DuplicateCapture(f); got != tc.want {
				t.Fatalf("DuplicateCapture(%+v) = %v want %v", f, got, tc.want)
			}
		})
	}
}

// Rule (c): a cancellation fee paid online directly is refunded when the
// cancellation is inside the free window or partner-caused.
func TestCancellationFeeRefund(t *testing.T) {
	assigned := time.Date(2026, 9, 19, 10, 0, 0, 0, time.UTC)
	within := assigned.Add(90 * time.Second)
	after := assigned.Add(5 * time.Minute)
	base := CancellationFeeFacts{Reason: "cancellation_fee", OutstandingStatus: OutstandingSettled, SettledOnline: true, AmountPaise: 1500,
		CancelledByKind: "customer", AssignedAt: &assigned, CancelledAt: &after, CancelFreeSeconds: 120}
	cases := []struct {
		name   string
		mutate func(*CancellationFeeFacts)
		want   int64
		fire   bool
	}{
		{"customer cancelled inside the free window", func(f *CancellationFeeFacts) { f.CancelledAt = &within }, 1500, true},
		{"cancellation reclassified as partner-caused", func(f *CancellationFeeFacts) { f.CancelledByKind = "partner" }, 1500, true},
		{"admin cancellation", func(f *CancellationFeeFacts) { f.CancelledByKind = "admin" }, 1500, true},
		// Mutation guards.
		{"customer cancelled after the free window: the fee stands", func(f *CancellationFeeFacts) {}, 0, false},
		{"no-show after the window stands", func(f *CancellationFeeFacts) { f.CancelledByKind = "no_show" }, 0, false},
		{"fee settled through the next ride's fare is not refunded by rule", func(f *CancellationFeeFacts) {
			f.SettledOnline = false
			f.CancelledAt = &within
		}, 0, false},
		{"still pending: nothing paid", func(f *CancellationFeeFacts) {
			f.OutstandingStatus = OutstandingPending
			f.CancelledAt = &within
		}, 0, false},
		{"already refunded", func(f *CancellationFeeFacts) {
			f.OutstandingStatus = OutstandingRefunded
			f.CancelledAt = &within
		}, 0, false},
		{"another outstanding reason", func(f *CancellationFeeFacts) {
			f.Reason = "waiting_charge"
			f.CancelledAt = &within
		}, 0, false},
		{"no assignment time", func(f *CancellationFeeFacts) { f.AssignedAt = nil; f.CancelledAt = &within }, 0, false},
		{"cancelled before assigned (inconsistent facts)", func(f *CancellationFeeFacts) {
			before := assigned.Add(-time.Minute)
			f.CancelledAt = &before
		}, 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := base
			tc.mutate(&f)
			got, fire := CancellationFeeRefund(f)
			if fire != tc.fire || got != tc.want {
				t.Fatalf("CancellationFeeRefund = %d,%v want %d,%v", got, fire, tc.want, tc.fire)
			}
		})
	}
}

func TestSystemActorIsStable(t *testing.T) {
	if SystemActorID == uuid.Nil || SystemActorID.String() != uuid.NewSHA1(uuid.NameSpaceURL, []byte("https://momentum.app/actor/mopedu-system")).String() {
		t.Fatalf("SystemActorID = %s", SystemActorID)
	}
}
