// Stash service tests. Skipped unless TEST_PG_DSN is set.
package service

import (
	"context"
	"errors"
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
	// Audit H1: only someone the user may see can be stashed; an open match
	// is one such relationship.
	matchID, _, err := st.CreateOrGetOpenMatch(context.Background(), user, cand, nil)
	if err != nil {
		t.Fatalf("seed match: %v", err)
	}
	if err := st.MarkMatchActive(context.Background(), matchID, uuid.New()); err != nil {
		t.Fatalf("activate match: %v", err)
	}

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

// Audit H1: a profile the user was never shown (here: one with no
// relationship to them at all) cannot be stashed, and the refusal is the
// same ErrCandidateUnavailable as for a user id with no profile.
func TestStashService_AddRejectsUnseenCandidate(t *testing.T) {
	svc, st, cleanup := newStashSvc(t)
	defer cleanup()
	user, unseen := uuid.New(), uuid.New()
	seedProfile(t, st, user)
	seedProfile(t, st, unseen)
	for _, id := range []uuid.UUID{unseen, uuid.New()} {
		if _, err := svc.AddStash(context.Background(), user, id); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("stash %s: err=%v, want ErrCandidateUnavailable", id, err)
		}
	}
	if out, _ := svc.ListStash(context.Background(), user); len(out) != 0 {
		t.Fatalf("refused stash was written: %d rows", len(out))
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
