//go:build integration

package service

// A failed first attempt followed by a captured retry, and stub intents
// crowding the reconciler window.
//
// Razorpay lets a customer retry on the same order after a failed attempt.
// The webhook used to finalise the intent FAILED on the first payment.failed,
// so the retry's payment.captured was refused (failed never becomes
// succeeded): the customer was charged and the order stayed failed.
//
// Same rig as reconcile_n2: the REAL Razorpay adapter against an httptest
// server, a live PostgreSQL (payments_it_test).
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -v

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func sendWebhook(t *testing.T, svc *Service, si staleIntent, eventID, eventType, payID string, amt int64) error {
	t.Helper()
	return svc.ApplyWebhook(context.Background(), WebhookInput{
		Provider:          "razorpay",
		EventID:           eventID,
		EventType:         eventType,
		ProviderOrderID:   si.providerOrder,
		ProviderPaymentID: payID,
		AmountMinor:       amt,
		Currency:          "INR",
	})
}

func failedAttemptsRecorded(t *testing.T, si staleIntent) int {
	t.Helper()
	return countBy(t,
		`SELECT count(*) FROM payments.provider_events
		  WHERE provider_order_id=$1 AND event_type='payment.failed'`, si.providerOrder)
}

func refundMarkers(t *testing.T, si staleIntent) int {
	t.Helper()
	return countBy(t, `SELECT count(*) FROM payments.refund_required WHERE intent_id=$1`, si.id)
}

// ─── Webhook ─────────────────────────────────────────────────────────

// The defect end to end: the first attempt fails, the retry on the same order
// is captured. The capture must settle the intent, exactly once, and nothing
// may ever have been published as failed.
func TestWebhookCaptureAfterAFailedAttemptSucceedsExactlyOnce(t *testing.T) {
	const amt = 118000
	si := seedStaleAged(t, amt, "INR", "order_retrywh_"+uuid.NewString()[:8], time.Minute)
	svc := svcWith(t, newRazorpayStub(t).provider())

	if err := sendWebhook(t, svc, si, "evt_fail_"+uuid.NewString()[:12], "payment.failed",
		"pay_first_"+uuid.NewString()[:8], amt); err != nil {
		t.Fatalf("failed-attempt webhook: %v", err)
	}
	if s := statusOf(t, si.id); s != "pending" {
		t.Fatalf("after a failed attempt the intent is %q, want pending (the customer may retry)", s)
	}
	requireOutboxEvents(t, si, "payment.failed", 0, "after the failed attempt")
	if n := failedAttemptsRecorded(t, si); n != 1 {
		t.Fatalf("failed attempts recorded = %d, want 1", n)
	}

	if err := sendWebhook(t, svc, si, "evt_cap_"+uuid.NewString()[:12], "payment.captured",
		"pay_retry_"+uuid.NewString()[:8], amt); err != nil {
		t.Fatalf("capture webhook: %v", err)
	}
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("after the captured retry the intent is %q, want succeeded — the customer was charged", s)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 1, "after the captured retry")
	requireOutboxEvents(t, si, "payment.failed", 0, "after the captured retry")
	if n := refundMarkers(t, si); n != 0 {
		t.Fatalf("a normal capture recorded %d needs-refund marker(s)", n)
	}
}

