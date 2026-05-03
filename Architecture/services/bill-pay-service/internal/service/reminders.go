package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

// CreateReminderRequest is the inbound shape for POST /v1/billpay/reminders.
type CreateReminderRequest struct {
	AccountID     uuid.UUID `json:"account_id"`
	DaysBeforeDue int       `json:"days_before_due"`
	Channels      []string  `json:"channels"`
}

// CreateReminder creates a reminder rule for a user-saved account.
func (s *Service) CreateReminder(ctx context.Context, userID uuid.UUID, req CreateReminderRequest) (*store.Reminder, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	if req.AccountID == uuid.Nil {
		return nil, fmt.Errorf("invalid: account_id required")
	}
	// Confirm the account belongs to the caller.
	if _, err := s.store.GetAccount(ctx, userID, req.AccountID); err != nil {
		return nil, err
	}
	return s.store.CreateReminder(ctx, store.CreateReminderInput{
		UserID:        userID,
		AccountID:     req.AccountID,
		DaysBeforeDue: req.DaysBeforeDue,
		Channels:      req.Channels,
	})
}

// ListReminders returns the active reminders for a user.
func (s *Service) ListReminders(ctx context.Context, userID uuid.UUID) ([]store.Reminder, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	return s.store.ListRemindersByUser(ctx, userID)
}

// DeleteReminder deactivates a reminder.
func (s *Service) DeleteReminder(ctx context.Context, userID, reminderID uuid.UUID) error {
	if userID == uuid.Nil || reminderID == uuid.Nil {
		return fmt.Errorf("invalid: ids required")
	}
	return s.store.DeleteReminder(ctx, userID, reminderID)
}

// RunReminderCron walks reminders whose latest fetched bill is within
// days_before_due of the due date and emits one BillDueSoon event per row.
// Returns the number of reminders fired. Idempotent for the same calendar
// day: store.ListDueReminders excludes rows where last_sent_at::date >= today.
func (s *Service) RunReminderCron(ctx context.Context, today time.Time) (int, error) {
	due, err := s.store.ListDueReminders(ctx, today)
	if err != nil {
		return 0, err
	}
	count := 0
	for _, r := range due {
		dueDateStr := r.BillDueDate.Format("2006-01-02")
		if err := s.producer.PublishBillDueSoon(ctx, r.UserID, r.AccountID, r.BillID, dueDateStr, r.BillAmount, r.Channels); err != nil {
			// Don't silently drop — payment-adjacent code per spec rule #7.
			slog.Error("billpay: publish bill due soon failed",
				"reminder", r.ReminderID,
				"identifier_masked", store.MaskIdentifier(r.Identifier),
				"error", err,
			)
			continue
		}
		if err := s.store.MarkReminderSent(ctx, r.ReminderID); err != nil {
			slog.Warn("billpay: mark reminder sent failed", "reminder", r.ReminderID, "error", err)
		}
		count++
	}
	return count, nil
}
