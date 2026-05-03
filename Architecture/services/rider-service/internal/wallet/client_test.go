package wallet

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestMockClient_DebitRecords(t *testing.T) {
	m := NewMockClient()
	uid := uuid.New()
	pid := uuid.New()
	res, err := m.DebitForSubscription(context.Background(), uid, 29900, pid, "idem-1")
	if err != nil {
		t.Fatalf("debit: %v", err)
	}
	if res.AmountPaise != 29900 || res.Status != "succeeded" {
		t.Fatalf("unexpected result: %+v", res)
	}
	debits := m.Debits()
	if len(debits) != 1 {
		t.Fatalf("expected 1 debit; got %d", len(debits))
	}
	if debits[0].UserID != uid || debits[0].PaymentID != pid {
		t.Fatalf("debit fields mismatch: %+v", debits[0])
	}
}

func TestMockClient_FailDebit(t *testing.T) {
	m := NewMockClient()
	m.FailDebit()
	_, err := m.DebitForSubscription(context.Background(), uuid.New(), 100, uuid.New(), "idem-x")
	if err == nil {
		t.Fatalf("expected simulated failure")
	}
	// FailDebit is one-shot.
	_, err = m.DebitForSubscription(context.Background(), uuid.New(), 100, uuid.New(), "idem-y")
	if err != nil {
		t.Fatalf("second call should succeed: %v", err)
	}
}

func TestMockClient_RefundRecords(t *testing.T) {
	m := NewMockClient()
	wallet := uuid.New()
	if err := m.RefundSubscription(context.Background(), wallet, 100, "test"); err != nil {
		t.Fatalf("refund: %v", err)
	}
	if got := m.Refunds(); len(got) != 1 || got[0].OriginalWalletTxnID != wallet {
		t.Fatalf("refund not recorded: %+v", got)
	}
}

func TestMockClient_FailRefund(t *testing.T) {
	m := NewMockClient()
	m.FailRefund()
	if err := m.RefundSubscription(context.Background(), uuid.New(), 100, "boom"); err == nil {
		t.Fatalf("expected failure")
	}
}

func TestHTTPClient_DebitValidation(t *testing.T) {
	c := NewHTTPClient("http://nope", "")
	cases := []struct {
		name string
		uid  uuid.UUID
		amt  int64
		key  string
	}{
		{"nil user", uuid.Nil, 100, "idem"},
		{"zero amount", uuid.New(), 0, "idem"},
		{"missing key", uuid.New(), 100, ""},
	}
	for _, c2 := range cases {
		_, err := c.DebitForSubscription(context.Background(), c2.uid, c2.amt, uuid.New(), c2.key)
		if err == nil {
			t.Errorf("%s: expected validation error", c2.name)
		}
	}
}
