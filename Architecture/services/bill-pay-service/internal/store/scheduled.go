package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrScheduledNotFound is returned when a scheduled-payment lookup misses.
var ErrScheduledNotFound = errors.New("scheduled: not found")

// CreateScheduledInput is the inbound shape for new billpay.scheduled_payments
// rows.
type CreateScheduledInput struct {
	UserID        uuid.UUID
	AccountID     uuid.UUID
	AmountPaise   *int64 // nil = pay full bill amount
	PaymentMethod string
	ScheduleKind  string
	NextRunDate   time.Time
}

// CreateScheduled inserts a new row and returns it.
func (s *Store) CreateScheduled(ctx context.Context, in CreateScheduledInput) (*ScheduledPayment, error) {
	if in.UserID == uuid.Nil || in.AccountID == uuid.Nil {
		return nil, fmt.Errorf("create scheduled: missing required ids")
	}
	switch in.ScheduleKind {
	case "one_off", "monthly":
	default:
		return nil, fmt.Errorf("create scheduled: invalid schedule_kind %q", in.ScheduleKind)
	}
	switch in.PaymentMethod {
	case "wallet", "upi":
	default:
		return nil, fmt.Errorf("create scheduled: invalid payment_method %q", in.PaymentMethod)
	}
	if in.NextRunDate.IsZero() {
		return nil, fmt.Errorf("create scheduled: next_run_date required")
	}
	const q = `
        INSERT INTO billpay.scheduled_payments (
            user_id, account_id, amount_paise, payment_method, schedule_kind, next_run_date
        ) VALUES ($1, $2, $3, $4, $5, $6)
        RETURNING id, user_id, account_id, amount_paise, payment_method, schedule_kind,
                  next_run_date, last_run_at, is_active, created_at`
	var sp ScheduledPayment
	if err := s.db.QueryRow(ctx, q,
		in.UserID, in.AccountID, in.AmountPaise, in.PaymentMethod, in.ScheduleKind, in.NextRunDate,
	).Scan(
		&sp.ID, &sp.UserID, &sp.AccountID, &sp.AmountPaise, &sp.PaymentMethod, &sp.ScheduleKind,
		&sp.NextRunDate, &sp.LastRunAt, &sp.IsActive, &sp.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert scheduled: %w", err)
	}
	return &sp, nil
}

// ListScheduledByUser returns all scheduled payments for a user (active + inactive).
func (s *Store) ListScheduledByUser(ctx context.Context, userID uuid.UUID) ([]ScheduledPayment, error) {
	const q = `
        SELECT id, user_id, account_id, amount_paise, payment_method, schedule_kind,
               next_run_date, last_run_at, is_active, created_at
        FROM billpay.scheduled_payments
        WHERE user_id = $1
        ORDER BY is_active DESC, next_run_date ASC`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list scheduled: %w", err)
	}
	defer rows.Close()
	return scanScheduled(rows)
}

// ListDueScheduled returns active rows whose next_run_date <= today.
func (s *Store) ListDueScheduled(ctx context.Context, today time.Time) ([]ScheduledPayment, error) {
	const q = `
        SELECT id, user_id, account_id, amount_paise, payment_method, schedule_kind,
               next_run_date, last_run_at, is_active, created_at
        FROM billpay.scheduled_payments
        WHERE is_active = true AND next_run_date <= $1::date
        ORDER BY next_run_date ASC
        LIMIT 500`
	rows, err := s.db.Query(ctx, q, today)
	if err != nil {
		return nil, fmt.Errorf("list due scheduled: %w", err)
	}
	defer rows.Close()
	return scanScheduled(rows)
}

// UpdateScheduledActive flips is_active. Used by PATCH endpoint and the
// cron's "deactivate after one_off run" path.
func (s *Store) UpdateScheduledActive(ctx context.Context, userID, scheduledID uuid.UUID, active bool) (*ScheduledPayment, error) {
	const q = `
        UPDATE billpay.scheduled_payments SET is_active = $3
        WHERE id = $1 AND user_id = $2
        RETURNING id, user_id, account_id, amount_paise, payment_method, schedule_kind,
                  next_run_date, last_run_at, is_active, created_at`
	var sp ScheduledPayment
	if err := s.db.QueryRow(ctx, q, scheduledID, userID, active).Scan(
		&sp.ID, &sp.UserID, &sp.AccountID, &sp.AmountPaise, &sp.PaymentMethod, &sp.ScheduleKind,
		&sp.NextRunDate, &sp.LastRunAt, &sp.IsActive, &sp.CreatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrScheduledNotFound
		}
		return nil, fmt.Errorf("update scheduled: %w", err)
	}
	return &sp, nil
}

// AdvanceScheduled bumps next_run_date by 30 days for monthly schedules.
// Sets last_run_at = now(). For one_off, the caller separately deactivates.
func (s *Store) AdvanceScheduled(ctx context.Context, scheduledID uuid.UUID) error {
	const q = `
        UPDATE billpay.scheduled_payments
        SET last_run_at = now(),
            next_run_date = CASE
                WHEN schedule_kind = 'monthly'
                THEN (next_run_date + INTERVAL '30 days')::date
                ELSE next_run_date
            END,
            is_active = CASE
                WHEN schedule_kind = 'one_off' THEN false
                ELSE is_active
            END
        WHERE id = $1`
	if _, err := s.db.Exec(ctx, q, scheduledID); err != nil {
		return fmt.Errorf("advance scheduled: %w", err)
	}
	return nil
}

// DeleteScheduled hard-deletes (it's purely user-state, not financial).
func (s *Store) DeleteScheduled(ctx context.Context, userID, scheduledID uuid.UUID) error {
	const q = `DELETE FROM billpay.scheduled_payments WHERE id = $1 AND user_id = $2`
	tag, err := s.db.Exec(ctx, q, scheduledID, userID)
	if err != nil {
		return fmt.Errorf("delete scheduled: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrScheduledNotFound
	}
	return nil
}

func scanScheduled(rows pgx.Rows) ([]ScheduledPayment, error) {
	var out []ScheduledPayment
	for rows.Next() {
		var sp ScheduledPayment
		if err := rows.Scan(
			&sp.ID, &sp.UserID, &sp.AccountID, &sp.AmountPaise, &sp.PaymentMethod, &sp.ScheduleKind,
			&sp.NextRunDate, &sp.LastRunAt, &sp.IsActive, &sp.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan scheduled: %w", err)
		}
		out = append(out, sp)
	}
	return out, rows.Err()
}
