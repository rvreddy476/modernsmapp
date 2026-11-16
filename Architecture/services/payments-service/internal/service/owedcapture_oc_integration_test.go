//go:build integration

package service

// payment.owed_capture and capture_mismatch refunds, against a live
// PostgreSQL (payments_it_test):
//
//   - a signed capture whose money does not match its intent is refused (the
//     intent stays pending, no provider inbox row, no payment.succeeded) AND
//     recorded as owed back once (refund_required, reason capture_mismatch);
//   - every owed payment is announced to the owner domain on the order's
//     partition, when recorded and when its refund lands — never as
//     payment.refunded.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

type ocEvent struct {
	Reason        string `json:"reason"`
	State         string `json:"state"`
	AmountMinor   int64  `json:"amount_minor"`
	RefundedMinor int64  `json:"refunded_minor"`
	Resolved      bool   `json:"resolved"`
	ReferenceType string `json:"reference_type"`
	ReferenceID   string `json:"reference_id"`
	RowID         string `json:"refund_required_id"`
}

// ocEvents reads the payment.owed_capture events on a reference's partition.
func ocEvents(t *testing.T, reference uuid.UUID) []ocEvent {
	t.Helper()
	rows, err := recPool.Query(context.Background(), `
		SELECT payload->'payload' FROM payments.outbox_events
		 WHERE event_type = $1 AND partition_key = $2 ORDER BY id`, postgres.EventPaymentOwedCapture, reference.String())
	if err != nil {
		t.Fatalf("read owed events: %v", err)
	}
	defer rows.Close()
	var out []ocEvent
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		var ev ocEvent
		if err := json.Unmarshal(raw, &ev); err != nil {
			t.Fatalf("owed event payload %s: %v", raw, err)
		}
		out = append(out, ev)
	}
	return out
}

