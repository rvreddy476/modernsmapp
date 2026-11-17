//go:build integration

package service

// com-2: every capture payments-service records as owed back to the buyer ends
// in exactly ONE refund at the provider once the refund-required worker runs —
// whichever way it was recorded, however many times the worker runs, and
// whether it was recorded before or after the worker was switched on. A
// provider failure is retried under the same idempotency key and then
// surfaces: counted, parked and on the alarm gauges, never silent.
//
// The three owed cases (com-2 a/b/c):
//
//	a  a capture after the reconciler failed the intent  late_capture_after_failed
//	b  a second, distinct capture on a paid intent        duplicate_capture
//	c  a capture whose amount does not match the intent   capture_mismatch
//
// Same rig as refundrequired_rr: the REAL Razorpay adapter against an
// httptest server, a live PostgreSQL (payments_it_test). CI only.
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -run Com2 -v

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/obs"
	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// com2Owed is one owed capture recorded the way production records it.
type com2Owed struct {
	si     staleIntent
	payID  string
	amount int64 // what the buyer paid on payID
	reason string
}

// com2LateAfterFailed: the reconciler fails a quiet intent, then the buyer's
// payment captures anyway.
func com2LateAfterFailed(t *testing.T, amt int64) com2Owed {
	t.Helper()
	si := seedStale(t, amt, "INR", "order_c2late_"+uuid.NewString()[:8])
	stub := newRazorpayStub(t)
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "failed", 1700000100))
	svcWith(t, stub.provider()).reconcileOnce(context.Background(), time.Minute)
	if s := statusOf(t, si.id); s != "failed" {
		t.Fatalf("setup: intent is %q, want failed", s)
	}
	pay := "pay_c2late_" + uuid.NewString()[:8]
	if err := sendWebhook(t, svcWith(t, newRWRazorpay(t).provider()), si,
		"evt_c2late_"+uuid.NewString()[:12], "payment.captured", pay, amt); err != nil {
		t.Fatalf("late capture: %v", err)
	}
	return com2Owed{si, pay, amt, "late_capture_after_failed"}
}

// com2Duplicate: payment A settles the intent, payment B captures too.
func com2Duplicate(t *testing.T, amt int64) com2Owed {
	t.Helper()
	si, _, payB := rrDuplicate(t, svcWith(t, newRWRazorpay(t).provider()), amt)
	return com2Owed{si, payB, amt, "duplicate_capture"}
}

// com2Mismatch: a signed capture for more than the intent.
func com2Mismatch(t *testing.T, amt int64) com2Owed {
	t.Helper()
	si := seedStale(t, amt, "INR", "order_c2mm_"+uuid.NewString()[:8])
	pay := "pay_c2mm_" + uuid.NewString()[:8]
	if err := sendWebhook(t, svcWith(t, newRWRazorpay(t).provider()), si,
		"evt_c2mm_"+uuid.NewString()[:12], "payment.captured", pay, amt+500); err == nil {
		t.Fatal("setup: a capture above the intent settled it")
	}
	return com2Owed{si, pay, amt + 500, "capture_mismatch"}
}

func com2Row(t *testing.T, o com2Owed) rrRow {
	t.Helper()
	var reason string
	if err := recPool.QueryRow(context.Background(),
		`SELECT reason FROM payments.refund_required WHERE intent_id = $1 AND provider_payment_id = $2`,
		o.si.id, o.payID).Scan(&reason); err != nil {
		t.Fatalf("no refund_required row for %s: %v", o.payID, err)
	}
	if reason != o.reason {
		t.Fatalf("recorded reason = %q, want %q", reason, o.reason)
	}
	return rrRead(t, o.si.id, o.payID)
}

func com2Refund(t *testing.T, svc *Service, o com2Owed, refundID string) {
	t.Helper()
	if err := svc.ApplyWebhook(context.Background(), WebhookInput{
		Provider: "razorpay", EventID: "evt_c2rf_" + uuid.NewString()[:12], EventType: "refund.processed",
		ProviderPaymentID: o.payID, ProviderRefundID: refundID, AmountMinor: o.amount, Currency: "INR",
	}); err != nil {
		t.Fatalf("refund webhook: %v", err)
	}
}

