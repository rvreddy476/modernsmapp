package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrPaymentNotFound is returned when a payment lookup misses.
var ErrPaymentNotFound = errors.New("payment: not found")

// CreatePaymentInput is the inbound shape for new billpay.payments rows.
type CreatePaymentInput struct {
	UserID         uuid.UUID
	AccountID      *uuid.UUID
	ProviderID     uuid.UUID
	AmountPaise    int64
	FeePaise       int64
	PaymentMethod  string
	BillID         *uuid.UUID
	IdempotencyKey string
}

// InsertPayment creates a new billpay.payments row in 'initiated' status.
// idempotency_key has a UNIQUE constraint — duplicate keys return a clear
// error so the caller can read back the existing row.
func (s *Store) InsertPayment(ctx context.Context, in CreatePaymentInput) (*Payment, error) {
	if in.UserID == uuid.Nil || in.ProviderID == uuid.Nil {
		return nil, fmt.Errorf("insert payment: missing required ids")
	}
	if in.AmountPaise <= 0 {
		return nil, fmt.Errorf("insert payment: amount must be positive")
	}
	switch in.PaymentMethod {
	case "wallet", "upi", "card":
	default:
		return nil, fmt.Errorf("insert payment: invalid payment_method %q", in.PaymentMethod)
	}
	if in.IdempotencyKey == "" {
		return nil, fmt.Errorf("insert payment: idempotency_key required")
	}
	const q = `
        INSERT INTO billpay.payments (
            user_id, account_id, provider_id, amount_paise, fee_paise,
            payment_method, bill_id, idempotency_key, status
        ) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'initiated')
        RETURNING id, user_id, account_id, provider_id, amount_paise, fee_paise,
                  payment_method, wallet_txn_id, upi_txn_ref, setu_payment_ref,
                  status, failure_reason, receipt_number, bill_id, idempotency_key,
                  created_at, settled_at`
	var p Payment
	if err := s.db.QueryRow(ctx, q,
		in.UserID, in.AccountID, in.ProviderID, in.AmountPaise, in.FeePaise,
		in.PaymentMethod, in.BillID, in.IdempotencyKey,
	).Scan(
		&p.ID, &p.UserID, &p.AccountID, &p.ProviderID, &p.AmountPaise, &p.FeePaise,
		&p.PaymentMethod, &p.WalletTxnID, &p.UPITxnRef, &p.SetuPaymentRef,
		&p.Status, &p.FailureReason, &p.ReceiptNumber, &p.BillID, &p.IdempotencyKey,
		&p.CreatedAt, &p.SettledAt,
	); err != nil {
		return nil, fmt.Errorf("insert payment: %w", err)
	}
	return &p, nil
}

// GetPayment fetches a single payment scoped to user_id.
func (s *Store) GetPayment(ctx context.Context, userID, paymentID uuid.UUID) (*Payment, error) {
	const q = `
        SELECT id, user_id, account_id, provider_id, amount_paise, fee_paise,
               payment_method, wallet_txn_id, upi_txn_ref, setu_payment_ref,
               status, failure_reason, receipt_number, bill_id, idempotency_key,
               created_at, settled_at
        FROM billpay.payments
        WHERE id = $1 AND user_id = $2`
	var p Payment
	if err := s.db.QueryRow(ctx, q, paymentID, userID).Scan(
		&p.ID, &p.UserID, &p.AccountID, &p.ProviderID, &p.AmountPaise, &p.FeePaise,
		&p.PaymentMethod, &p.WalletTxnID, &p.UPITxnRef, &p.SetuPaymentRef,
		&p.Status, &p.FailureReason, &p.ReceiptNumber, &p.BillID, &p.IdempotencyKey,
		&p.CreatedAt, &p.SettledAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, fmt.Errorf("get payment: %w", err)
	}
	return &p, nil
}

// GetPaymentByID fetches without user scoping. Used by the Setu webhook
// receiver and the scheduled-cron's failure reporting.
func (s *Store) GetPaymentByID(ctx context.Context, paymentID uuid.UUID) (*Payment, error) {
	const q = `
        SELECT id, user_id, account_id, provider_id, amount_paise, fee_paise,
               payment_method, wallet_txn_id, upi_txn_ref, setu_payment_ref,
               status, failure_reason, receipt_number, bill_id, idempotency_key,
               created_at, settled_at
        FROM billpay.payments WHERE id = $1`
	var p Payment
	if err := s.db.QueryRow(ctx, q, paymentID).Scan(
		&p.ID, &p.UserID, &p.AccountID, &p.ProviderID, &p.AmountPaise, &p.FeePaise,
		&p.PaymentMethod, &p.WalletTxnID, &p.UPITxnRef, &p.SetuPaymentRef,
		&p.Status, &p.FailureReason, &p.ReceiptNumber, &p.BillID, &p.IdempotencyKey,
		&p.CreatedAt, &p.SettledAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, fmt.Errorf("get payment by id: %w", err)
	}
	return &p, nil
}

