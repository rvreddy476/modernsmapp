//go:build integration

package service

// Migration 018: a duplicate capture is refunded by payments-service itself,
// and its refund never touches the order it duplicated.
//
//	capture A settles the intent → capture B is recorded in refund_required →
//	the worker refunds B at the provider under refund-required-<row id> →
//	the signed refund webhook for B resolves the row; the intent stays
//	succeeded, its refunded balance stays 0, and no payment.refunded is
//	published.
//
// Same rig as refundworker_rw: the REAL Razorpay adapter against an httptest
// server, a live PostgreSQL (payments_it_test).
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -v

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

type rrRow struct {
	id               int64
	resolved, parked bool
	providerRefundID string
	refundedMinor    int64
	attempts         int
}

func rrRead(t *testing.T, intentID uuid.UUID, payID string) rrRow {
	t.Helper()
	var r rrRow
	if err := recPool.QueryRow(context.Background(),
		`SELECT id, resolved_at IS NOT NULL, parked_at IS NOT NULL, COALESCE(provider_refund_id,''),
		        refunded_minor, refund_attempts
		   FROM payments.refund_required WHERE intent_id = $1 AND provider_payment_id = $2`, intentID, payID).
		Scan(&r.id, &r.resolved, &r.parked, &r.providerRefundID, &r.refundedMinor, &r.attempts); err != nil {
		t.Fatalf("read refund_required: %v", err)
	}
	return r
}

// rrDuplicate settles an intent with payment A and records payment B as a
// duplicate capture.
func rrDuplicate(t *testing.T, svc *Service, amt int64) (staleIntent, string, string) {
	t.Helper()
	si := seedStaleAged(t, amt, "INR", "order_rr_"+uuid.NewString()[:8], time.Minute)
	payA, payB := "pay_A_"+uuid.NewString()[:8], "pay_B_"+uuid.NewString()[:8]
	if err := sendWebhook(t, svc, si, "evt_capA_"+uuid.NewString()[:12], "payment.captured", payA, amt); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if err := sendWebhook(t, svc, si, "evt_capB_"+uuid.NewString()[:12], "payment.captured", payB, amt); err != nil {
		t.Fatalf("second capture: %v", err)
	}
	return si, payA, payB
}

// rrTick is one worker tick with exactly this row due: every other open row
// is pushed out of the window so it is not driven through this test's
// scripted provider.
func rrTick(t *testing.T, svc *Service, id int64) {
	t.Helper()
	ctx := context.Background()
	if _, err := recPool.Exec(ctx,
		`UPDATE payments.refund_required SET next_attempt_at = NOW() + INTERVAL '30 days'
		  WHERE resolved_at IS NULL AND id <> $1`, id); err != nil {
		t.Fatalf("isolating the queue: %v", err)
	}
	if _, err := recPool.Exec(ctx,
		`UPDATE payments.refund_required SET next_attempt_at = NOW() - INTERVAL '1 second' WHERE id = $1`, id); err != nil {
		t.Fatalf("making the row due: %v", err)
	}
	svc.drainRefundRequired(ctx)
}

func rrIntentUntouched(t *testing.T, si staleIntent, why string) {
	t.Helper()
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("%s: intent moved to %q, want succeeded", why, s)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.payment_intents
		  WHERE id=$1 AND COALESCE(refunded_amount_minor,0) <> 0`, si.id); n != 0 {
		t.Fatalf("%s: the intent's refunded balance moved", why)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.outbox_events
		  WHERE event_type='payment.refunded' AND partition_key=$1`, si.id.String()); n != 0 {
		t.Fatalf("%s: %d payment.refunded event(s) published for the order", why, n)
	}
}

