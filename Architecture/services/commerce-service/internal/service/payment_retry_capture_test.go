package service

import (
	"testing"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Defect 3: a payment_failed order on which money landed afterwards (a late
// capture being refunded) is never offered a retry: it would charge the buyer
// a second time.
func TestCanRetryPayment_RefusedOnceMoneyLanded(t *testing.T) {
	payer := uuid.New()
	for pay, want := range map[string]bool{
		"failed": true, "pending": true, "": true,
		"paid": false, "refund_pending": false, "partially_refunded": false, "refunded": false,
	} {
		o := &postgres.Order{Status: "payment_failed", PaymentStatus: pay, CustomerUserID: payer}
		if got := CanRetryPayment(o, payer); got != want {
			t.Errorf("payment_failed with payment_status %q: CanRetryPayment = %v, want %v", pay, got, want)
		}
	}
}
