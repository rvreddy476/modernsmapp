//go:build integration

package postgres

import (
	"context"
	"testing"
)

// schemaStateLockKey guards `attribute_schema_state`, which is a SINGLE row
// shared by the whole database.
//
// Two publish tests — one here, one in internal/http — each publish
// and then assert the version went up by exactly one. `go test ./...` runs
// packages in parallel against one dev database, so roughly one run in four
// they interleave and one of them sees +2. Loosening the assertion to "went
// up" would hide the failure that matters, which is a publish that bumps
// nothing. A session advisory lock keeps the strict assertion and simply makes
// the two tests take turns.
//
// The same constant appears in internal/http; Postgres advisory
// locks are keyed by number across the whole database, which is exactly the
// scope needed here, and Go's package boundary is the reason it cannot be one
// declaration.
const schemaStateLockKey = 8250251

func lockSchemaState(t *testing.T) {
	t.Helper()
	ctx := context.Background()
	conn, err := testPool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire for schema-state lock: %v", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock($1)`, schemaStateLockKey); err != nil {
		conn.Release()
		t.Fatalf("take schema-state lock: %v", err)
	}
	t.Cleanup(func() {
		// Released on the same connection that took it — a session-level
		// advisory lock belongs to its connection, so returning this one to
		// the pool while still holding it would leak the lock into whatever
		// query borrows it next.
		if _, err := conn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, schemaStateLockKey); err != nil {
			t.Logf("release schema-state lock: %v", err)
		}
		conn.Release()
	})
}
