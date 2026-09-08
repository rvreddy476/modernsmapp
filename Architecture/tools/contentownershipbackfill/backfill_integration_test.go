//go:build integration

// These tests create and use their OWN database, named below, and never
// touch `app`, `commerce_db` or any other live database. Pointing an
// integration suite at a live database is what put 4,700 fixture sellers
// into commerce_db; the schema here is built from scratch every run and the
// database is dropped and recreated at the start, so it cannot be shared
// with anything real.
//
//	go test -tags=integration ./... \
//	  -args   # (DSN comes from the environment)
//
//	CONTENT_OWNERSHIP_BACKFILL_DSN=postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable
package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// scratchDB is deliberately not `app`, not `commerce_db`, and not any of the
// analytics_it_* databases another suite owns.
const scratchDB = "contentownershipbackfill_it_test"

// schemaSQL is the minimum shape scanSQL and upsertSQL touch. content_ownership
// is copied from analytics-service/database/migrations/004, index included, so
// the test exercises the real primary key and the real conflict path.
const schemaSQL = `
CREATE SCHEMA IF NOT EXISTS analytics;

CREATE TABLE users (
    id uuid PRIMARY KEY
);

CREATE TABLE posts (
    id                    uuid PRIMARY KEY,
    author_id             uuid        NOT NULL,
    content_type          text        NOT NULL DEFAULT 'post',
    created_at            timestamptz NOT NULL,
    visibility            text        NOT NULL DEFAULT 'public',
    review_status         text        NOT NULL DEFAULT 'approved',
    deleted_at            timestamptz,
    publish_at            timestamptz,
    content_type_explicit boolean     NOT NULL DEFAULT false
);

CREATE TABLE video_metadata (
    post_id        uuid PRIMARY KEY,
    upload_status  text NOT NULL DEFAULT 'pending',
    final_category text NOT NULL DEFAULT 'flick'
);

CREATE TABLE analytics.content_ownership (
    content_id   UUID PRIMARY KEY,
    creator_id   UUID NOT NULL,
    content_type TEXT NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    projected_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_content_ownership_creator
    ON analytics.content_ownership (creator_id, created_at DESC);
`

