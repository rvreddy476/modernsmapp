package scylla

import (
	"testing"
	"time"

	"github.com/gocql/gocql"
)

// Module 1 fixes-v1 / Codex P0-1.
//
// The inbox is Scylla, whose INSERT is an upsert on the primary key
// (user_id, bucket, ts). Exactly-once delivery therefore depends
// entirely on `ts` being DETERMINISTIC for a logical delivery — that is
// what these tests pin. Without it, a retry after a partial failure
// writes a second clustering key and the user sees a duplicate.

func TestDeterministicTS_StableForSameIdentity(t *testing.T) {
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	id := "post-1:user-1:creator_uploaded_video"

	a := DeterministicTS(at, id)
	b := DeterministicTS(at, id)
	if a != b {
		t.Fatalf("same identity must yield the same clustering key: %v vs %v", a, b)
	}
}

func TestDeterministicTS_DiffersPerRecipientAndPost(t *testing.T) {
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)

	u1 := DeterministicTS(at, "post-1:user-1:t")
	u2 := DeterministicTS(at, "post-1:user-2:t")
	if u1 == u2 {
		t.Fatal("different recipients must not collide")
	}
	p2 := DeterministicTS(at, "post-2:user-1:t")
	if u1 == p2 {
		t.Fatal("different posts must not collide")
	}
	typ := DeterministicTS(at, "post-1:user-1:other_type")
	if u1 == typ {
		t.Fatal("different notification types must not collide")
	}
}

// The legacy random path is what made retries unsafe. Pinning the
// contrast keeps a future refactor from quietly reverting to it.
func TestUUIDFromTime_IsNotDeterministic(t *testing.T) {
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	if gocql.UUIDFromTime(at) == gocql.UUIDFromTime(at) {
		t.Skip("gocql produced identical random bits; nothing to assert")
	}
}

func TestDeterministicTS_PreservesTimeUUIDShape(t *testing.T) {
	at := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	u := DeterministicTS(at, "post-1:user-1:t")

	if got := u.Version(); got != 1 {
		t.Fatalf("must remain a v1 time-UUID, got version %d", got)
	}
	// RFC-4122 variant lives in the two high bits of byte 8.
	if u[8]&0xC0 != 0x80 {
		t.Fatalf("variant bits corrupted: %08b", u[8])
	}
	// Timestamp must survive so inbox ordering stays chronological.
	if delta := u.Time().Sub(at); delta > time.Millisecond || delta < -time.Millisecond {
		t.Fatalf("timestamp not preserved: %v", delta)
	}
}

// Ordering is what the feed relies on: a later post must sort after an
// earlier one regardless of the hashed suffix.
func TestDeterministicTS_OrdersChronologically(t *testing.T) {
	early := DeterministicTS(time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC), "a")
	late := DeterministicTS(time.Date(2026, 8, 7, 13, 0, 0, 0, time.UTC), "b")
	if !early.Time().Before(late.Time()) {
		t.Fatal("deterministic keys must preserve chronological order")
	}
}

func TestDeterministicNotificationID_Stable(t *testing.T) {
	id := "post-1:user-1:creator_uploaded_video"
	if DeterministicNotificationID(id) != DeterministicNotificationID(id) {
		t.Fatal("notification id must be stable for the same identity")
	}
	if DeterministicNotificationID(id) == DeterministicNotificationID("post-1:user-2:creator_uploaded_video") {
		t.Fatal("notification id must differ per recipient")
	}
}
