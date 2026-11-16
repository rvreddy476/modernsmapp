package postgres

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// feed-30: the parts of the reaction toggle that need no database. The lock
// and the statements are proved against Postgres in
// reactions_toggle_integration_test.go (CI); this pins what is sent and what
// a toggle reports when its write touched no row.

func TestSettleReactionToggle_NeitherAddsNorRemoves(t *testing.T) {
	// Another writer put the row there between the read and the insert.
	got := settleReactionToggle("like")
	if !got.IsSet || got.NewType != "like" || got.PrevType != "like" {
		t.Fatalf("settled on a stored like = %+v; want set, with the same type before and after", got)
	}
	// Another writer removed it between the read and the delete.
	got = settleReactionToggle("")
	if got.IsSet || got.NewType != "" || got.PrevType != "" {
		t.Fatalf("settled on no reaction = %+v; want unset, with nothing before", got)
	}
}

// The key is one viewer's reaction on one target: a different viewer, post
// or target type is a different lock, so unrelated taps never queue.
func TestReactionLockKey_IsPerTargetAndViewer(t *testing.T) {
	post, user := uuid.New(), uuid.New()
	base := reactionLockKey("post", post, user)
	if base != reactionLockKey("post", post, user) {
		t.Fatal("the same viewer and target gave two keys")
	}
	for name, other := range map[string]string{
		"another viewer": reactionLockKey("post", post, uuid.New()),
		"another post":   reactionLockKey("post", uuid.New(), user),
		"another type":   reactionLockKey("comment", post, user),
		"swapped ids":    reactionLockKey("post", user, post),
	} {
		if other == base {
			t.Fatalf("%s shares the lock key", name)
		}
	}
}

func TestReactionToggleStatements(t *testing.T) {
	norm := func(s string) string { return strings.Join(strings.Fields(s), " ") }
	// Transaction-scoped and blocking: the second tap waits for the first
	// to commit, then reads what it wrote. A try-lock would drop the tap; a
	// session lock would outlive the transaction on a pooled connection.
	if reactionLockSQL != `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))` {
		t.Fatalf("lock statement = %q", reactionLockSQL)
	}
	if got := norm(reactionInsertSQL); !strings.HasSuffix(got, "ON CONFLICT (target_type, target_id, user_id) DO NOTHING") {
		t.Fatalf("the insert must not fail on a row written since the read: %q", got)
	}
	for name, stmt := range map[string]string{"read": reactionReadSQL, "delete": reactionDeleteSQL, "update": reactionUpdateSQL} {
		if got := norm(stmt); !strings.Contains(got, "WHERE target_type = $1 AND target_id = $2 AND user_id = $3") {
			t.Fatalf("%s is not scoped to one viewer's reaction on one target: %q", name, got)
		}
	}
}
