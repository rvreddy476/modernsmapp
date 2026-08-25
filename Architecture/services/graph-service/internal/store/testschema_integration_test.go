//go:build integration

package store

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Module 3 SR-2 — the COMPLETE schema for the live suite.
//
// Why this file exists.
//
// The previous live run applied database/setup.sql plus whichever migrations
// happened to succeed. Migration 004 (close_friends, circles, circle_members,
// relationship_labels, favorites) fails against a bare graph database because
// its foreign keys reference a `users` table that graph-service does not own —
// identity does. So those five tables were simply ABSENT.
//
// BlockAtomic probes for them with to_regclass and skips what is missing, so
// the block-sweep test passed by sweeping nothing. That hid a real defect: the
// sweep statements named columns (`owner_id`, `member_id`) that do not exist
// in the real schema, and every one of them would have raised 42703 and
// aborted the block transaction in any deployment where 004 HAD been applied.
// Block would have failed outright in production.
//
// A safety test that runs against absent tables asserts nothing. So the suite
// now builds the full schema — including a local stand-in for the identity
// `users` table that the FKs need — and seeds every relationship table before
// asserting that a block sweeps them.

// testSchemaDDL is the complete graph schema, including the identity-owned
// `users` projection the migration FKs point at.
// gen_random_uuid() is built into PostgreSQL 13+, so no pgcrypto extension is
// required — which also means the suite runs on a stock server with no
// superuser step.
const testSchemaDDL = `
-- Identity-owned projection. graph-service does not create this in
-- production; it lives in the shared identity database. Declared here so the
-- migration FKs resolve and the suite exercises the REAL constraints.
CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS follows (
    follower_id UUID NOT NULL,
    followee_id UUID NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (follower_id, followee_id)
);

CREATE TABLE IF NOT EXISTS blocks (
    blocker_id UUID NOT NULL,
    blocked_id UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (blocker_id, blocked_id)
);

CREATE TABLE IF NOT EXISTS counts (
    user_id         UUID PRIMARY KEY,
    follower_count  BIGINT NOT NULL DEFAULT 0,
    following_count BIGINT NOT NULL DEFAULT 0,
    friend_count    BIGINT NOT NULL DEFAULT 0,
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS connections (
    user_a     UUID NOT NULL,
    user_b     UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_a, user_b)
);

CREATE TABLE IF NOT EXISTS connection_requests (
    sender_id    UUID NOT NULL,
    receiver_id  UUID NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    source       TEXT,
    message      TEXT,
    is_filtered  BOOLEAN NOT NULL DEFAULT FALSE,
    filtered_at  TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    responded_at TIMESTAMPTZ,
    expires_at   TIMESTAMPTZ,
    PRIMARY KEY (sender_id, receiver_id)
);

-- ── migration 004 + 007: the tables that were MISSING from the last run ──
CREATE TABLE IF NOT EXISTS close_friends (
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    friend_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    source    TEXT NOT NULL DEFAULT 'manual',
    PRIMARY KEY (user_id, friend_id)
);

CREATE TABLE IF NOT EXISTS circles (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    owner_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       VARCHAR(100) NOT NULL,
    emoji      VARCHAR(10),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS circle_members (
    circle_id UUID NOT NULL REFERENCES circles(id) ON DELETE CASCADE,
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (circle_id, user_id)
);

CREATE TABLE IF NOT EXISTS relationship_labels (
    user_id    UUID NOT NULL,
    target_id  UUID NOT NULL,
    label      TEXT NOT NULL CHECK (label IN ('best_friend','family','colleague','classmate','acquaintance')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, target_id)
);

CREATE TABLE IF NOT EXISTS favorites (
    user_id   UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    target_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    added_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (user_id, target_id)
);

-- migration 003: mutes (soft-block, no notification). Mutes are ONE-WAY and
-- must never become symmetric — that is a Module 1/2 contract enforced by
-- block_symmetry_integration_test.go, which shares this schema.
CREATE SCHEMA IF NOT EXISTS graph;

CREATE TABLE IF NOT EXISTS graph.mutes (
    muter_id   UUID NOT NULL,
    muted_id   UUID NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (muter_id, muted_id)
);

-- ── migration 009: durable graph events ──
CREATE TABLE IF NOT EXISTS graph_outbox_events (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type      TEXT        NOT NULL,
    actor_id        UUID        NOT NULL,
    target_id       UUID        NOT NULL,
    pair_seq        BIGINT      NOT NULL,
    payload         JSONB       NOT NULL,
    occurred_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    published       BOOLEAN     NOT NULL DEFAULT FALSE,
    published_at    TIMESTAMPTZ,
    attempts        INT         NOT NULL DEFAULT 0,
    last_error      TEXT,
    leased_until    TIMESTAMPTZ,
    last_attempt_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS graph_pair_seq (
    lo_id UUID   NOT NULL,
    hi_id UUID   NOT NULL,
    seq   BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (lo_id, hi_id)
);

-- SR-5: follow_requests is deliberately NOT created. Launch is
-- public-accounts-only. If this table reappears, BlockAtomic must sweep it.
DROP TABLE IF EXISTS follow_requests;
`

