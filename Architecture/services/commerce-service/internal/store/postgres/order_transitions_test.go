package postgres

import "testing"

// TestPaymentStatusTransitions pins the payment_status state machine. The
// rows that matter most: a fresh checkout (pending) and a retried
// checkout (failed) can be paid; a paid order cannot be marked failed by a
// late payment.failed; terminal refund states accept nothing that would
// revive them.
func TestPaymentStatusTransitions(t *testing.T) {
	cases := []struct {
		from, to string
		want     bool
	}{
		{"pending", "paid", true},
		{"processing", "paid", true},
		{"failed", "paid", true}, // retry after a failed attempt
		{"paid", "paid", false},  // idempotent replay is refused by the guard
		{"refunded", "paid", false},
		{"partially_refunded", "paid", false},
		{"refund_pending", "paid", false},

		{"pending", "failed", true},
		{"processing", "failed", true},
		{"paid", "failed", false}, // late failed must not clobber a capture
		{"failed", "failed", false},

		{"pending", "processing", true},
		{"paid", "processing", false},

		{"paid", "refund_pending", true},
		{"paid", "refunded", true},
		{"paid", "partially_refunded", true},
		{"pending", "refunded", false},

		{"pending", "bogus", false},
	}
	for _, tc := range cases {
		if got := PaymentStatusTransitionAllowed(tc.from, tc.to); got != tc.want {
			t.Errorf("%s -> %s: allowed=%v, want %v", tc.from, tc.to, got, tc.want)
		}
	}
	if got := PaymentStatusAllowedFrom("bogus"); len(got) != 0 {
		t.Errorf("unknown target should have no sources, got %v", got)
	}
	// The returned slice is a copy — mutating it must not corrupt the table.
	src := PaymentStatusAllowedFrom("paid")
	src[0] = "corrupted"
	if !PaymentStatusTransitionAllowed("pending", "paid") {
		t.Fatal("PaymentStatusAllowedFrom leaked the internal slice")
	}
}

// TestOrderPayable pins the combined (status, payment_status) guard that
// MarkOrderPaid encodes in SQL, and OrderStatusAfterPaid's projection.
func TestOrderPayable(t *testing.T) {
	cases := []struct {
		name          string
		status        string
		paymentStatus string
		payable       bool
		afterStatus   string
	}{
		{"prepaid checkout", "payment_pending", "pending", true, "confirmed"},
		{"legacy created", "created", "pending", true, "confirmed"},
		{"retry after failed attempt", "payment_pending", "failed", true, "confirmed"},
		{"COD confirmed, settles later", "confirmed", "pending", true, "confirmed"},
		{"B2B credit terms confirmed", "confirmed", "pending", true, "confirmed"},
		{"already paid + confirmed", "confirmed", "paid", false, "confirmed"},
		{"already paid, still payment_pending (legacy)", "payment_pending", "paid", false, "confirmed"},
		{"cancelled", "cancelled", "pending", false, "cancelled"},
		{"cancelled but pending money", "cancelled", "processing", false, "cancelled"},
		{"awaiting B2B approval", "awaiting_approval", "pending", false, "awaiting_approval"},
		{"shipped", "shipped", "paid", false, "shipped"},
		{"refunded", "refunded", "refunded", false, "refunded"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := OrderPayable(tc.status, tc.paymentStatus); got != tc.payable {
				t.Errorf("OrderPayable(%q,%q)=%v, want %v", tc.status, tc.paymentStatus, got, tc.payable)
			}
			if got := OrderStatusAfterPaid(tc.status); got != tc.afterStatus {
				t.Errorf("OrderStatusAfterPaid(%q)=%q, want %q", tc.status, got, tc.afterStatus)
			}
		})
	}
}
