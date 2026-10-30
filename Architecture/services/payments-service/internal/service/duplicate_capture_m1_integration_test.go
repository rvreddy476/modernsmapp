//go:build integration

package service

// M1 (audit 2026-10-30): a second, distinct capture on an intent another
// payment already settled. The buyer has paid twice; the second payment must
// be recorded as owed back (payments.refund_required, reason
// duplicate_capture), exactly like a late capture on a failed intent. An exact
// redelivery of the settling capture, under its own or another event id
// (order.paid), stays a no-op.
//
// Same rig as failedretry_d1: the REAL Razorpay adapter against an httptest
// server, a live PostgreSQL (payments_it_test).
//
//	PAYMENTS_TEST_DSN=... go test -tags=integration ./internal/service/... -v

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func duplicateCaptureMarkers(t *testing.T, si staleIntent, payID string) int {
	t.Helper()
	return countBy(t,
		`SELECT count(*) FROM payments.refund_required
		  WHERE intent_id=$1 AND provider_payment_id=$2 AND reason='duplicate_capture'
		    AND resolved_at IS NULL`, si.id, payID)
}

func TestSecondCaptureOnAPaidIntentRecordsOneRefundMarker(t *testing.T) {
	const amt = 118000
	si := seedStaleAged(t, amt, "INR", "order_dupcap_"+uuid.NewString()[:8], time.Minute)
	svc := svcWith(t, newRazorpayStub(t).provider())

	payA := "pay_A_" + uuid.NewString()[:8]
	evtA := "evt_capA_" + uuid.NewString()[:12]
	if err := sendWebhook(t, svc, si, evtA, "payment.captured", payA, amt); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("after the first capture the intent is %q, want succeeded", s)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 1, "first capture")

	// The settling payment announced again: the same event redelivered, and
	// order.paid under another event id. Neither is money owed back.
	if err := sendWebhook(t, svc, si, evtA, "payment.captured", payA, amt); !errors.Is(err, ErrWebhookDuplicate) {
		t.Fatalf("redelivery returned %v, want ErrWebhookDuplicate", err)
	}
	if err := sendWebhook(t, svc, si, "evt_orderpaid_"+uuid.NewString()[:12], "order.paid", payA, amt); err != nil {
		t.Fatalf("order.paid for the settling payment: %v", err)
	}
	if n := refundMarkers(t, si); n != 0 {
		t.Fatalf("an exact redelivery recorded %d needs-refund marker(s), want 0", n)
	}

	// A second, distinct payment on the same provider order.
	logs := captureLogs(t)
	payB := "pay_B_" + uuid.NewString()[:8]
	evtB := "evt_capB_" + uuid.NewString()[:12]
	if err := sendWebhook(t, svc, si, evtB, "payment.captured", payB, amt); err != nil {
		t.Fatalf("second capture: %v (it must be acknowledged once the marker is durable)", err)
	}
	if n := duplicateCaptureMarkers(t, si, payB); n != 1 {
		t.Fatalf("duplicate_capture markers for the second payment = %d, want 1", n)
	}
	if n := refundMarkers(t, si); n != 1 {
		t.Fatalf("needs-refund markers = %d after a second capture, want 1 (the settling payment is not owed back)", n)
	}
	var markerAmount int64
	var markerApp string
	if err := recPool.QueryRow(context.Background(),
		`SELECT amount_minor, COALESCE(application_id,'') FROM payments.refund_required
		  WHERE intent_id=$1 AND provider_payment_id=$2`, si.id, payB).Scan(&markerAmount, &markerApp); err != nil {
		t.Fatalf("read the marker: %v", err)
	}
	var intentApp string
	if err := recPool.QueryRow(context.Background(),
		`SELECT COALESCE(application_id,'') FROM payments.payment_intents WHERE id=$1`, si.id).Scan(&intentApp); err != nil {
		t.Fatalf("read the intent application: %v", err)
	}
	if markerAmount != amt || markerApp != intentApp {
		t.Fatalf("marker amount=%d application=%q, want %d / %q", markerAmount, markerApp, amt, intentApp)
	}
	if n := countBy(t,
		`SELECT count(*) FROM payments.payment_audit_log
		  WHERE intent_id=$1 AND event='duplicate_capture_refund_required'`, si.id); n != 1 {
		t.Fatalf("duplicate-capture audit rows = %d, want 1", n)
	}
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("a second capture moved the intent to %q, want succeeded", s)
	}
	var settled string
	if err := recPool.QueryRow(context.Background(),
		`SELECT COALESCE(provider_payment_id,'') FROM payments.payment_intents WHERE id=$1`, si.id).Scan(&settled); err != nil {
		t.Fatalf("read the settling payment: %v", err)
	}
	if settled != payA {
		t.Fatalf("the intent's payment id became %q, want the settling payment %q", settled, payA)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 1, "second capture")
	var loud bool
	for _, m := range logs.atOrAbove(slog.LevelError) {
		if strings.Contains(m, "refund") {
			loud = true
		}
	}
	if !loud || len(logs.mentioning(payB)) == 0 || len(logs.mentioning(si.id.String())) == 0 {
		t.Fatalf("a duplicate capture must log an ERROR naming the intent and payment; got %v", logs.atOrAbove(slog.LevelError))
	}

	// The second payment announced again: still one marker.
	if err := sendWebhook(t, svc, si, evtB, "payment.captured", payB, amt); !errors.Is(err, ErrWebhookDuplicate) {
		t.Fatalf("redelivery of the second capture returned %v, want ErrWebhookDuplicate", err)
	}
	if err := sendWebhook(t, svc, si, "evt_orderpaidB_"+uuid.NewString()[:12], "order.paid", payB, amt); err != nil {
		t.Fatalf("order.paid for the second payment: %v", err)
	}
	if n := refundMarkers(t, si); n != 1 {
		t.Fatalf("needs-refund markers = %d after replays of the second capture, want exactly 1", n)
	}
	requireOutboxEvents(t, si, "payment.succeeded", 1, "after replays")
}

