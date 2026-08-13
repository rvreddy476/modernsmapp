//go:build integration

package service

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/database"
	store "github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

type deliveryMessageStore struct {
	mu            sync.Mutex
	messageIDs    []uuid.UUID
	failMessage   bool
	failInboxOnce bool
	inboxWrites   map[uuid.UUID]int
}

func (s *deliveryMessageStore) CreateMessage(_ context.Context, message *scylla.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messageIDs = append(s.messageIDs, message.MsgID)
	if s.failMessage {
		return errors.New("injected message-store failure")
	}
	return nil
}

func (s *deliveryMessageStore) UpsertInbox(_ context.Context, userID, _, _ uuid.UUID, _ string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failInboxOnce {
		s.failInboxOnce = false
		return errors.New("injected inbox failure")
	}
	s.inboxWrites[userID]++
	return nil
}

func TestScheduledMessageIsNotMarkedSentWhenDeliveryFails(t *testing.T) {
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
		t.Fatal(err)
	}

	conversationID, senderID, receiverID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by) VALUES ($1, 'direct', $2)`, conversationID, senderID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.conversation_members (conversation_id, user_id, role)
		VALUES ($1, $2, 'member'), ($1, $3, 'member')
	`, conversationID, senderID, receiverID); err != nil {
		t.Fatal(err)
	}
	scheduledID := uuid.New()
	if _, err := pool.Exec(ctx, `
		INSERT INTO chat.scheduled_messages (id, conversation_id, sender_id, type, content, send_at, status)
		VALUES ($1, $2, $3, 'text', 'deliver later', NOW() - INTERVAL '1 minute', 'pending')
	`, scheduledID, conversationID, senderID); err != nil {
		t.Fatal(err)
	}

	conversationStore := store.New(pool)
	messageStore := &deliveryMessageStore{failMessage: true, inboxWrites: make(map[uuid.UUID]int)}
	redisAddress := os.Getenv("REDIS_ADDR")
	if redisAddress == "" {
		redisAddress = "127.0.0.1:6379"
	}
	rdb := redis.NewClient(&redis.Options{Addr: redisAddress})
	defer rdb.Close()
	service := New(conversationStore, messageStore, rdb, nil, slog.Default(), time.Second)
	service.processScheduledMessages(ctx)

	var status, lastError string
	var attemptCount int
	var nextAttempt time.Time
	if err := pool.QueryRow(ctx, `
		SELECT status, attempt_count, last_error, next_attempt_at
		FROM chat.scheduled_messages WHERE id=$1
	`, scheduledID).Scan(&status, &attemptCount, &lastError, &nextAttempt); err != nil {
		t.Fatal(err)
	}
	if status != "pending" || attemptCount != 1 || lastError == "" || !nextAttempt.After(time.Now().Add(-time.Second)) {
		t.Fatalf("failed scheduled delivery not queued for retry: status=%q attempts=%d last_error=%q next=%v", status, attemptCount, lastError, nextAttempt)
	}
	for attempt := 1; attempt < scheduledMessageMaxAttempts; attempt++ {
		if err := conversationStore.RecordScheduledMessageFailure(ctx, scheduledID, "still unavailable", scheduledMessageMaxAttempts); err != nil {
			t.Fatal(err)
		}
	}
	var failedAt *time.Time
	if err := pool.QueryRow(ctx, `SELECT status, attempt_count, failed_at FROM chat.scheduled_messages WHERE id=$1`, scheduledID).Scan(&status, &attemptCount, &failedAt); err != nil {
		t.Fatal(err)
	}
	if status != "failed" || attemptCount != scheduledMessageMaxAttempts || failedAt == nil {
		t.Fatalf("retry budget not terminal/queryable: status=%q attempts=%d failed_at=%v", status, attemptCount, failedAt)
	}
	listed, err := conversationStore.ListScheduledMessages(ctx, conversationID, senderID)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 1 || listed[0].Status != "failed" || listed[0].LastError == nil {
		t.Fatalf("terminal scheduled failure not visible to sender: %+v", listed)
	}
}

