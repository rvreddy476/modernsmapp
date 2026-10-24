//go:build integration

package chatdb

// Same disposable *_test database as mute_integration_test.go
// (CHAT_MUTE_TEST_DSN); it rebuilds the slice of the chat schema it reads.

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestHiddenForReadsTheCalleesLockOnSourceOrPairDM(t *testing.T) {
	dsn := os.Getenv("CHAT_MUTE_TEST_DSN")
	if dsn == "" {
		t.Skip("CHAT_MUTE_TEST_DSN not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var dbName string
	if err := pool.QueryRow(ctx, `SELECT current_database()`).Scan(&dbName); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatalf("refusing to run against %q: database name must end in _test", dbName)
	}
	reader := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
	caller, callee := uuid.New(), uuid.New()

	mustExec(t, pool, `DROP SCHEMA IF EXISTS chat CASCADE`)
	if reader.HiddenFor(ctx, callee, caller, nil) {
		t.Fatal("before the schema exists nothing is hidden")
	}
	mustExec(t, pool, `CREATE SCHEMA chat`)
	mustExec(t, pool, `CREATE TABLE chat.direct_conversation_keys (user_a UUID NOT NULL, user_b UUID NOT NULL,
		conversation_id UUID NOT NULL, PRIMARY KEY (user_a, user_b))`)
	mustExec(t, pool, `CREATE TABLE chat.conversation_members (conversation_id UUID NOT NULL, user_id UUID NOT NULL,
		left_at TIMESTAMPTZ, PRIMARY KEY (conversation_id, user_id))`)
	if reader.HiddenFor(ctx, callee, caller, nil) {
		t.Fatal("before migration 018 (no locked column) nothing is hidden")
	}
	mustExec(t, pool, `ALTER TABLE chat.conversation_members ADD COLUMN locked BOOLEAN NOT NULL DEFAULT FALSE`)

	dm, group := uuid.New(), uuid.New()
	a, b := caller.String(), callee.String()
	if a > b {
		a, b = b, a
	}
	mustExec(t, pool, `INSERT INTO chat.direct_conversation_keys VALUES ($1, $2, $3)`, a, b, dm)
	mustExec(t, pool, `INSERT INTO chat.conversation_members (conversation_id, user_id) VALUES ($1, $2), ($1, $3), ($4, $2), ($4, $3)`, dm, caller, callee, group)
	if reader.HiddenFor(ctx, callee, caller, nil) {
		t.Fatal("nothing locked yet")
	}
	mustExec(t, pool, `UPDATE chat.conversation_members SET locked = true WHERE conversation_id = $1 AND user_id = $2`, dm, callee)
	if !reader.HiddenFor(ctx, callee, caller, nil) || !reader.HiddenFor(ctx, callee, caller, &group) {
		t.Fatal("the callee's hidden DM must flag the call whatever source is named")
	}
	if reader.HiddenFor(ctx, caller, callee, nil) {
		t.Fatal("the CALLER is unaffected by the callee's hide")
	}
	if reader.HiddenFor(ctx, callee, uuid.Nil, &group) {
		t.Fatal("a group call from an unhidden group is not hidden")
	}
	mustExec(t, pool, `UPDATE chat.conversation_members SET locked = true WHERE conversation_id = $1 AND user_id = $2`, group, callee)
	if !reader.HiddenFor(ctx, callee, uuid.Nil, &group) {
		t.Fatal("a group call from a hidden group is hidden")
	}
	mustExec(t, pool, `UPDATE chat.conversation_members SET left_at = now() WHERE conversation_id = $1 AND user_id = $2`, group, callee)
	if reader.HiddenFor(ctx, callee, uuid.Nil, &group) {
		t.Fatal("a lock on a membership the callee left does not count")
	}
}
