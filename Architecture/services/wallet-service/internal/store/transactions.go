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
// non-tx path. The update is conditional on status = 'pending', so exactly
// one caller can make the transition and only that caller sees
// transitioned=true. A replay against an already-settled row is not an
// error but reports false: the caller must not move money on false.
func (s *Store) MarkSettled(ctx context.Context, tx pgx.Tx, txID uuid.UUID, status string, bankRef *string, failureReason *string) (transitioned bool, err error) {
	_, transitioned, err = s.settlePending(ctx, tx, txID, status, bankRef, failureReason)
	return transitioned, err
}

// settledRow is what a pending->final transition hands back: whose money it
// was and how much, read from the row itself rather than from the caller.
type settledRow struct {
	UserID      uuid.UUID
	AmountPaise int64
}

// settlePending is the one conditional pending->final update. ok=false with
// a nil error means the row exists but is no longer pending; a missing row
// is ErrTransactionNotFound.
func (s *Store) settlePending(ctx context.Context, tx pgx.Tx, txID uuid.UUID, status string, bankRef, failureReason *string) (settledRow, bool, error) {
	if status != "succeeded" && status != "failed" && status != "reversed" && status != "pending_invite" {
		return settledRow{}, false, fmt.Errorf("invalid terminal status: %q", status)
	}
	const q = `
        UPDATE wallet.transactions
        SET status = $2,
            bank_txn_ref = COALESCE($3, bank_txn_ref),
            failure_reason = $4,
            settled_at = now()
        WHERE id = $1 AND status = 'pending'
        RETURNING user_id, amount_paise`
	var row settledRow
	err := s.runner(tx).QueryRow(ctx, q, txID, status, bankRef, failureReason).Scan(&row.UserID, &row.AmountPaise)
	if err == nil {
		return row, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return settledRow{}, false, fmt.Errorf("mark settled: %w", err)
	}
	const probe = `SELECT 1 FROM wallet.transactions WHERE id = $1`
	var x int
	if err := s.runner(tx).QueryRow(ctx, probe, txID).Scan(&x); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return settledRow{}, false, ErrTransactionNotFound
		}
		return settledRow{}, false, fmt.Errorf("probe transaction: %w", err)
	}
	return settledRow{}, false, nil
}

// ErrUPIRefAlreadyUsed is returned by SettleTopUp when a different top-up has
// already been credited for the same UPI reference: one inbound UPI payment
// credits one top-up, never two.
var ErrUPIRefAlreadyUsed = errors.New("top-up: upi reference already credited to another top-up")

