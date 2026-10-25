package postgres

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

// One key per (kind, row): stable, distinct across rows, and distinct across
// kinds, because a reel draft publishes as a post with the same id.
func TestMediaHolderLockKeyIsPerKindAndRow(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if mediaHolderLockKey(HolderPostCover, a) != mediaHolderLockKey(HolderPostCover, a) {
		t.Fatal("the key for one row must be stable")
	}
	if mediaHolderLockKey(HolderPostCover, a) == mediaHolderLockKey(HolderPostCover, b) {
		t.Fatal("two rows share a lock key")
	}
	if mediaHolderLockKey(HolderPostCover, a) == mediaHolderLockKey(HolderReelDraft, a) {
		t.Fatal("a draft and the post it published as share a lock key")
	}
}

// A published or deleted reel draft holds nothing; every other state does.
func TestReelDraftLivenessRule(t *testing.T) {
	if reelDraftLiveSQL != `status NOT IN ('deleted', 'published')` {
		t.Fatalf("reel draft liveness is %q", reelDraftLiveSQL)
	}
}

func TestHoldingIDs(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	nilID := uuid.Nil
	if got := holdingIDs(&a, &a); len(got) != 1 {
		t.Fatalf("duplicate kept: %v", got)
	}
	if got := holdingIDs(nil, &nilID); len(got) != 0 {
		t.Fatalf("nothing held: %v", got)
	}
	if got := holdingIDs(&a, &b); len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("order: %v", got)
	}
}
