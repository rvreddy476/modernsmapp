package store

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCreateAccount_RoundTripAndIdempotency(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "AIRTEL-acc", "mobile_postpaid", "Airtel", nil)
	uid := uuid.New()

	first, err := s.CreateAccount(ctx, CreateAccountInput{
		UserID: uid, ProviderID: provID,
		Identifier: "9876543210", Label: "My Phone",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if first.UserID != uid || first.Identifier != "9876543210" {
		t.Fatalf("round trip fail: %+v", first)
	}

	// Replay with same tuple: must return the same row id (UNIQUE upsert).
	again, err := s.CreateAccount(ctx, CreateAccountInput{
		UserID: uid, ProviderID: provID,
		Identifier: "9876543210", Label: "Updated Label",
	})
	if err != nil {
		t.Fatalf("re-create: %v", err)
	}
	if again.ID != first.ID {
		t.Fatalf("expected same account id on replay; got %s vs %s", first.ID, again.ID)
	}
	if again.Label != "Updated Label" {
		t.Fatalf("expected label update on replay; got %q", again.Label)
	}
}

func TestListAccounts_FiltersDeleted(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "VI-acc", "mobile_postpaid", "Vi", nil)
	uid := uuid.New()

	a, _ := s.CreateAccount(ctx, CreateAccountInput{UserID: uid, ProviderID: provID, Identifier: "9000000001", Label: "A"})
	_, _ = s.CreateAccount(ctx, CreateAccountInput{UserID: uid, ProviderID: provID, Identifier: "9000000002", Label: "B"})

	if err := s.SoftDeleteAccount(ctx, uid, a.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	out, err := s.ListAccountsByUser(ctx, uid)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 || out[0].Identifier != "9000000002" {
		t.Fatalf("expected 1 row with identifier 9000000002; got %+v", out)
	}
}

func TestGetAccount_NotFound(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	_, err := s.GetAccount(context.Background(), uuid.New(), uuid.New())
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("expected ErrAccountNotFound; got %v", err)
	}
}

func TestUpdateAccount_AppliesPatch(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "JIO-acc", "mobile_postpaid", "Jio", nil)
	uid := uuid.New()
	acc, _ := s.CreateAccount(ctx, CreateAccountInput{UserID: uid, ProviderID: provID, Identifier: "9000000003", Label: "A"})

	on := true
	newLabel := "Patched"
	got, err := s.UpdateAccount(ctx, uid, acc.ID, UpdateAccountInput{
		Label:          &newLabel,
		IsDefault:      &on,
		AutopayEnabled: &on,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if got.Label != "Patched" || !got.IsDefault || !got.AutopayEnabled {
		t.Fatalf("patch not applied: %+v", got)
	}
}

func TestInsertBillAndMarkPaid(t *testing.T) {
	s, cleanup := billpayTestStore(t)
	defer cleanup()
	ctx := context.Background()
	provID := seedProvider(t, s, "BSES-bill", "electricity", "BSES", []string{"DL"})
	uid := uuid.New()
	acc, _ := s.CreateAccount(ctx, CreateAccountInput{UserID: uid, ProviderID: provID, Identifier: "1234567890", Label: "Home"})

	due := "2026-05-15"
	bill, err := s.InsertBill(ctx, InsertBillInput{
		AccountID: acc.ID, BillAmountPaise: 250000, BillDueDate: &due,
	})
	if err != nil {
		t.Fatalf("insert bill: %v", err)
	}
	if bill.Status != "fetched" {
		t.Fatalf("expected fetched status; got %q", bill.Status)
	}
	if err := s.MarkBillPaid(ctx, bill.ID, uuid.New()); err != nil {
		t.Fatalf("mark paid: %v", err)
	}
	got, _ := s.GetBill(ctx, bill.ID)
	if got.Status != "paid" {
		t.Fatalf("expected paid; got %q", got.Status)
	}
}
