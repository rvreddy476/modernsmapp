//go:build integration

package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Delivered ticks (migration 017): the delivered cursor is monotonic and
// reports whether it moved; the batch reads return it per member; the read
// cursor reader returns nil for a member who never read; the batch policy
// read returns only projected users.
func TestDeliveredCursorsMonotonicAndBatched(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	sender, reader := uuid.New(), uuid.New()
	convID, _, err := store.CreateDirectConversation(ctx, sender, reader, sender)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for _, q := range []string{
			`DELETE FROM chat.delivered_cursors WHERE conversation_id = $1`,
			`DELETE FROM chat.read_cursors WHERE conversation_id = $1`,
			`DELETE FROM chat.conversation_members WHERE conversation_id = $1`,
			`DELETE FROM chat.direct_conversation_keys WHERE conversation_id = $1`,
			`DELETE FROM chat.conversations WHERE id = $1`,
		} {
			_, _ = pool.Exec(ctx, q, convID)
		}
		_, _ = pool.Exec(ctx, `DELETE FROM chat.user_policy WHERE user_id = $1`, reader)
	})

	base := time.Now().UTC().Truncate(time.Millisecond)
	msg := uuid.New()
	moved, err := store.UpsertDeliveredCursor(ctx, convID, reader, &msg, base)
	if err != nil || !moved {
		t.Fatalf("first ack: moved=%v err=%v", moved, err)
	}
	// Replayed and older acks never move it back.
	for _, ts := range []time.Time{base, base.Add(-time.Minute)} {
		moved, err = store.UpsertDeliveredCursor(ctx, convID, reader, nil, ts)
		if err != nil || moved {
			t.Fatalf("stale ack %v: moved=%v err=%v", ts, moved, err)
		}
	}
	moved, err = store.UpsertDeliveredCursor(ctx, convID, reader, nil, base.Add(time.Second))
	if err != nil || !moved {
		t.Fatalf("newer ack: moved=%v err=%v", moved, err)
	}

	all, err := store.GetConversationDeliveredCursors(ctx, convID)
	if err != nil || !all[reader].LastDeliveredAt.Equal(base.Add(time.Second)) {
		t.Fatalf("conversation cursors: %+v %v", all, err)
	}
	others, err := store.GetOtherMembersDeliveredCursors(ctx, sender, []uuid.UUID{convID})
	if err != nil || !others[convID][reader].LastDeliveredAt.Equal(base.Add(time.Second)) {
		t.Fatalf("other members: %+v %v", others, err)
	}
	mine, err := store.GetOtherMembersDeliveredCursors(ctx, reader, []uuid.UUID{convID})
	if err != nil || len(mine[convID]) != 0 {
		t.Fatalf("the viewer's own cursor must be excluded: %+v %v", mine, err)
	}

	if rc, err := store.GetReadCursor(ctx, convID, reader); err != nil || rc != nil {
		t.Fatalf("no read cursor yet: %+v %v", rc, err)
	}
	if err := store.UpsertReadCursor(ctx, convID, reader, msg, base); err != nil {
		t.Fatal(err)
	}
	if rc, err := store.GetReadCursor(ctx, convID, reader); err != nil || rc == nil || !rc.LastReadAt.Equal(base) {
		t.Fatalf("read cursor: %+v %v", rc, err)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO chat.user_policy (user_id, read_receipts_visibility, refreshed_at) VALUES ($1, 'no_one', now())`, reader); err != nil {
		t.Fatal(err)
	}
	pols, err := store.GetUserPolicies(ctx, []uuid.UUID{reader, sender})
	if err != nil || len(pols) != 1 || pols[reader].ReadReceiptsVisibility != "no_one" {
		t.Fatalf("policies: %+v %v", pols, err)
	}
}