// A refund does not hide a later, distinct capture: the intent is no longer
// "succeeded", but it is still paid, and a new payment on it is still owed back.
func TestSecondCaptureOnARefundedIntentRecordsARefundMarker(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	for _, status := range []string{"partially_refunded", "refunded"} {
		t.Run(status, func(t *testing.T) {
			si := seedStaleAged(t, amt, "INR", "order_dupcapref_"+uuid.NewString()[:8], time.Minute)
			svc := svcWith(t, newRazorpayStub(t).provider())
			payA := "pay_A_" + uuid.NewString()[:8]
			if err := sendWebhook(t, svc, si, "evt_capA_"+uuid.NewString()[:12], "payment.captured", payA, amt); err != nil {
				t.Fatalf("first capture: %v", err)
			}
			if _, err := recPool.Exec(ctx,
				`UPDATE payments.payment_intents SET status=$2 WHERE id=$1`, si.id, status); err != nil {
				t.Fatalf("mark %s: %v", status, err)
			}

			if err := sendWebhook(t, svc, si, "evt_orderpaid_"+uuid.NewString()[:12], "order.paid", payA, amt); err != nil {
				t.Fatalf("order.paid for the settling payment: %v", err)
			}
			if n := refundMarkers(t, si); n != 0 {
				t.Fatalf("the settling payment announced again recorded %d marker(s), want 0", n)
			}

			payC := "pay_C_" + uuid.NewString()[:8]
			if err := sendWebhook(t, svc, si, "evt_capC_"+uuid.NewString()[:12], "payment.captured", payC, amt); err != nil {
				t.Fatalf("second capture: %v", err)
			}
			if n := duplicateCaptureMarkers(t, si, payC); n != 1 {
				t.Fatalf("duplicate_capture markers = %d on a %s intent, want 1", n, status)
			}
			if s := statusOf(t, si.id); s != status {
				t.Fatalf("intent is %q after a second capture, want %q unchanged", s, status)
			}
		})
	}
}
