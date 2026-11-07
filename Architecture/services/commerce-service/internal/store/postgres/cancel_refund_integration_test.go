//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/google/uuid"
)

// Cancelling a paid or partially refunded order, against a real database:
// committed stock comes back once, and the refund command is for exactly what
// is still owed.

// paidOrder checks out qty units at unit paise each and pays for it.
func paidOrder(t *testing.T, stock, qty int, unit int64) (*fixture, *CheckoutResult) {
	t.Helper()
	ctx := context.Background()
	f := newFixture(t, stock, unit, "18")
	f.addToCart(qty, unit)
	res, err := f.store.Checkout(ctx, f.params(f.quote(0), "idem-"+uuid.NewString()))
	if err != nil {
		t.Fatalf("checkout: %v", err)
	}
	if err := f.store.ApplyPaymentSucceeded(ctx, PaymentEvent{
		EventID: "evt-" + uuid.NewString(), EventType: "payment.succeeded", OrderID: res.OrderID,
		AmountMinor: res.TotalMinor, Currency: "INR", PayerID: f.userID,
	}); err != nil {
		t.Fatalf("pay: %v", err)
	}
	if total, _ := f.inventory(); total != stock-qty {
		t.Fatalf("stock after payment = %d, want %d", total, stock-qty)
	}
	return f, res
}

type cancelState struct {
	payStatus     string
	commands      int
	commandAmount int64
	refundMinor   int64
}

func readCancelState(t *testing.T, orderID uuid.UUID) cancelState {
	t.Helper()
	var s cancelState
	if err := testPool.QueryRow(context.Background(), `
		SELECT o.payment_status,
		       (SELECT count(*) FROM order_refund_commands WHERE order_id = o.id),
		       (SELECT COALESCE(SUM(amount_minor),0) FROM order_refund_commands WHERE order_id = o.id),
		       COALESCE((SELECT (payload->'payload'->>'refund_minor')::bigint FROM outbox_events
		                  WHERE partition_key = o.id::text AND event_type = 'commerce.order.cancelled'
		                  ORDER BY id DESC LIMIT 1), -1)
		  FROM orders o WHERE o.id = $1`, orderID).
		Scan(&s.payStatus, &s.commands, &s.commandAmount, &s.refundMinor); err != nil {
		t.Fatalf("read cancel state: %v", err)
	}
	return s
}

func TestCancelPaidOrder_RefundsTheTotalAndRestocks(t *testing.T) {
	ctx := context.Background()
	f, res := paidOrder(t, 10, 2, 100000)
	if err := f.store.CancelOrder(ctx, res.OrderID, f.userID, "customer", "changed my mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	s := readCancelState(t, res.OrderID)
	if s.payStatus != "refund_pending" || s.commands != 1 || s.commandAmount != res.TotalMinor.Int64() ||
		s.refundMinor != res.TotalMinor.Int64() {
		t.Fatalf("after cancel: %+v, want refund_pending and one command for %d", s, res.TotalMinor)
	}
	if total, reserved := f.inventory(); total != 10 || reserved != 0 {
		t.Fatalf("stock after cancel = %d (reserved %d), want 10 (0)", total, reserved)
	}
}

func TestCancelPartiallyRefundedOrder_RefundsTheRemainderAndRestocks(t *testing.T) {
	ctx := context.Background()
	f, res := paidOrder(t, 10, 2, 100000)

	// A ₹200 partial refund (a dashboard refund, say): money back, goods not.
	const partial = money.Paise(20000)
	if _, err := f.store.SettleRefund(ctx, RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, RefundID: "rfnd_" + uuid.NewString(),
		AmountMinor: partial, ProviderStatus: "partially_refunded",
	}); err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if s := readCancelState(t, res.OrderID); s.payStatus != "partially_refunded" {
		t.Fatalf("after the partial refund payment_status = %q, want partially_refunded", s.payStatus)
	}

	if err := f.store.CancelOrder(ctx, res.OrderID, f.userID, "customer", "changed my mind"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	want := res.TotalMinor.Sub(partial).Int64()
	s := readCancelState(t, res.OrderID)
	if s.payStatus != "refund_pending" || s.commands != 1 || s.commandAmount != want || s.refundMinor != want {
		t.Fatalf("after cancel: %+v, want refund_pending and one command for the remaining %d", s, want)
	}
	if total, reserved := f.inventory(); total != 10 || reserved != 0 {
		t.Fatalf("stock after cancel = %d (reserved %d), want 10 (0)", total, reserved)
	}

	// The remainder settles: the order is fully refunded, never over-refunded.
	r, err := f.store.SettleRefund(ctx, RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, RefundID: "rfnd_" + uuid.NewString(),
		AmountMinor: money.Paise(want), ProviderStatus: "refunded",
	})
	if err != nil || !r.Full || r.RefundedTotal != res.TotalMinor {
		t.Fatalf("remainder: r=%+v err=%v", r, err)
	}
	if s := readCancelState(t, res.OrderID); s.payStatus != "refunded" {
		t.Fatalf("after the remainder payment_status = %q, want refunded", s.payStatus)
	}
}

// Stock already put back for the order (a restocked return) is not put back
// a second time by the cancel.
func TestCancelPartiallyRefundedOrder_RestocksOnlyWhatIsStillOut(t *testing.T) {
	ctx := context.Background()
	f, res := paidOrder(t, 10, 2, 100000)
	mustExec(t, `UPDATE inventory_items SET total_qty = total_qty + 1 WHERE variant_id = $1`, f.variantID)
	mustExec(t, `INSERT INTO inventory_ledger (variant_id, order_id, delta_total, reason, actor_type)
	             VALUES ($1, $2, 1, 'return_restock', 'system')`, f.variantID, res.OrderID)
	if _, err := f.store.SettleRefund(ctx, RefundEvent{
		EventID: "evt-rf-" + uuid.NewString(), OrderID: res.OrderID, RefundID: "rfnd_" + uuid.NewString(),
		AmountMinor: 100000, ProviderStatus: "partially_refunded",
	}); err != nil {
		t.Fatalf("partial refund: %v", err)
	}
	if err := f.store.CancelOrder(ctx, res.OrderID, f.userID, "customer", "the rest"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if total, _ := f.inventory(); total != 10 {
		t.Fatalf("stock after cancel = %d, want 10 (one unit was already back)", total)
	}
	if s := readCancelState(t, res.OrderID); s.commandAmount != res.TotalMinor.Int64()-100000 {
		t.Fatalf("refund command = %d, want %d", s.commandAmount, res.TotalMinor.Int64()-100000)
	}
}
