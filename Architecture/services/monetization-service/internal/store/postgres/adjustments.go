package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ---------------------------------------------------------------------------
// Adjustments — the correction primitive (plan Phase 2A)
// ---------------------------------------------------------------------------
//
// An adjustment is a signed movement on a creator's wallet that is
// identified by its CAUSE, not by the call that made it. The idempotency
// key is the cause ('adj:<kind>:<ref>'); the transactions row is inserted
// ON CONFLICT DO NOTHING on that key, and only when that insert lands does
// the wallet move and the double-entry leg get written — all in the same
// transaction. Posting the same cause again returns the row that already
// exists and moves nothing.
//
// A settled earning's money columns are never updated by this path. The
// correction is a NEW row that points at its cause.

// AdjustmentInput is everything PostAdjustmentTx needs. The two account
// IDs are resolved by the caller (Service.EnsureAccount) so the ledger leg
// commits with the wallet movement; leave them nil to skip the leg, which
// only tests should do.
type AdjustmentInput struct {
	CreatorID      uuid.UUID
	AmountPaise    int64 // signed; negative takes money back
	Currency       string
	IdempotencyKey string
	ReferenceType  string
	ReferenceID    string
	Description    string

	CreatorWalletAccountID   uuid.UUID
	PlatformRevenueAccountID uuid.UUID
	LedgerReferenceType      string
	LedgerReferenceID        *uuid.UUID
}

// AdjustmentResult reports what happened. Applied is false when the cause
// had already been posted: Transaction is then the existing row and
// BalanceAfter the wallet as it stands, untouched.
type AdjustmentResult struct {
	Transaction  Transaction
	Applied      bool
	BalanceAfter int64
}

// PostAdjustmentTx applies one adjustment inside the caller's transaction:
// lock the ledger row, insert the keyed transaction, move the balance by
// the signed amount, write the opposing double-entry leg.
//
// The balance may go below zero. That is the true state after reversing a
// credit the creator has already withdrawn; it blocks further withdrawal
// (RequestPayout checks balance >= amount) until earned back.
func (s *Store) PostAdjustmentTx(ctx context.Context, tx pgx.Tx, in AdjustmentInput) (*AdjustmentResult, error) {
	if in.AmountPaise == 0 {
		return nil, errors.New("ADJUSTMENT_ZERO: amount must be non-zero")
	}
	if in.IdempotencyKey == "" {
		return nil, errors.New("ADJUSTMENT_UNKEYED: an adjustment must carry its cause")
	}
	if in.Currency == "" {
		in.Currency = "INR"
	}

	// The wallet row must exist before it is locked; a creator whose only
	// prior activity was accruing has none yet.
	if _, err := tx.Exec(ctx, `
		INSERT INTO creator_ledger (user_id, balance, currency)
		VALUES ($1, 0, $2)
		ON CONFLICT (user_id) DO NOTHING
	`, in.CreatorID, in.Currency); err != nil {
		return nil, fmt.Errorf("ensure ledger row: %w", err)
	}
	var balance int64
	if err := tx.QueryRow(ctx, `
		SELECT balance FROM creator_ledger WHERE user_id = $1 FOR UPDATE
	`, in.CreatorID).Scan(&balance); err != nil {
		return nil, fmt.Errorf("lock ledger row: %w", err)
	}

	t := Transaction{
		ID:            uuid.New(),
		WalletID:      in.CreatorID,
		Type:          "adjustment",
		AmountPaise:   in.AmountPaise,
		Currency:      in.Currency,
		Status:        "completed",
		ReferenceType: in.ReferenceType,
		ReferenceID:   in.ReferenceID,
		Description:   in.Description,
		CreatedAt:     time.Now(),
	}
	tag, err := tx.Exec(ctx, `
		INSERT INTO transactions (
			id, wallet_id, type, amount, currency, status,
			reference_type, reference_id, description, idempotency_key, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
		ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
	`, t.ID, t.WalletID, t.Type, t.AmountPaise, t.Currency, t.Status,
		t.ReferenceType, t.ReferenceID, t.Description, in.IdempotencyKey, t.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert adjustment transaction: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Already applied. Hand back the row that did it; move no money.
		existing, err := getTransactionByIdempotencyKey(ctx, tx, in.IdempotencyKey)
		if err != nil {
			return nil, err
		}
		if existing == nil {
			return nil, fmt.Errorf("adjustment %q conflicted but no row carries the key", in.IdempotencyKey)
		}
		return &AdjustmentResult{Transaction: *existing, Applied: false, BalanceAfter: balance}, nil
	}

	var after int64
	if err := tx.QueryRow(ctx, `
		UPDATE creator_ledger
		SET balance = balance + $2, updated_at = NOW()
		WHERE user_id = $1
		RETURNING balance
	`, in.CreatorID, in.AmountPaise).Scan(&after); err != nil {
		return nil, fmt.Errorf("move balance: %w", err)
	}

	// The opposing double-entry leg. A positive adjustment is money the
	// platform gives the creator (platform_revenue -> wallet); a negative
	// one is money the creator gives back (wallet -> platform_revenue).
	// ledger_entries.amount_paise is CHECKed > 0, so direction carries the
	// sign.
	if in.CreatorWalletAccountID != uuid.Nil && in.PlatformRevenueAccountID != uuid.Nil {
		debit, credit := in.PlatformRevenueAccountID, in.CreatorWalletAccountID
		amount := in.AmountPaise
		if amount < 0 {
			debit, credit = in.CreatorWalletAccountID, in.PlatformRevenueAccountID
			amount = -amount
		}
		refType := in.LedgerReferenceType
		if refType == "" {
			refType = "adjustment"
		}
		if err := insertLedgerLegRef(ctx, tx, debit, credit, amount, in.Currency,
			refType, in.LedgerReferenceID, in.IdempotencyKey, in.Description); err != nil {
			return nil, fmt.Errorf("ledger leg: %w", err)
		}
	}
	return &AdjustmentResult{Transaction: t, Applied: true, BalanceAfter: after}, nil
}

