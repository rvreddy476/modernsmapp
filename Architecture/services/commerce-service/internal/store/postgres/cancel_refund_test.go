package postgres

import (
	"testing"

	"github.com/atpost/commerce-service/internal/money"
)

// Cancelling an order that holds the buyer's money restocks it and refunds
// what is still owed. partially_refunded is such an order: the old test was
// `payStatus == "paid"`, so cancelling one refunded nothing and kept its stock
// out.
func TestMoneyWasTaken(t *testing.T) {
	for st, want := range map[string]bool{
		"paid": true, "partially_refunded": true,
		// An open refund command already exists, or this is a late capture on
		// a dead order whose stock was never committed.
		"refund_pending": false,
		"refunded":       false,
		"pending":        false, "processing": false, "failed": false, "": false,
	} {
		if got := moneyWasTaken(st); got != want {
			t.Errorf("moneyWasTaken(%q) = %v, want %v", st, got, want)
		}
	}
}

func TestCancelRefundDue(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		total, settled, owed money.Paise
		want                 money.Paise
	}{
		{"paid, nothing refunded", 200000, 0, 0, 200000},
		{"partially refunded: only the remainder", 200000, 20000, 0, 180000},
		{"an open command already owes part of it", 200000, 20000, 30000, 150000},
		{"fully covered by settlements", 200000, 200000, 0, 0},
		{"fully covered by an open command", 200000, 0, 200000, 0},
		{"never negative", 200000, 150000, 100000, 0},
		{"a free order owes nothing", 0, 0, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cancelRefundDue(tc.total, tc.settled, tc.owed); got != tc.want {
				t.Fatalf("cancelRefundDue(%d, %d, %d) = %d, want %d",
					tc.total, tc.settled, tc.owed, got, tc.want)
			}
		})
	}
}

func TestRestockQty(t *testing.T) {
	for _, tc := range []struct{ bought, returned, want int }{
		{2, 0, 2},
		{2, 1, 1},
		{2, 2, 0},
		{2, 3, 0}, // never take stock away on a cancel
	} {
		if got := restockQty(tc.bought, tc.returned); got != tc.want {
			t.Errorf("restockQty(%d, %d) = %d, want %d", tc.bought, tc.returned, got, tc.want)
		}
	}
}