func TestCom2EveryOwedCaseEndsInOneRefund(t *testing.T) {
	const amt = 118000
	for _, tc := range []struct {
		name   string
		record func(*testing.T, int64) com2Owed
	}{
		{"a: capture after the intent failed", com2LateAfterFailed},
		{"b: second capture on a paid intent", com2Duplicate},
		{"c: amount mismatch", com2Mismatch},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := tc.record(t, amt)
			row := com2Row(t, o)
			if row.resolved || row.parked {
				t.Fatalf("freshly recorded row = %+v, want open", row)
			}

			rzp := newRWRazorpay(t)
			svc := svcWith(t, rzp.provider())
			refundID := "rfnd_c2_" + uuid.NewString()[:10]
			rzp.scriptRefund(o.payID, rwOK(rwRefundEntity(refundID, o.payID, o.amount, "processed")))
			issued := obs.OwedRefundIssuedCount("submitted")

			rrTick(t, svc, row.id)
			// The lease lapses before the webhook: the worker asks again, under
			// the SAME key, which the provider answers with the same refund.
			rrTick(t, svc, row.id)
			if n := rzp.count("POST /v1/payments/" + o.payID + "/refund"); n != 2 {
				t.Fatalf("refund requests = %d, want 2 (the claim and its re-claim)", n)
			}
			want := (postgres.OwedCapture{ID: row.id}).IdempotencyKey()
			for _, k := range rzp.keys() {
				if k != want {
					t.Fatalf("idempotency keys = %v, want every one %q: two keys are two refunds", rzp.keys(), want)
				}
			}
			if d := obs.OwedRefundIssuedCount("submitted") - issued; d != 1 {
				t.Fatalf("payments_owed_refunds_issued_total{submitted} moved by %v, want 1 (one refund)", d)
			}

			com2Refund(t, svc, o, refundID)
			if got := rrRead(t, o.si.id, o.payID); !got.resolved || got.refundedMinor != o.amount {
				t.Fatalf("after the signed refund: %+v, want resolved with %d refunded", got, o.amount)
			}
			// A second run issues no second refund.
			rrTick(t, svc, row.id)
			if n := rzp.count("POST /v1/payments/" + o.payID + "/refund"); n != 2 {
				t.Fatalf("a resolved owed capture was refunded again: %d requests", n)
			}
		})
	}
}

