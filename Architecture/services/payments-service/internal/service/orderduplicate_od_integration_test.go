//go:build integration

package service

// commerce.order.duplicate_capture: a second paid intent on one commerce
// order is refunded by payments-service, and only that intent's payment.
//
//	intents A (the order's) and B (the duplicate) both captured →
//	RecordOrderDuplicateCapture writes B's payment to refund_required →
//	the worker refunds B at the provider under refund-required-<row id> →
//	B's signed refund webhook resolves the row; neither intent's ledger moves
//	and no payment.refunded reaches commerce.
//
// Same rig as refundrequired_rr: the real Razorpay adapter against an
// httptest server, a live PostgreSQL (payments_it_test).

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/google/uuid"
)

// odPaidIntent seeds a commerce intent for order and captures it with a
// fresh payment id. capture=false leaves it pending.
func odPaidIntent(t *testing.T, svc *Service, order uuid.UUID, amt int64, capture bool) (staleIntent, string) {
	t.Helper()
	si := staleIntent{id: uuid.New(), providerOrder: "order_od_" + uuid.NewString()[:8], referenceID: order}
	si.idemKey = "idem-" + si.id.String()
	if _, err := recPool.Exec(context.Background(), `
		INSERT INTO payments.payment_intents
		    (id, payer_id, payee_id, reference_type, reference_id, amount, amount_minor,
		     currency, method, status, provider, provider_ref, provider_order_id,
		     owner_domain, idempotency_key, application_id)
		VALUES ($1,$2,$3,'order',$4,$5,$6,'INR','upi','pending','razorpay',$7,$7,
		        'commerce-service',$8,'mstore')`,
		si.id, uuid.New(), uuid.New(), order, float64(amt)/100.0, amt, si.providerOrder, si.idemKey); err != nil {
		t.Fatalf("seed intent: %v", err)
	}
	pay := "pay_od_" + uuid.NewString()[:8]
	if capture {
		if err := sendWebhook(t, svc, si, "evt_od_"+uuid.NewString()[:12], "payment.captured", pay, amt); err != nil {
			t.Fatalf("capture: %v", err)
		}
	}
	return si, pay
}

func odReport(order uuid.UUID, dup, bound staleIntent, amt int64) postgres.OrderDuplicateCapture {
	return postgres.OrderDuplicateCapture{EventID: "evt-dup-" + uuid.NewString(), OrderID: order,
		IntentID: dup.id, BoundIntentID: bound.id, AmountMinor: amt, Currency: "INR"}
}

