// Stashes store integration tests. Skipped unless TEST_PG_DSN is set.
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func stashTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping stashes store integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return New(pool), func() { pool.Close() }
}

func TestAddStash_AndList(t *testing.T) {
	s, cleanup := stashTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, cand := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, user)
	ensureProfileForTest(t, s, cand)

	expires := time.Now().Add(48 * time.Hour)
	if err := s.AddStash(ctx, user, cand, expires); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := s.ListStash(ctx, user)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(out))
	}
	if out[0].CandidateID != cand {
		t.Fatalf("candidate id mismatch")
	}
}

func TestAddStash_Idempotent(t *testing.T) {
	s, cleanup := stashTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, cand := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, user)
	ensureProfileForTest(t, s, cand)

	if err := s.AddStash(ctx, user, cand, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("first add: %v", err)
	}
	if err := s.AddStash(ctx, user, cand, time.Now().Add(72*time.Hour)); err != nil {
		t.Fatalf("second add: %v", err)
	}
	out, _ := s.ListStash(ctx, user)
	if len(out) != 1 {
		t.Fatalf("expected idempotent insert; got %d entries", len(out))
	}
}

func TestRemoveStash(t *testing.T) {
	s, cleanup := stashTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, cand := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, user)
	ensureProfileForTest(t, s, cand)
	if err := s.AddStash(ctx, user, cand, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.RemoveStash(ctx, user, cand, "unstashed"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if err := s.RemoveStash(ctx, user, cand, "again"); err == nil {
		t.Fatalf("expected ErrStashNotFound on second remove")
	}
}

func TestMarkStashReactivated(t *testing.T) {
	s, cleanup := stashTestStore(t)
	defer cleanup()
	ctx := context.Background()
	user, cand := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, user)
	ensureProfileForTest(t, s, cand)
	if err := s.AddStash(ctx, user, cand, time.Now().Add(24*time.Hour)); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := s.MarkStashReactivated(ctx, user, cand, "they posted a flick"); err != nil {
		t.Fatalf("mark reactivated: %v", err)
	}
	out, err := s.ListStash(ctx, user)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 || out[0].ReactivationSignal == nil || *out[0].ReactivationSignal == "" {
		t.Fatalf("expected reactivation signal; got %+v", out)
	}
}