func TestRefundRequiredWorkerRefundsADuplicateCaptureAndTheWebhookResolvesIt(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	rzp := newRWRazorpay(t)
	svc := svcWith(t, rzp.provider())
	si, _, payB := rrDuplicate(t, svc, amt)
	row := rrRead(t, si.id, payB)

	refundID := "rfnd_rr_" + uuid.NewString()[:10]
	rzp.scriptRefund(payB, rwOK(rwRefundEntity(refundID, payB, amt, "processed")))
	rrTick(t, svc, row.id)

	if n := rzp.count("POST /v1/payments/" + payB + "/refund"); n != 1 {
		t.Fatalf("refund requests for the duplicate payment = %d, want 1 (calls: %v)", n, rzp.all())
	}
	if keys := rzp.keys(); len(keys) != 1 || keys[0] != (postgres.OwedCapture{ID: row.id}).IdempotencyKey() {
		t.Fatalf("idempotency keys sent = %v, want [refund-required-%d]", keys, row.id)
	}
	got := rrRead(t, si.id, payB)
	if got.resolved || got.providerRefundID != refundID || got.attempts != 1 {
		t.Fatalf("after the refund call: %+v, want submitted (provider refund %s), unresolved, 1 attempt", got, refundID)
	}
	// Leased: the row is not claimable again straight away.
	if claimed, err := postgres.New(recPool).ClaimDueRefundRequired(ctx, 1000); err != nil {
		t.Fatal(err)
	} else {
		for _, c := range claimed {
			if c.ID == row.id {
				t.Fatal("a just-claimed row was claimable again inside its lease")
			}
		}
	}
	rrIntentUntouched(t, si, "after the refund call")

	// The provider's signed refund event: it names the payment, not the order.
	err := svc.ApplyWebhook(ctx, WebhookInput{
		Provider: "razorpay", EventID: "evt_rfnd_" + uuid.NewString()[:12], EventType: "refund.processed",
		ProviderPaymentID: payB, ProviderRefundID: refundID, AmountMinor: amt, Currency: "INR",
	})
	if err != nil {
		t.Fatalf("refund webhook: %v", err)
	}
	got = rrRead(t, si.id, payB)
	if !got.resolved || got.refundedMinor != amt {
		t.Fatalf("after the refund webhook: %+v, want resolved with %d refunded", got, amt)
	}
	rrIntentUntouched(t, si, "after the refund webhook")

	// The same refund under another event id is counted once.
	if err := svc.ApplyWebhook(ctx, WebhookInput{
		Provider: "razorpay", EventID: "evt_rfnd2_" + uuid.NewString()[:12], EventType: "refund.created",
		ProviderPaymentID: payB, ProviderRefundID: refundID, AmountMinor: amt, Currency: "INR",
	}); err != nil {
		t.Fatalf("the same refund redelivered: %v", err)
	}
	if got := rrRead(t, si.id, payB); got.refundedMinor != amt {
		t.Fatalf("a redelivered refund was counted twice: refunded_minor = %d", got.refundedMinor)
	}
	// Resolved rows are never claimed again.
	rrTick(t, svc, row.id)
	if n := rzp.count("POST /v1/payments/" + payB + "/refund"); n != 1 {
		t.Fatalf("a resolved row was refunded again: %d requests", n)
	}
}

// Item 2 on its own: a refund of the duplicate that also names the provider
// order (so it resolves to the real intent by order id) still books against
// the owed row, never the intent.
func TestRefundOfADuplicateCaptureNamingTheOrderLeavesTheOrderAlone(t *testing.T) {
	ctx := context.Background()
	const amt = 90000
	svc := svcWith(t, newRWRazorpay(t).provider())
	si, _, payB := rrDuplicate(t, svc, amt)

	if err := svc.ApplyWebhook(ctx, WebhookInput{
		Provider: "razorpay", EventID: "evt_rfnd_" + uuid.NewString()[:12], EventType: "refund.processed",
		ProviderOrderID: si.providerOrder, ProviderPaymentID: payB,
		ProviderRefundID: "rfnd_dash_" + uuid.NewString()[:10], AmountMinor: amt, Currency: "INR",
	}); err != nil {
		t.Fatalf("refund webhook: %v", err)
	}
	if got := rrRead(t, si.id, payB); !got.resolved || got.refundedMinor != amt {
		t.Fatalf("owed row after the refund: %+v, want resolved", got)
	}
	rrIntentUntouched(t, si, "a refund of the duplicate")
}

// A refund of a payment that is neither the settling one nor a recorded owed
// capture is refused and leaves no inbox row, so the provider retries until
// the capture that explains it has been recorded.
func TestRefundOfAnUnrecordedPaymentIsRefusedAndRetried(t *testing.T) {
	ctx := context.Background()
	const amt = 60000
	svc := svcWith(t, newRWRazorpay(t).provider())
	si := seedStaleAged(t, amt, "INR", "order_rrx_"+uuid.NewString()[:8], time.Minute)
	if err := sendWebhook(t, svc, si, "evt_capA_"+uuid.NewString()[:12], "payment.captured", "pay_A_"+uuid.NewString()[:8], amt); err != nil {
		t.Fatalf("capture: %v", err)
	}
	eventID := "evt_rfndx_" + uuid.NewString()[:12]
	err := svc.ApplyWebhook(ctx, WebhookInput{
		Provider: "razorpay", EventID: eventID, EventType: "refund.processed",
		ProviderOrderID: si.providerOrder, ProviderPaymentID: "pay_C_" + uuid.NewString()[:8],
		ProviderRefundID: "rfnd_x_" + uuid.NewString()[:10], AmountMinor: amt, Currency: "INR",
	})
	if !errors.Is(err, postgres.ErrRefundOfUnrecordedPayment) {
		t.Fatalf("err = %v, want ErrRefundOfUnrecordedPayment", err)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.provider_events WHERE event_id=$1`, eventID); n != 0 {
		t.Fatal("the inbox row survived a refused refund; the redelivery would be answered 200")
	}
	rrIntentUntouched(t, si, "a refund of an unrecorded payment")
}
