package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestCreateReminder_DefaultsAndValidation(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-rem-1", "electricity", "Prov", nil)
	uid := uuid.New()
	acc, _ := s.CreateAccount(ctx, CreateAccountInput{
		UserID: uid, ProviderID: provID, Identifier: "1234567890", Label: "Home",
	})

	// no days, no channels -> defaults to 3 + ['push']
	r, err := s.CreateReminder(ctx, CreateReminderInput{UserID: uid, AccountID: acc.ID})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if r.DaysBeforeDue != 3 || len(r.Channels) != 1 || r.Channels[0] != "push" {
		t.Fatalf("defaults wrong: %+v", r)
	}

	// invalid channel
	if _, err := s.CreateReminder(ctx, CreateReminderInput{
		UserID: uid, AccountID: acc.ID, Channels: []string{"telegram"},
	}); err == nil {
		t.Fatalf("expected invalid-channel error")
	}
}

func TestListAndDeleteReminder(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-rem-2", "electricity", "Prov", nil)
	uid := uuid.New()
	acc, _ := s.CreateAccount(ctx, CreateAccountInput{
		UserID: uid, ProviderID: provID, Identifier: "1234567891", Label: "Home",
	})
	r, _ := s.CreateReminder(ctx, CreateReminderInput{UserID: uid, AccountID: acc.ID})

	out, err := s.ListRemindersByUser(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 || out[0].ID != r.ID {
		t.Fatalf("list mismatch: %+v", out)
	}

	if err := s.DeleteReminder(ctx, uid, r.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	out, _ = s.ListRemindersByUser(ctx, uid)
	if len(out) != 0 {
		t.Fatalf("expected 0 active after delete; got %d", len(out))
	}
}

func TestDeleteReminder_NotFound(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	err := s.DeleteReminder(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrReminderNotFound) {
		t.Fatalf("expected ErrReminderNotFound; got %v", err)
	}
}

func TestListDueReminders_PicksWithinWindow(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "PROV-rem-3", "electricity", "Prov", nil)
	uid := uuid.New()
	acc, _ := s.CreateAccount(ctx, CreateAccountInput{
		UserID: uid, ProviderID: provID, Identifier: "1234567892", Label: "Home",
	})
	if _, err := s.CreateReminder(ctx, CreateReminderInput{
		UserID: uid, AccountID: acc.ID, DaysBeforeDue: 5,
	}); err != nil {
		t.Fatalf("create reminder: %v", err)
	}

	today := time.Now().Truncate(24 * time.Hour)
	due := today.AddDate(0, 0, 3).Format("2006-01-02")
	if _, err := s.InsertBill(ctx, InsertBillInput{
		AccountID: acc.ID, BillAmountPaise: 10000, BillDueDate: &due,
	}); err != nil {
		t.Fatalf("insert bill: %v", err)
	}

	rows, err := s.ListDueReminders(ctx, today)
	if err != nil {
		t.Fatalf("list due: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 due reminder; got %d", len(rows))
	}
	if rows[0].UserID != uid {
		t.Fatalf("wrong user")
	}
}