// A provider failure is retried (same key, attempts and last_error recorded,
// counted as retryable), and a refusal the provider will not change on retry
// parks the row: counted by reason, on the parked gauge, never retried
// without a person.
func TestCom2ProviderFailureIsRetriedThenSurfaces(t *testing.T) {
	const amt = 64000
	o := com2Duplicate(t, amt)
	row := com2Row(t, o)

	rzp := newRWRazorpay(t)
	svc := svcWith(t, rzp.provider())
	rzp.scriptRefund(o.payID,
		rwReply{http.StatusInternalServerError, `{"error":{"code":"SERVER_ERROR","description":"We are facing some trouble completing your request at the moment. Please try again shortly."}}`},
		rwReply{http.StatusBadRequest, rwError("The amount must be atleast INR 1.00", "input_validation_failed")})
	retryable := obs.OwedRefundFailedCount("retryable")
	rejected := obs.OwedRefundFailedCount("provider_rejected")

	rrTick(t, svc, row.id)
	got := rrRead(t, o.si.id, o.payID)
	if got.parked || got.resolved || got.attempts != 1 {
		t.Fatalf("after a 500: %+v, want open with 1 attempt", got)
	}
	var lastErr string
	if err := recPool.QueryRow(context.Background(),
		`SELECT COALESCE(last_error,'') FROM payments.refund_required WHERE id = $1`, row.id).Scan(&lastErr); err != nil || lastErr == "" {
		t.Fatalf("a failed attempt left no last_error (%q, %v)", lastErr, err)
	}
	if d := obs.OwedRefundFailedCount("retryable") - retryable; d != 1 {
		t.Fatalf("payments_owed_refund_failures_total{retryable} moved by %v, want 1", d)
	}

	rrTick(t, svc, row.id)
	got = rrRead(t, o.si.id, o.payID)
	if !got.parked || got.resolved || got.attempts != 2 {
		t.Fatalf("after a terminal 400: %+v, want parked, unresolved, 2 attempts", got)
	}
	if d := obs.OwedRefundFailedCount("provider_rejected") - rejected; d != 1 {
		t.Fatalf("payments_owed_refund_failures_total{provider_rejected} moved by %v, want 1", d)
	}
	svc.refreshOwedCaptureGauges(context.Background())
	if _, parked, _ := obs.OwedCaptures(); parked < 1 {
		t.Fatalf("payments_owed_captures_parked = %v with a parked row, want >= 1", parked)
	}

	// Parked means the worker leaves it: no third request.
	rrTick(t, svc, row.id)
	if n := rzp.count("POST /v1/payments/" + o.payID + "/refund"); n != 2 {
		t.Fatalf("a parked row was attempted again: %d requests", n)
	}
	for _, k := range rzp.keys() {
		if k != (postgres.OwedCapture{ID: row.id}).IdempotencyKey() {
			t.Fatalf("a retry changed the idempotency key: %v", rzp.keys())
		}
	}
}

// Rows recorded while the worker was off (prod and QA before com-2) are
// picked up when it starts: no cursor, no "only new rows". They sit at the
// migration's next_attempt_at default and the gauges report their age first.
func TestCom2BacklogRecordedWhileTheWorkerWasOffIsDrained(t *testing.T) {
	ctx := context.Background()
	const amt = 51000
	backlog := []com2Owed{com2Duplicate(t, amt), com2Mismatch(t, amt), com2LateAfterFailed(t, amt)}
	ids := make([]int64, len(backlog))
	for i, o := range backlog {
		ids[i] = com2Row(t, o).id
	}
	// As the worker-off months left them: never attempted, detected days ago.
	if _, err := recPool.Exec(ctx,
		`UPDATE payments.refund_required SET next_attempt_at = NOW() + INTERVAL '30 days'
		  WHERE resolved_at IS NULL AND NOT (id = ANY($1))`, ids); err != nil {
		t.Fatalf("isolating the queue: %v", err)
	}
	if _, err := recPool.Exec(ctx,
		`UPDATE payments.refund_required
		    SET next_attempt_at = '-infinity', refund_attempts = 0, detected_at = NOW() - INTERVAL '3 days'
		  WHERE id = ANY($1)`, ids); err != nil {
		t.Fatalf("ageing the backlog: %v", err)
	}

	rzp := newRWRazorpay(t)
	svc := svcWith(t, rzp.provider())
	svc.refreshOwedCaptureGauges(ctx)
	if open, _, age := obs.OwedCaptures(); open < 3 || age < 3*24*3600-60 {
		t.Fatalf("gauges before the worker: unrefunded=%v oldest_age=%vs, want >= 3 and >= 3 days", open, age)
	}

	for _, o := range backlog {
		rzp.scriptRefund(o.payID, rwOK(rwRefundEntity("rfnd_c2b_"+uuid.NewString()[:10], o.payID, o.amount, "processed")))
	}
	svc.drainRefundRequired(ctx) // the worker's first tick
	for _, o := range backlog {
		if n := rzp.count("POST /v1/payments/" + o.payID + "/refund"); n != 1 {
			t.Fatalf("backlog row %s (%s): %d refund requests on the first tick, want 1", o.payID, o.reason, n)
		}
		if got := rrRead(t, o.si.id, o.payID); got.providerRefundID == "" || got.attempts != 1 {
			t.Fatalf("backlog row %s after the first tick: %+v, want submitted on attempt 1", o.payID, got)
		}
	}
}