// requireRefusedAndOwedBack is requireNoTerminalEffect for a capture whose
// money moved but did not match: nothing settled, nothing published but the
// owed-capture announcement, and the payment recorded once as owed back.
func requireRefusedAndOwedBack(t *testing.T, si staleIntent, payID, why string) {
	t.Helper()
	if s := statusOf(t, si.id); s != "pending" {
		t.Fatalf("%s: intent moved to %q, want pending", why, s)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.outbox_events WHERE partition_key=$1 AND event_type <> $2`,
		si.referenceID.String(), postgres.EventPaymentOwedCapture); n != 0 {
		t.Fatalf("%s: %d outbox row(s) other than the owed-capture notice", why, n)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.provider_events WHERE provider_order_id=$1`, si.providerOrder); n != 0 {
		t.Fatalf("%s: %d provider-inbox row(s) written", why, n)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.refund_required
		  WHERE intent_id=$1 AND provider_payment_id=$2 AND reason='capture_mismatch' AND resolved_at IS NULL`,
		si.id, payID); n != 1 {
		t.Fatalf("%s: capture_mismatch rows = %d, want 1", why, n)
	}
	evs := ocEvents(t, si.referenceID)
	if len(evs) != 1 || evs[0].Reason != "capture_mismatch" || evs[0].State != postgres.OwedStateRefundRequired {
		t.Fatalf("%s: owed events = %+v, want one capture_mismatch refund_required", why, evs)
	}
}

func TestMismatchedCaptureIsOwedBackOnceAndTheIntentCanStillSettle(t *testing.T) {
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_oc_mm_"+uuid.NewString()[:8])
	svc := svcWith(t, newRazorpayStub(t).provider())
	pay := "pay_oc_mm_" + uuid.NewString()[:8]

	if err := sendWebhook(t, svc, si, "evt_oc_mm_"+uuid.NewString()[:12], "payment.captured", pay, amt+500); err == nil {
		t.Fatal("a capture above the intent settled it")
	}
	requireRefusedAndOwedBack(t, si, pay, "capture above the intent")
	// The provider retries (same payment, new delivery): still one row, one
	// announcement.
	if err := sendWebhook(t, svc, si, "evt_oc_mm_"+uuid.NewString()[:12], "payment.captured", pay, amt+500); err == nil {
		t.Fatal("the retried mismatch settled the intent")
	}
	requireRefusedAndOwedBack(t, si, pay, "the provider's retry")

	// A correct payment still settles the intent; the owed row is untouched.
	good := "pay_oc_ok_" + uuid.NewString()[:8]
	if err := sendWebhook(t, svc, si, "evt_oc_ok_"+uuid.NewString()[:12], "payment.captured", good, amt); err != nil {
		t.Fatalf("correct capture: %v", err)
	}
	if s := statusOf(t, si.id); s != "succeeded" {
		t.Fatalf("intent = %q after the correct capture", s)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.refund_required WHERE intent_id=$1`, si.id); n != 1 {
		t.Fatalf("refund_required rows = %d, want only the mismatch", n)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.outbox_events WHERE event_type='payment.succeeded' AND partition_key=$1`,
		si.referenceID.String()); n != 1 {
		t.Fatalf("payment.succeeded rows = %d, want 1", n)
	}
}

// A capture that cannot be named for a refund (no currency, no amount) is
// refused exactly as before and nothing is recorded.
func TestMismatchedCaptureWithoutRefundableFieldsRecordsNothing(t *testing.T) {
	const amt = 118000
	si := seedStale(t, amt, "INR", "order_oc_nr_"+uuid.NewString()[:8])
	svc := svcWith(t, newRazorpayStub(t).provider())
	if err := svc.ApplyWebhook(context.Background(), WebhookInput{
		Provider: "razorpay", EventID: "evt_oc_nr_" + uuid.NewString()[:12], EventType: "payment.captured",
		ProviderOrderID: si.providerOrder, ProviderPaymentID: "pay_oc_nr", AmountMinor: amt, Currency: "",
	}); err == nil {
		t.Fatal("a capture with no currency settled the intent")
	}
	requireNoTerminalEffect(t, si, "no currency")
	if n := countBy(t, `SELECT count(*) FROM payments.refund_required WHERE intent_id=$1`, si.id); n != 0 {
		t.Fatalf("recorded %d owed row(s) for a capture a refund cannot name", n)
	}
}

// A second provider payment on one intent: announced when recorded and when
// the worker's refund lands, on the order's partition, never payment.refunded.
func TestOwedCaptureAnnouncedForARepeatCaptureAndItsRefund(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	rzp := newRWRazorpay(t)
	svc := svcWith(t, rzp.provider())
	si, _, payB := rrDuplicate(t, svc, amt)

	evs := ocEvents(t, si.referenceID)
	if len(evs) != 1 || evs[0].Reason != "duplicate_capture" || evs[0].State != postgres.OwedStateRefundRequired ||
		evs[0].AmountMinor != amt || evs[0].ReferenceType != "order" || evs[0].ReferenceID != si.referenceID.String() {
		t.Fatalf("owed events after the repeat capture = %+v", evs)
	}
	row := rrRead(t, si.id, payB)
	refundID := "rfnd_oc_" + uuid.NewString()[:10]
	rzp.scriptRefund(payB, rwOK(rwRefundEntity(refundID, payB, amt, "processed")))
	rrTick(t, svc, row.id)
	if err := svc.ApplyWebhook(ctx, WebhookInput{
		Provider: "razorpay", EventID: "evt_rfnd_oc_" + uuid.NewString()[:12], EventType: "refund.processed",
		ProviderOrderID: si.providerOrder, ProviderPaymentID: payB, ProviderRefundID: refundID,
		AmountMinor: amt, Currency: "INR",
	}); err != nil {
		t.Fatalf("refund webhook: %v", err)
	}
	evs = ocEvents(t, si.referenceID)
	if len(evs) != 2 || evs[1].State != postgres.OwedStateRefunded || !evs[1].Resolved || evs[1].RefundedMinor != amt {
		t.Fatalf("owed events after the refund = %+v, want a refunded announcement", evs)
	}
	rrIntentUntouched(t, si, "the repeat capture's refund")
}

// A duplicate order capture commerce reported is announced with commerce's
// own intent, so commerce books it to the row it already holds.
func TestOwedCaptureAnnouncedForADuplicateOrderCapture(t *testing.T) {
	ctx := context.Background()
	const amt = 109629
	svc := svcWith(t, newRWRazorpay(t).provider())
	store := postgres.New(recPool)
	order := uuid.New()
	a, _ := odPaidIntent(t, svc, order, amt, true)
	b, _ := odPaidIntent(t, svc, order, amt, true)
	if ok, err := store.RecordOrderDuplicateCapture(ctx, odReport(order, b, a, amt)); err != nil || !ok {
		t.Fatalf("record: %v, %v", ok, err)
	}
	if _, err := store.RecordOrderDuplicateCapture(ctx, odReport(order, b, a, amt)); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	var intentID string
	if err := recPool.QueryRow(ctx, `SELECT payload->'payload'->>'intent_id' FROM payments.outbox_events
		  WHERE event_type=$1 AND partition_key=$2`, postgres.EventPaymentOwedCapture, order.String()).Scan(&intentID); err != nil {
		t.Fatalf("one owed event for the duplicate: %v", err)
	}
	if intentID != b.id.String() {
		t.Fatalf("owed event names intent %s, want the duplicate %s", intentID, b.id)
	}
	if evs := ocEvents(t, order); len(evs) != 1 || evs[0].Reason != "duplicate_order_capture" || evs[0].AmountMinor != amt {
		t.Fatalf("owed events = %+v", evs)
	}
}
