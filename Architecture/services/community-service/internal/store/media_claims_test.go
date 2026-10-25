package store

import (
	"testing"

	"github.com/atpost/community-service/internal/mediaclient"
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
	if mediaHolderLockKey(a) != mediaHolderLockKey(a) {
		t.Fatal("the key for one row must be stable")
	}
	if mediaHolderLockKey(a) == mediaHolderLockKey(b) {
		t.Fatal("two rows share a lock key")
	}
}

func TestCommunityMediaColumn(t *testing.T) {
	for kind, want := range map[string]string{
		mediaclient.KindCommunityAvatar: "avatar_media_id",
		mediaclient.KindCommunityBanner: "banner_media_id",
	} {
		if got, err := communityMediaColumn(kind); err != nil || got != want {
			t.Errorf("%s -> %q, %v", kind, got, err)
		}
	}
	if _, err := communityMediaColumn("avatar_media_id; DROP TABLE communities"); err == nil {
		t.Fatal("an unknown kind reached the SQL")
	}
}
