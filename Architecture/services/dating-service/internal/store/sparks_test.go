// Sparks store integration tests.
//
// Use a real Postgres if TEST_PG_DSN is exported in the env; otherwise
// t.Skip so CI can still run unit tests against the package.
package store

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func sparkTestStore(t *testing.T) (*Store, func()) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping sparks store integration tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	// Bootstrap minimal tables for the package under test. We rely on the
	// dating-service schema being already applied (set up by the same
	// container that exposes TEST_PG_DSN).
	return New(pool), func() { pool.Close() }
}

func ensureProfileForTest(t *testing.T, s *Store, id uuid.UUID) {
	t.Helper()
	if _, err := s.db.Exec(context.Background(), `
        INSERT INTO dating_profiles (user_id) VALUES ($1)
        ON CONFLICT DO NOTHING`, id); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
}

func TestCreateSpark_HappyPath(t *testing.T) {
	s, cleanup := sparkTestStore(t)
	defer cleanup()
	ctx := context.Background()
	from, to := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, from)
	ensureProfileForTest(t, s, to)

	sp, err := s.CreateSpark(ctx, from, to, "photo", "0", "love this shot")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sp.FromUserID != from || sp.ToUserID != to {
		t.Fatalf("user ids mismatch: %+v", sp)
	}
	if sp.TargetKind != "photo" || sp.TargetRef != "0" {
		t.Fatalf("target mismatch: %+v", sp)
	}
}

func TestCreateSpark_DuplicateReturnsExisting(t *testing.T) {
	s, cleanup := sparkTestStore(t)
	defer cleanup()
	ctx := context.Background()
	from, to := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, from)
	ensureProfileForTest(t, s, to)

	first, err := s.CreateSpark(ctx, from, to, "prompt", "p1", "hi")
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	second, err := s.CreateSpark(ctx, from, to, "prompt", "p1", "")
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("expected same id on duplicate; got %s vs %s", first.ID, second.ID)
	}
}

func TestCreateSpark_RejectsSelf(t *testing.T) {
	s, cleanup := sparkTestStore(t)
	defer cleanup()
	uid := uuid.New()
	if _, err := s.CreateSpark(context.Background(), uid, uid, "photo", "0", ""); err == nil {
		t.Fatalf("expected error sparking self")
	}
}

func TestCreateSpark_InvalidTargetKind(t *testing.T) {
	s, cleanup := sparkTestStore(t)
	defer cleanup()
	from, to := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, from)
	ensureProfileForTest(t, s, to)
	if _, err := s.CreateSpark(context.Background(), from, to, "bogus", "x", ""); err == nil {
		t.Fatalf("expected error for invalid target kind")
	}
}

func TestHasReverseSparks(t *testing.T) {
	s, cleanup := sparkTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)

	got, err := s.HasReverseSparks(ctx, a, b)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got {
		t.Fatalf("expected false before any sparks")
	}
	if _, err := s.CreateSpark(ctx, b, a, "photo", "0", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, err = s.HasReverseSparks(ctx, a, b)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !got {
		t.Fatalf("expected true after b sparks a")
	}
}

func TestListIncomingSparks(t *testing.T) {
	s, cleanup := sparkTestStore(t)
	defer cleanup()
	ctx := context.Background()
	a, b, recipient := uuid.New(), uuid.New(), uuid.New()
	ensureProfileForTest(t, s, a)
	ensureProfileForTest(t, s, b)
	ensureProfileForTest(t, s, recipient)
	if _, err := s.CreateSpark(ctx, a, recipient, "photo", "0", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := s.CreateSpark(ctx, b, recipient, "prompt", "p1", ""); err != nil {
		t.Fatalf("seed: %v", err)
	}
	out, err := s.ListIncomingSparks(ctx, recipient, 10, 0)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
}

func TestDeleteSpark_OwnerOnly(t *testing.T) {
	s, cleanup := sparkTestStore(t)
	defer cleanup()
	ctx := context.Background()
	owner, other, recipient := uuid.New(), uuid.New(), uuid.New()
	ensureProfileForTest(t, s, owner)
	ensureProfileForTest(t, s, other)
	ensureProfileForTest(t, s, recipient)
	sp, err := s.CreateSpark(ctx, owner, recipient, "photo", "0", "")
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	// Other user cannot delete.
	if err := s.DeleteSpark(ctx, sp.ID, other); err == nil {
		t.Fatalf("expected ErrSparkNotFound when non-owner deletes")
	}
	// Owner can.
	if err := s.DeleteSpark(ctx, sp.ID, owner); err != nil {
		t.Fatalf("owner delete: %v", err)
	}
	if _, err := s.GetSpark(ctx, sp.ID); err == nil {
		t.Fatalf("expected ErrSparkNotFound after delete")
	}
}
