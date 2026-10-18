//go:build integration

package scylla

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Unsent (soft-deleted) messages must not use up a history page. Before the
// fix the query LIMIT counted them, so the newest run of unsent messages hid
// every older live message in the same month and the page came back empty.
func TestHistoryPageSkipsUnsentMessagesWithoutLosingOlderOnes(t *testing.T) {
	host := os.Getenv("SCYLLA_HOST")
	if host == "" {
		t.Fatal("SCYLLA_HOST is required")
	}
	keyspace, err := integrationKeyspace(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
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

	conv, sender := uuid.New(), uuid.New()
	bucket := CurrentBucket()
	base := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)
	put := func(i int, text string) *Message {
		t.Helper()
		ts := base.Add(time.Duration(i) * time.Second)
		m := &Message{ConversationID: conv, Bucket: bucket, Ts: ts, MsgID: uuid.New(), SenderID: sender,
			Type: "text", Text: text, CreatedAt: ts}
		if err := store.CreateMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	// Oldest first: three live messages, then six that get unsent.
	var live []*Message
	for i := 0; i < 3; i++ {
		live = append(live, put(i, "live"))
	}
	for i := 3; i < 9; i++ {
		m := put(i, "unsent")
		if err := store.SoftDeleteMessage(ctx, conv, bucket, m.Ts, m.MsgID); err != nil {
			t.Fatal(err)
		}
	}

	page, next, err := store.GetMessages(ctx, conv, nil, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 3 {
		t.Fatalf("first page = %d messages, want the 3 live ones behind the unsent run", len(page))
	}
	for i, m := range page {
		if want := live[len(live)-1-i].MsgID; m.MsgID != want {
			t.Fatalf("page[%d] = %v, want %v (newest first)", i, m.MsgID, want)
		}
	}

	// Paging from the middle also skips unsent rows without stopping early.
	page2, _, err := store.GetMessages(ctx, conv, &MessageCursor{Bucket: bucket, Ts: base.Add(9 * time.Second), MsgID: uuid.Nil}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page2) != 2 || page2[0].MsgID != live[2].MsgID || page2[1].MsgID != live[1].MsgID {
		t.Fatalf("cursor page = %+v, want the two newest live messages", page2)
	}
	_ = next
}
