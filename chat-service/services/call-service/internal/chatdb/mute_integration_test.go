//go:build integration

package chatdb

// Run against a DISPOSABLE database whose name ends in _test:
//
//	CHAT_MUTE_TEST_DSN=postgres://postgres:postgres@localhost:5432/chat_b2_presence_test?sslmode=disable \
//	  go test -tags integration ./internal/chatdb/ -count=1
//
// It creates the slice of message-service's chat schema the reader touches.

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

func mustExec(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), sql, args...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

func TestCallsMutedReadsSourceAndPairDM(t *testing.T) {
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

	mustExec(t, pool, `DROP SCHEMA IF EXISTS chat CASCADE`)
	caller, callee := uuid.New(), uuid.New()
	// Before the chat schema exists at all: not muted, no error surfaced.
	if reader.CallsMuted(ctx, callee, caller, nil) {
		t.Fatal("missing table must read as not muted")
	}

	mustExec(t, pool, `CREATE SCHEMA chat`)
	mustExec(t, pool, `CREATE TABLE chat.direct_conversation_keys (user_a UUID NOT NULL, user_b UUID NOT NULL,
		conversation_id UUID NOT NULL, PRIMARY KEY (user_a, user_b), CHECK (user_a < user_b))`)
	// Pre-migration-011 shape: no mute_calls column.
	mustExec(t, pool, `CREATE TABLE chat.conversation_settings (conversation_id UUID NOT NULL, user_id UUID NOT NULL,
		is_muted BOOLEAN NOT NULL DEFAULT FALSE, PRIMARY KEY (conversation_id, user_id))`)

	dm, group := uuid.New(), uuid.New()
	a, b := caller.String(), callee.String()
	if a > b {
		a, b = b, a
	}
	mustExec(t, pool, `INSERT INTO chat.direct_conversation_keys (user_a, user_b, conversation_id) VALUES ($1, $2, $3)`, a, b, dm)
	mustExec(t, pool, `INSERT INTO chat.conversation_settings (conversation_id, user_id, is_muted) VALUES ($1, $2, true)`, dm, callee)
	if reader.CallsMuted(ctx, callee, caller, nil) {
		t.Fatal("missing mute_calls column must read as not muted (message mute is not call mute)")
	}

	mustExec(t, pool, `ALTER TABLE chat.conversation_settings ADD COLUMN mute_calls BOOLEAN NOT NULL DEFAULT false`)
	if reader.CallsMuted(ctx, callee, caller, nil) {
		t.Fatal("default mute_calls=false must ring")
	}

	// Mute on the pair's DM: muted whatever source the caller names.
	mustExec(t, pool, `UPDATE chat.conversation_settings SET mute_calls = true WHERE conversation_id = $1 AND user_id = $2`, dm, callee)
	if !reader.CallsMuted(ctx, callee, caller, nil) {
		t.Fatal("DM mute_calls must mute a direct call")
	}
	if !reader.CallsMuted(ctx, callee, caller, &group) {
		t.Fatal("naming another conversation as source must not bypass the DM mute")
	}
	// The mute is the CALLEE's row, never the caller's.
	if reader.CallsMuted(ctx, caller, callee, nil) {
		t.Fatal("callee's mute must not mute calls the other way")
	}

	// Mute only on the source conversation.
	mustExec(t, pool, `UPDATE chat.conversation_settings SET mute_calls = false`)
	mustExec(t, pool, `INSERT INTO chat.conversation_settings (conversation_id, user_id, mute_calls) VALUES ($1, $2, true)`, group, callee)
	if !reader.CallsMuted(ctx, callee, caller, &group) {
		t.Fatal("source-conversation mute_calls must mute")
	}
	if reader.CallsMuted(ctx, callee, caller, nil) {
		t.Fatal("a group's mute must not mute an unrelated direct call")
	}
}
