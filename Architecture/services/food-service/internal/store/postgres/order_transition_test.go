package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/atpost/food-service/internal/orderstate"
	"github.com/google/uuid"
)

func runTransition(t *testing.T, s *Store, tr OrderTransition) error {
	t.Helper()
	ctx := context.Background()
	tx, err := s.db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	terr := transitionOrderTx(ctx, tx, tr)
	// Commit even on error: anything the writer did before returning would
	// persist and be caught by the assertions.
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
	return terr
}

func TestTransitionOrderTx_AppliesWithRealHistory(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	orderID, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	owner := readRestaurantOwner(t, s, orderID)
	if err := runTransition(t, s, OrderTransition{
		OrderID: orderID, From: orderstate.Confirmed, To: orderstate.Preparing,
		Actor: orderstate.ActorRestaurant, ChangedBy: &owner, Reason: "accepted",
	}); err != nil {
		t.Fatalf("transition: %v", err)
	}
	assertOrderStatus(t, s, orderID, "PREPARING")
	assertHistoryChain(t, s, orderID, "CONFIRMED", []string{"PREPARING"})
}

func TestTransitionOrderTx_RefusesActorNotAllowed(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	orderID, _, customer := seedOrderWithItem(t, s, "CONFIRMED")
	err := runTransition(t, s, OrderTransition{
		OrderID: orderID, From: orderstate.Confirmed, To: orderstate.Preparing,
		Actor: orderstate.ActorCustomer, ChangedBy: &customer,
	})
	if !errors.Is(err, ErrOrderTransitionNotAllowed) {
		t.Fatalf("want ErrOrderTransitionNotAllowed, got %v", err)
	}
	assertOrderStatus(t, s, orderID, "CONFIRMED")
	if h := readHistory(t, s, orderID); len(h) != 0 {
		t.Fatalf("history written for a refused transition: %v", h)
	}
}

func TestTransitionOrderTx_StaleFromIsAConflict(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	orderID, _, customer := seedOrderWithItem(t, s, "PREPARING")
	// The caller read CONFIRMED, but the row has moved on.
	err := runTransition(t, s, OrderTransition{
		OrderID: orderID, From: orderstate.Confirmed, To: orderstate.CancelledByCustomer,
		Actor: orderstate.ActorCustomer, ChangedBy: &customer,
	})
	if !errors.Is(err, ErrOrderStatusConflict) {
		t.Fatalf("want ErrOrderStatusConflict, got %v", err)
	}
	assertOrderStatus(t, s, orderID, "PREPARING")
	if h := readHistory(t, s, orderID); len(h) != 0 {
		t.Fatalf("history written for a conflicting transition: %v", h)
	}
}

func TestTransitionOrderTx_CancellationStampsOrder(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	orderID, _, _ := seedOrderWithItem(t, s, "CONFIRMED")
	admin := uuid.New()
	if err := runTransition(t, s, OrderTransition{
		OrderID: orderID, From: orderstate.Confirmed, To: orderstate.CancelledByAdmin,
		Actor: orderstate.ActorAdmin, ChangedBy: &admin, Reason: "fraud",
	}); err != nil {
		t.Fatal(err)
	}
	var by *uuid.UUID
	var reason *string
	var stamped bool
	if err := s.db.QueryRow(context.Background(), `
		SELECT cancelled_by, cancellation_reason, cancelled_at IS NOT NULL FROM food.orders WHERE id = $1
	`, orderID).Scan(&by, &reason, &stamped); err != nil {
		t.Fatal(err)
	}
	if by == nil || *by != admin || reason == nil || *reason != "fraud" || !stamped {
		t.Fatalf("cancel fields not stamped: by=%v reason=%v stamped=%v", by, reason, stamped)
	}
}

// Two writers race from the same status; the guard lets exactly one through.
func TestTransitionOrderTx_ConcurrentWritersOneWins(t *testing.T) {
	s, cleanup := foodTestStore(t)
	defer cleanup()
	orderID, _, customer := seedOrderWithItem(t, s, "PREPARING")
	owner := readRestaurantOwner(t, s, orderID)
	trs := []OrderTransition{
		{OrderID: orderID, From: orderstate.Preparing, To: orderstate.CancelledByCustomer, Actor: orderstate.ActorCustomer, ChangedBy: &customer},
		{OrderID: orderID, From: orderstate.Preparing, To: orderstate.ReadyForPickup, Actor: orderstate.ActorRestaurant, ChangedBy: &owner},
	}
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := range trs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx := context.Background()
			tx, err := s.db.Begin(ctx)
			if err != nil {
				errs[i] = err
				return
			}
			defer tx.Rollback(ctx)
			if errs[i] = transitionOrderTx(ctx, tx, trs[i]); errs[i] == nil {
				errs[i] = tx.Commit(ctx)
			}
		}(i)
	}
	wg.Wait()
	wins := 0
	for _, e := range errs {
		if e == nil {
			wins++
		} else if !errors.Is(e, ErrOrderStatusConflict) {
			t.Fatalf("loser failed with %v, want ErrOrderStatusConflict", e)
		}
	}
	if wins != 1 {
		t.Fatalf("want exactly one winner, got %d (%v)", wins, errs)
	}
	if h := readHistory(t, s, orderID); len(h) != 1 {
		t.Fatalf("want one history row, got %v", h)
	}
}