// FeeLegInput describes the platform-fee leg mirrored back out when the
// income it was taken from is reversed.
type FeeLegInput struct {
	AmountPaise         int64
	Currency            string
	IdempotencyKey      string // 'adj_fee:<kind>:<ref>'
	PlatformFeeAccount  uuid.UUID
	PlatformRevenueAcct uuid.UUID
	ReferenceType       string
	ReferenceID         *uuid.UUID
	Description         string
}

// PostFeeReversalLegTx moves a platform fee back out of
// platform_revenue_fees into platform_revenue, keyed on the cause so a
// retry cannot post it twice. No wallet is involved: the fee never
// touched one. Returns false when the key had already been posted.
func (s *Store) PostFeeReversalLegTx(ctx context.Context, tx pgx.Tx, in FeeLegInput) (bool, error) {
	if in.AmountPaise <= 0 {
		return false, nil
	}
	if in.IdempotencyKey == "" {
		return false, errors.New("FEE_LEG_UNKEYED: a fee reversal must carry its cause")
	}
	if in.Currency == "" {
		in.Currency = "INR"
	}
	var exists bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM ledger_entries WHERE idempotency_key = $1)`, in.IdempotencyKey).Scan(&exists); err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}
	refType := in.ReferenceType
	if refType == "" {
		refType = "adjustment_fee"
	}
	if err := insertLedgerLegRef(ctx, tx, in.PlatformFeeAccount, in.PlatformRevenueAcct, in.AmountPaise, in.Currency,
		refType, in.ReferenceID, in.IdempotencyKey, in.Description); err != nil {
		return false, err
	}
	return true, nil
}

// FreezeLedgerTx sets is_frozen on the creator's ledger row. Used when a
// reversal drives the balance below zero: the money is already gone and a
// human has to decide what happens next.
func (s *Store) FreezeLedgerTx(ctx context.Context, tx pgx.Tx, creatorID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE creator_ledger SET is_frozen = TRUE, updated_at = NOW() WHERE user_id = $1
	`, creatorID)
	return err
}

// GetTransactionByIdempotencyKey returns the adjustment posted under a
// cause, or nil.
func (s *Store) GetTransactionByIdempotencyKey(ctx context.Context, key string) (*Transaction, error) {
	return getTransactionByIdempotencyKey(ctx, s.db, key)
}

func getTransactionByIdempotencyKey(ctx context.Context, q DBTX, key string) (*Transaction, error) {
	var t Transaction
	var desc *string
	err := q.QueryRow(ctx, `
		SELECT id, wallet_id, type, amount, currency, status, reference_type, reference_id, description, created_at
		FROM transactions WHERE idempotency_key = $1
	`, key).Scan(&t.ID, &t.WalletID, &t.Type, &t.AmountPaise, &t.Currency, &t.Status,
		&t.ReferenceType, &t.ReferenceID, &desc, &t.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if desc != nil {
		t.Description = *desc
	}
	return &t, nil
}

// SumAdjustmentsBetween totals the completed adjustment transactions that
// LANDED on a creator's wallet in [from, to) — signed, so a reversal and a
// later correction net against each other on the statement of the period
// they were posted in.
func (s *Store) SumAdjustmentsBetween(ctx context.Context, creatorID uuid.UUID, from, to time.Time) (count int64, totalPaise int64, err error) {
	err = s.db.QueryRow(ctx, `
		SELECT COUNT(*)::BIGINT, COALESCE(SUM(amount), 0)::BIGINT
		FROM transactions
		WHERE wallet_id = $1
		  AND type = 'adjustment'
		  AND status = 'completed'
		  AND created_at >= $2 AND created_at < $3
	`, creatorID, from, to).Scan(&count, &totalPaise)
	return
}

// insertLedgerLegRef is insertLedgerLeg with a nullable reference id.
func insertLedgerLegRef(ctx context.Context, tx pgx.Tx, debitAccountID, creditAccountID uuid.UUID, amountPaise int64, currency, refType string, refID *uuid.UUID, idempotencyKey, description string) error {
	if amountPaise <= 0 {
		return nil
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO ledger_entries (
			id, debit_account_id, credit_account_id, amount_paise, currency,
			reference_type, reference_id, idempotency_key, description, created_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,NOW())
	`, uuid.New(), debitAccountID, creditAccountID, amountPaise, currency,
		refType, refID, idempotencyKey, description); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE accounts SET balance_paise = balance_paise - $2 WHERE id = $1`,
		debitAccountID, amountPaise); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx,
		`UPDATE accounts SET balance_paise = balance_paise + $2 WHERE id = $1`,
		creditAccountID, amountPaise); err != nil {
		return err
	}
	return nil
}