// allRelationshipTables is the closed list a block must leave empty for the
// pair. Adding a relationship table to the schema without adding it here (and
// to BlockAtomic's sweep) is the failure this list is meant to make loud.
var allRelationshipTables = []struct {
	name string
	// countSQL takes ($1, $2) = the pair and returns rows still linking them.
	countSQL string
}{
	{"follows", `SELECT COUNT(*) FROM follows
		WHERE (follower_id = $1 AND followee_id = $2) OR (follower_id = $2 AND followee_id = $1)`},
	{"connections", `SELECT COUNT(*) FROM connections
		WHERE (user_a = $1 AND user_b = $2) OR (user_a = $2 AND user_b = $1)`},
	{"connection_requests", `SELECT COUNT(*) FROM connection_requests
		WHERE (sender_id = $1 AND receiver_id = $2) OR (sender_id = $2 AND receiver_id = $1)`},
	{"close_friends", `SELECT COUNT(*) FROM close_friends
		WHERE (user_id = $1 AND friend_id = $2) OR (user_id = $2 AND friend_id = $1)`},
	{"favorites", `SELECT COUNT(*) FROM favorites
		WHERE (user_id = $1 AND target_id = $2) OR (user_id = $2 AND target_id = $1)`},
	{"relationship_labels", `SELECT COUNT(*) FROM relationship_labels
		WHERE (user_id = $1 AND target_id = $2) OR (user_id = $2 AND target_id = $1)`},
	{"circle_members", `SELECT COUNT(*) FROM circle_members cm
		WHERE (cm.user_id = $2 AND cm.circle_id IN (SELECT id FROM circles WHERE owner_id = $1))
		   OR (cm.user_id = $1 AND cm.circle_id IN (SELECT id FROM circles WHERE owner_id = $2))`},
}

// graphPool connects and installs the complete schema.
func graphPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	pool, err := pgxpool.New(context.Background(), dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)

	if _, err := pool.Exec(context.Background(), testSchemaDDL); err != nil {
		t.Fatalf("install complete schema: %v", err)
	}

	// Guard against silently regressing to the incomplete-schema state that
	// made the previous run vacuous.
	for _, tbl := range allRelationshipTables {
		var reg *string
		if err := pool.QueryRow(context.Background(),
			`SELECT to_regclass($1)::text`, "public."+tbl.name).Scan(&reg); err != nil {
			t.Fatalf("probe %s: %v", tbl.name, err)
		}
		if reg == nil {
			t.Fatalf("table %s is missing: BlockAtomic would skip it and this suite "+
				"would assert nothing about it", tbl.name)
		}
	}
	return pool
}

// seedUsers inserts the identity projection rows the FKs require.
func seedUsers(t *testing.T, pool *pgxpool.Pool, ids ...interface{ String() string }) {
	t.Helper()
	for _, id := range ids {
		if _, err := pool.Exec(context.Background(),
			`INSERT INTO users (id) VALUES ($1) ON CONFLICT DO NOTHING`, id.String()); err != nil {
			t.Fatalf("seed user %s: %v", id.String(), err)
		}
	}
}
