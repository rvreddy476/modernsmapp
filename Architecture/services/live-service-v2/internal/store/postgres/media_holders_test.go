package postgres

import (
	"testing"

	"github.com/google/uuid"
)

// The cover protocol's per-stream serialisation (LockMediaHolder) must WAIT
// for the other writer and must end with the transaction. A try_ lock would
// let the second writer proceed as if alone; a session lock would outlive a
// crashed request on a pooled connection; a shared lock would not exclude.
func TestMediaHolderLockWaitsAndIsTransactionScoped(t *testing.T) {
	if lockMediaHolderSQL != `SELECT pg_advisory_xact_lock($1)` {
		t.Fatalf("lock statement is %q; want the waiting, exclusive, transaction-scoped advisory lock", lockMediaHolderSQL)
	}
}

func TestMediaHolderLockKeyIsPerStream(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if first, again := mediaHolderLockKey(a), mediaHolderLockKey(a); first != again {
		t.Fatal("the key for one stream must be stable")
	}
	if mediaHolderLockKey(a) == mediaHolderLockKey(b) {
		t.Fatal("two streams share a lock key")
	}
}

func TestOptionalIDLetsTheDatabaseChooseForNil(t *testing.T) {
	if optionalID(uuid.Nil) != nil {
		t.Fatal("uuid.Nil must be NULL so the column default applies")
	}
	id := uuid.New()
	if got := optionalID(id); got == nil || *got != id {
		t.Fatalf("optionalID(%s) = %v", id, got)
	}
}
