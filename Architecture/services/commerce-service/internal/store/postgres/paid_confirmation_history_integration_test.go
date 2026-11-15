//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// A webhook-confirmed order (payment.succeeded → ApplyPaymentSucceeded) has
// exactly one payment_pending → confirmed history line, however often the
// event is redelivered: the same event id again (caught by the inbox) and the
// same payment under a new event id (a redelivery verdict). Holds with or
// without trg_order_transition attached.
func TestPaymentSucceeded_WritesOneConfirmationHistoryLine(t *testing.T) {
	ctx := context.Background()
	f, res, intent := pendingOrder(t)

	evt := PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		IntentID: intent, AmountMinor: res.TotalMinor, Currency: "INR", PayerID: f.userID,
	}
	if err := f.store.ApplyPaymentSucceeded(ctx, evt); err != nil {
		t.Fatalf("capture: %v", err)
	}
	if err := f.store.ApplyPaymentSucceeded(ctx, evt); !errors.Is(err, ErrDuplicatePaymentEvt) {
		t.Fatalf("same event again: %v, want ErrDuplicatePaymentEvt", err)
	}
	again := evt
	again.EventID = "evt-" + uuid.NewString()
	if err := f.store.ApplyPaymentSucceeded(ctx, again); err != nil {
		t.Fatalf("same payment under a new event id: %v", err)
	}

	var status, payStatus string
	var lines int
	if err := testPool.QueryRow(ctx, `
		SELECT o.status, o.payment_status,
		       (SELECT count(*) FROM order_status_history h
		         WHERE h.order_id = o.id AND h.to_status = 'confirmed')
		  FROM orders o WHERE o.id = $1`, res.OrderID).Scan(&status, &payStatus, &lines); err != nil {
		t.Fatalf("read order: %v", err)
	}
	if status != "confirmed" || payStatus != "paid" {
		t.Fatalf("order is %s/%s, want confirmed/paid", status, payStatus)
	}
	if lines != 1 {
		t.Fatalf("confirmed history lines = %d, want exactly 1", lines)
	}
	var from, actor string
	if err := testPool.QueryRow(ctx, `
		SELECT COALESCE(from_status,''), actor_type FROM order_status_history
		 WHERE order_id = $1 AND to_status = 'confirmed'`, res.OrderID).Scan(&from, &actor); err != nil {
		t.Fatalf("read history line: %v", err)
	}
	if from != "payment_pending" || actor != "system" {
		t.Fatalf("history line %s -> confirmed by %s, want payment_pending by system", from, actor)
	}
}
