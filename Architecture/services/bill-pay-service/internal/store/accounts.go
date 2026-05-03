package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrAccountNotFound is returned when an account lookup misses.
var ErrAccountNotFound = errors.New("account: not found")

// CreateAccountInput is the inbound shape for new billpay.accounts rows.
type CreateAccountInput struct {
	UserID          uuid.UUID
	ProviderID      uuid.UUID
	Identifier      string
	ExtraParamsJSON []byte
	Label           string
}

// CreateAccount inserts a new account or returns the existing one if the
// (user_id, provider_id, identifier) tuple is already saved (idempotent).
// Returns the resulting account.
func (s *Store) CreateAccount(ctx context.Context, in CreateAccountInput) (*Account, error) {
	if in.UserID == uuid.Nil || in.ProviderID == uuid.Nil {
		return nil, fmt.Errorf("create account: missing required ids")
	}
	if in.Identifier == "" {
		return nil, fmt.Errorf("create account: identifier required")
	}
	if in.Label == "" {
		return nil, fmt.Errorf("create account: label required")
	}
	if len(in.ExtraParamsJSON) == 0 {
		in.ExtraParamsJSON = []byte("{}")
	}
	const q = `
        INSERT INTO billpay.accounts (user_id, provider_id, identifier, extra_params, label)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (user_id, provider_id, identifier) DO UPDATE
            SET label = EXCLUDED.label,
                extra_params = EXCLUDED.extra_params,
                deleted_at = NULL
        RETURNING id, user_id, provider_id, identifier, extra_params, label,
                  is_default, autopay_enabled, created_at, deleted_at`
	row := s.db.QueryRow(ctx, q,
		in.UserID, in.ProviderID, in.Identifier, in.ExtraParamsJSON, in.Label,
	)
	var a Account
	if err := row.Scan(
		&a.ID, &a.UserID, &a.ProviderID, &a.Identifier, &a.ExtraParams, &a.Label,
		&a.IsDefault, &a.AutopayEnabled, &a.CreatedAt, &a.DeletedAt,
	); err != nil {
		return nil, fmt.Errorf("insert account: %w", err)
	}
	return &a, nil
}

