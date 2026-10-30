package postgres

import (
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/money"
	"github.com/atpost/shared/paymentevents"
	"github.com/google/uuid"
)

// Audit 2026-10-30 M2: the refund decision table.

func TestDecideRefund_PartialDoesNotCloseALargerCancelRefund(t *testing.T) {
	cancel := openRefundCommand{ID: uuid.New(), IntentID: "i-1", AmountMinor: 200000}
	o := refundOrder{PaymentStatus: "refund_pending", TotalMinor: 200000, IntentID: "i-1"}

	// A ₹200 dashboard partial arrives while the ₹2,000 cancel refund is owed.
	d, err := decideRefund(o, RefundEvent{IntentID: "i-1", AmountMinor: 20000}, 0, []openRefundCommand{cancel})
	if err != nil {
		t.Fatal(err)
	}
	if d.Full || d.CloseAll || d.SettleCommand != nil {
		t.Fatalf("a partial closed the cancel refund: %+v", d)
	}
	if d.NewTotal != 20000 {
		t.Fatalf("NewTotal = %d, want 20000", d.NewTotal)
	}
	// Still owed: the order stays refund_pending (no change from refund_pending).
	if d.PaymentStatus != "" || d.OutboxType != "commerce.order.partially_refunded" {
		t.Fatalf("status %q outbox %q", d.PaymentStatus, d.OutboxType)
	}

	// The remaining ₹1,800 later settles: refunds now cover the order.
	d, err = decideRefund(o, RefundEvent{IntentID: "i-1", AmountMinor: 180000}, 20000, []openRefundCommand{cancel})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Full || !d.CloseAll || d.PaymentStatus != "refunded" || d.OutboxType != "commerce.order.refunded" {
		t.Fatalf("covering refund: %+v", d)
	}
}

func TestDecideRefund_FullRefundOfAPaidOrder(t *testing.T) {
	o := refundOrder{PaymentStatus: "refund_pending", TotalMinor: 134800}
	cmd := openRefundCommand{ID: uuid.New(), AmountMinor: 134800}
	d, err := decideRefund(o, RefundEvent{IntentID: "i-1", AmountMinor: 134800}, 0, []openRefundCommand{cmd})
	if err != nil {
		t.Fatal(err)
	}
	if !d.Full || !d.CloseAll || d.PaymentStatus != "refunded" || d.OutboxType != "commerce.order.refunded" {
		t.Fatalf("full: %+v", d)
	}
}

func TestDecideRefund_PartialOnAPaidOrderWithNoCommand(t *testing.T) {
	o := refundOrder{PaymentStatus: "paid", TotalMinor: 200000, IntentID: "i-1"}
	d, err := decideRefund(o, RefundEvent{IntentID: "i-1", AmountMinor: 20000}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Full || d.PaymentStatus != "partially_refunded" || d.OutboxType != "commerce.order.partially_refunded" {
		t.Fatalf("partial: %+v", d)
	}
	// A second partial on a partially_refunded order: no status move, still partial.
	o.PaymentStatus = "partially_refunded"
	d, err = decideRefund(o, RefundEvent{IntentID: "i-1", AmountMinor: 30000}, 20000, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d.Full || d.PaymentStatus != "" || d.NewTotal != 50000 {
		t.Fatalf("second partial: %+v", d)
	}
}

func TestDecideRefund_PartialSettlesOnlyAnExactlyMatchingCommand(t *testing.T) {
	other := openRefundCommand{ID: uuid.New(), IntentID: "i-2", AmountMinor: 50000}
	exact := openRefundCommand{ID: uuid.New(), IntentID: "", AmountMinor: 50000}
	bigger := openRefundCommand{ID: uuid.New(), IntentID: "i-1", AmountMinor: 150000}
	o := refundOrder{PaymentStatus: "refund_pending", TotalMinor: 200000}
	d, err := decideRefund(o, RefundEvent{IntentID: "i-1", AmountMinor: 50000}, 0, []openRefundCommand{other, exact, bigger})
	if err != nil {
		t.Fatal(err)
	}
	if d.SettleCommand == nil || *d.SettleCommand != exact.ID {
		t.Fatalf("settled %v, want the exact-amount command on a matching intent %s", d.SettleCommand, exact.ID)
	}
	// Others are still owed: refund_pending stays.
	if d.PaymentStatus != "" || d.CloseAll {
		t.Fatalf("%+v", d)
	}

	// When the matched command was the only one, nothing remains owed.
	d, err = decideRefund(o, RefundEvent{IntentID: "i-1", AmountMinor: 50000}, 0, []openRefundCommand{exact})
	if err != nil {
		t.Fatal(err)
	}
	if d.PaymentStatus != "partially_refunded" {
		t.Fatalf("PaymentStatus = %q, want partially_refunded", d.PaymentStatus)
	}
}

func TestDecideRefund_Mismatches(t *testing.T) {
	o := refundOrder{PaymentStatus: "paid", TotalMinor: 100000, IntentID: "i-1"}
	for _, tc := range []struct {
		name    string
		ev      RefundEvent
		already money.Paise
		field   string
	}{
		{"another intent", RefundEvent{IntentID: "i-9", AmountMinor: 100}, 0, paymentevents.FieldIntent},
		{"zero amount", RefundEvent{IntentID: "i-1", AmountMinor: 0}, 0, paymentevents.FieldAmount},
		{"more than the order", RefundEvent{IntentID: "i-1", AmountMinor: 100001}, 0, paymentevents.FieldAmount},
		{"cumulative over the order", RefundEvent{IntentID: "i-1", AmountMinor: 60000}, 50000, paymentevents.FieldAmount},
	} {
		_, err := decideRefund(o, tc.ev, tc.already, nil)
		var me *paymentevents.MismatchError
		if !errors.As(err, &me) || me.Field != tc.field {
			t.Fatalf("%s: err = %v, want %s mismatch", tc.name, err, tc.field)
		}
	}
}

func TestDecideRefund_UnpaidOrderIsRecordedButNotMoved(t *testing.T) {
	for _, st := range []string{"pending", "processing", "failed"} {
		o := refundOrder{PaymentStatus: st, TotalMinor: 100000}
		d, err := decideRefund(o, RefundEvent{IntentID: "i-1", AmountMinor: 100000}, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if d.PaymentStatus != "" || d.OutboxType != "" {
			t.Fatalf("%s: moved the order or announced a refund: %+v", st, d)
		}
	}
}
