package service

import (
	"strings"
	"testing"

	"github.com/atpost/wallet-service/internal/store"
)

func merchantPayment(amount int64) *store.Transaction {
	return &store.Transaction{Type: "merchant_pay", Direction: "debit", Status: "succeeded", AmountPaise: amount}
}

func TestCheckRefund_AllowsUpToWhatIsLeft(t *testing.T) {
	if err := checkRefund(merchantPayment(10000), 0, 10000); err != nil {
		t.Fatalf("full refund refused: %v", err)
	}
	if err := checkRefund(merchantPayment(10000), 6000, 4000); err != nil {
		t.Fatalf("refund of exactly what is left refused: %v", err)
	}
}

// Two refunds that are each within the original must not add up past it.
func TestCheckRefund_RefusesMoreThanIsLeft(t *testing.T) {
	err := checkRefund(merchantPayment(10000), 6000, 4001)
	if err == nil || !strings.HasPrefix(err.Error(), "invalid: ") {
		t.Fatalf("refund of 4001 with 4000 left: got %v, want an invalid: error", err)
	}
	if err := checkRefund(merchantPayment(10000), 10000, 1); err == nil {
		t.Fatalf("refund after a full refund must be refused")
	}
}

func TestCheckRefund_RefusesNonMerchantOriginals(t *testing.T) {
	for _, orig := range []*store.Transaction{
		{Type: "top_up", Direction: "credit", Status: "succeeded", AmountPaise: 1000},
		{Type: "receive", Direction: "credit", Status: "succeeded", AmountPaise: 1000},
		{Type: "send", Direction: "debit", Status: "succeeded", AmountPaise: 1000},
		{Type: "merchant_pay", Direction: "credit", Status: "succeeded", AmountPaise: 1000},
	} {
		if err := checkRefund(orig, 0, 100); err == nil {
			t.Errorf("refund against %s/%s must be refused", orig.Type, orig.Direction)
		}
	}
}

func TestCheckRefund_RefusesUnsettledOriginal(t *testing.T) {
	for _, status := range []string{"pending", "failed", "reversed"} {
		orig := merchantPayment(1000)
		orig.Status = status
		if err := checkRefund(orig, 0, 100); err == nil {
			t.Errorf("refund against a %s payment must be refused", status)
		}
	}
}
