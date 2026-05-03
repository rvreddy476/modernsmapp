package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrBalanceNotFound indicates the user has no wallet row yet.
var ErrBalanceNotFound = errors.New("balance: not found")

// ErrInsufficientBalance is returned by atomic debit attempts when the user's
// available_paise mirror is below the requested amount. Important: this is
// the MIRROR view; the partner bank PPI is the source of truth, but our
// service refuses to even attempt the bank-side transfer if the mirror says
// we do not have funds. This protects us from race conditions and reduces
// load on the partner bank API.
var ErrInsufficientBalance = errors.New("balance: insufficient funds")

// EnsureBalance creates a wallet.balances row for the user if missing. The
// bankAccountRef is the partner-bank PPI sub-account id assigned by the
// BankClient.OpenSubAccount call. This is idempotent: a duplicate call does
// not overwrite an existing balance.
func (s *Store) EnsureBalance(ctx context.Context, userID uuid.UUID, bankAccountRef string) (*Balance, error) {
	const q = `
        INSERT INTO wallet.balances (user_id, bank_account_ref)
        VALUES ($1, $2)
        ON CONFLICT (user_id) DO UPDATE
            SET updated_at = now()
        RETURNING user_id, bank_account_ref, available_paise, pending_in_paise, pending_out_paise,
                  kyc_tier, monthly_limit_paise, is_frozen, frozen_reason, last_synced_at,
                  created_at, updated_at`
	row := s.db.QueryRow(ctx, q, userID, bankAccountRef)
	return scanBalance(row)
}

// GetBalance reads the balance row. Returns ErrBalanceNotFound if absent.
func (s *Store) GetBalance(ctx context.Context, userID uuid.UUID) (*Balance, error) {
	const q = `
        SELECT user_id, bank_account_ref, available_paise, pending_in_paise, pending_out_paise,
               kyc_tier, monthly_limit_paise, is_frozen, frozen_reason, last_synced_at,
               created_at, updated_at
        FROM wallet.balances WHERE user_id = $1`
	row := s.db.QueryRow(ctx, q, userID)
	b, err := scanBalance(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBalanceNotFound
		}
		return nil, err
	}
	return b, nil
}

