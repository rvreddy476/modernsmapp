package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ErrTransactionNotFound is returned when a transaction lookup misses.
var ErrTransactionNotFound = errors.New("transaction: not found")

// CreateTransactionInput is the inbound shape for new wallet.transactions
// rows. Direction MUST be set; Type MUST be one of the CHECK constraint
// values.
type CreateTransactionInput struct {
	UserID             uuid.UUID
	Type               string
	Direction          string
	AmountPaise        int64
	CounterpartyUserID *uuid.UUID
	CounterpartyPhone  *string
	CounterpartyLabel  *string
	MerchantService    *string
	MerchantRef        *string
	Status             string
	BankTxnRef         *string
	UPITxnRef          *string
	IdempotencyKey     *string
	Metadata           map[string]any
}

// dbRunner is the small subset of pgxpool.Pool / pgx.Tx the store needs. Both
// types satisfy it, so callers may pass either a pool or an in-flight tx.
type dbRunner interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// runner returns the right dbRunner: tx if non-nil, else the pool.
func (s *Store) runner(tx pgx.Tx) dbRunner {
	if tx != nil {
		return tx
	}
	return s.db
}

// InsertTransaction creates a new wallet.transactions row and returns the
// generated id + created_at. Pass tx=nil for the simple non-tx path; the
// send-saga passes a tx so the sender debit + transaction-row insert share
// one atomic step.
func (s *Store) InsertTransaction(ctx context.Context, tx pgx.Tx, in CreateTransactionInput) (*Transaction, error) {
	if in.Status == "" {
		in.Status = "pending"
	}
	if in.Direction != "credit" && in.Direction != "debit" {
		return nil, fmt.Errorf("invalid direction: %q", in.Direction)
	}
	if in.AmountPaise <= 0 {
		return nil, fmt.Errorf("amount must be positive")
	}
	meta := []byte(`{}`)
	if in.Metadata != nil {
		b, err := json.Marshal(in.Metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
		meta = b
	}
	const q = `
        INSERT INTO wallet.transactions (
            user_id, type, direction, amount_paise,
            counterparty_user_id, counterparty_phone, counterparty_label,
            merchant_service, merchant_ref,
            status, bank_txn_ref, upi_txn_ref,
            idempotency_key, metadata
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
        RETURNING id, created_at`
	var id uuid.UUID
	var created time.Time
	row := s.runner(tx).QueryRow(ctx, q,
		in.UserID, in.Type, in.Direction, in.AmountPaise,
		in.CounterpartyUserID, in.CounterpartyPhone, in.CounterpartyLabel,
		in.MerchantService, in.MerchantRef,
		in.Status, in.BankTxnRef, in.UPITxnRef,
		in.IdempotencyKey, meta,
	)
	if err := row.Scan(&id, &created); err != nil {
		return nil, fmt.Errorf("insert transaction: %w", err)
	}
	return &Transaction{
		ID:                 id,
		UserID:             in.UserID,
		Type:               in.Type,
		Direction:          in.Direction,
		AmountPaise:        in.AmountPaise,
		CounterpartyUserID: in.CounterpartyUserID,
		CounterpartyPhone:  in.CounterpartyPhone,
		CounterpartyLabel:  in.CounterpartyLabel,
		MerchantService:    in.MerchantService,
		MerchantRef:        in.MerchantRef,
		Status:             in.Status,
		BankTxnRef:         in.BankTxnRef,
		UPITxnRef:          in.UPITxnRef,
		IdempotencyKey:     in.IdempotencyKey,
		CreatedAt:          created,
	}, nil
}

// MarkSettled flips a pending row to a final status. Pass tx=nil for the
// non-tx path. Idempotent: if the row is already settled the call is a
// no-op (rowsAffected = 0 + the row exists).
func (s *Store) MarkSettled(ctx context.Context, tx pgx.Tx, txID uuid.UUID, status string, bankRef *string, failureReason *string) error {
	if status != "succeeded" && status != "failed" && status != "reversed" && status != "pending_invite" {
		return fmt.Errorf("invalid terminal status: %q", status)
	}
	const q = `
        UPDATE wallet.transactions
        SET status = $2,
            bank_txn_ref = COALESCE($3, bank_txn_ref),
            failure_reason = $4,
            settled_at = now()
        WHERE id = $1 AND status = 'pending'`
	tag, err := s.runner(tx).Exec(ctx, q, txID, status, bankRef, failureReason)
	if err != nil {
		return fmt.Errorf("mark settled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		const probe = `SELECT 1 FROM wallet.transactions WHERE id = $1`
		var x int
		if err := s.runner(tx).QueryRow(ctx, probe, txID).Scan(&x); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTransactionNotFound
			}
			return fmt.Errorf("probe transaction: %w", err)
		}
	}
	return nil
}

// SetUPIRef stores the UPI Intent reference on a top-up transaction once the
// client has launched the UPI app. Idempotent: replays from the client are
// safe (only updates when current value is NULL or equal).
func (s *Store) SetUPIRef(ctx context.Context, txID uuid.UUID, upiRef string) error {
	const q = `
        UPDATE wallet.transactions
        SET upi_txn_ref = $2
        WHERE id = $1 AND (upi_txn_ref IS NULL OR upi_txn_ref = $2)`
	tag, err := s.db.Exec(ctx, q, txID, upiRef)
	if err != nil {
		return fmt.Errorf("set upi ref: %w", err)
	}
	if tag.RowsAffected() == 0 {
		const probe = `SELECT upi_txn_ref FROM wallet.transactions WHERE id = $1`
		var existing *string
		if err := s.db.QueryRow(ctx, probe, txID).Scan(&existing); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrTransactionNotFound
			}
			return err
		}
		if existing != nil && *existing != upiRef {
			return fmt.Errorf("upi ref conflict: row already has different ref")
		}
	}
	return nil
}