func TestWebhookReplayedFailedAttemptIsANoOp(t *testing.T) {
	const amt = 118000
	si := seedStaleAged(t, amt, "INR", "order_replayfail_"+uuid.NewString()[:8], time.Minute)
	svc := svcWith(t, newRazorpayStub(t).provider())
	evt := "evt_fail_" + uuid.NewString()[:12]
	payID := "pay_" + uuid.NewString()[:12]

	if err := sendWebhook(t, svc, si, evt, "payment.failed", payID, amt); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	err := sendWebhook(t, svc, si, evt, "payment.failed", payID, amt)
	if !errors.Is(err, ErrWebhookDuplicate) {
		t.Fatalf("replay returned %v, want ErrWebhookDuplicate", err)
	}
	if s := statusOf(t, si.id); s != "pending" {
		t.Fatalf("intent is %q after a replayed failed attempt, want pending", s)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1`,
		si.referenceID.String()); n != 0 {
		t.Fatalf("%d outbox row(s) after a failed attempt and its replay, want 0", n)
	}
	if n := failedAttemptsRecorded(t, si); n != 1 {
		t.Fatalf("failed attempts recorded = %d after a replay, want 1", n)
	}
}

// ─── Reconciler: the retry window ────────────────────────────────────

// Every attempt failed, but the customer is still inside the retry window.
func TestReconcileKeepsAllFailedAttemptsPendingInsideTheWindow(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStaleAged(t, amt, "INR", "order_inwindow_"+uuid.NewString()[:8], 5*time.Minute)

	stub := newRazorpayStub(t)
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "failed", 1700000100))
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "failed", 1700000200))
	svc := svcWith(t, stub.provider())
	svc.reconcileOnce(ctx, time.Minute)

	stub.requireReadOrderPayments(t, si.providerOrder)
	requireNoTerminalEffect(t, si, "all attempts failed, 5m into a 15m retry window")
}

// Every attempt failed and the window has passed: FAILED, published once.
func TestReconcileFailsAllFailedAttemptsPastTheWindowOnce(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_pastwindow_"+uuid.NewString()[:8]) // 2h old

	stub := newRazorpayStub(t)
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "failed", 1700000100))
	svc := svcWith(t, stub.provider())
	svc.reconcileOnce(ctx, time.Minute)

	if s := statusOf(t, si.id); s != "failed" {
		t.Fatalf("intent status = %q, want failed", s)
	}
	requireOutboxEvents(t, si, "payment.failed", 1, "first tick past the window")

	svc.reconcileOnce(ctx, time.Minute)
	requireOutboxEvents(t, si, "payment.failed", 1, "second tick")
	requireOutboxEvents(t, si, "payment.succeeded", 0, "second tick")
}

// The window runs from the LATEST failed attempt, not only from the intent's
// creation: a customer who retried a minute ago is still retrying.
func TestReconcileQuietWindowRunsFromTheLatestFailedAttempt(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_recentfail_"+uuid.NewString()[:8]) // 2h old
	payID := "pay_" + uuid.NewString()[:12]

	stub := newRazorpayStub(t)
	stub.setPayment(rzpPayment(payID, si.providerOrder, amt, "INR", "failed", 1700000100))
	svc := svcWith(t, stub.provider())

	if err := sendWebhook(t, svc, si, "evt_fail_"+uuid.NewString()[:12], "payment.failed", payID, amt); err != nil {
		t.Fatalf("failed-attempt webhook: %v", err)
	}
	svc.reconcileOnce(ctx, time.Minute)
	if s := statusOf(t, si.id); s != "pending" {
		t.Fatalf("intent is %q with a failed attempt recorded moments ago, want pending", s)
	}
	requireOutboxEvents(t, si, "payment.failed", 0, "attempt inside the window")

	if _, err := recPool.Exec(ctx,
		`UPDATE payments.provider_events SET received_at = NOW() - INTERVAL '1 hour'
		  WHERE provider_order_id = $1`, si.providerOrder); err != nil {
		t.Fatalf("ageing the failed attempt: %v", err)
	}
	svc.reconcileOnce(ctx, time.Minute)
	if s := statusOf(t, si.id); s != "failed" {
		t.Fatalf("intent is %q an hour after its last failed attempt, want failed", s)
	}
	requireOutboxEvents(t, si, "payment.failed", 1, "attempt past the window")
}

// An order nobody ever tried to pay does not stay pending for ever.
func TestReconcileFailsAnOrderWithNoAttemptsPastTheWindowOnce(t *testing.T) {
	ctx := context.Background()
	si := seedStale(t, 118000, "INR", "order_abandoned_"+uuid.NewString()[:8]) // 2h old

	stub := newRazorpayStub(t) // empty payments collection
	svc := svcWith(t, stub.provider())
	svc.reconcileOnce(ctx, time.Minute)
	svc.reconcileOnce(ctx, time.Minute)

	stub.requireReadOrderPayments(t, si.providerOrder)
	if s := statusOf(t, si.id); s != "failed" {
		t.Fatalf("an abandoned order is %q two hours on, want failed", s)
	}
	requireOutboxEvents(t, si, "payment.failed", 1, "abandoned order, two ticks")
}

// ─── Late capture on a FAILED intent ─────────────────────────────────

// The customer paid after the window closed and the intent was finalised
// FAILED. The intent is not revived, nothing is published as succeeded, and
// exactly one durable needs-refund marker exists however often the capture is
// announced.
func TestLateCaptureOnAFailedIntentRecordsOneRefundMarker(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_latecap_"+uuid.NewString()[:8]) // 2h old

	stub := newRazorpayStub(t)
	stub.setPayment(rzpPayment("pay_"+uuid.NewString()[:12], si.providerOrder, amt, "INR", "failed", 1700000100))
	svc := svcWith(t, stub.provider())
	svc.reconcileOnce(ctx, time.Minute)
	if s := statusOf(t, si.id); s != "failed" {
		t.Fatalf("setup: intent is %q, want failed past the window", s)
	}

	logs := captureLogs(t)
	payID := "pay_late_" + uuid.NewString()[:8]
	evt := "evt_latecap_" + uuid.NewString()[:12]
	if err := sendWebhook(t, svc, si, evt, "payment.captured", payID, amt); err != nil {
		t.Fatalf("late capture webhook: %v (it must be acknowledged once the marker is durable)", err)
	}
	if s := statusOf(t, si.id); s != "failed" {
		t.Fatalf("a late capture moved the FAILED intent to %q; it must not be revived", s)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 0, "late capture")
	requireOutboxEvents(t, si, "payment.failed", 1, "late capture")
	if n := refundMarkers(t, si); n != 1 {
		t.Fatalf("needs-refund markers = %d after a late capture, want 1", n)
	}
	var loud bool
	for _, m := range logs.atOrAbove(slog.LevelError) {
		if strings.Contains(m, "refund") {
			loud = true
		}
	}
	if !loud || len(logs.mentioning(payID)) == 0 || len(logs.mentioning(si.id.String())) == 0 {
		t.Fatalf("a late capture must log an ERROR naming the intent and payment; got %v", logs.atOrAbove(slog.LevelError))
	}

	// The same event redelivered, and the same payment announced again under
	// another event id (Razorpay's order.paid).
	if err := sendWebhook(t, svc, si, evt, "payment.captured", payID, amt); err != nil && !errors.Is(err, ErrWebhookDuplicate) {
		t.Fatalf("redelivery: %v", err)
	}
	if err := sendWebhook(t, svc, si, "evt_orderpaid_"+uuid.NewString()[:12], "order.paid", payID, amt); err != nil {
		t.Fatalf("order.paid for the same late payment: %v", err)
	}
	if n := refundMarkers(t, si); n != 1 {
		t.Fatalf("needs-refund markers = %d after replays, want exactly 1", n)
	}
	if s := statusOf(t, si.id); s != "failed" {
		t.Fatalf("intent is %q after replays, want failed", s)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 0, "after replays")
}

// ─── Stub intents and the reconciler window ──────────────────────────

// Sixty stale stub intents, all OLDER than the one real stale intent. The
// window is fifty rows, oldest first, so if the query does not exclude stubs
// the real intent never makes it in.
func TestReconcileWindowIsNotCrowdedByStubIntents(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	real := seedStale(t, amt, "INR", "order_crowd_"+uuid.NewString()[:8]) // 2h old

	run := uuid.NewString()[:8]
	prefix := "order_stub_crowd_" + run + "_"
	for i := 0; i < 60; i++ {
		if _, err := recPool.Exec(ctx, `
			INSERT INTO payments.payment_intents
			    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
			     currency, method, status, provider, provider_ref, provider_order_id,
			     owner_domain, idempotency_key, created_at)
			VALUES ($1,$2,$3,'order',$4,1180,118000,'INR','upi','pending','razorpay',
			        $5, $5, 'commerce', $6, NOW() - INTERVAL '3 hours')`,
			uuid.New(), uuid.New(), uuid.New(), uuid.New(),
			fmt.Sprintf("%s%02d", prefix, i), "idem-stubcrowd-"+run+fmt.Sprint(i)); err != nil {
			t.Fatalf("seed stub intent %d: %v", i, err)
		}
	}
	t.Cleanup(func() {
		_, _ = recPool.Exec(context.Background(),
			`DELETE FROM payments.payment_intents WHERE provider_order_id LIKE $1 AND status = 'pending'`,
			strings.ReplaceAll(prefix, "_", `\_`)+"%")
	})

	stub := newRazorpayStub(t)
	stub.setPayment(capturedPayment("pay_"+uuid.NewString()[:12], real.providerOrder, amt, "INR"))
	svcWith(t, stub.provider()).reconcileOnce(ctx, time.Minute)

	for _, p := range stub.requestedPaths() {
		if strings.Contains(p, "order_stub_") {
			t.Fatalf("a stub order reference reached the provider: %s", p)
		}
	}
	if s := statusOf(t, real.id); s != "succeeded" {
		t.Fatalf("the real stale intent is %q after one tick, want succeeded — stub intents crowded it out of the window", s)
	}
	requireOutboxEvents(t, real, "payment.succeeded", 1, "one tick behind sixty stubs")
}
