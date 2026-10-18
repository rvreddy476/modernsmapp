//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Unsent messages and messages a member hides for themselves leave that
// member's unread counter exactly once, and never touch a counter that no
// longer includes them (already read, or the sender's own).
func TestUnreadReleasedOnUnsendAndHideForMe(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	sender, reader := uuid.New(), uuid.New()
	convID, _, err := store.CreateDirectConversation(ctx, sender, reader, sender)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM chat.hidden_messages WHERE conversation_id = $1`,
			`DELETE FROM chat.read_cursors WHERE conversation_id = $1`,
			`DELETE FROM chat.conversation_members WHERE conversation_id = $1`,
			`DELETE FROM chat.direct_conversation_keys WHERE conversation_id = $1`,
			`DELETE FROM chat.conversations WHERE id = $1`,
		} {
			_, _ = pool.Exec(ctx, q, convID)
		}
	})
	setUnread := func(u uuid.UUID, n int) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE chat.conversation_members SET unread_count = $3 WHERE conversation_id = $1 AND user_id = $2`, convID, u, n); err != nil {
			t.Fatal(err)
		}
	}
	unread := func(u uuid.UUID) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(ctx, `SELECT unread_count FROM chat.conversation_members WHERE conversation_id = $1 AND user_id = $2`, convID, u).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	sentAt := time.Now().Add(-time.Minute)
	setUnread(reader, 3)
	setUnread(sender, 2)

	// Unsend: the reader's counter drops by one, the sender's is untouched.
	m1 := uuid.New()
	if err := store.ReleaseUnreadForUnsent(ctx, convID, m1, sender, sentAt); err != nil {
		t.Fatal(err)
	}
	if got := unread(reader); got != 2 {
		t.Fatalf("reader after unsend = %d, want 2", got)
	}
	if got := unread(sender); got != 2 {
		t.Fatalf("sender's own counter changed on unsend: %d", got)
	}

	// Hide for me: once only, however often it is retried.
	m2 := uuid.New()
	for i := 0; i < 2; i++ {
		if err := store.HideMessageForMe(ctx, reader, convID, m2, sender, sentAt); err != nil {
			t.Fatal(err)
		}
	}
	if got := unread(reader); got != 1 {
		t.Fatalf("reader after hide-for-me twice = %d, want 1", got)
	}
	// A message the reader already hid is not released again when unsent.
	if err := store.ReleaseUnreadForUnsent(ctx, convID, m2, sender, sentAt); err != nil {
		t.Fatal(err)
	}
	if got := unread(reader); got != 1 {
		t.Fatalf("hidden-then-unsent released twice: %d", got)
	}

	// Already read past it: no release.
	if _, err := pool.Exec(ctx, `INSERT INTO chat.read_cursors (conversation_id, user_id, last_read_at) VALUES ($1, $2, NOW())`, convID, reader); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseUnreadForUnsent(ctx, convID, uuid.New(), sender, sentAt); err != nil {
		t.Fatal(err)
	}
	if got := unread(reader); got != 1 {
		t.Fatalf("a read message was released: %d", got)
	}
}
