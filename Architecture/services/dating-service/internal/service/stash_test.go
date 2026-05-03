// Stash service tests. Skipped unless TEST_PG_DSN is set.
package service

import (
	"context"
	"os"
	"testing"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func newStashSvc(t *testing.T) (*Service, *store.Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping stash service tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	st := store.New(pool)
	return New(st, nil), st, func() { pool.Close() }
}

func TestStashService_AddRemoveList(t *testing.T) {
	svc, st, cleanup := newStashSvc(t)
	defer cleanup()
	user, cand := uuid.New(), uuid.New()
	seedProfile(t, st, user)
	seedProfile(t, st, cand)

	if _, err := svc.AddStash(context.Background(), user, cand); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := svc.ListStash(context.Background(), user)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 stash, got %d", len(out))
	}
	if err := svc.RemoveStash(context.Background(), user, cand, "passed"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	out, _ = svc.ListStash(context.Background(), user)
	if len(out) != 0 {
		t.Fatalf("expected 0 after remove, got %d", len(out))
	}
}

func TestStashService_AddRejectsSelf(t *testing.T) {
	svc, st, cleanup := newStashSvc(t)
	defer cleanup()
	user := uuid.New()
	seedProfile(t, st, user)
	if _, err := svc.AddStash(context.Background(), user, user); err == nil {
		t.Fatalf("expected error for stashing self")
	}
}

func TestStashService_AddRejectsMissingCandidate(t *testing.T) {
	svc, st, cleanup := newStashSvc(t)
	defer cleanup()
	user, missing := uuid.New(), uuid.New()
	seedProfile(t, st, user)
	if _, err := svc.AddStash(context.Background(), user, missing); err == nil {
		t.Fatalf("expected error for missing candidate")
	}
}
