package store

import (
	"testing"

	"github.com/google/uuid"
)

// The claim protocol's per-row serialisation (LockMediaHolder) must WAIT for
// the other writer and must end with the transaction. A try_ lock would let
// the second writer proceed as if alone; a session lock would outlive a
// crashed request on a pooled connection; a shared lock would not exclude.
func TestMediaHolderLockWaitsAndIsTransactionScoped(t *testing.T) {
	if lockMediaHolderSQL != `SELECT pg_advisory_xact_lock($1)` {
		t.Fatalf("lock statement is %q; want the waiting, exclusive, transaction-scoped advisory lock", lockMediaHolderSQL)
	}
}

func TestMediaHolderLockKeyIsPerRow(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if first, again := mediaHolderLockKey(a), mediaHolderLockKey(a); first != again {
		t.Fatal("the key for one row must be stable")
	}
	if mediaHolderLockKey(a) == mediaHolderLockKey(b) {
		t.Fatal("two rows share a lock key")
	}
}
