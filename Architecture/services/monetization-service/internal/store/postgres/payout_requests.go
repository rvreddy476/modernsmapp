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
// payout_requests — the withdrawal record (plan Phase 3A)
// ---------------------------------------------------------------------------
//
// Everything here takes the caller's transaction. Service.RequestPayout
// composes these under one WithTx so the ledger lock, the hold or the
// TDS entry, the transactions row, the double-entry leg and the
// payout_requests insert commit or vanish together.

// PayoutRequestRow is the full payout_requests row as written by the
// withdrawal path. TransactionID and NetPaise are nil on a held request:
// it was recorded before it was priced, and no money moved.
type PayoutRequestRow struct {
	ID             uuid.UUID  `json:"id"`
	UserID         uuid.UUID  `json:"user_id"`
	TransactionID  *uuid.UUID `json:"transaction_id,omitempty"`
	AmountPaise    int64      `json:"amount_paise"`
	Currency       string     `json:"currency"`
	Status         string     `json:"status"`
	PayoutMethodID *uuid.UUID `json:"payout_method_id,omitempty"`
	RequestedAt    time.Time  `json:"requested_at"`
	ProcessedAt    *time.Time `json:"processed_at,omitempty"`
	Notes          string     `json:"notes,omitempty"`
	TDSPaise       int64      `json:"tds_paise"`
	NetPaise       *int64     `json:"net_paise,omitempty"`
	IdempotencyKey *string    `json:"idempotency_key,omitempty"`

	// The rail's columns (plan Phase 4A). ProviderReference is the
	// provider's payout id once the request has reached it; UTR is the
	// bank's reference, captured on paid.
	ProviderReference *string    `json:"provider_reference,omitempty"`
	ProviderStatus    *string    `json:"provider_status,omitempty"`
	SubmittedAt       *time.Time `json:"submitted_at,omitempty"`
	LastReconciledAt  *time.Time `json:"last_reconciled_at,omitempty"`
	UTR               *string    `json:"utr,omitempty"`
	FailureReason     *string    `json:"failure_reason,omitempty"`
	RetryCount        int        `json:"retry_count"`
}

const payoutRequestColumns = `id, user_id, transaction_id, amount, currency, status, payout_method_id,
	requested_at, processed_at, notes, tds_paise, net_paise, idempotency_key,
	provider_reference, provider_status, submitted_at, last_reconciled_at, utr, failure_reason, retry_count`

func scanPayoutRequestRow(row pgx.Row) (*PayoutRequestRow, error) {
	var r PayoutRequestRow
	if err := row.Scan(
		&r.ID, &r.UserID, &r.TransactionID, &r.AmountPaise, &r.Currency, &r.Status, &r.PayoutMethodID,
		&r.RequestedAt, &r.ProcessedAt, &r.Notes, &r.TDSPaise, &r.NetPaise, &r.IdempotencyKey,
		&r.ProviderReference, &r.ProviderStatus, &r.SubmittedAt, &r.LastReconciledAt, &r.UTR, &r.FailureReason, &r.RetryCount,
	); err != nil {
		return nil, err
	}
	return &r, nil
}

