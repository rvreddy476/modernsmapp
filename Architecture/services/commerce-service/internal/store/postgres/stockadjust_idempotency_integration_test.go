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

// A refusal under a key is NOT stored, deliberately. The key promises "applied
// at most once"; a refusal applied nothing, so there is nothing to replay. A
// resend of the same key is checked against the stock as it is then, and
// still applies at most once. (Android spends the key on any definite answer,
// so in practice the key is never sent again.)
func TestStockAdjust_ARefusalUnderAKeyIsNotRecorded(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	f := newStockFixture(t, 3, 0)
	adj := f.adjust(-5, "damage")
	adj.IdempotencyKey = "stock-" + uuid.NewString()

	for i := 0; i < 2; i++ {
		if _, err := store.AdjustStock(ctx, adj); !errors.Is(err, ErrInsufficientStock) {
			t.Fatalf("attempt %d: err = %v, want ErrInsufficientStock", i+1, err)
		}
	}
	var rows int
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM inventory_adjustments
	                             WHERE seller_id=$1 AND idempotency_key=$2`,
		f.sellerID, adj.IdempotencyKey).Scan(&rows)
	if rows != 0 {
		t.Fatalf("a refused adjustment stored its key (%d rows)", rows)
	}
	if n := totalOf(t, f.variantID); n != 3 {
		t.Fatalf("total_qty = %d, want 3", n)
	}

	// Restocked meanwhile: the same key now applies, once.
	if _, err := store.AdjustStock(ctx, f.adjust(10, "purchase")); err != nil {
		t.Fatal(err)
	}
	first, err := store.AdjustStock(ctx, adj)
	if err != nil || first.Replayed || first.TotalQty != 8 {
		t.Fatalf("resent key after restock: %+v %v", first, err)
	}
	again, err := store.AdjustStock(ctx, adj)
	if err != nil || !again.Replayed || again.TotalQty != 8 {
		t.Fatalf("second resend: %+v %v, want a replay", again, err)
	}
	if n := totalOf(t, f.variantID); n != 8 {
		t.Fatalf("total_qty = %d, want 8 (-5 once)", n)
	}
}
