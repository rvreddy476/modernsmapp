package store

import (
	"testing"

	"github.com/atpost/identity-profile-service/internal/mediaclient"
	"github.com/google/uuid"
)

// The claim protocol's per-profile serialisation (LockMediaHolder) must WAIT
// for the other writer and must end with the transaction. A try_ lock would
// let the second writer proceed as if alone; a session lock would outlive a
// crashed request on a pooled connection; a shared lock would not exclude.
func TestMediaHolderLockWaitsAndIsTransactionScoped(t *testing.T) {
	if lockMediaHolderSQL != `SELECT pg_advisory_xact_lock($1)` {
		t.Fatalf("lock statement is %q; want the waiting, exclusive, transaction-scoped advisory lock", lockMediaHolderSQL)
	}
}

func TestMediaHolderLockKeyIsPerProfile(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if first, again := mediaHolderLockKey(a), mediaHolderLockKey(a); first != again {
		t.Fatal("the key for one profile must be stable")
	}
	if mediaHolderLockKey(a) == mediaHolderLockKey(b) {
		t.Fatal("two profiles share a lock key")
	}
}

// The kind picks the column; anything else is refused before it can reach
// the SQL it is spliced into.
func TestProfileMediaColumn(t *testing.T) {
	for kind, want := range map[string]string{
		mediaclient.KindProfileAvatar: "avatar_media_id",
		mediaclient.KindProfileCover:  "cover_media_id",
	} {
		if got, err := profileMediaColumn(kind); err != nil || got != want {
			t.Fatalf("%s -> %q, %v; want %q", kind, got, err, want)
		}
	}
	for _, bad := range []string{"", "channel_avatar", "avatar_media_id; DROP TABLE profile.profiles"} {
		if _, err := profileMediaColumn(bad); err == nil {
			t.Fatalf("kind %q was accepted", bad)
		}
	}
}
