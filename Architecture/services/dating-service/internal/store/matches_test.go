// Matches store integration tests. Skipped unless TEST_PG_DSN is set.
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func matchTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping matches store integration tests")
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

func TestCreateAndMarkMatchActive(t *testing.T) {
	s, cleanup := matchTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)

	id, err := s.CreateMatchPending(ctx, nil, a, b, map[string]any{"target_kind": "photo", "target_ref": "0"})
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	conv := uuid.New()
	if err := s.MarkMatchActive(ctx, id, conv); err != nil {
		t.Fatalf("mark active: %v", err)
	}
	m, err := s.GetMatch(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if m.ConversationID == nil || *m.ConversationID != conv {
		t.Fatalf("conversation id not set: %+v", m.ConversationID)
	}
	if m.Status != "matched" {
		t.Fatalf("expected status=matched, got %s", m.Status)
	}
	if m.ExpiresAt == nil {
		t.Fatalf("expected expires_at to be set")
	}
}

func TestDeleteMatch(t *testing.T) {
	s, cleanup := matchTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	id, err := s.CreateMatchPending(ctx, nil, a, b, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.DeleteMatch(ctx, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetMatch(ctx, id); err == nil {
		t.Fatalf("expected ErrMatchNotFound")
	}
}

func TestCanonicalPair(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	x, y := canonicalPair(a, b)
	if x.String() >= y.String() {
		t.Fatalf("canonical order broken")
	}
	x2, y2 := canonicalPair(b, a)
	if x != x2 || y != y2 {
		t.Fatalf("canonical not stable across argument order")
	}
}

func TestCloseMatch(t *testing.T) {
	s, cleanup := matchTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	id, err := s.CreateMatchPending(ctx, nil, a, b, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.CloseMatch(ctx, id, a); err != nil {
		t.Fatalf("close: %v", err)
	}
	m, err := s.GetMatch(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if m.Status != "closed" {
		t.Fatalf("status = %s, want closed", m.Status)
	}
}

func TestExpireStaleMatches(t *testing.T) {
	s, cleanup := matchTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	id, err := s.CreateMatchPending(ctx, nil, a, b, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkMatchActive(ctx, id, uuid.New()); err != nil {
		t.Fatalf("active: %v", err)
	}
	// Force expires_at into the past.
	if _, err := s.db.Exec(ctx, `UPDATE dating_matches SET expires_at = now() - INTERVAL '1 day', first_message_at = NULL WHERE id = $1`, id); err != nil {
		t.Fatalf("rewrite expires_at: %v", err)
	}
	expired, err := s.ExpireStaleMatches(ctx)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if len(expired) == 0 {
		t.Fatalf("expected at least one expired match")
	}
}

func TestMarkQuietMatches(t *testing.T) {
	s, cleanup := matchTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	id, err := s.CreateMatchPending(ctx, nil, a, b, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkMatchActive(ctx, id, uuid.New()); err != nil {
		t.Fatalf("active: %v", err)
	}
	if _, err := s.db.Exec(ctx, `UPDATE dating_matches SET last_message_at = now() - INTERVAL '20 days' WHERE id = $1`, id); err != nil {
		t.Fatalf("rewrite last_message_at: %v", err)
	}
	quieted, err := s.MarkQuietMatches(ctx)
	if err != nil {
		t.Fatalf("quiet: %v", err)
	}
	if len(quieted) == 0 {
		t.Fatalf("expected at least one quiet match")
	}
}

func TestRecordFirstMessage(t *testing.T) {
	s, cleanup := matchTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	id, err := s.CreateMatchPending(ctx, nil, a, b, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.MarkMatchActive(ctx, id, uuid.New()); err != nil {
		t.Fatalf("active: %v", err)
	}
	if err := s.RecordFirstMessage(ctx, id, time.Now()); err != nil {
		t.Fatalf("record: %v", err)
	}
	m, err := s.GetMatch(ctx, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if m.FirstMessageAt == nil {
		t.Fatalf("first_message_at not set")
	}
	if m.Status != "conversing" {
		t.Fatalf("expected status=conversing, got %s", m.Status)
	}
}