func TestDuplicateOrderCaptureRefundsOnlyTheDuplicate(t *testing.T) {
	ctx := context.Background()
	const amt = 118000
	rzp := newRWRazorpay(t)
	svc := svcWith(t, rzp.provider())
	store := postgres.New(recPool)
	order := uuid.New()
	a, payA := odPaidIntent(t, svc, order, amt, true)
	b, payB := odPaidIntent(t, svc, order, amt, true)

	recorded, err := store.RecordOrderDuplicateCapture(ctx, odReport(order, b, a, amt))
	if err != nil || !recorded {
		t.Fatalf("record: %v, %v; want a new row", recorded, err)
	}
	// Redelivered under another event id: one row, one refund.
	if again, err := store.RecordOrderDuplicateCapture(ctx, odReport(order, b, a, amt)); err != nil || again {
		t.Fatalf("redelivery: %v, %v; want a no-op", again, err)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.refund_required WHERE intent_id = $1`, b.id); n != 1 {
		t.Fatalf("refund_required rows for the duplicate = %d, want 1", n)
	}
	if n := countBy(t, `SELECT count(*) FROM payments.refund_required WHERE intent_id = $1`, a.id); n != 0 {
		t.Fatalf("the order's own payment was recorded as owed back")
	}
	var reason, app string
	var owed int64
	if err := recPool.QueryRow(ctx, `SELECT reason, amount_minor, COALESCE(application_id,'')
		  FROM payments.refund_required WHERE intent_id = $1`, b.id).Scan(&reason, &owed, &app); err != nil {
		t.Fatal(err)
	}
	if reason != "duplicate_order_capture" || owed != amt || app != "mstore" {
		t.Fatalf("row = %s/%d/%s, want duplicate_order_capture/%d/mstore", reason, owed, app, amt)
	}

	// No second route to the same money.
	if _, _, err := store.CreateRefundCommand(ctx, b.id, 1000, "dashboard", "od-"+uuid.NewString(),
		"commerce-service", "commerce-service", "mstore"); !errors.Is(err, postgres.ErrIntentOwedBack) {
		t.Fatalf("refund command on the duplicate: %v, want ErrIntentOwedBack", err)
	}

	row := rrRead(t, b.id, payB)
	refundID := "rfnd_od_" + uuid.NewString()[:10]
	rzp.scriptRefund(payB, rwOK(rwRefundEntity(refundID, payB, amt, "processed")))
	rrTick(t, svc, row.id)
	if n := rzp.count("POST /v1/payments/" + payB + "/refund"); n != 1 {
		t.Fatalf("refunds of the duplicate = %d, want 1 (calls %v)", n, rzp.all())
	}
	if n := rzp.count("POST /v1/payments/" + payA + "/refund"); n != 0 {
		t.Fatalf("the order's own payment was refunded %d time(s)", n)
	}

	// The provider's refund event names B's order and payment; it settles the
	// row, never B's intent (which would publish payment.refunded to commerce).
	if err := svc.ApplyWebhook(ctx, WebhookInput{
		Provider: "razorpay", EventID: "evt_rfnd_od_" + uuid.NewString()[:12], EventType: "refund.processed",
		ProviderOrderID: b.providerOrder, ProviderPaymentID: payB, ProviderRefundID: refundID,
		AmountMinor: amt, Currency: "INR",
	}); err != nil {
		t.Fatalf("refund webhook: %v", err)
	}
	if got := rrRead(t, b.id, payB); !got.resolved || got.refundedMinor != amt {
		t.Fatalf("row after the refund webhook: %+v, want resolved", got)
	}
	rrIntentUntouched(t, b, "the duplicate's refund")
	rrIntentUntouched(t, a, "the duplicate's refund")
}

// The order's only payment is never refunded on a report, however it is
// worded, and nothing is written for a report the ledger does not bear out.
func TestDuplicateOrderCaptureRefusals(t *testing.T) {
	ctx := context.Background()
	const amt = 50000
	svc := svcWith(t, newRWRazorpay(t).provider())
	store := postgres.New(recPool)

	order := uuid.New()
	bound, _ := odPaidIntent(t, svc, order, amt, false) // never captured
	dup, _ := odPaidIntent(t, svc, order, amt, true)
	otherOrder, _ := odPaidIntent(t, svc, uuid.New(), amt, true)

	reserved, _ := odPaidIntent(t, svc, order, amt, true)
	paidBound, _ := odPaidIntent(t, svc, order, amt, true)
	if _, _, err := store.CreateRefundCommand(ctx, reserved.id, 1000, "dashboard", "od-"+uuid.NewString(),
		"commerce-service", "commerce-service", "mstore"); err != nil {
		t.Fatalf("reserve a refund on the would-be duplicate: %v", err)
	}

	for _, tc := range []struct {
		name string
		ev   postgres.OrderDuplicateCapture
	}{
		{"the bound intent never captured: the duplicate is the order's only payment", odReport(order, dup, bound, amt)},
		{"the bound intent is another order's", odReport(order, dup, otherOrder, amt)},
		{"the duplicate is another order's", odReport(order, otherOrder, paidBound, amt)},
		{"the duplicate is already being refunded", odReport(order, reserved, paidBound, amt)},
		{"the amount is not the duplicate's", odReport(order, dup, paidBound, amt-1)},
		{"the duplicate is the bound intent", odReport(order, dup, dup, amt)},
		{"an intent that does not exist", postgres.OrderDuplicateCapture{EventID: "evt-x", OrderID: order,
			IntentID: uuid.New(), BoundIntentID: paidBound.id, AmountMinor: amt, Currency: "INR"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := countBy(t, `SELECT count(*) FROM payments.refund_required WHERE intent_id = $1`, tc.ev.IntentID)
			recorded, err := store.RecordOrderDuplicateCapture(ctx, tc.ev)
			if !errors.Is(err, postgres.ErrOrderDuplicateRefused) || recorded {
				t.Fatalf("record = %v, %v; want ErrOrderDuplicateRefused", recorded, err)
			}
			if after := countBy(t, `SELECT count(*) FROM payments.refund_required WHERE intent_id = $1`, tc.ev.IntentID); after != before {
				t.Fatalf("a refused report wrote a row (%d → %d)", before, after)
			}
		})
	}
}
