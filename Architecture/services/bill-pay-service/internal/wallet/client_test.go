package wallet

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestMockClient_DebitRecordsCall(t *testing.T) {
	m := NewMockClient()
	uid := uuid.New()
	pid := uuid.New()
	res, err := m.DebitForBillPay(context.Background(), uid, 12500, pid, "k1")
	if err != nil {
		t.Fatalf("debit: %v", err)
	}
	if res == nil || res.AmountPaise != 12500 {
		t.Fatalf("debit result wrong: %+v", res)
	}
	if got := len(m.Debits()); got != 1 {
		t.Fatalf("expected 1 recorded debit; got %d", got)
	}
}

func TestMockClient_FailDebit(t *testing.T) {
	m := NewMockClient()
	m.FailDebit()
	if _, err := m.DebitForBillPay(context.Background(), uuid.New(), 100, uuid.New(), "k"); err == nil {
		t.Fatalf("expected debit failure")
	}
	// Subsequent call should succeed (one-shot fail).
	if _, err := m.DebitForBillPay(context.Background(), uuid.New(), 100, uuid.New(), "k2"); err != nil {
		t.Fatalf("expected recovery: %v", err)
	}
}

func TestMockClient_RefundRecordsCall(t *testing.T) {
	m := NewMockClient()
	if err := m.RefundForBillPay(context.Background(), uuid.New(), 100, "test"); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if got := len(m.Refunds()); got != 1 {
		t.Fatalf("expected 1 recorded refund; got %d", got)
	}
}

func TestMockClient_FailRefund(t *testing.T) {
	m := NewMockClient()
	m.FailRefund()
	if err := m.RefundForBillPay(context.Background(), uuid.New(), 100, "x"); err == nil {
		t.Fatalf("expected refund failure")
	}
}

func TestHTTPClient_DebitValidation(t *testing.T) {
	c := NewHTTPClient("http://wallet-service:8114", "key")
	if _, err := c.DebitForBillPay(context.Background(), uuid.Nil, 100, uuid.New(), "k"); err == nil {
		t.Fatalf("expected nil-user error")
	}
	if _, err := c.DebitForBillPay(context.Background(), uuid.New(), 0, uuid.New(), "k"); err == nil {
		t.Fatalf("expected zero-amount error")
	}
	if _, err := c.DebitForBillPay(context.Background(), uuid.New(), 1, uuid.New(), ""); err == nil {
		t.Fatalf("expected missing-key error")
	}
}
