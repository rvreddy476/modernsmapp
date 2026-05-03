package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

// CreateScheduledRequest is the inbound shape for POST /v1/billpay/scheduled.
type CreateScheduledRequest struct {
	AccountID     uuid.UUID `json:"account_id"`
	AmountPaise   *int64    `json:"amount_paise,omitempty"`
	PaymentMethod string    `json:"payment_method"`
	ScheduleKind  string    `json:"schedule_kind"`
	NextRunDate   string    `json:"next_run_date"` // ISO date
}

// CreateScheduled creates a scheduled-payment rule.
func (s *Service) CreateScheduled(ctx context.Context, userID uuid.UUID, req CreateScheduledRequest) (*store.ScheduledPayment, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	if _, err := s.store.GetAccount(ctx, userID, req.AccountID); err != nil {
		return nil, err
	}
	d, err := time.Parse("2006-01-02", req.NextRunDate)
	if err != nil {
		return nil, fmt.Errorf("invalid: next_run_date must be YYYY-MM-DD")
	}
	return s.store.CreateScheduled(ctx, store.CreateScheduledInput{
		UserID:        userID,
		AccountID:     req.AccountID,
		AmountPaise:   req.AmountPaise,
		PaymentMethod: req.PaymentMethod,
		ScheduleKind:  req.ScheduleKind,
		NextRunDate:   d,
	})
}

// ListScheduled returns the user's scheduled-payment rows.
func (s *Service) ListScheduled(ctx context.Context, userID uuid.UUID) ([]store.ScheduledPayment, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user id required")
	}
	return s.store.ListScheduledByUser(ctx, userID)
}

// UpdateScheduledActiveRequest is the inbound shape for PATCH /scheduled/:id.
type UpdateScheduledActiveRequest struct {
	IsActive *bool `json:"is_active,omitempty"`
}

// UpdateScheduledActive flips is_active.
func (s *Service) UpdateScheduledActive(ctx context.Context, userID, scheduledID uuid.UUID, active bool) (*store.ScheduledPayment, error) {
	if userID == uuid.Nil || scheduledID == uuid.Nil {
		return nil, fmt.Errorf("invalid: ids required")
	}
	return s.store.UpdateScheduledActive(ctx, userID, scheduledID, active)
}

// DeleteScheduled hard-deletes a scheduled-payment row.
func (s *Service) DeleteScheduled(ctx context.Context, userID, scheduledID uuid.UUID) error {
	if userID == uuid.Nil || scheduledID == uuid.Nil {
		return fmt.Errorf("invalid: ids required")
	}
	return s.store.DeleteScheduled(ctx, userID, scheduledID)
}

// RunScheduledCron iterates active rows where next_run_date <= today and
// invokes Pay() for each. Returns (executed, failed) counts. Each call uses
// a deterministic idempotency key (scheduledID + run date) so a re-run of
// the cron on the same day is safe.
func (s *Service) RunScheduledCron(ctx context.Context, today time.Time) (executed int, failed int, err error) {
	due, err := s.store.ListDueScheduled(ctx, today)
	if err != nil {
		return 0, 0, err
	}
	for _, sp := range due {
		// Resolve account → provider → identifier (the saved one).
		acc, err := s.store.GetAccountByID(ctx, sp.AccountID)
		if err != nil {
			s.failScheduled(ctx, sp, fmt.Sprintf("get_account: %v", err))
			failed++
			continue
		}
		prov, err := s.store.GetProvider(ctx, acc.ProviderID)
		if err != nil {
			s.failScheduled(ctx, sp, fmt.Sprintf("get_provider: %v", err))
			failed++
			continue
		}
		// Decide amount: explicit, else fetch the latest bill.
		var amount int64
		if sp.AmountPaise != nil && *sp.AmountPaise > 0 {
			amount = *sp.AmountPaise
		} else {
			bill, err := s.FetchBill(ctx, acc.UserID, acc.ID)
			if err != nil {
				s.failScheduled(ctx, sp, fmt.Sprintf("fetch_bill: %v", err))
				failed++
				continue
			}
			amount = bill.BillAmountPaise
		}
		if amount <= 0 {
			s.failScheduled(ctx, sp, "no_billable_amount")
			failed++
			continue
		}
		key := scheduledIdempotencyKey(sp.ID, today)
		res, err := s.Pay(ctx, acc.UserID, PayRequest{
			AccountID:      &acc.ID,
			ProviderID:     prov.ID,
			Identifier:     acc.Identifier,
			AmountPaise:    amount,
			PaymentMethod:  sp.PaymentMethod,
			IdempotencyKey: key,
		})
		if err != nil {
			s.failScheduled(ctx, sp, fmt.Sprintf("pay: %v", err))
			failed++
			continue
		}
		if err := s.store.AdvanceScheduled(ctx, sp.ID); err != nil {
			slog.Warn("billpay: advance scheduled failed", "scheduled", sp.ID, "error", err)
		}
		if err := s.producer.PublishScheduledExecuted(ctx, acc.UserID, sp.ID, res.PaymentID, amount); err != nil {
			slog.Warn("billpay: publish scheduled executed failed", "scheduled", sp.ID, "error", err)
		}
		executed++
	}
	return executed, failed, nil
}

func (s *Service) failScheduled(ctx context.Context, sp store.ScheduledPayment, reason string) {
	if err := s.producer.PublishScheduledFailed(ctx, sp.UserID, sp.ID, reason); err != nil {
		slog.Warn("billpay: publish scheduled failed failed", "scheduled", sp.ID, "error", err)
	}
}

// scheduledIdempotencyKey is a deterministic key for a given scheduled
// payment + run date. Re-runs of the cron on the same day MUST hit the same
// key so Pay() returns the cached result.
func scheduledIdempotencyKey(scheduledID uuid.UUID, runDate time.Time) string {
	h := sha256.Sum256([]byte("billpay.scheduled:" + scheduledID.String() + ":" + runDate.Format("2006-01-02")))
	return "sched-" + hex.EncodeToString(h[:16])
}
