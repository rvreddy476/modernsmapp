package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

func TestRunScheduledCron_ExecutesDuePayments(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	ctx := context.Background()
	uid := uuid.New()
	provID := h.seedProvider(t, "PROV-sched-svc-1", "electricity", "Test Prov")
	accID := h.seedAccount(t, uid, provID, "1234567890")

	amount := int64(40000)
	yesterday := time.Now().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	if _, err := h.store.CreateScheduled(ctx, store.CreateScheduledInput{
		UserID: uid, AccountID: accID,
		AmountPaise: &amount, PaymentMethod: "wallet",
		ScheduleKind: "monthly", NextRunDate: yesterday,
	}); err != nil {
		t.Fatalf("create scheduled: %v", err)
	}

	executed, failed, err := h.svc.RunScheduledCron(ctx, time.Now())
	if err != nil {
		t.Fatalf("run cron: %v", err)
	}
	if executed != 1 || failed != 0 {
		t.Fatalf("expected 1 executed / 0 failed; got %d / %d", executed, failed)
	}
	// Wallet was debited once.
	if got := len(h.wallet.Debits()); got != 1 {
		t.Fatalf("expected 1 wallet debit; got %d", got)
	}
}

func TestRunScheduledCron_IsIdempotentOnSameDay(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	ctx := context.Background()
	uid := uuid.New()
	provID := h.seedProvider(t, "PROV-sched-svc-2", "electricity", "Test Prov")
	accID := h.seedAccount(t, uid, provID, "9999999999")

	amount := int64(15000)
	yesterday := time.Now().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	if _, err := h.store.CreateScheduled(ctx, store.CreateScheduledInput{
		UserID: uid, AccountID: accID,
		AmountPaise: &amount, PaymentMethod: "wallet",
		ScheduleKind: "one_off", NextRunDate: yesterday,
	}); err != nil {
		t.Fatalf("create scheduled: %v", err)
	}
	// First run executes; cron deactivates one_off.
	if _, _, err := h.svc.RunScheduledCron(ctx, time.Now()); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	// Second run finds no due rows.
	executed, failed, err := h.svc.RunScheduledCron(ctx, time.Now())
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if executed != 0 || failed != 0 {
		t.Fatalf("expected 0/0 on second run; got %d/%d", executed, failed)
	}
}
