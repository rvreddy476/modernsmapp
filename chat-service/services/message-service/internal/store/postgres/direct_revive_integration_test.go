//go:build integration

package postgres

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// A pair whose direct conversation a block severed must be able to talk
// again once the block is gone: CreateDirectConversation re-admits both
// members (reported as created so the request downgrade applies), hides the
// history from before the sever, and reopens a finished request row.
func TestCreateDirectConversationRevivesSeveredPair(t *testing.T) {
	store, pool := testStore(t)
	ctx := context.Background()
	a, b := uuid.New(), uuid.New()

	convID, created, err := store.CreateDirectConversation(ctx, a, b, a)
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM chat.message_requests WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversation_members WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.direct_conversation_keys WHERE conversation_id = $1`, convID)
		_, _ = pool.Exec(ctx, `DELETE FROM chat.conversations WHERE id = $1`, convID)
	})
	if err := store.CreateMessageRequest(ctx, convID, a, b); err != nil {
		t.Fatal(err)
	}

	// Still active: the ordinary existing-conversation path.
	again, created, err := store.CreateDirectConversation(ctx, b, a, b)
	if err != nil || again != convID || created {
		t.Fatalf("existing active: conv=%v created=%v err=%v", again, created, err)
	}

	if _, _, err := store.SeverDirectConversation(ctx, a, b); err != nil {
		t.Fatal(err)
	}
	var severedGen int64
	if err := pool.QueryRow(ctx, `SELECT max(join_gen) FROM chat.conversation_members WHERE conversation_id = $1`, convID).Scan(&severedGen); err != nil {
		t.Fatal(err)
	}

	revived, created, err := store.CreateDirectConversation(ctx, b, a, b)
	if err != nil || revived != convID || !created {
		t.Fatalf("revive: conv=%v created=%v err=%v", revived, created, err)
	}
	var active, cleared int
	var minGen int64
	if err := pool.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE left_at IS NULL),
		       count(*) FILTER (WHERE cleared_before IS NOT NULL),
		       min(join_gen)
		FROM chat.conversation_members WHERE conversation_id = $1`, convID).Scan(&active, &cleared, &minGen); err != nil {
		t.Fatal(err)
	}
	if active != 2 || cleared != 2 {
		t.Fatalf("after revive: active=%d cleared=%d, want 2 and 2", active, cleared)
	}
	if minGen <= severedGen {
		t.Fatalf("re-admitted members must get a fresh join generation: min=%d severed=%d", minGen, severedGen)
	}

	// The blocked request row is reopened for the new sender.
	if _, err := pool.Exec(ctx, `UPDATE chat.message_requests SET status = 'blocked', responded_at = NOW() WHERE conversation_id = $1`, convID); err != nil {
		t.Fatal(err)
	}
	if err := store.CreateMessageRequest(ctx, convID, b, a); err != nil {
		t.Fatal(err)
	}
	var status string
	var sender uuid.UUID
	if err := pool.QueryRow(ctx, `SELECT status, sender_id FROM chat.message_requests WHERE conversation_id = $1`, convID).Scan(&status, &sender); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || sender != b {
		t.Fatalf("request after revive: status=%s sender=%v, want pending from b", status, sender)
	}
	// A pending request retried keeps its sender.
	if err := store.CreateMessageRequest(ctx, convID, a, b); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT sender_id FROM chat.message_requests WHERE conversation_id = $1`, convID).Scan(&sender); err != nil {
		t.Fatal(err)
	}
	if sender != b {
		t.Fatalf("pending request was overwritten by a retry: sender=%v", sender)
	}
}
