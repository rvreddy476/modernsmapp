package bank

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func TestMockClient_OpenSubAccount_RejectsNilUUID(t *testing.T) {
	m := NewMockClient()
	if _, err := m.OpenSubAccount(context.Background(), uuid.Nil); err == nil {
		t.Fatalf("expected error for nil uuid")
	}
}

func TestMockClient_OpenSubAccount_HappyPath(t *testing.T) {
	m := NewMockClient()
	id := uuid.New()
	ref, err := m.OpenSubAccount(context.Background(), id)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if ref == "" {
		t.Fatalf("expected non-empty ref")
	}
	if ref2, _ := m.OpenSubAccount(context.Background(), id); ref2 != ref {
		t.Fatalf("expected idempotent open; got %s vs %s", ref, ref2)
	}
}

func TestMockClient_Transfer_DebitsCredits(t *testing.T) {
	m := NewMockClient()
	from := "ref-from"
	to := "ref-to"
	m.SeedBalance(from, 5000)

	if err := m.Transfer(context.Background(), from, to, 1500, "txn-1"); err != nil {
		t.Fatalf("transfer: %v", err)
	}
	got, _ := m.GetBalance(context.Background(), from)
	if got != 3500 {
		t.Fatalf("from balance: want 3500, got %d", got)
	}
	got, _ = m.GetBalance(context.Background(), to)
	if got != 1500 {
		t.Fatalf("to balance: want 1500, got %d", got)
	}
	if len(m.Transfers()) != 1 {
		t.Fatalf("expected 1 transfer recorded")
	}
}

func TestMockClient_Transfer_InsufficientBalance(t *testing.T) {
	m := NewMockClient()
	if err := m.Transfer(context.Background(), "ref-from", "ref-to", 100, "txn-1"); err == nil {
		t.Fatalf("expected insufficient-balance error")
	}
}

func TestMockClient_Transfer_FailNext(t *testing.T) {
	m := NewMockClient()
	m.SeedBalance("ref-from", 1000)
	m.FailNext("ref-from")
	if err := m.Transfer(context.Background(), "ref-from", "ref-to", 100, "txn"); err == nil {
		t.Fatalf("expected armed failure")
	}
	// FailNext is one-shot.
	if err := m.Transfer(context.Background(), "ref-from", "ref-to", 100, "txn"); err != nil {
		t.Fatalf("expected success after one-shot failure consumed: %v", err)
	}
}

func TestMockClient_VerifyUPIInbound_Sentinels(t *testing.T) {
	m := NewMockClient()
	cases := []struct {
		ref       string
		amount    int64
		wantOK    bool
		expectErr bool
	}{
		{"normal-ref", 1000, true, false},
		{"missing-upi", 1000, false, false},
		{"fail-upi", 1000, false, true},
		{"normal-ref", 0, false, true},
	}
	for _, tc := range cases {
		got, err := m.VerifyUPIInbound(context.Background(), tc.ref, tc.amount)
		if tc.expectErr && err == nil {
			t.Errorf("ref=%s: want error", tc.ref)
		}
		if !tc.expectErr && err != nil {
			t.Errorf("ref=%s: unexpected error: %v", tc.ref, err)
		}
		if got != tc.wantOK {
			t.Errorf("ref=%s: want ok=%v, got %v", tc.ref, tc.wantOK, got)
		}
	}
}

func TestMockClient_Refund_Sentinels(t *testing.T) {
	m := NewMockClient()
	if err := m.Refund(context.Background(), "fail-refund", 100); err == nil {
		t.Fatalf("expected sentinel failure")
	}
	if err := m.Refund(context.Background(), "any-ref", 100); err != nil {
		t.Fatalf("expected success: %v", err)
	}
	if err := m.Refund(context.Background(), "any-ref", 0); err == nil {
		t.Fatalf("expected zero-amount rejection")
	}
}