// ListAccountsByUser returns all live (non-deleted) accounts for a user.
func (s *Store) ListAccountsByUser(ctx context.Context, userID uuid.UUID) ([]Account, error) {
	const q = `
        SELECT id, user_id, provider_id, identifier, extra_params, label,
               is_default, autopay_enabled, created_at, deleted_at
        FROM billpay.accounts
        WHERE user_id = $1 AND deleted_at IS NULL
        ORDER BY is_default DESC, created_at DESC`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list accounts: %w", err)
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		var a Account
		if err := rows.Scan(
			&a.ID, &a.UserID, &a.ProviderID, &a.Identifier, &a.ExtraParams, &a.Label,
			&a.IsDefault, &a.AutopayEnabled, &a.CreatedAt, &a.DeletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan account: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetAccount fetches a single account scoped to user_id.
func (s *Store) GetAccount(ctx context.Context, userID, accountID uuid.UUID) (*Account, error) {
	const q = `
        SELECT id, user_id, provider_id, identifier, extra_params, label,
               is_default, autopay_enabled, created_at, deleted_at
        FROM billpay.accounts
        WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	var a Account
	if err := s.db.QueryRow(ctx, q, accountID, userID).Scan(
		&a.ID, &a.UserID, &a.ProviderID, &a.Identifier, &a.ExtraParams, &a.Label,
		&a.IsDefault, &a.AutopayEnabled, &a.CreatedAt, &a.DeletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAccountNotFound
		}
		return nil, fmt.Errorf("get account: %w", err)
	}
	return &a, nil
}

// GetAccountByID fetches an account WITHOUT user scoping. Used by the
// scheduled-cron and reminder-cron which iterate across all users.
func (s *Store) GetAccountByID(ctx context.Context, accountID uuid.UUID) (*Account, error) {
	const q = `
        SELECT id, user_id, provider_id, identifier, extra_params, label,
               is_default, autopay_enabled, created_at, deleted_at
        FROM billpay.accounts WHERE id = $1`
	var a Account
	if err := s.db.QueryRow(ctx, q, accountID).Scan(
		&a.ID, &a.UserID, &a.ProviderID, &a.Identifier, &a.ExtraParams, &a.Label,
		&a.IsDefault, &a.AutopayEnabled, &a.CreatedAt, &a.DeletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAccountNotFound
		}
		return nil, fmt.Errorf("get account by id: %w", err)
	}
	return &a, nil
}

// UpdateAccountInput collects the patchable fields. Pointers are tri-state:
// nil means "leave unchanged".
type UpdateAccountInput struct {
	Label          *string
	IsDefault      *bool
	AutopayEnabled *bool
}

// UpdateAccount applies the given patch. Returns the updated row.
func (s *Store) UpdateAccount(ctx context.Context, userID, accountID uuid.UUID, in UpdateAccountInput) (*Account, error) {
	const q = `
        UPDATE billpay.accounts SET
            label = COALESCE($3, label),
            is_default = COALESCE($4, is_default),
            autopay_enabled = COALESCE($5, autopay_enabled)
        WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL
        RETURNING id, user_id, provider_id, identifier, extra_params, label,
                  is_default, autopay_enabled, created_at, deleted_at`
	var a Account
	if err := s.db.QueryRow(ctx, q,
		accountID, userID, in.Label, in.IsDefault, in.AutopayEnabled,
	).Scan(
		&a.ID, &a.UserID, &a.ProviderID, &a.Identifier, &a.ExtraParams, &a.Label,
		&a.IsDefault, &a.AutopayEnabled, &a.CreatedAt, &a.DeletedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAccountNotFound
		}
		return nil, fmt.Errorf("update account: %w", err)
	}
	return &a, nil
}

// SoftDeleteAccount marks the row as deleted. Idempotent: a second call on an
// already-deleted row returns nil without error.
func (s *Store) SoftDeleteAccount(ctx context.Context, userID, accountID uuid.UUID) error {
	const q = `
        UPDATE billpay.accounts
        SET deleted_at = now()
        WHERE id = $1 AND user_id = $2 AND deleted_at IS NULL`
	tag, err := s.db.Exec(ctx, q, accountID, userID)
	if err != nil {
		return fmt.Errorf("soft delete account: %w", err)
	}
	if tag.RowsAffected() == 0 {
		const probe = `SELECT 1 FROM billpay.accounts WHERE id = $1 AND user_id = $2`
		var x int
		if err := s.db.QueryRow(ctx, probe, accountID, userID).Scan(&x); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrAccountNotFound
			}
			return fmt.Errorf("probe account: %w", err)
		}
		// already deleted — treated as success.
	}
	return nil
}

// InsertBill stores a freshly-fetched bill snapshot from Setu.
type InsertBillInput struct {
	AccountID       uuid.UUID
	BillAmountPaise int64
	BillPeriodStart *string // ISO date, may be empty
	BillPeriodEnd   *string
	BillDueDate     *string
	BillNumber      *string
	CustomerName    *string
	SetuBillRef     *string
}

// InsertBill creates a new billpay.bills row and returns the id.
func (s *Store) InsertBill(ctx context.Context, in InsertBillInput) (*Bill, error) {
	if in.AccountID == uuid.Nil {
		return nil, fmt.Errorf("insert bill: account id required")
	}
	if in.BillAmountPaise < 0 {
		return nil, fmt.Errorf("insert bill: amount may not be negative")
	}
	const q = `
        INSERT INTO billpay.bills (
            account_id, bill_amount_paise, bill_period_start, bill_period_end,
            bill_due_date, bill_number, customer_name, setu_bill_ref, status
        ) VALUES ($1, $2, NULLIF($3,'')::date, NULLIF($4,'')::date,
                  NULLIF($5,'')::date, $6, $7, $8, 'fetched')
        RETURNING id, account_id, bill_amount_paise,
                  bill_period_start, bill_period_end, bill_due_date,
                  bill_number, customer_name, setu_bill_ref, status,
                  fetched_at, paid_at, payment_id`
	var (
		startStr, endStr, dueStr string
	)
	if in.BillPeriodStart != nil {
		startStr = *in.BillPeriodStart
	}
	if in.BillPeriodEnd != nil {
		endStr = *in.BillPeriodEnd
	}
	if in.BillDueDate != nil {
		dueStr = *in.BillDueDate
	}
	var b Bill
	if err := s.db.QueryRow(ctx, q,
		in.AccountID, in.BillAmountPaise, startStr, endStr, dueStr,
		in.BillNumber, in.CustomerName, in.SetuBillRef,
	).Scan(
		&b.ID, &b.AccountID, &b.BillAmountPaise,
		&b.BillPeriodStart, &b.BillPeriodEnd, &b.BillDueDate,
		&b.BillNumber, &b.CustomerName, &b.SetuBillRef, &b.Status,
		&b.FetchedAt, &b.PaidAt, &b.PaymentID,
	); err != nil {
		return nil, fmt.Errorf("insert bill: %w", err)
	}
	return &b, nil
}

// GetBill fetches a single bill by id.
func (s *Store) GetBill(ctx context.Context, billID uuid.UUID) (*Bill, error) {
	const q = `
        SELECT id, account_id, bill_amount_paise,
               bill_period_start, bill_period_end, bill_due_date,
               bill_number, customer_name, setu_bill_ref, status,
               fetched_at, paid_at, payment_id
        FROM billpay.bills WHERE id = $1`
	var b Bill
	if err := s.db.QueryRow(ctx, q, billID).Scan(
		&b.ID, &b.AccountID, &b.BillAmountPaise,
		&b.BillPeriodStart, &b.BillPeriodEnd, &b.BillDueDate,
		&b.BillNumber, &b.CustomerName, &b.SetuBillRef, &b.Status,
		&b.FetchedAt, &b.PaidAt, &b.PaymentID,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("bill: not found")
		}
		return nil, fmt.Errorf("get bill: %w", err)
	}
	return &b, nil
}

// MarkBillPaid flips a bill row from 'fetched' to 'paid' and links the
// payment id. Idempotent: an already-paid row is left alone.
func (s *Store) MarkBillPaid(ctx context.Context, billID, paymentID uuid.UUID) error {
	const q = `
        UPDATE billpay.bills
        SET status = 'paid', paid_at = now(), payment_id = $2
        WHERE id = $1 AND status = 'fetched'`
	if _, err := s.db.Exec(ctx, q, billID, paymentID); err != nil {
		return fmt.Errorf("mark bill paid: %w", err)
	}
	return nil
}