// GetTransaction fetches a single transaction by id (scoped to user_id).
func (s *Store) GetTransaction(ctx context.Context, userID, txID uuid.UUID) (*Transaction, error) {
	const q = `
        SELECT id, user_id, type, direction, amount_paise,
               counterparty_user_id, counterparty_phone, counterparty_label,
               merchant_service, merchant_ref,
               status, bank_txn_ref, upi_txn_ref, failure_reason,
               idempotency_key, created_at, settled_at
        FROM wallet.transactions WHERE id = $1 AND user_id = $2`
	row := s.db.QueryRow(ctx, q, txID, userID)
	t, err := scanTransaction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTransactionNotFound
		}
		return nil, err
	}
	return t, nil
}

// GetTransactionByID fetches a transaction without a user_id scope. Used by
// internal endpoints (refund) where the caller already authoritatively
// identifies the row.
func (s *Store) GetTransactionByID(ctx context.Context, txID uuid.UUID) (*Transaction, error) {
	const q = `
        SELECT id, user_id, type, direction, amount_paise,
               counterparty_user_id, counterparty_phone, counterparty_label,
               merchant_service, merchant_ref,
               status, bank_txn_ref, upi_txn_ref, failure_reason,
               idempotency_key, created_at, settled_at
        FROM wallet.transactions WHERE id = $1`
	row := s.db.QueryRow(ctx, q, txID)
	t, err := scanTransaction(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTransactionNotFound
		}
		return nil, err
	}
	return t, nil
}

// ListTransactions paginates by created_at DESC, optionally filtered by type
// and direction. Cursor format is RFC3339Nano so it is easy to inspect in
// logs.
func (s *Store) ListTransactions(ctx context.Context, userID uuid.UUID, txType, direction, cursor string, limit int) ([]Transaction, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	args := []any{userID}
	conds := []string{"user_id = $1"}
	if txType != "" {
		args = append(args, txType)
		conds = append(conds, fmt.Sprintf("type = $%d", len(args)))
	}
	if direction != "" {
		args = append(args, direction)
		conds = append(conds, fmt.Sprintf("direction = $%d", len(args)))
	}
	if cursor != "" {
		t, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("invalid cursor: %w", err)
		}
		args = append(args, t)
		conds = append(conds, fmt.Sprintf("created_at < $%d", len(args)))
	}
	args = append(args, limit+1)
	q := `
        SELECT id, user_id, type, direction, amount_paise,
               counterparty_user_id, counterparty_phone, counterparty_label,
               merchant_service, merchant_ref,
               status, bank_txn_ref, upi_txn_ref, failure_reason,
               idempotency_key, created_at, settled_at
        FROM wallet.transactions
        WHERE ` + strings.Join(conds, " AND ") + `
        ORDER BY created_at DESC LIMIT $` + fmt.Sprintf("%d", len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list transactions: %w", err)
	}
	defer rows.Close()
	out := make([]Transaction, 0, limit)
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, "", err
		}
		out = append(out, *t)
	}
	if rows.Err() != nil {
		return nil, "", rows.Err()
	}
	nextCursor := ""
	if len(out) > limit {
		nextCursor = out[limit-1].CreatedAt.Format(time.RFC3339Nano)
		out = out[:limit]
	}
	return out, nextCursor, nil
}

// ListPendingTopUpsOlderThan returns top-ups still pending and created
// before the cutoff. Used by cmd/expirer to mark stale top-ups failed and
// refund pending_in_paise.
func (s *Store) ListPendingTopUpsOlderThan(ctx context.Context, cutoff time.Time, limit int) ([]Transaction, error) {
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	const q = `
        SELECT id, user_id, type, direction, amount_paise,
               counterparty_user_id, counterparty_phone, counterparty_label,
               merchant_service, merchant_ref,
               status, bank_txn_ref, upi_txn_ref, failure_reason,
               idempotency_key, created_at, settled_at
        FROM wallet.transactions
        WHERE type = 'top_up' AND status = 'pending' AND created_at < $1
        ORDER BY created_at ASC LIMIT $2`
	rows, err := s.db.Query(ctx, q, cutoff, limit)
	if err != nil {
		return nil, fmt.Errorf("list pending top-ups: %w", err)
	}
	defer rows.Close()
	var out []Transaction
	for rows.Next() {
		t, err := scanTransaction(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func scanTransaction(row pgx.Row) (*Transaction, error) {
	var t Transaction
	if err := row.Scan(
		&t.ID, &t.UserID, &t.Type, &t.Direction, &t.AmountPaise,
		&t.CounterpartyUserID, &t.CounterpartyPhone, &t.CounterpartyLabel,
		&t.MerchantService, &t.MerchantRef,
		&t.Status, &t.BankTxnRef, &t.UPITxnRef, &t.FailureReason,
		&t.IdempotencyKey, &t.CreatedAt, &t.SettledAt,
	); err != nil {
		return nil, err
	}
	return &t, nil
}