// GetPaymentBySetuRef looks up by Setu's payment ref. Used by the webhook
// receiver to find the canonical row.
func (s *Store) GetPaymentBySetuRef(ctx context.Context, setuRef string) (*Payment, error) {
	const q = `
        SELECT id, user_id, account_id, provider_id, amount_paise, fee_paise,
               payment_method, wallet_txn_id, upi_txn_ref, setu_payment_ref,
               status, failure_reason, receipt_number, bill_id, idempotency_key,
               created_at, settled_at
        FROM billpay.payments WHERE setu_payment_ref = $1`
	var p Payment
	if err := s.db.QueryRow(ctx, q, setuRef).Scan(
		&p.ID, &p.UserID, &p.AccountID, &p.ProviderID, &p.AmountPaise, &p.FeePaise,
		&p.PaymentMethod, &p.WalletTxnID, &p.UPITxnRef, &p.SetuPaymentRef,
		&p.Status, &p.FailureReason, &p.ReceiptNumber, &p.BillID, &p.IdempotencyKey,
		&p.CreatedAt, &p.SettledAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPaymentNotFound
		}
		return nil, fmt.Errorf("get payment by setu ref: %w", err)
	}
	return &p, nil
}

// MarkPaymentSubmitted flips initiated → submitted, attaching Setu's
// payment ref. Idempotent: re-running on a row already past 'submitted'
// is a no-op.
func (s *Store) MarkPaymentSubmitted(ctx context.Context, paymentID uuid.UUID, setuRef string) error {
	const q = `
        UPDATE billpay.payments
        SET status = 'submitted',
            setu_payment_ref = $2
        WHERE id = $1 AND status = 'initiated'`
	if _, err := s.db.Exec(ctx, q, paymentID, setuRef); err != nil {
		return fmt.Errorf("mark payment submitted: %w", err)
	}
	return nil
}

// MarkPaymentSucceeded flips a payment to terminal 'succeeded'. Sets
// receipt_number (BBPS RRN). Idempotent.
func (s *Store) MarkPaymentSucceeded(ctx context.Context, paymentID uuid.UUID, receiptNumber string) error {
	const q = `
        UPDATE billpay.payments
        SET status = 'succeeded',
            receipt_number = COALESCE(NULLIF($2,''), receipt_number),
            settled_at = now()
        WHERE id = $1 AND status IN ('initiated','submitted')`
	if _, err := s.db.Exec(ctx, q, paymentID, receiptNumber); err != nil {
		return fmt.Errorf("mark payment succeeded: %w", err)
	}
	return nil
}

// MarkPaymentFailed flips a payment to terminal 'failed' with a reason.
// Idempotent.
func (s *Store) MarkPaymentFailed(ctx context.Context, paymentID uuid.UUID, reason string) error {
	const q = `
        UPDATE billpay.payments
        SET status = 'failed',
            failure_reason = $2,
            settled_at = now()
        WHERE id = $1 AND status IN ('initiated','submitted')`
	if _, err := s.db.Exec(ctx, q, paymentID, reason); err != nil {
		return fmt.Errorf("mark payment failed: %w", err)
	}
	return nil
}

// MarkPaymentRefunded flips a succeeded payment to 'refunded'.
func (s *Store) MarkPaymentRefunded(ctx context.Context, paymentID uuid.UUID, reason string) error {
	const q = `
        UPDATE billpay.payments
        SET status = 'refunded',
            failure_reason = COALESCE(NULLIF($2,''), failure_reason)
        WHERE id = $1 AND status IN ('succeeded','failed')`
	if _, err := s.db.Exec(ctx, q, paymentID, reason); err != nil {
		return fmt.Errorf("mark payment refunded: %w", err)
	}
	return nil
}

// AttachWalletTxn records the wallet-service transaction id on a payment row.
// Used right after the wallet debit succeeds.
func (s *Store) AttachWalletTxn(ctx context.Context, paymentID, walletTxnID uuid.UUID) error {
	const q = `
        UPDATE billpay.payments
        SET wallet_txn_id = $2
        WHERE id = $1 AND wallet_txn_id IS NULL`
	if _, err := s.db.Exec(ctx, q, paymentID, walletTxnID); err != nil {
		return fmt.Errorf("attach wallet txn: %w", err)
	}
	return nil
}

// ListPaymentsByUser returns paginated payments for a user. cursor is the
// created_at timestamp of the last row of the previous page (RFC3339Nano).
// status filter is optional; pass "" for no filter.
func (s *Store) ListPaymentsByUser(ctx context.Context, userID uuid.UUID, status, cursor string, limit int) ([]Payment, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	const q = `
        SELECT id, user_id, account_id, provider_id, amount_paise, fee_paise,
               payment_method, wallet_txn_id, upi_txn_ref, setu_payment_ref,
               status, failure_reason, receipt_number, bill_id, idempotency_key,
               created_at, settled_at
        FROM billpay.payments
        WHERE user_id = $1
          AND ($2 = '' OR status = $2)
          AND ($3 = '' OR created_at < $3::timestamptz)
        ORDER BY created_at DESC
        LIMIT $4`
	rows, err := s.db.Query(ctx, q, userID, status, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("list payments: %w", err)
	}
	defer rows.Close()
	var out []Payment
	for rows.Next() {
		var p Payment
		if err := rows.Scan(
			&p.ID, &p.UserID, &p.AccountID, &p.ProviderID, &p.AmountPaise, &p.FeePaise,
			&p.PaymentMethod, &p.WalletTxnID, &p.UPITxnRef, &p.SetuPaymentRef,
			&p.Status, &p.FailureReason, &p.ReceiptNumber, &p.BillID, &p.IdempotencyKey,
			&p.CreatedAt, &p.SettledAt,
		); err != nil {
			return nil, fmt.Errorf("scan payment: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
