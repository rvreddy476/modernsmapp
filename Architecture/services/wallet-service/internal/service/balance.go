package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/wallet-service/internal/store"
	"github.com/google/uuid"
)

// balanceLazyRefreshAge controls when GetBalance triggers an async re-pull
// from the partner bank. 60 s matches the spec.
const balanceLazyRefreshAge = 60 * time.Second

// GetBalance returns the user's wallet balance. If no row exists yet, opens a
// PPI sub-account at the partner bank and creates the mirror row. If
// last_synced_at is older than balanceLazyRefreshAge, kicks off a background
// refresh against the bank — the response always returns the current mirror
// snapshot (eventual-consistency, per BC-of-PPI design).
func (s *Service) GetBalance(ctx context.Context, userID uuid.UUID) (*store.Balance, error) {
	b, err := s.store.GetBalance(ctx, userID)
	if err != nil && !errors.Is(err, store.ErrBalanceNotFound) {
		return nil, fmt.Errorf("get balance: %w", err)
	}
	if errors.Is(err, store.ErrBalanceNotFound) {
		ref, openErr := s.bank.OpenSubAccount(ctx, userID)
		if openErr != nil {
			return nil, fmt.Errorf("open sub-account: %w", openErr)
		}
		b, err = s.store.EnsureBalance(ctx, userID, ref)
		if err != nil {
			return nil, fmt.Errorf("ensure balance: %w", err)
		}
	}
	if time.Since(b.LastSyncedAt) > balanceLazyRefreshAge {
		// Async refresh — caller does not wait. We deliberately decouple the
		// fetch latency from the API latency; the bank may be slow.
		go s.refreshFromBank(b.UserID, b.BankAccountRef)
	}
	return b, nil
}

// refreshFromBank reads the live balance from the partner bank and updates
// the mirror. Errors are logged but never propagated — the mirror is a
// snapshot, not the truth.
func (s *Service) refreshFromBank(userID uuid.UUID, ref string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	paise, err := s.bank.GetBalance(ctx, ref)
	if err != nil {
		slog.Warn("wallet: bank balance refresh failed", "user_id", userID, "error", err)
		return
	}
	if err := s.store.SetSynced(ctx, userID, paise); err != nil {
		slog.Warn("wallet: mirror refresh failed", "user_id", userID, "error", err)
	}
}

// AssertWithinMonthlyLimit returns an error if the requested amount would
// breach the user's KYC tier monthly cap. v1 uses a per-call check on the
// stored monthly_limit_paise; a future iteration adds a 30-day rolling sum
// from wallet.transactions. Errors are prefixed "invalid: " so the http
// handler maps them to 400.
func (s *Service) AssertWithinMonthlyLimit(ctx context.Context, userID uuid.UUID, amountPaise int64) error {
	b, err := s.GetBalance(ctx, userID)
	if err != nil {
		return err
	}
	if b.IsFrozen {
		return fmt.Errorf("forbidden: wallet frozen")
	}
	if amountPaise > b.MonthlyLimitPaise {
		return fmt.Errorf("invalid: amount exceeds KYC tier limit (limit_paise=%d)", b.MonthlyLimitPaise)
	}
	return nil
}
