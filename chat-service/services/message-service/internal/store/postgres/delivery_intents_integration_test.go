//go:build integration

package postgres_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestDeliveryIntentIsStableAndOutboxIsExactlyOnce(t *testing.T) {
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

	conversationID := uuid.New()
	senderID := uuid.New()
	receiverID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by) VALUES ($1, 'direct', $2)`, conversationID, senderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id, user_id, role)
		VALUES ($1, $2, 'member'), ($1, $3, 'member')
	`, conversationID, senderID, receiverID); err != nil {
		t.Fatal(err)
	}

	conversationStore := store.New(pool)
	createdAt := time.Now().UTC().Truncate(time.Microsecond)
	first, err := conversationStore.ReserveMessageDeliveryIntent(ctx, store.MessageDeliveryIntent{
		IdempotencyKey: "send-1", RequestHash: "hash-1",
		ConversationID: conversationID, SenderID: senderID,
		MessageID: uuid.New(), Bucket: createdAt.Format("200601"), MessageTS: createdAt,
		MessageType: "text", MessageText: "namaste", MemberIDs: []uuid.UUID{senderID, receiverID},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := conversationStore.ReserveMessageDeliveryIntent(ctx, store.MessageDeliveryIntent{
		IdempotencyKey: "send-1", RequestHash: "hash-1",
		ConversationID: conversationID, SenderID: senderID,
		MessageID: uuid.New(), Bucket: "209901", MessageTS: createdAt.Add(time.Hour),
		MessageType: "text", MessageText: "different generated values", MemberIDs: []uuid.UUID{senderID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.MessageID != second.MessageID || !first.MessageTS.Equal(second.MessageTS) || first.Bucket != second.Bucket {
		t.Fatalf("retry changed Scylla identity: first=%+v second=%+v", first, second)
	}

	_, err = conversationStore.ReserveMessageDeliveryIntent(ctx, store.MessageDeliveryIntent{
		IdempotencyKey: "send-1", RequestHash: "different-hash",
		ConversationID: conversationID, SenderID: senderID, MessageID: uuid.New(),
		Bucket: createdAt.Format("200601"), MessageTS: createdAt, MessageType: "text", MemberIDs: []uuid.UUID{senderID},
	})
	if !errors.Is(err, store.ErrDeliveryIntentConflict) {
		t.Fatalf("expected delivery intent conflict, got %v", err)
	}

	payload := map[string]string{"message_id": first.MessageID.String()}
	for index := 0; index < 2; index++ {
		if err := conversationStore.InsertOutboxEventOnce(ctx, "message-created:"+first.MessageID.String(), "MessageCreated", payload); err != nil {
			t.Fatal(err)
		}
	}
	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM chat.outbox_events WHERE dedupe_key = $1`, "message-created:"+first.MessageID.String()).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("expected one outbox row, got %d", outboxCount)
	}
	mediaID := uuid.New()
	if err := conversationStore.InsertMessageMediaReference(ctx, first.MessageID, mediaID, conversationID); err != nil {
		t.Fatal(err)
	}
	allowed, err := conversationStore.ViewerMayAccessChatMedia(ctx, receiverID, mediaID)
	if err != nil || !allowed {
		t.Fatalf("active recipient denied canonical chat media: allowed=%v err=%v", allowed, err)
	}
	allowed, err = conversationStore.ViewerMayAccessChatMedia(ctx, uuid.New(), mediaID)
	if err != nil || allowed {
		t.Fatalf("outsider received canonical chat media: allowed=%v err=%v", allowed, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE chat.conversation_members SET left_at=NOW() WHERE conversation_id=$1 AND user_id=$2`, conversationID, receiverID); err != nil {
		t.Fatal(err)
	}
	allowed, err = conversationStore.ViewerMayAccessChatMedia(ctx, receiverID, mediaID)
	if err != nil || allowed {
		t.Fatalf("removed recipient retained media access: allowed=%v err=%v", allowed, err)
	}

	pending, err := conversationStore.FetchPendingMessageDeliveryIntents(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending before completion = %d, err=%v", len(pending), err)
	}
	if err := conversationStore.CompleteMessageDeliveryIntent(ctx, first.IdempotencyKey); err != nil {
		t.Fatal(err)
	}
	pending, err = conversationStore.FetchPendingMessageDeliveryIntents(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("pending after completion = %d, err=%v", len(pending), err)
	}
}
