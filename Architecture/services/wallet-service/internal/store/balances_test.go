package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestEnsureBalance_CreatesRow(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	b, err := s.EnsureBalance(ctx, uid, "ref-1")
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if b.UserID != uid {
		t.Fatalf("user id mismatch")
	}
	if b.BankAccountRef != "ref-1" {
		t.Fatalf("ref mismatch: %q", b.BankAccountRef)
	}
	if b.AvailablePaise != 0 {
		t.Fatalf("expected zero balance on create")
	}
	if b.KYCTier != KYCMinimal {
		t.Fatalf("expected minimal tier")
	}
	if b.MonthlyLimitPaise != 1000000 {
		t.Fatalf("expected 10k INR cap; got %d", b.MonthlyLimitPaise)
	}
}

func TestEnsureBalance_Idempotent(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	first, _ := s.EnsureBalance(ctx, uid, "ref-x")
	second, _ := s.EnsureBalance(ctx, uid, "ref-y") // ref change should NOT overwrite
	if second.BankAccountRef != "ref-x" {
		t.Fatalf("ensure should not overwrite ref; got %q", second.BankAccountRef)
	}
	if !second.UpdatedAt.After(first.UpdatedAt) && !second.UpdatedAt.Equal(first.UpdatedAt) {
		t.Fatalf("updated_at should not regress")
	}
}

func TestGetBalance_NotFound(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	if _, err := s.GetBalance(context.Background(), uuid.New()); !errors.Is(err, ErrBalanceNotFound) {
		t.Fatalf("expected ErrBalanceNotFound; got %v", err)
	}
}

func TestCreditAvailable_DropsPendingIn(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if _, err := s.EnsureBalance(ctx, uid, "ref"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := s.AdjustPendingIn(ctx, uid, 5000); err != nil {
		t.Fatalf("adjust pending: %v", err)
	}
	if err := s.CreditAvailable(ctx, uid, 5000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	b, _ := s.GetBalance(ctx, uid)
	if b.AvailablePaise != 5000 {
		t.Fatalf("expected available=5000; got %d", b.AvailablePaise)
	}
	if b.PendingInPaise != 0 {
		t.Fatalf("expected pending_in=0; got %d", b.PendingInPaise)
	}
}

func TestDebitAvailableTx_Insufficient(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if _, err := s.EnsureBalance(ctx, uid, "ref"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	tx, _ := s.db.Begin(ctx)
	defer tx.Rollback(ctx)
	if err := s.DebitAvailableTx(ctx, tx, uid, 100); !errors.Is(err, ErrInsufficientBalance) {
		t.Fatalf("expected ErrInsufficientBalance; got %v", err)
	}
}

func TestDebitAvailableTx_Frozen(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if _, err := s.EnsureBalance(ctx, uid, "ref"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := s.CreditAvailable(ctx, uid, 1000); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.Freeze(ctx, uid, "test"); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	tx, _ := s.db.Begin(ctx)
	defer tx.Rollback(ctx)
	err := s.DebitAvailableTx(ctx, tx, uid, 500)
	if err == nil {
		t.Fatalf("expected error when frozen")
	}
}

func TestSetKYCTier_BumpsLimit(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if _, err := s.EnsureBalance(ctx, uid, "ref"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := s.SetKYCTier(ctx, uid, KYCFull); err != nil {
		t.Fatalf("set tier: %v", err)
	}
	b, _ := s.GetBalance(ctx, uid)
	if b.KYCTier != KYCFull {
		t.Fatalf("tier not updated")
	}
	if b.MonthlyLimitPaise != 20000000 {
		t.Fatalf("expected full limit 2 lakh paise; got %d", b.MonthlyLimitPaise)
	}
}

func TestFreezeUnfreeze(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if _, err := s.EnsureBalance(ctx, uid, "ref"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := s.Freeze(ctx, uid, "abuse_review"); err != nil {
		t.Fatalf("freeze: %v", err)
	}
	b, _ := s.GetBalance(ctx, uid)
	if !b.IsFrozen {
		t.Fatalf("expected is_frozen=true")
	}
	if err := s.Unfreeze(ctx, uid); err != nil {
		t.Fatalf("unfreeze: %v", err)
	}
	b, _ = s.GetBalance(ctx, uid)
	if b.IsFrozen {
		t.Fatalf("expected is_frozen=false")
	}
}

func TestMonthlyLimitForTier(t *testing.T) {
	cases := map[KYCTier]int64{
		KYCMinimal:  1000000,
		KYCFull:     20000000,
		KYCEnhanced: 50000000,
		"unknown":   1000000,
	}
	for tier, want := range cases {
		if got := MonthlyLimitForTier(tier); got != want {
			t.Errorf("limit(%s) = %d, want %d", tier, got, want)
		}
	}
}