// SetSynced refreshes the snapshot the mirror holds from a partner-bank read.
// Used by the lazy-refresh path in service.GetBalance.
func (s *Store) SetSynced(ctx context.Context, userID uuid.UUID, availablePaise int64) error {
	const q = `
        UPDATE wallet.balances
        SET available_paise = $2,
            last_synced_at = now(),
            updated_at = now()
        WHERE user_id = $1`
	tag, err := s.db.Exec(ctx, q, userID, availablePaise)
	if err != nil {
		return fmt.Errorf("set synced: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

// AdjustPendingIn changes the pending_in_paise counter atomically. Positive
// delta = a top-up just started; negative delta = a top-up succeeded or
// expired. Caller is responsible for the matching available_paise mutation.
func (s *Store) AdjustPendingIn(ctx context.Context, userID uuid.UUID, delta int64) error {
	const q = `
        UPDATE wallet.balances
        SET pending_in_paise = pending_in_paise + $2,
            updated_at = now()
        WHERE user_id = $1`
	tag, err := s.db.Exec(ctx, q, userID, delta)
	if err != nil {
		return fmt.Errorf("adjust pending_in: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

// CreditAvailable adds amountPaise to the user's available balance and
// decrements pending_in_paise by the same amount. Used when a top-up settles.
// Both fields move atomically inside a single UPDATE.
func (s *Store) CreditAvailable(ctx context.Context, userID uuid.UUID, amountPaise int64) error {
	if amountPaise <= 0 {
		return fmt.Errorf("credit available: amount must be positive")
	}
	const q = `
        UPDATE wallet.balances
        SET available_paise = available_paise + $2,
            pending_in_paise = GREATEST(pending_in_paise - $2, 0),
            updated_at = now()
        WHERE user_id = $1`
	tag, err := s.db.Exec(ctx, q, userID, amountPaise)
	if err != nil {
		return fmt.Errorf("credit available: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

// DebitAvailableTx atomically moves amountPaise from available_paise into
// pending_out_paise. Returns ErrInsufficientBalance if available < amount.
// Runs inside the supplied tx so callers can chain a transactions-row insert
// in the same atomic step (the send-saga relies on this).
func (s *Store) DebitAvailableTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, amountPaise int64) error {
	if amountPaise <= 0 {
		return fmt.Errorf("debit available: amount must be positive")
	}
	const q = `
        UPDATE wallet.balances
        SET available_paise = available_paise - $2,
            pending_out_paise = pending_out_paise + $2,
            updated_at = now()
        WHERE user_id = $1 AND available_paise >= $2 AND is_frozen = false`
	tag, err := tx.Exec(ctx, q, userID, amountPaise)
	if err != nil {
		return fmt.Errorf("debit available: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Distinguish missing row vs frozen vs insufficient.
		const probe = `SELECT available_paise, is_frozen FROM wallet.balances WHERE user_id = $1`
		var have int64
		var frozen bool
		if err := tx.QueryRow(ctx, probe, userID).Scan(&have, &frozen); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrBalanceNotFound
			}
			return fmt.Errorf("probe balance: %w", err)
		}
		if frozen {
			return fmt.Errorf("balance: account frozen")
		}
		return ErrInsufficientBalance
	}
	return nil
}

// SettleDebitTx finalises a successful debit: pending_out_paise -= amount.
// available_paise is already decremented by DebitAvailableTx. Run inside a tx.
func (s *Store) SettleDebitTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, amountPaise int64) error {
	const q = `
        UPDATE wallet.balances
        SET pending_out_paise = GREATEST(pending_out_paise - $2, 0),
            updated_at = now()
        WHERE user_id = $1`
	tag, err := tx.Exec(ctx, q, userID, amountPaise)
	if err != nil {
		return fmt.Errorf("settle debit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

// ReverseDebitTx is the saga compensation: the partner bank refused the
// transfer, so refund the in-flight pending_out back to available_paise.
func (s *Store) ReverseDebitTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, amountPaise int64) error {
	const q = `
        UPDATE wallet.balances
        SET available_paise = available_paise + $2,
            pending_out_paise = GREATEST(pending_out_paise - $2, 0),
            updated_at = now()
        WHERE user_id = $1`
	tag, err := tx.Exec(ctx, q, userID, amountPaise)
	if err != nil {
		return fmt.Errorf("reverse debit: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

// CreditAvailableTx is the in-tx version of CreditAvailable. Used when the
// recipient is also an in-AtPost user and the send-saga wants both legs
// atomic.
func (s *Store) CreditAvailableTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, amountPaise int64) error {
	if amountPaise <= 0 {
		return fmt.Errorf("credit available: amount must be positive")
	}
	const q = `
        UPDATE wallet.balances
        SET available_paise = available_paise + $2,
            updated_at = now()
        WHERE user_id = $1`
	tag, err := tx.Exec(ctx, q, userID, amountPaise)
	if err != nil {
		return fmt.Errorf("credit available tx: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

// SetKYCTier upgrades a user's KYC tier and resets the monthly_limit_paise
// to the corresponding cap.
func (s *Store) SetKYCTier(ctx context.Context, userID uuid.UUID, tier KYCTier) error {
	limit := MonthlyLimitForTier(tier)
	const q = `
        UPDATE wallet.balances
        SET kyc_tier = $2, monthly_limit_paise = $3, updated_at = now()
        WHERE user_id = $1`
	tag, err := s.db.Exec(ctx, q, userID, string(tier), limit)
	if err != nil {
		return fmt.Errorf("set kyc tier: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

// Freeze marks the wallet frozen with a reason. Used by trust-safety.
func (s *Store) Freeze(ctx context.Context, userID uuid.UUID, reason string) error {
	const q = `
        UPDATE wallet.balances
        SET is_frozen = true, frozen_reason = $2, updated_at = now()
        WHERE user_id = $1`
	tag, err := s.db.Exec(ctx, q, userID, reason)
	if err != nil {
		return fmt.Errorf("freeze: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

// Unfreeze clears the freeze flag.
func (s *Store) Unfreeze(ctx context.Context, userID uuid.UUID) error {
	const q = `
        UPDATE wallet.balances
        SET is_frozen = false, frozen_reason = NULL, updated_at = now()
        WHERE user_id = $1`
	tag, err := s.db.Exec(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("unfreeze: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrBalanceNotFound
	}
	return nil
}

func scanBalance(row pgx.Row) (*Balance, error) {
	var b Balance
	var tier string
	var lastSynced, created, updated time.Time
	if err := row.Scan(
		&b.UserID, &b.BankAccountRef, &b.AvailablePaise, &b.PendingInPaise,
		&b.PendingOutPaise, &tier, &b.MonthlyLimitPaise, &b.IsFrozen,
		&b.FrozenReason, &lastSynced, &created, &updated,
	); err != nil {
		return nil, err
	}
	b.KYCTier = KYCTier(tier)
	b.LastSyncedAt = lastSynced
	b.CreatedAt = created
	b.UpdatedAt = updated
	return &b, nil
}
