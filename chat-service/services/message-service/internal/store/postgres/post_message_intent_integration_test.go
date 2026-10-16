//go:build integration

package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TikTok parity T-25: the durable delivery intent carries a post card's
// post_id through reserve, read-back and repair fetch, and a text intent
// keeps it NULL. Also proves setup.sql converges an intents table created
// before the column existed (ADD COLUMN IF NOT EXISTS on a second boot).
func TestDeliveryIntentRoundTripsPostID(t *testing.T) {
	dsn := os.Getenv("CHAT_POSTGRES_DSN")
	if dsn == "" {
		t.Fatal("CHAT_POSTGRES_DSN is required")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `DROP SCHEMA IF EXISTS chat CASCADE`); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// An older database: the column is missing until the next boot adds it.
	if _, err := pool.Exec(ctx, `ALTER TABLE chat.message_delivery_intents DROP COLUMN post_id`); err != nil {
		t.Fatal(err)
	}
	if err := store.BootstrapSchema(ctx, pool, database.SetupSQL); err != nil {
		t.Fatalf("re-bootstrap must converge the post_id column: %v", err)
	}

	conversationID, senderID, receiverID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by) VALUES ($1, 'direct', $2)`, conversationID, senderID); err != nil {
		t.Fatal(err)
	}
	s := store.New(pool)
	now := time.Now().UTC().Truncate(time.Microsecond)
	postID := uuid.New()

	reserved, err := s.ReserveMessageDeliveryIntent(ctx, store.MessageDeliveryIntent{
		IdempotencyKey: "post-1", RequestHash: "h-post",
		ConversationID: conversationID, SenderID: senderID,
		MessageID: uuid.New(), Bucket: now.Format("200601"), MessageTS: now,
		MessageType: "post", MessageText: "", PostID: &postID,
		MemberIDs: []uuid.UUID{senderID, receiverID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reserved.PostID == nil || *reserved.PostID != postID || reserved.MessageType != "post" {
		t.Fatalf("reserved intent lost the post: %+v", reserved)
	}
	if _, err := s.ReserveMessageDeliveryIntent(ctx, store.MessageDeliveryIntent{
		IdempotencyKey: "text-1", RequestHash: "h-text",
		ConversationID: conversationID, SenderID: senderID,
		MessageID: uuid.New(), Bucket: now.Format("200601"), MessageTS: now.Add(time.Millisecond),
		MessageType: "text", MessageText: "hi", MemberIDs: []uuid.UUID{senderID, receiverID},
	}); err != nil {
		t.Fatal(err)
	}

	pending, err := s.FetchPendingMessageDeliveryIntents(ctx, 10)
	if err != nil || len(pending) != 2 {
		t.Fatalf("pending = %d, err = %v", len(pending), err)
	}
	for _, p := range pending {
		switch p.IdempotencyKey {
		case "post-1":
			if p.PostID == nil || *p.PostID != postID {
				t.Fatalf("repair fetch lost the post id: %+v", p)
			}
		case "text-1":
			if p.PostID != nil {
				t.Fatalf("a text intent must carry no post id: %+v", p)
			}
		}
	}
}