func (*deliveryMessageStore) GetMessage(context.Context, uuid.UUID, string, time.Time, uuid.UUID) (*scylla.Message, error) {
	return nil, nil
}
func (*deliveryMessageStore) GetMessages(context.Context, uuid.UUID, *scylla.MessageCursor, int) ([]scylla.Message, *scylla.MessageCursor, error) {
	return nil, nil, nil
}
func (*deliveryMessageStore) SoftDeleteMessage(context.Context, uuid.UUID, string, time.Time, uuid.UUID) error {
	return nil
}
func (*deliveryMessageStore) AddReaction(context.Context, uuid.UUID, string, time.Time, uuid.UUID, string, uuid.UUID) error {
	return nil
}
func (*deliveryMessageStore) RemoveReaction(context.Context, uuid.UUID, string, time.Time, uuid.UUID, string, uuid.UUID) error {
	return nil
}
func (*deliveryMessageStore) HasReaction(context.Context, uuid.UUID, string, time.Time, uuid.UUID, string, uuid.UUID) (bool, error) {
	return false, nil
}
func (*deliveryMessageStore) GetReactionsForMessages(context.Context, uuid.UUID, string, []scylla.MsgKey) (map[uuid.UUID][]scylla.Reaction, error) {
	return nil, nil
}

func TestDeliveryRetryRepairsWithoutChangingMessageIdentity(t *testing.T) {
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
		t.Fatal(err)
	}

	conversationID, senderID, receiverID := uuid.New(), uuid.New(), uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO chat.conversations (id, type, created_by) VALUES ($1, 'direct', $2)`, conversationID, senderID); err != nil {
		t.Fatal(err)
	}
	conversationStore := store.New(pool)
	createdAt := time.Now().UTC()
	intent, err := conversationStore.ReserveMessageDeliveryIntent(ctx, store.MessageDeliveryIntent{
		IdempotencyKey: "repair-1", RequestHash: "hash-1",
		ConversationID: conversationID, SenderID: senderID, MessageID: uuid.New(),
		Bucket: createdAt.Format("200601"), MessageTS: createdAt,
		MessageType: "text", MessageText: "durable hello", MemberIDs: []uuid.UUID{senderID, receiverID},
		SourceApp: "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	messageStore := &deliveryMessageStore{failInboxOnce: true, inboxWrites: make(map[uuid.UUID]int)}
	service := &Service{convStore: conversationStore, msgStore: messageStore, log: slog.Default()}

	if err := service.completeMessageDelivery(ctx, intent); err == nil {
		t.Fatal("injected inbox failure was swallowed")
	}
	pending, err := conversationStore.FetchPendingMessageDeliveryIntents(ctx, 10)
	if err != nil || len(pending) != 1 {
		t.Fatalf("failed delivery was not left pending: count=%d err=%v", len(pending), err)
	}
	if err := service.completeMessageDelivery(ctx, intent); err != nil {
		t.Fatalf("repair failed: %v", err)
	}
	pending, err = conversationStore.FetchPendingMessageDeliveryIntents(ctx, 10)
	if err != nil || len(pending) != 0 {
		t.Fatalf("repaired delivery remained pending: count=%d err=%v", len(pending), err)
	}
	if len(messageStore.messageIDs) != 2 || messageStore.messageIDs[0] != messageStore.messageIDs[1] {
		t.Fatalf("repair changed message identity: %v", messageStore.messageIDs)
	}
	for _, memberID := range []uuid.UUID{senderID, receiverID} {
		if messageStore.inboxWrites[memberID] != 1 {
			t.Fatalf("member %s inbox writes = %d", memberID, messageStore.inboxWrites[memberID])
		}
	}
	var outboxCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM chat.outbox_events WHERE dedupe_key = $1`, "message-created:"+intent.MessageID.String()).Scan(&outboxCount); err != nil {
		t.Fatal(err)
	}
	if outboxCount != 1 {
		t.Fatalf("expected one durable outbox event, got %d", outboxCount)
	}
}
