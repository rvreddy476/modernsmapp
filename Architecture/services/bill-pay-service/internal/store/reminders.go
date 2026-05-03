package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrReminderNotFound is returned when a reminder lookup misses.
var ErrReminderNotFound = errors.New("reminder: not found")

// CreateReminderInput is the inbound shape for new billpay.reminders rows.
type CreateReminderInput struct {
	AccountID     uuid.UUID
	UserID        uuid.UUID
	DaysBeforeDue int
	Channels      []string
}

// CreateReminder inserts a new reminder and returns it. days_before_due
// defaults to 3, channels default to ['push'] if empty.
func (s *Store) CreateReminder(ctx context.Context, in CreateReminderInput) (*Reminder, error) {
	if in.UserID == uuid.Nil || in.AccountID == uuid.Nil {
		return nil, fmt.Errorf("create reminder: missing required ids")
	}
	if in.DaysBeforeDue <= 0 {
		in.DaysBeforeDue = 3
	}
	if in.DaysBeforeDue > 30 {
		return nil, fmt.Errorf("create reminder: days_before_due must be <= 30")
	}
	if len(in.Channels) == 0 {
		in.Channels = []string{"push"}
	}
	for _, ch := range in.Channels {
		switch ch {
		case "push", "sms", "email":
		default:
			return nil, fmt.Errorf("create reminder: invalid channel %q", ch)
		}
	}
	const q = `
        INSERT INTO billpay.reminders (account_id, user_id, days_before_due, channels)
        VALUES ($1, $2, $3, $4)
        RETURNING id, account_id, user_id, days_before_due, channels,
                  is_active, last_sent_at, created_at`
	var r Reminder
	if err := s.db.QueryRow(ctx, q,
		in.AccountID, in.UserID, in.DaysBeforeDue, in.Channels,
	).Scan(
		&r.ID, &r.AccountID, &r.UserID, &r.DaysBeforeDue, &r.Channels,
		&r.IsActive, &r.LastSentAt, &r.CreatedAt,
	); err != nil {
		return nil, fmt.Errorf("insert reminder: %w", err)
	}
	return &r, nil
}

// ListRemindersByUser returns all active reminders for a user.
func (s *Store) ListRemindersByUser(ctx context.Context, userID uuid.UUID) ([]Reminder, error) {
	const q = `
        SELECT id, account_id, user_id, days_before_due, channels,
               is_active, last_sent_at, created_at
        FROM billpay.reminders
        WHERE user_id = $1 AND is_active = true
        ORDER BY created_at DESC`
	rows, err := s.db.Query(ctx, q, userID)
	if err != nil {
		return nil, fmt.Errorf("list reminders: %w", err)
	}
	defer rows.Close()
	var out []Reminder
	for rows.Next() {
		var r Reminder
		if err := rows.Scan(
			&r.ID, &r.AccountID, &r.UserID, &r.DaysBeforeDue, &r.Channels,
			&r.IsActive, &r.LastSentAt, &r.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan reminder: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteReminder soft-deactivates by flipping is_active. The cron will
// stop picking it up.
func (s *Store) DeleteReminder(ctx context.Context, userID, reminderID uuid.UUID) error {
	const q = `
        UPDATE billpay.reminders
        SET is_active = false
        WHERE id = $1 AND user_id = $2 AND is_active = true`
	tag, err := s.db.Exec(ctx, q, reminderID, userID)
	if err != nil {
		return fmt.Errorf("delete reminder: %w", err)
	}
	if tag.RowsAffected() == 0 {
		const probe = `SELECT 1 FROM billpay.reminders WHERE id = $1 AND user_id = $2`
		var x int
		if err := s.db.QueryRow(ctx, probe, reminderID, userID).Scan(&x); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return ErrReminderNotFound
			}
			return fmt.Errorf("probe reminder: %w", err)
		}
	}
	return nil
}

// DueReminderRow is one row returned by ListDueReminders. Joins reminder +
// account + latest bill so the cron has everything it needs to send.
type DueReminderRow struct {
	ReminderID    uuid.UUID
	UserID        uuid.UUID
	AccountID     uuid.UUID
	Channels      []string
	DaysBeforeDue int
	BillID        uuid.UUID
	BillDueDate   time.Time
	BillAmount    int64
	ProviderName  string
	Identifier    string
}

// ListDueReminders returns reminders whose latest fetched bill is within
// days_before_due of bill_due_date AND has not been sent for that bill.
// "today" is a parameter so tests can pin a clock without freezing.
func (s *Store) ListDueReminders(ctx context.Context, today time.Time) ([]DueReminderRow, error) {
	const q = `
        SELECT r.id, r.user_id, r.account_id, r.channels, r.days_before_due,
               b.id, b.bill_due_date, b.bill_amount_paise,
               p.name, a.identifier
        FROM billpay.reminders r
        JOIN billpay.accounts a ON a.id = r.account_id AND a.deleted_at IS NULL
        JOIN billpay.providers p ON p.id = a.provider_id
        JOIN LATERAL (
            SELECT id, bill_due_date, bill_amount_paise
            FROM billpay.bills
            WHERE account_id = r.account_id AND status = 'fetched'
            ORDER BY fetched_at DESC LIMIT 1
        ) b ON true
        WHERE r.is_active = true
          AND b.bill_due_date IS NOT NULL
          AND b.bill_due_date >= $1::date
          AND b.bill_due_date <= ($1::date + (r.days_before_due || ' days')::interval)::date
          AND (r.last_sent_at IS NULL OR r.last_sent_at::date < $1::date)`
	rows, err := s.db.Query(ctx, q, today)
	if err != nil {
		return nil, fmt.Errorf("list due reminders: %w", err)
	}
	defer rows.Close()
	var out []DueReminderRow
	for rows.Next() {
		var r DueReminderRow
		if err := rows.Scan(
			&r.ReminderID, &r.UserID, &r.AccountID, &r.Channels, &r.DaysBeforeDue,
			&r.BillID, &r.BillDueDate, &r.BillAmount,
			&r.ProviderName, &r.Identifier,
		); err != nil {
			return nil, fmt.Errorf("scan due reminder: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// MarkReminderSent updates last_sent_at = now(). Best-effort: errors are
// surfaced but the cron should keep going.
func (s *Store) MarkReminderSent(ctx context.Context, reminderID uuid.UUID) error {
	const q = `UPDATE billpay.reminders SET last_sent_at = now() WHERE id = $1`
	if _, err := s.db.Exec(ctx, q, reminderID); err != nil {
		return fmt.Errorf("mark reminder sent: %w", err)
	}
	return nil
}
