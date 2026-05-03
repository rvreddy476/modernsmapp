package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestScheduled_CreateAndList(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-sched-1", "electricity", "Prov", nil)
	uid := uuid.New()
	acc, _ := s.CreateAccount(ctx, CreateAccountInput{
		UserID: uid, ProviderID: provID, Identifier: "1111111111", Label: "Home",
	})
	next := time.Now().AddDate(0, 0, 1).Truncate(24 * time.Hour)
	sp, err := s.CreateScheduled(ctx, CreateScheduledInput{
		UserID: uid, AccountID: acc.ID,
		PaymentMethod: "wallet", ScheduleKind: "monthly", NextRunDate: next,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sp.ScheduleKind != "monthly" {
		t.Fatalf("kind mismatch: %q", sp.ScheduleKind)
	}
	out, err := s.ListScheduledByUser(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1; got %d", len(out))
	}
}

func TestScheduled_ValidationRejectsBadKind(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	if _, err := s.CreateScheduled(context.Background(), CreateScheduledInput{
		UserID: uuid.New(), AccountID: uuid.New(),
		PaymentMethod: "wallet", ScheduleKind: "weekly",
		NextRunDate: time.Now().AddDate(0, 0, 1),
	}); err == nil {
		t.Fatalf("expected invalid schedule_kind error")
	}
}

func TestScheduled_DueAndAdvance(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-sched-2", "electricity", "Prov", nil)
	uid := uuid.New()
	acc, _ := s.CreateAccount(ctx, CreateAccountInput{
		UserID: uid, ProviderID: provID, Identifier: "2222222222", Label: "Home",
	})
	yesterday := time.Now().AddDate(0, 0, -1).Truncate(24 * time.Hour)
	sp, _ := s.CreateScheduled(ctx, CreateScheduledInput{
		UserID: uid, AccountID: acc.ID,
		PaymentMethod: "wallet", ScheduleKind: "monthly", NextRunDate: yesterday,
	})
	due, err := s.ListDueScheduled(ctx, time.Now())
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(due) != 1 || due[0].ID != sp.ID {
		t.Fatalf("expected sp due; got %+v", due)
	}
	if err := s.AdvanceScheduled(ctx, sp.ID); err != nil {
		t.Fatalf("advance: %v", err)
	}
	out, _ := s.ListScheduledByUser(ctx, uid)
	if len(out) != 1 || !out[0].NextRunDate.After(yesterday) {
		t.Fatalf("expected advanced next_run_date; got %+v", out)
	}
}

func TestScheduled_AdvanceDeactivatesOneOff(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-sched-3", "electricity", "Prov", nil)
	uid := uuid.New()
	acc, _ := s.CreateAccount(ctx, CreateAccountInput{
		UserID: uid, ProviderID: provID, Identifier: "3333333333", Label: "Home",
	})
	sp, _ := s.CreateScheduled(ctx, CreateScheduledInput{
		UserID: uid, AccountID: acc.ID,
		PaymentMethod: "wallet", ScheduleKind: "one_off",
		NextRunDate: time.Now().AddDate(0, 0, -1),
	})
	if err := s.AdvanceScheduled(ctx, sp.ID); err != nil {
		t.Fatalf("advance: %v", err)
	}
	out, _ := s.ListScheduledByUser(ctx, uid)
	if len(out) != 1 || out[0].IsActive {
		t.Fatalf("one-off should deactivate after advance; got %+v", out)
	}
}

func TestScheduled_DeleteNotFound(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	err := s.DeleteScheduled(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrScheduledNotFound) {
		t.Fatalf("expected ErrScheduledNotFound; got %v", err)
	}
}