func scratchPool(t *testing.T) *pgxpool.Pool {
	t.Helper()

	admin := strings.TrimSpace(os.Getenv("CONTENT_OWNERSHIP_BACKFILL_DSN"))
	if admin == "" {
		t.Skip("CONTENT_OWNERSHIP_BACKFILL_DSN is required (point it at the `postgres` maintenance database)")
	}
	ctx := context.Background()

	cfg, err := pgx.ParseConfig(admin)
	if err != nil {
		t.Fatalf("parse admin dsn: %v", err)
	}
	if cfg.Database == scratchDB {
		t.Fatalf("CONTENT_OWNERSHIP_BACKFILL_DSN must point at a maintenance database, not %s", scratchDB)
	}
	// Guard rail, not politeness: refuse to be aimed at anything real.
	for _, forbidden := range []string{"app", "commerce_db", "identity_db"} {
		if cfg.Database == forbidden {
			t.Fatalf("refusing to run against %q; point CONTENT_OWNERSHIP_BACKFILL_DSN at `postgres`", forbidden)
		}
	}

	adminConn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	defer adminConn.Close(ctx)

	// DROP + CREATE, so every run starts from a schema this file defines.
	if _, err := adminConn.Exec(ctx, `DROP DATABASE IF EXISTS `+scratchDB+` WITH (FORCE)`); err != nil {
		t.Fatalf("drop scratch database: %v", err)
	}
	if _, err := adminConn.Exec(ctx, `CREATE DATABASE `+scratchDB); err != nil {
		t.Fatalf("create scratch database: %v", err)
	}

	cfg.Database = scratchDB
	pool, err := pgxpool.New(ctx, connString(admin, scratchDB))
	if err != nil {
		t.Fatalf("connect scratch: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(ctx, schemaSQL); err != nil {
		t.Fatalf("bootstrap schema: %v", err)
	}
	return pool
}

// connString rewrites the database segment of a URL-style DSN.
func connString(dsn, db string) string {
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		return dsn
	}
	sslmode := "disable"
	if cfg.TLSConfig != nil {
		sslmode = "require"
	}
	return "postgres://" + cfg.User + ":" + cfg.Password + "@" +
		cfg.Host + ":" + itoa(int(cfg.Port)) + "/" + db + "?sslmode=" + sslmode
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

type seed struct {
	id          uuid.UUID
	author      uuid.UUID
	contentType string
	createdAt   time.Time
	visibility  string
	deleted     bool
}

func insertUser(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO users (id) VALUES ($1)`, id); err != nil {
		t.Fatalf("insert user: %v", err)
	}
}

func insertPost(t *testing.T, pool *pgxpool.Pool, s seed) {
	t.Helper()
	vis := s.visibility
	if vis == "" {
		vis = "public"
	}
	var deletedAt *time.Time
	if s.deleted {
		d := s.createdAt.Add(time.Hour)
		deletedAt = &d
	}
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO posts (id, author_id, content_type, created_at, visibility, deleted_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		s.id, s.author, s.contentType, s.createdAt, vis, deletedAt); err != nil {
		t.Fatalf("insert post: %v", err)
	}
}

func ownershipOf(t *testing.T, pool *pgxpool.Pool, id uuid.UUID) (creator uuid.UUID, contentType string, createdAt time.Time, found bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(),
		`SELECT creator_id, content_type, created_at FROM analytics.content_ownership WHERE content_id = $1`,
		id).Scan(&creator, &contentType, &createdAt)
	if err == pgx.ErrNoRows {
		return uuid.Nil, "", time.Time{}, false
	}
	if err != nil {
		t.Fatalf("read ownership: %v", err)
	}
	return creator, contentType, createdAt, true
}

// TestBackfillEndToEnd walks the whole tool: a dry run must write nothing,
// and the apply that follows must produce exactly the rows the dry run
// promised — with the post's own created_at, and with the conflicting row
// left untouched.
func TestBackfillEndToEnd(t *testing.T) {
	pool := scratchPool(t)
	ctx := context.Background()

	known := uuid.New()
	other := uuid.New()
	insertUser(t, pool, known)
	insertUser(t, pool, other)
	ghost := uuid.New() // deliberately NOT in users

	day := func(n int) time.Time {
		return time.Date(2026, 8, 22, 4, 0, 0, 0, time.UTC).AddDate(0, 0, n)
	}

	plain := uuid.New()
	flick := uuid.New()
	priv := uuid.New()
	gone := uuid.New()
	orphan := uuid.New()
	stale := uuid.New()
	conflicted := uuid.New()

	insertPost(t, pool, seed{id: plain, author: known, contentType: "post", createdAt: day(0)})
	insertPost(t, pool, seed{id: flick, author: known, contentType: "flick", createdAt: day(1)})
	insertPost(t, pool, seed{id: priv, author: known, contentType: "long_video", createdAt: day(2), visibility: "private"})
	insertPost(t, pool, seed{id: gone, author: known, contentType: "post", createdAt: day(3), deleted: true})
	insertPost(t, pool, seed{id: orphan, author: ghost, contentType: "post", createdAt: day(4)})
	insertPost(t, pool, seed{id: stale, author: known, contentType: "flick", createdAt: day(5)})
	insertPost(t, pool, seed{id: conflicted, author: known, contentType: "post", createdAt: day(6)})

	// stale already has a row from the same creator, but the kind is out of
	// date — the upsert corrects it.
	mustExec(t, pool, `INSERT INTO analytics.content_ownership
		(content_id, creator_id, content_type, created_at) VALUES ($1,$2,'long_video',$3)`,
		stale, known, day(5))
	// conflicted is claimed by somebody else. Immutable: never rewritten.
	mustExec(t, pool, `INSERT INTO analytics.content_ownership
		(content_id, creator_id, content_type, created_at) VALUES ($1,$2,'post',$3)`,
		conflicted, other, day(6))

	pol := policy{}

	// --- dry run --------------------------------------------------------
	// batch of 2 on purpose: seven posts is four pages, so the keyset
	// pagination is exercised rather than assumed.
	dry := run(ctx, pool, pol, false, 2, 0, 0)
	if dry.scanned != 7 {
		t.Fatalf("dry scanned = %d, want 7", dry.scanned)
	}
	if got := len(dry.wouldProject); got != 4 {
		t.Fatalf("wouldProject = %d, want 4 (plain, flick, priv, gone); %v", got, dry.wouldProject)
	}
	if got := len(dry.wouldRetype); got != 1 {
		t.Fatalf("wouldRetype = %d, want 1 (stale); %v", got, dry.wouldRetype)
	}
	if got := len(dry.skippedUnknownAuthor); got != 1 {
		t.Fatalf("skippedUnknownAuthor = %d, want 1 (orphan)", got)
	}
	if got := len(dry.conflicts); got != 1 {
		t.Fatalf("conflicts = %d, want 1 (conflicted); %v", got, dry.conflicts)
	}
	if !strings.Contains(dry.conflicts[0], conflicted.String()) {
		t.Fatalf("conflict does not name the post: %s", dry.conflicts[0])
	}

	var rowsAfterDry int
	mustScan(t, pool, `SELECT COUNT(*) FROM analytics.content_ownership`, &rowsAfterDry)
	if rowsAfterDry != 2 {
		t.Fatalf("dry run wrote to the database: %d rows, want the 2 seeded", rowsAfterDry)
	}
	if _, ct, _, _ := ownershipOf(t, pool, stale); ct != "long_video" {
		t.Fatalf("dry run mutated content_type to %q", ct)
	}

	// --- apply ----------------------------------------------------------
	got := run(ctx, pool, pol, true, 2, 0, 0)
	if got.projected != 4 {
		t.Fatalf("projected = %d, want 4", got.projected)
	}
	if got.retyped != 1 {
		t.Fatalf("retyped = %d, want 1", got.retyped)
	}
	if len(got.writeFailed) != 0 {
		t.Fatalf("write failures: %v", got.writeFailed)
	}
	if len(got.conflicts) != 1 {
		t.Fatalf("conflicts on apply = %d, want 1", len(got.conflicts))
	}

	// The private and the soft-deleted post are both projected: an ownership
	// row grants nothing, and both decisions are deliberate.
	for _, id := range []uuid.UUID{plain, flick, priv, gone} {
		if _, _, _, ok := ownershipOf(t, pool, id); !ok {
			t.Fatalf("post %s was not projected", id)
		}
	}
	if _, _, _, ok := ownershipOf(t, pool, orphan); ok {
		t.Fatal("a post by an author who does not exist was projected")
	}

	// content_type is copied verbatim from posts.
	if _, ct, _, _ := ownershipOf(t, pool, flick); ct != "flick" {
		t.Fatalf("flick projected as %q", ct)
	}
	if _, ct, _, _ := ownershipOf(t, pool, priv); ct != "long_video" {
		t.Fatalf("long_video projected as %q", ct)
	}
	// The stale row's kind is corrected, its creator and created_at are not.
	creator, ct, createdAt, _ := ownershipOf(t, pool, stale)
	if ct != "flick" {
		t.Fatalf("stale content_type = %q, want flick", ct)
	}
	if creator != known {
		t.Fatalf("stale creator changed to %s", creator)
	}
	if !createdAt.Equal(day(5)) {
		t.Fatalf("stale created_at = %s, want %s", createdAt, day(5))
	}

	// created_at is the POST's, never the run's. Getting this wrong
	// misdates every historical earning against
	// idx_content_ownership_creator (creator_id, created_at DESC).
	for i, id := range []uuid.UUID{plain, flick, priv, gone} {
		_, _, createdAt, _ := ownershipOf(t, pool, id)
		if !createdAt.Equal(day(i)) {
			t.Fatalf("post %s projected with created_at %s, want %s", id, createdAt, day(i))
		}
	}

	// The conflicting row is exactly as it was. Immutability held.
	creator, ct, _, _ = ownershipOf(t, pool, conflicted)
	if creator != other || ct != "post" {
		t.Fatalf("conflicting ownership was rewritten: creator=%s content_type=%s", creator, ct)
	}

	// --- idempotence ----------------------------------------------------
	again := run(ctx, pool, pol, true, 500, 0, 0)
	if again.projected != 0 || again.retyped != 0 {
		t.Fatalf("re-run was not a no-op: projected=%d retyped=%d", again.projected, again.retyped)
	}
	if again.alreadyProjected != 5 {
		t.Fatalf("alreadyProjected = %d, want 5", again.alreadyProjected)
	}
	if len(again.conflicts) != 1 {
		t.Fatalf("the conflict stopped being reported on re-run")
	}
}

// TestIncludeUnknownAuthorsIsOptIn proves the flag actually changes the
// outcome — the 401 fixture posts on the live stack ride on this default.
func TestIncludeUnknownAuthorsIsOptIn(t *testing.T) {
	pool := scratchPool(t)
	ctx := context.Background()

	ghost := uuid.New()
	id := uuid.New()
	insertPost(t, pool, seed{id: id, author: ghost, contentType: "flick",
		createdAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)})

	if got := run(ctx, pool, policy{}, true, 100, 0, 0); got.projected != 0 {
		t.Fatalf("default policy projected an unknown author")
	}
	if _, _, _, ok := ownershipOf(t, pool, id); ok {
		t.Fatal("row written under the default policy")
	}

	got := run(ctx, pool, policy{includeUnknownAuthors: true}, true, 100, 0, 0)
	if got.projected != 1 {
		t.Fatalf("projected = %d, want 1 under -include-unknown-authors", got.projected)
	}
	if _, ct, _, ok := ownershipOf(t, pool, id); !ok || ct != "flick" {
		t.Fatalf("row missing or mistyped: ok=%t content_type=%q", ok, ct)
	}
}

// TestSkipDeleted covers the other half of the deliberate default.
func TestSkipDeleted(t *testing.T) {
	pool := scratchPool(t)
	ctx := context.Background()

	author := uuid.New()
	insertUser(t, pool, author)
	id := uuid.New()
	insertPost(t, pool, seed{id: id, author: author, contentType: "post",
		createdAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), deleted: true})

	if got := run(ctx, pool, policy{skipDeleted: true}, true, 100, 0, 0); got.projected != 0 {
		t.Fatalf("-skip-deleted still projected a soft-deleted post")
	}
	if got := run(ctx, pool, policy{}, true, 100, 0, 0); got.projected != 1 {
		t.Fatalf("default policy did not project a soft-deleted post")
	}
}

// TestLimitStopsEarly guards the -limit flag against the keyset loop.
func TestLimitStopsEarly(t *testing.T) {
	pool := scratchPool(t)
	ctx := context.Background()

	author := uuid.New()
	insertUser(t, pool, author)
	for i := 0; i < 10; i++ {
		insertPost(t, pool, seed{id: uuid.New(), author: author, contentType: "post",
			createdAt: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(i) * time.Minute)})
	}
	got := run(ctx, pool, policy{}, false, 3, 7, 0)
	if got.scanned != 7 {
		t.Fatalf("scanned = %d, want 7", got.scanned)
	}
	if len(got.wouldProject) != 7 {
		t.Fatalf("wouldProject = %d, want 7", len(got.wouldProject))
	}
}

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("exec %q: %v", sql, err)
	}
}

func mustScan(t *testing.T, pool *pgxpool.Pool, sql string, dst ...any) {
	t.Helper()
	if err := pool.QueryRow(context.Background(), sql).Scan(dst...); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
}
