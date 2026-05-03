package service

import (
	"context"
	"testing"
	"time"

	"github.com/atpost/bill-pay-service/internal/store"
	"github.com/google/uuid"
)

func TestRunReminderCron_FiresWithinWindow(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	ctx := context.Background()
	uid := uuid.New()
	provID := h.seedProvider(t, "PROV-rem-svc-1", "electricity", "Test Prov")
	accID := h.seedAccount(t, uid, provID, "1234567890")

	if _, err := h.store.CreateReminder(ctx, store.CreateReminderInput{
		UserID: uid, AccountID: accID, DaysBeforeDue: 7, Channels: []string{"push"},
	}); err != nil {
		t.Fatalf("create reminder: %v", err)
	}
	today := time.Now().Truncate(24 * time.Hour)
	due := today.AddDate(0, 0, 3).Format("2006-01-02")
	if _, err := h.store.InsertBill(ctx, store.InsertBillInput{
		AccountID: accID, BillAmountPaise: 100000, BillDueDate: &due,
	}); err != nil {
		t.Fatalf("insert bill: %v", err)
	}

	count, err := h.svc.RunReminderCron(ctx, today)
	if err != nil {
		t.Fatalf("run cron: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 reminder fired; got %d", count)
	}

	// Run again same day — should be 0 (last_sent_at gates).
	again, err := h.svc.RunReminderCron(ctx, today)
	if err != nil {
		t.Fatalf("run cron again: %v", err)
	}
	if again != 0 {
		t.Fatalf("expected 0 second run on same day; got %d", again)
	}
}

func TestCreateReminder_RejectsForeignAccount(t *testing.T) {
	h := newTestService(t)
	defer h.cleanup()
	if _, err := h.svc.CreateReminder(context.Background(), uuid.New(), CreateReminderRequest{
		AccountID: uuid.New(), DaysBeforeDue: 3, Channels: []string{"push"},
	}); err == nil {
		t.Fatalf("expected error when account not owned by user")
	}
}
