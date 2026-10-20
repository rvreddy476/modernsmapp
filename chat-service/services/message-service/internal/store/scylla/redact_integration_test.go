//go:build integration

package scylla

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Disappearing messages: the expiry redaction wipes every piece of content
// from the row (keeping it, flagged deleted) and drops the per-member inbox
// projection rows the message wrote. Microsecond timestamps (as the expiry
// row stores them) must address the millisecond Scylla row.
func TestRedactMessageWipesContentAndInboxProjection(t *testing.T) {
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

	conv, sender, reader, media, post, quoted := uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ts := time.Now().UTC().Truncate(time.Millisecond).Add(-time.Hour)
	tsMicro := ts.Add(456 * time.Microsecond)
	bucket := ts.Format("200601")
	m := &Message{ConversationID: conv, Bucket: bucket, Ts: ts, MsgID: uuid.New(), SenderID: sender, Type: "media",
		Text: "secret", MediaID: &media, PostID: &post, ReplyToID: &quoted, ReplyToPreview: "quoted secret",
		Extra: `{"expires_at":"2026-01-01T00:00:00Z"}`, CreatedAt: ts}
	if err := store.CreateMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	for _, u := range []uuid.UUID{sender, reader} {
		if err := store.UpsertInbox(ctx, u, conv, sender, "secret", ts); err != nil {
			t.Fatal(err)
		}
	}

	if err := store.RedactMessage(ctx, conv, bucket, tsMicro, m.MsgID); err != nil {
		t.Fatal(err)
	}
	got, err := store.GetMessage(ctx, conv, bucket, ts, m.MsgID)
	if err != nil || got == nil {
		t.Fatalf("row must remain: %v %v", got, err)
	}
	if !got.IsDeleted || got.Text != "" || got.MediaID != nil || got.PostID != nil || got.ReplyToPreview != "" || got.Extra != "" {
		t.Fatalf("content survived redaction: %+v", got)
	}
	if page, _, err := store.GetMessages(ctx, conv, nil, 10); err != nil || len(page) != 0 {
		t.Fatalf("a redacted message must not be served: %d (%v)", len(page), err)
	}

	if err := store.DeleteInboxProjection(ctx, []uuid.UUID{sender, reader}, conv, tsMicro); err != nil {
		t.Fatal(err)
	}
	for _, u := range []uuid.UUID{sender, reader} {
		var n int
		if err := session.Query(`SELECT COUNT(*) FROM conversations_by_user WHERE user_id = ? AND bucket = ?`,
			uuidToGocql(u), bucket).WithContext(ctx).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Fatalf("inbox projection for %s still has %d rows", u, n)
		}
	}
}
