//go:build integration

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
)

// Android audit 2026-10-30 M3: a +50 whose response was lost and is retried
// with the same Idempotency-Key applies once.

func totalOf(t *testing.T, variantID uuid.UUID) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT total_qty FROM inventory_items WHERE variant_id=$1`, variantID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestStockAdjust_SameKeyAppliesOnce(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 10, 2)
	adj := f.adjust(50, "purchase")
	adj.IdempotencyKey = "stock-" + uuid.NewString()

	first, err := store.AdjustStock(ctx, adj)
	if err != nil {
		t.Fatal(err)
	}
	if first.TotalQty != 60 || first.Replayed {
		t.Fatalf("first = %+v", first)
	}
	// The retry, and a concurrent pair of retries: no further movement.
	again, err := store.AdjustStock(ctx, adj)
	if err != nil {
		t.Fatal(err)
	}
	if !again.Replayed || again.TotalQty != 60 || again.ReservedQty != 2 || again.Available != 58 {
		t.Fatalf("retry = %+v, want the first result replayed", again)
	}
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := store.AdjustStock(ctx, adj); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := totalOf(t, f.variantID); n != 60 {
		t.Fatalf("total_qty = %d after retries, want 60 (+50 once)", n)
	}
	var rows int
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM inventory_adjustments WHERE variant_id=$1`, f.variantID).Scan(&rows)
	if rows != 1 {
		t.Fatalf("adjustment rows = %d, want 1", rows)
	}

	// The same key on a different request is refused, and moves nothing.
	other := adj
	other.Delta = 5
	if _, err := store.AdjustStock(ctx, other); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key, different delta: err = %v, want ErrIdempotencyConflict", err)
	}
	// A fresh key is a new adjustment.
	next := f.adjust(50, "purchase")
	next.IdempotencyKey = "stock-" + uuid.NewString()
	if lvl, err := store.AdjustStock(ctx, next); err != nil || lvl.TotalQty != 110 {
		t.Fatalf("fresh key: %+v %v", lvl, err)
	}
}

func TestStockAdjust_WithoutAKeyBehavesAsBefore(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 10, 0)
	for i := 0; i < 2; i++ {
		if _, err := store.AdjustStock(ctx, f.adjust(5, "purchase")); err != nil {
			t.Fatal(err)
		}
	}
	if n := totalOf(t, f.variantID); n != 20 {
		t.Fatalf("total_qty = %d, want 20 (two keyless adjustments both apply)", n)
	}
}

func TestStockAdjust_KeysArePerSeller(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	a, b := newStockFixture(t, 0, 0), newStockFixture(t, 0, 0)
	key := "shared-" + uuid.NewString()
	adjA, adjB := a.adjust(3, "purchase"), b.adjust(4, "purchase")
	adjA.IdempotencyKey, adjB.IdempotencyKey = key, key
	if _, err := store.AdjustStock(ctx, adjA); err != nil {
		t.Fatal(err)
	}
	if lvl, err := store.AdjustStock(ctx, adjB); err != nil || lvl.Replayed || lvl.TotalQty != 4 {
		t.Fatalf("another seller's identical key: %+v %v", lvl, err)
	}
}
