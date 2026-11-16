package postgres

import (
	"hash/fnv"
	"strings"
	"testing"
)

// feed-07: the relay lock's key and statements. The lock's behaviour is
// Postgres's own and needs a database (CI); this pins what is sent.

func TestOutboxRelayLock_KeyAndStatements(t *testing.T) {
	if outboxRelayLockKey <= 0 {
		t.Fatalf("lock key = %d, want a positive bigint", outboxRelayLockKey)
	}
	// shared/outbox derives its relay keys from "shared/outbox:relay:<table>";
	// this one must not be the key that scheme gives the same table.
	h := fnv.New64a()
	_, _ = h.Write([]byte("shared/outbox:relay:post_outbox_events"))
	if shared := int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF); shared == outboxRelayLockKey {
		t.Fatal("lock key collides with shared/outbox's key for the same table")
	}
	// Session-level and non-blocking: a transaction-level lock would end with
	// the statement, and a blocking one would queue every replica's pass.
	if outboxRelayTryLockSQL != `SELECT pg_try_advisory_lock($1)` {
		t.Fatalf("try-lock statement = %q", outboxRelayTryLockSQL)
	}
	if outboxRelayUnlockSQL != `SELECT pg_advisory_unlock($1)` {
		t.Fatalf("unlock statement = %q", outboxRelayUnlockSQL)
	}
}

// Migration 085: the relay's read breaks a created_at tie on seq (insert
// order), rows without one first, then id. The ordering itself is proved
// against Postgres in outbox_lock_integration_test.go (CI).
func TestOutboxRelayRead_OrdersTiesByInsertSequence(t *testing.T) {
	if !strings.Contains(unpublishedOutboxSQL, "ORDER BY created_at ASC, seq ASC NULLS FIRST, id ASC") {
		t.Fatalf("relay read order changed:\n%s", unpublishedOutboxSQL)
	}
	if !strings.Contains(unpublishedOutboxSQL, "WHERE published = FALSE") {
		t.Fatalf("relay read must take unpublished rows only:\n%s", unpublishedOutboxSQL)
	}
}
