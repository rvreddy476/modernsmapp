package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestInsertTransaction_PendingTopUp(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()

	idem := "idemkey-1"
	tx, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
		UserID:         uid,
		Type:           "top_up",
		Direction:      "credit",
		AmountPaise:    1000,
		Status:         "pending",
		IdempotencyKey: &idem,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if tx.ID == uuid.Nil {
		t.Fatalf("expected generated id")
	}
}

func TestInsertTransaction_RejectsBadDirection(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	if _, err := s.InsertTransaction(context.Background(), nil, CreateTransactionInput{
		UserID: uuid.New(), Type: "top_up", Direction: "sideways", AmountPaise: 100,
	}); err == nil {
		t.Fatalf("expected rejection of bad direction")
	}
}

func TestInsertTransaction_RejectsZeroAmount(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	if _, err := s.InsertTransaction(context.Background(), nil, CreateTransactionInput{
		UserID: uuid.New(), Type: "top_up", Direction: "credit", AmountPaise: 0,
	}); err == nil {
		t.Fatalf("expected rejection of zero amount")
	}
}

func TestMarkSettled_Idempotent(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	tx, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
		UserID: uuid.New(), Type: "top_up", Direction: "credit", AmountPaise: 1000,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	bankRef := "BANK-1"
	if err := s.MarkSettled(ctx, nil, tx.ID, "succeeded", &bankRef, nil); err != nil {
		t.Fatalf("settle: %v", err)
	}
	// Second call should not error — idempotent.
	if err := s.MarkSettled(ctx, nil, tx.ID, "succeeded", &bankRef, nil); err != nil {
		t.Fatalf("idempotent settle: %v", err)
	}
}

func TestMarkSettled_RejectsInvalidStatus(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	if err := s.MarkSettled(context.Background(), nil, uuid.New(), "weird", nil, nil); err == nil {
		t.Fatalf("expected rejection of invalid status")
	}
}

func TestMarkSettled_NotFound(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	err := s.MarkSettled(context.Background(), nil, uuid.New(), "succeeded", nil, nil)
	if !errors.Is(err, ErrTransactionNotFound) {
		t.Fatalf("expected not-found; got %v", err)
	}
}

func TestSetUPIRef_Idempotent(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	tx, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
		UserID: uuid.New(), Type: "top_up", Direction: "credit", AmountPaise: 1000,
	})
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.SetUPIRef(ctx, tx.ID, "UPI-1"); err != nil {
		t.Fatalf("set 1st: %v", err)
	}
	if err := s.SetUPIRef(ctx, tx.ID, "UPI-1"); err != nil {
		t.Fatalf("set 2nd same: %v", err)
	}
	if err := s.SetUPIRef(ctx, tx.ID, "UPI-2"); err == nil {
		t.Fatalf("expected conflict on different upi ref")
	}
}

func TestListTransactions_ByDirectionAndType(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	for i := 0; i < 3; i++ {
		_, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
			UserID: uid, Type: "top_up", Direction: "credit", AmountPaise: int64(100 + i),
		})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	_, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
		UserID: uid, Type: "send", Direction: "debit", AmountPaise: 50,
	})
	if err != nil {
		t.Fatalf("seed send: %v", err)
	}

	credits, _, err := s.ListTransactions(ctx, uid, "", "credit", "", 50)
	if err != nil {
		t.Fatalf("list credits: %v", err)
	}
	if len(credits) != 3 {
		t.Fatalf("expected 3 credits; got %d", len(credits))
	}

	sends, _, err := s.ListTransactions(ctx, uid, "send", "", "", 50)
	if err != nil {
		t.Fatalf("list sends: %v", err)
	}
	if len(sends) != 1 {
		t.Fatalf("expected 1 send; got %d", len(sends))
	}
}

func TestListPendingTopUpsOlderThan(t *testing.T) {
	s, cleanup := walletTestStore(t)
	defer cleanup()
	ctx := context.Background()
	uid := uuid.New()
	if _, err := s.InsertTransaction(ctx, nil, CreateTransactionInput{
		UserID: uid, Type: "top_up", Direction: "credit", AmountPaise: 100, Status: "pending",
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Future cutoff catches everything.
	got, err := s.ListPendingTopUpsOlderThan(ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) == 0 {
		t.Fatalf("expected at least one stale top-up")
	}
	// Past cutoff catches nothing for newly-inserted rows.
	got, _ = s.ListPendingTopUpsOlderThan(ctx, time.Now().Add(-time.Hour), 100)
	if len(got) != 0 {
		// note: might still pick up rows from prior test runs in the same DB.
		// As long as it doesn't include OUR new uid.
		for _, g := range got {
			if g.UserID == uid {
				t.Fatalf("did not expect new row to be selected by past cutoff")
			}
		}
	}
}
