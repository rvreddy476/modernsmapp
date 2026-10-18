//go:build integration

package peers

// Run against a DISPOSABLE database whose name ends in _test:
//
//	PEERS_TEST_DSN=postgres://postgres:postgres@localhost:5432/chat_b2_presence_test?sslmode=disable \
//	  go test -tags integration ./internal/peers/ -count=1
//
// The test creates the minimal slice of message-service's chat schema it
// reads (conversations, conversation_members) in that database.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDMPeersOnlyAcceptedActiveDirectConversations(t *testing.T) {
	dsn := os.Getenv("PEERS_TEST_DSN")
	if dsn == "" {
		t.Skip("PEERS_TEST_DSN not set")
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
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS chat CASCADE`,
		`CREATE SCHEMA chat`,
		`CREATE TABLE chat.conversations (id UUID PRIMARY KEY, type TEXT NOT NULL,
			is_request BOOLEAN NOT NULL DEFAULT FALSE, updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW())`,
		`CREATE TABLE chat.conversation_members (conversation_id UUID NOT NULL REFERENCES chat.conversations(id),
			user_id UUID NOT NULL, left_at TIMESTAMPTZ, PRIMARY KEY (conversation_id, user_id))`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			t.Fatal(err)
		}
	}

	me := uuid.New()
	friend, older, stranger, leaver, groupMate := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	add := func(kind string, isRequest bool, age string, other uuid.UUID, otherLeft bool) {
		conv := uuid.New()
		if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id, type, is_request, updated_at)
			VALUES ($1, $2, $3, NOW() - $4::interval)`, conv, kind, isRequest, age); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id) VALUES ($1, $2)`, conv, me); err != nil {
			t.Fatal(err)
		}
		var left any
		if otherLeft {
			left = "2026-01-01T00:00:00Z"
		}
		if _, err := pool.Exec(ctx, `INSERT INTO chat.conversation_members (conversation_id, user_id, left_at) VALUES ($1, $2, $3::timestamptz)`, conv, other, left); err != nil {
			t.Fatal(err)
		}
	}
	add("direct", false, "1 minute", friend, false)
	add("direct", false, "1 day", older, false)
	add("direct", true, "1 second", stranger, false) // pending request
	add("direct", false, "1 second", leaver, true)
	add("group", false, "1 second", groupMate, false)
	// A second, older direct conversation with the same friend (seen on dev:
	// it doubled every presence frame). The friend must still appear once,
	// ranked by the newest shared conversation.
	add("direct", false, "2 days", friend, false)

	got, err := New(pool).DMPeers(ctx, me, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != friend || got[1] != older {
		t.Fatalf("peers = %v, want [friend older] (each peer once; no requests, leavers or group members)", got)
	}
	if limited, _ := New(pool).DMPeers(ctx, me, 1); len(limited) != 1 || limited[0] != friend {
		t.Fatalf("limit must keep the most recent: %v", limited)
	}
}