// SettleTopUp marks a pending top-up succeeded AND credits its amount to the
// owner's available balance in ONE database transaction. The settle is the
// conditional update in settlePending, so when two confirms race only the
// one that made the transition credits; the other gets settled=false and no
// money moves. The amount credited is the row's own amount_paise.
//
// The row's upi_txn_ref (stored by SetUPIRef before the bank check) must not
// already have credited another top-up. Refs are serialised with a
// transaction-scoped advisory lock so two rows carrying the same ref cannot
// both pass that check; a reused ref leaves the row pending (the expirer
// fails it) and returns ErrUPIRefAlreadyUsed.
func (s *Store) SettleTopUp(ctx context.Context, txID uuid.UUID, bankRef string) (settled bool, err error) {
	pgtx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin settle top-up: %w", err)
	}
	defer func() { _ = pgtx.Rollback(ctx) }()

	var upiRef *string
	const refQ = `SELECT upi_txn_ref FROM wallet.transactions WHERE id = $1 AND type = 'top_up'`
	if err := pgtx.QueryRow(ctx, refQ, txID).Scan(&upiRef); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrTransactionNotFound
		}
		return false, fmt.Errorf("read top-up upi ref: %w", err)
	}
	if upiRef != nil {
		if _, err := pgtx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('wallet.upi_ref:' || $1::text))`, *upiRef); err != nil {
			return false, fmt.Errorf("lock upi ref: %w", err)
		}
		const usedQ = `
            SELECT EXISTS (
                SELECT 1 FROM wallet.transactions
                WHERE type = 'top_up' AND status = 'succeeded'
                  AND upi_txn_ref = $1 AND id <> $2)`
		var reused bool
		if err := pgtx.QueryRow(ctx, usedQ, *upiRef, txID).Scan(&reused); err != nil {
			return false, fmt.Errorf("check upi ref reuse: %w", err)
		}
		if reused {
			return false, ErrUPIRefAlreadyUsed
		}
	}

	ref := bankRef
	row, ok, err := s.settlePending(ctx, pgtx, txID, "succeeded", &ref, nil)
	if err != nil || !ok {
		return false, err
	}
	if err := s.CreditAvailable(ctx, pgtx, row.UserID, row.AmountPaise); err != nil {
		return false, err
	}
	if err := pgtx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit settle top-up: %w", err)
	}
	return true, nil
}

// ExpireTopUp marks a pending top-up failed AND releases its pending_in_paise
// in ONE database transaction, only when this call made the transition. A
// top-up a concurrent confirm already settled reports expired=false and the
// balance is left alone.
func (s *Store) ExpireTopUp(ctx context.Context, txID uuid.UUID, reason string) (expired bool, err error) {
	pgtx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin expire top-up: %w", err)
	}
	defer func() { _ = pgtx.Rollback(ctx) }()

	why := reason
	row, ok, err := s.settlePending(ctx, pgtx, txID, "failed", nil, &why)
	if err != nil || !ok {
		return false, err
	}
	if err := s.AdjustPendingIn(ctx, pgtx, row.UserID, -row.AmountPaise); err != nil {
		return false, err
	}
	if err := pgtx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit expire top-up: %w", err)
	}
	return true, nil
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

// transactionColumns is the column list scanTransaction reads.
const transactionColumns = `id, user_id, type, direction, amount_paise,
               counterparty_user_id, counterparty_phone, counterparty_label,
               merchant_service, merchant_ref,
               status, bank_txn_ref, upi_txn_ref, failure_reason,
               idempotency_key, created_at, settled_at`

// ClaimIdempotencyKeyTx serialises money-moving calls that carry the same
// (user, type, idempotency key) for the rest of tx, then returns the row an
// earlier call already wrote under that key, or nil when this call is the
// first. The wallet.idempotency lookup that callers make first is only a
// fast path: two concurrent calls both miss it, and without this claim both
// would debit. The lock is transaction-scoped (released on commit or
// rollback), and the earlier call's row is visible once it has committed.
func (s *Store) ClaimIdempotencyKeyTx(ctx context.Context, tx pgx.Tx, userID uuid.UUID, txType, key string) (*Transaction, error) {
	if key == "" {
		return nil, fmt.Errorf("claim idempotency: key required")
	}
	const lockQ = `SELECT pg_advisory_xact_lock(hashtext('wallet.idem:' || $1::text || ':' || $2::text || ':' || $3::text))`
	if _, err := tx.Exec(ctx, lockQ, userID.String(), txType, key); err != nil {
		return nil, fmt.Errorf("lock idempotency key: %w", err)
	}
	q := `SELECT ` + transactionColumns + `
        FROM wallet.transactions
        WHERE user_id = $1 AND type = $2 AND idempotency_key = $3
        ORDER BY created_at LIMIT 1`
	t, err := scanTransaction(tx.QueryRow(ctx, q, userID, txType, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find prior transaction for key: %w", err)
	}
	return t, nil
}

// LockForRefundTx locks the original transaction row (FOR UPDATE) for the
// rest of tx and returns it with the paise already refunded against it.
// Holding the lock while the refund is checked, sent to the bank and
// recorded is what stops two concurrent refunds from both passing the
// "not more than the original" check.
func (s *Store) LockForRefundTx(ctx context.Context, tx pgx.Tx, originalID uuid.UUID) (*Transaction, int64, error) {
	q := `SELECT ` + transactionColumns + ` FROM wallet.transactions WHERE id = $1 FOR UPDATE`
	original, err := scanTransaction(tx.QueryRow(ctx, q, originalID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, 0, ErrTransactionNotFound
		}
		return nil, 0, fmt.Errorf("lock original transaction: %w", err)
	}
	const sumQ = `
        SELECT COALESCE(SUM(amount_paise), 0)::bigint
        FROM wallet.transactions
        WHERE user_id = $1 AND type = 'refund'
          AND status IN ('pending', 'succeeded')
          AND metadata->>'original_transaction_id' = $2`
	var refunded int64
	if err := tx.QueryRow(ctx, sumQ, original.UserID, originalID.String()).Scan(&refunded); err != nil {
		return nil, 0, fmt.Errorf("sum prior refunds: %w", err)
	}
	return original, refunded, nil
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
