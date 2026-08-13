//go:build integration

package scylla

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// TestMessageAndInboxReplayIsIdempotent proves the load-bearing Scylla part
// of message-delivery repair: a retry with the reserved message identity
// updates the same canonical message and inbox rows instead of duplicating
// either projection.
func TestMessageAndInboxReplayIsIdempotent(t *testing.T) {
	host := os.Getenv("SCYLLA_HOST")
	if host == "" {
		t.Fatal("SCYLLA_HOST is required")
	}

	system := connectWithRetry(t, host, "system", 2*time.Minute)
	if err := system.Query(`CREATE KEYSPACE IF NOT EXISTS chatservice_m5integration WITH replication = {'class': 'SimpleStrategy', 'replication_factor': 1}`).Exec(); err != nil {
		system.Close()
		t.Fatalf("create integration keyspace: %v", err)
	}
	system.Close()

	session := connectWithRetry(t, host, "chatservice_m5integration", 30*time.Second)
	defer session.Close()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS messages (
			conversation_id UUID, bucket TEXT, ts TIMESTAMP, msg_id UUID,
			sender_id UUID, type TEXT, text TEXT, media_id UUID,
			is_deleted BOOLEAN, created_at TIMESTAMP,
			PRIMARY KEY ((conversation_id, bucket), ts, msg_id)
		) WITH CLUSTERING ORDER BY (ts DESC, msg_id DESC)`,
		`CREATE TABLE IF NOT EXISTS conversations_by_user (
			user_id UUID, bucket TEXT, last_ts TIMESTAMP, conversation_id UUID,
			last_sender_id UUID, last_text TEXT,
			PRIMARY KEY ((user_id, bucket), last_ts, conversation_id)
		) WITH CLUSTERING ORDER BY (last_ts DESC, conversation_id DESC)`,
		`TRUNCATE messages`,
		`TRUNCATE conversations_by_user`,
	} {
		if err := session.Query(statement).Exec(); err != nil {
			t.Fatalf("apply integration schema %q: %v", statement, err)
		}
	}

	store := New(session)
	ctx := context.Background()
	conversationID, messageID, senderID, recipientID := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	messageTS := time.Now().UTC().Truncate(time.Millisecond)
	message := &Message{
		ConversationID: conversationID,
		Bucket:         messageTS.Format("200601"),
		Ts:             messageTS,
		MsgID:          messageID,
		SenderID:       senderID,
		Type:           "text",
		Text:           "durable namaste",
		CreatedAt:      messageTS,
	}

	for attempt := 0; attempt < 2; attempt++ {
		if err := store.CreateMessage(ctx, message); err != nil {
			t.Fatalf("message replay %d: %v", attempt+1, err)
		}
		if err := store.UpsertInbox(ctx, recipientID, conversationID, senderID, message.Text, messageTS); err != nil {
			t.Fatalf("inbox replay %d: %v", attempt+1, err)
		}
	}

	var messageCount int
	if err := session.Query(`SELECT count(*) FROM messages WHERE conversation_id = ? AND bucket = ?`, uuidToGocql(conversationID), message.Bucket).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 1 {
		t.Fatalf("replaying one reserved message produced %d canonical rows", messageCount)
	}

	var inboxCount int
	if err := session.Query(`SELECT count(*) FROM conversations_by_user WHERE user_id = ? AND bucket = ?`, uuidToGocql(recipientID), message.Bucket).Scan(&inboxCount); err != nil {
		t.Fatalf("count inbox rows: %v", err)
	}
	if inboxCount != 1 {
		t.Fatalf("replaying one reserved message produced %d inbox rows", inboxCount)
	}

	got, err := store.GetMessage(ctx, conversationID, message.Bucket, messageTS, messageID)
	if err != nil {
		t.Fatalf("read replayed message: %v", err)
	}
	if got == nil || got.MsgID != messageID || got.Text != message.Text {
		t.Fatalf("canonical message changed during replay: got=%+v", got)
	}
}

func connectWithRetry(t *testing.T, host, keyspace string, timeout time.Duration) *gocql.Session {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		cluster := gocql.NewCluster(host)
		cluster.Keyspace = keyspace
		cluster.Consistency = gocql.One
		cluster.Timeout = 5 * time.Second
		cluster.ConnectTimeout = 5 * time.Second
		session, err := cluster.CreateSession()
		if err == nil {
			return session
		}
		lastErr = err
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("connect to Scylla keyspace %s at %s: %v", keyspace, host, lastErr)
	return nil
}
