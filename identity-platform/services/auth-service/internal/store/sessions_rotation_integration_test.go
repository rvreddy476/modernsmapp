//go:build integration

package store

// Refresh rotation, reuse detection and session cleanup against live
// PostgreSQL with the REAL database/setup.sql (CI only).
//
//	AUTH_ROLES_POSTGRES_DSN=postgres://.../auth_it_test \
//	  go test -tags integration ./internal/store/ -run SessionRotation
//
// AUTH_ROLES_POSTGRES_DSN, not POSTGRES_DSN: the rest of this package installs
// a hand-written subset into POSTGRES_DSN, and these tests need the real
// schema (the new index and auth.refresh_token_history). Refuses any database
// whose name does not end in _test.

import (
	"context"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/identity-auth-service/database"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func rotationPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("AUTH_ROLES_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("AUTH_ROLES_POSTGRES_DSN not set")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: invalid")
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run: database %q does not end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := BootstrapSchema(context.Background(), pool, database.SetupSQL); err != nil {
		t.Fatalf("install schema: %v", err)
	}
	return pool
}

func rotationSession(t *testing.T, st *Store, pool *pgxpool.Pool, hash string, expires time.Time) *Session {
	t.Helper()
	ctx := context.Background()
	uid := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO auth.users (user_id, email) VALUES ($1, $2)`,
		uid, uid.String()+"@rotation.test"); err != nil {
		t.Fatalf("user: %v", err)
	}
	sess := &Session{ID: uuid.New(), UserID: uid, RefreshToken: hash, CreatedAt: time.Now(), ExpiresAt: expires}
	if err := st.CreateSession(ctx, sess); err != nil {
		t.Fatalf("session: %v", err)
	}
	return sess
}

func TestSessionRotationIndexExists(t *testing.T) {
	pool := rotationPool(t)
	for _, idx := range []string{"idx_sessions_refresh_token_hash", "idx_sessions_expires_at", "idx_sessions_revoked_at"} {
		var valid bool
		err := pool.QueryRow(context.Background(), `
			SELECT i.indisvalid FROM pg_index i JOIN pg_class c ON c.oid = i.indexrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE n.nspname = 'auth' AND c.relname = $1`, idx).Scan(&valid)
		if err != nil || !valid {
			t.Fatalf("index %s missing or invalid: %v", idx, err)
		}
	}
}

func TestSessionRotationIsConditionalOnOldHash(t *testing.T) {
	pool := rotationPool(t)
	st := New(pool)
	ctx := context.Background()
	old := uuid.NewString()
	sess := rotationSession(t, st, pool, old, time.Now().Add(time.Hour))

	ok, err := st.RotateSessionWithFingerprint(ctx, sess.ID, old, "new-1", "1.1.1.1", time.Now().Add(time.Hour), false)
	if err != nil || !ok {
		t.Fatalf("first rotation: %v %v", ok, err)
	}
	// The same old hash again: the row no longer holds it.
	ok, err = st.RotateSessionWithFingerprint(ctx, sess.ID, old, "new-2", "1.1.1.1", time.Now().Add(time.Hour), false)
	if err != nil || ok {
		t.Fatalf("stale rotation succeeded: %v %v", ok, err)
	}
	got, _ := st.GetSessionByRefreshTokenHash(ctx, "new-1")
	if got == nil || got.ID != sess.ID {
		t.Fatal("winning hash lost")
	}
	if sid, err := st.LookupRotatedRefreshToken(ctx, old); err != nil || sid == nil || *sid != sess.ID {
		t.Fatalf("rotated-away hash not in history: %v %v", sid, err)
	}
	if sid, _ := st.LookupRotatedRefreshToken(ctx, "never-issued"); sid != nil {
		t.Fatal("unknown hash found in history")
	}
}

func TestSessionRotationConcurrentOnlyOneWins(t *testing.T) {
	pool := rotationPool(t)
	st := New(pool)
	old := uuid.NewString()
	sess := rotationSession(t, st, pool, old, time.Now().Add(time.Hour))
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := st.RotateSessionWithFingerprint(context.Background(), sess.ID, old,
				uuid.NewString(), "", time.Now().Add(time.Hour), false)
			if err == nil && ok {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("%d concurrent rotations of one token succeeded", wins)
	}
}

func TestSessionRotationRefusesRevokedSession(t *testing.T) {
	pool := rotationPool(t)
	st := New(pool)
	ctx := context.Background()
	old := uuid.NewString()
	sess := rotationSession(t, st, pool, old, time.Now().Add(time.Hour))
	if err := st.RevokeSession(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := st.RotateSessionWithFingerprint(ctx, sess.ID, old, "new", "", time.Now().Add(time.Hour), false); err != nil || ok {
		t.Fatalf("revoked session rotated: %v %v", ok, err)
	}
}

func TestSessionRotationRevokeFamily(t *testing.T) {
	pool := rotationPool(t)
	st := New(pool)
	ctx := context.Background()
	sess := rotationSession(t, st, pool, uuid.NewString(), time.Now().Add(time.Hour))
	ids, err := st.RevokeSessionFamily(ctx, sess.ID)
	if err != nil || len(ids) != 1 || ids[0] != sess.ID {
		t.Fatalf("revoke family: %v %v", ids, err)
	}
	got, _ := st.GetSessionByID(ctx, sess.ID)
	if got == nil || got.RevokedAt == nil || got.IsActive {
		t.Fatal("session still live after family revoke")
	}
	if ids, _ := st.RevokeSessionFamily(ctx, sess.ID); len(ids) != 0 {
		t.Fatalf("second revoke reported %v", ids)
	}
}

func TestSessionRotationPurgeIsBoundedAndKeepsLiveRows(t *testing.T) {
	pool := rotationPool(t)
	st := New(pool)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `DELETE FROM auth.sessions WHERE expires_at < NOW() OR revoked_at IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	live := rotationSession(t, st, pool, uuid.NewString(), time.Now().Add(time.Hour))
	recentlyRevoked := rotationSession(t, st, pool, uuid.NewString(), time.Now().Add(time.Hour))
	_ = st.RevokeSession(ctx, recentlyRevoked.ID)
	var dead []uuid.UUID
	for i := 0; i < 3; i++ {
		s := rotationSession(t, st, pool, uuid.NewString(), time.Now().Add(-48*time.Hour))
		dead = append(dead, s.ID)
	}
	// History rows must go with their session.
	old := uuid.NewString()
	withHistory := rotationSession(t, st, pool, old, time.Now().Add(time.Hour))
	if ok, _ := st.RotateSessionWithFingerprint(ctx, withHistory.ID, old, uuid.NewString(), "", time.Now().Add(-48*time.Hour), false); !ok {
		t.Fatal("rotate")
	}

	n, err := st.PurgeDeadSessions(ctx, 24*time.Hour, 2)
	if err != nil || n != 2 {
		t.Fatalf("bounded purge deleted %d (%v), want 2", n, err)
	}
	n, err = st.PurgeDeadSessions(ctx, 24*time.Hour, 100)
	if err != nil || n != 2 {
		t.Fatalf("second purge deleted %d (%v), want the remaining 2", n, err)
	}
	for _, id := range append(dead, withHistory.ID) {
		if s, _ := st.GetSessionByID(ctx, id); s != nil {
			t.Fatalf("dead session %s survived", id)
		}
	}
	for _, id := range []uuid.UUID{live.ID, recentlyRevoked.ID} {
		if s, _ := st.GetSessionByID(ctx, id); s == nil {
			t.Fatalf("session %s inside retention was purged", id)
		}
	}
	if sid, _ := st.LookupRotatedRefreshToken(ctx, old); sid != nil {
		t.Fatal("history row outlived its session")
	}
}