// InsertPayoutRequestTx writes one payout_requests row. The caller sets
// the id so the transactions row and the ledger leg can be keyed to it
// before it exists.
func (s *Store) InsertPayoutRequestTx(ctx context.Context, tx pgx.Tx, r *PayoutRequestRow) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	if r.Currency == "" {
		r.Currency = "INR"
	}
	if r.RequestedAt.IsZero() {
		r.RequestedAt = time.Now()
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO payout_requests
			(id, user_id, transaction_id, amount, currency, status, payout_method_id,
			 requested_at, notes, tds_paise, net_paise, idempotency_key)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
	`, r.ID, r.UserID, r.TransactionID, r.AmountPaise, r.Currency, r.Status, r.PayoutMethodID,
		r.RequestedAt, r.Notes, r.TDSPaise, r.NetPaise, r.IdempotencyKey)
	if err != nil {
		return fmt.Errorf("insert payout request: %w", err)
	}
	return nil
}

// GetPayoutRequestByIdempotencyKeyTx returns the request a client key was
// already used for, or nil.
func (s *Store) GetPayoutRequestByIdempotencyKeyTx(ctx context.Context, db DBTX, key string) (*PayoutRequestRow, error) {
	r, err := scanPayoutRequestRow(db.QueryRow(ctx,
		`SELECT `+payoutRequestColumns+` FROM payout_requests WHERE idempotency_key = $1`, key))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// GetPayoutRequest returns one request by id, or nil.
func (s *Store) GetPayoutRequest(ctx context.Context, id uuid.UUID) (*PayoutRequestRow, error) {
	r, err := scanPayoutRequestRow(s.db.QueryRow(ctx,
		`SELECT `+payoutRequestColumns+` FROM payout_requests WHERE id = $1`, id))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return r, nil
}

// CountPayoutRequestsSinceTx counts every request a creator has made
// since `since`, whatever became of it. A held request counts: the
// velocity gate is about attempts, not successes.
func (s *Store) CountPayoutRequestsSinceTx(ctx context.Context, db DBTX, userID uuid.UUID, since time.Time) (int, error) {
	var n int
	err := db.QueryRow(ctx, `
		SELECT count(*) FROM payout_requests WHERE user_id = $1 AND requested_at >= $2
	`, userID, since).Scan(&n)
	return n, err
}

// LockedLedger is the creator_ledger row as read under FOR UPDATE.
type LockedLedger struct {
	BalancePaise       int64
	PendingPayoutPaise int64
	IsFrozen           bool
	CreatedAt          time.Time
}

// LockLedgerTx locks the creator's ledger row for the rest of the
// transaction and returns what it holds, or nil when the creator has no
// ledger row at all. Serialising on this row is what makes two concurrent
// withdrawals of the same balance impossible.
func (s *Store) LockLedgerTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID) (*LockedLedger, error) {
	var l LockedLedger
	err := tx.QueryRow(ctx, `
		SELECT balance, pending_payout, is_frozen, created_at
		FROM creator_ledger
		WHERE user_id = $1
		FOR UPDATE
	`, userID).Scan(&l.BalancePaise, &l.PendingPayoutPaise, &l.IsFrozen, &l.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("lock ledger row: %w", err)
	}
	return &l, nil
}

// GetPayoutMethodTx returns the payout method with this id IF it belongs
// to userID; nil otherwise. A method that exists but is someone else's is
// indistinguishable from one that does not exist, on purpose.
func (s *Store) GetPayoutMethodTx(ctx context.Context, db DBTX, userID, methodID uuid.UUID) (*PayoutMethod, error) {
	m, err := scanPayoutMethod(db.QueryRow(ctx,
		`SELECT `+payoutMethodColumns+` FROM payout_methods WHERE id = $1 AND user_id = $2`, methodID, userID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return m, nil
}

// MoveBalanceToPendingPayoutTx is the ledger move of a withdrawal:
// balance -= gross, pending_payout += gross. The balance guard is
// repeated in SQL even though the caller checked it under the lock.
func (s *Store) MoveBalanceToPendingPayoutTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, grossPaise int64) error {
	tag, err := tx.Exec(ctx, `
		UPDATE creator_ledger
		SET balance = balance - $2, pending_payout = pending_payout + $2, updated_at = NOW()
		WHERE user_id = $1 AND balance >= $2
	`, userID, grossPaise)
	if err != nil {
		return fmt.Errorf("move balance to pending payout: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInsufficientFunds
	}
	return nil
}

// InsertPayoutTransactionTx writes the pending payout transaction, keyed
// 'payout:<request id>' so a replayed insert cannot land twice.
func (s *Store) InsertPayoutTransactionTx(ctx context.Context, tx pgx.Tx, t *Transaction, idempotencyKey string) error {
	if t.ID == uuid.Nil {
		t.ID = uuid.New()
	}
	if t.CreatedAt.IsZero() {
		t.CreatedAt = time.Now()
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO transactions (
			id, wallet_id, type, amount, currency, status,
			reference_type, reference_id, description, idempotency_key, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`, t.ID, t.WalletID, t.Type, t.AmountPaise, t.Currency, t.Status,
		t.ReferenceType, t.ReferenceID, t.Description, idempotencyKey, t.CreatedAt)
	if err != nil {
		return fmt.Errorf("insert payout transaction: %w", err)
	}
	return nil
}
