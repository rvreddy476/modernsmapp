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

	keyspace, err := integrationKeyspace(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	// The real schema, not a copy: scylla/schema.cql rewritten for the test
	// keyspace, then the same boot-time convergence the service runs.
	statements, err := loadRealSchema(keyspace)
	if err != nil {
		t.Fatal(err)
	}

	system := connectWithRetry(t, host, "system", 2*time.Minute)
	for _, statement := range statements {
		if err := system.Query(statement).Exec(); err != nil {
			system.Close()
			t.Fatalf("apply schema.cql statement %q: %v", statement, err)
		}
	}
	system.Close()

	session := connectWithRetry(t, host, keyspace, 30*time.Second)
	defer session.Close()
	store := New(session)
	ctx := context.Background()
	if _, err := store.EnsureMessageColumns(ctx, keyspace); err != nil {
		t.Fatalf("converge %s.messages: %v", keyspace, err)
	}
	for _, table := range []string{"messages", "conversations_by_user", "message_reactions"} {
		if err := session.Query("TRUNCATE " + table).Exec(); err != nil {
			t.Fatalf("truncate %s.%s: %v", keyspace, table, err)
		}
	}

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
