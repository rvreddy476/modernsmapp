//go:build integration

package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// The delivery start counter against the real schema (migration 033).
// CI only, on a _test database (leasePool enforces it).
func TestTranscodeDeliveryCountsStartsFinishesAndResetsPerRequest(t *testing.T) {
	pool := leasePool(t)
	store := New(pool)
	ctx := context.Background()
	id, _ := seedLeaseAsset(t, pool, nil, 0, 0, 0)

	// Two starts with no finish between: one lost run.
	if s, f, err := store.BeginTranscodeDelivery(ctx, id, "ev-a"); err != nil || s != 1 || f != 0 {
		t.Fatalf("first start = %d/%d %v, want 1/0", s, f, err)
	}
	if s, f, err := store.BeginTranscodeDelivery(ctx, id, "ev-a"); err != nil || s != 2 || f != 0 {
		t.Fatalf("second start = %d/%d %v, want 2/0", s, f, err)
	}
	if err := store.FinishTranscodeDelivery(ctx, id, "ev-a"); err != nil {
		t.Fatal(err)
	}
	if s, f, err := store.BeginTranscodeDelivery(ctx, id, "ev-a"); err != nil || s != 3 || f != 1 {
		t.Fatalf("third start = %d/%d %v, want 3/1", s, f, err)
	}
	// A finish for another request does not count.
	if err := store.FinishTranscodeDelivery(ctx, id, "ev-other"); err != nil {
		t.Fatal(err)
	}
	// A new request resets.
	if s, f, err := store.BeginTranscodeDelivery(ctx, id, "ev-b"); err != nil || s != 1 || f != 0 {
		t.Fatalf("new request start = %d/%d %v, want 1/0", s, f, err)
	}
	if _, _, err := store.BeginTranscodeDelivery(ctx, uuid.New(), "ev-x"); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("gone asset err = %v, want ErrMediaNotFound", err)
	}
}
