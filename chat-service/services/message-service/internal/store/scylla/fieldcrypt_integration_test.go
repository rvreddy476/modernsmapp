//go:build integration

package scylla

// Field encryption at rest against a real Scylla: the message row and the
// inbox projection hold ciphertext, reads return plaintext, a pre-encryption
// row still reads, unsend blanks the content, and the backfill seals legacy
// rows once (LWT compare-and-set).
//
// Run: SCYLLA_HOST=... go test ./internal/store/scylla -tags integration -run FieldCrypt

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/fieldbackfill"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

func fieldCryptStore(t *testing.T) (*MessageStore, *gocql.Session) {
	t.Helper()
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
	t.Cleanup(session.Close)
	store := New(session)
	if _, err := store.EnsureMessageColumns(context.Background(), keyspace); err != nil {
		t.Fatal(err)
	}
	store.SetFieldCipher(testCipher(t))
	return store, session
}

func TestFieldCryptScyllaRowsHoldCiphertext(t *testing.T) {
	store, session := fieldCryptStore(t)
	ctx := context.Background()
	conv, sender := uuid.New(), uuid.New()
	ts := time.Now().UTC().Add(-time.Minute).Add(321 * time.Microsecond)
	bucket := ts.Format("200601")
	m := &Message{ConversationID: conv, Bucket: bucket, Ts: ts, MsgID: uuid.New(), SenderID: sender, Type: "text",
		Text: secret, ReplyToPreview: "quoted " + secret, Extra: `{"caption":"locker"}`, CreatedAt: ts}
	if err := store.CreateMessage(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertInbox(ctx, sender, conv, sender, secret, ts); err != nil {
		t.Fatal(err)
	}
	var text, reply, extra string
	if err := session.Query(`SELECT text, reply_to_preview, extra FROM messages WHERE conversation_id = ? AND bucket = ? AND ts = ? AND msg_id = ?`,
		uuidToGocql(conv), bucket, ts, uuidToGocql(m.MsgID)).Scan(&text, &reply, &extra); err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{text, reply, extra} {
		if !fieldcrypt.IsSealed(v) || strings.Contains(v, "locker") {
			t.Fatalf("a content column holds %.30q", v)
		}
	}
	var lastText string
	if err := session.Query(`SELECT last_text FROM conversations_by_user WHERE user_id = ? AND bucket = ? AND last_ts = ? AND conversation_id = ?`,
		uuidToGocql(sender), bucket, ts, uuidToGocql(conv)).Scan(&lastText); err != nil {
		t.Fatal(err)
	}
	if !fieldcrypt.IsSealed(lastText) {
		t.Fatal("the inbox projection holds plaintext")
	}
	got, err := store.GetMessage(ctx, conv, bucket, ts, m.MsgID)
	if err != nil || got.Text != secret || got.ReplyToPreview != "quoted "+secret || got.Extra != `{"caption":"locker"}` {
		t.Fatalf("GetMessage = %+v, %v", got, err)
	}

	// Unsend blanks the content.
	if err := store.SoftDeleteMessage(ctx, conv, bucket, ts, m.MsgID); err != nil {
		t.Fatal(err)
	}
	got, err = store.GetMessage(ctx, conv, bucket, ts, m.MsgID)
	if err != nil || !got.IsDeleted || got.Text != "" || got.ReplyToPreview != "" || got.Extra != "" {
		t.Fatalf("after unsend: %+v, %v", got, err)
	}
}

func TestFieldCryptScyllaBackfillSealsLegacyRowsOnce(t *testing.T) {
	store, session := fieldCryptStore(t)
	ctx := context.Background()
	conv, sender := uuid.New(), uuid.New()
	ts := time.Now().UTC().Truncate(time.Millisecond).Add(-2 * time.Minute)
	bucket := ts.Format("200601")
	msgID := uuid.New()
	// A row exactly as message-service wrote it before 22 Oct 2026.
	if err := session.Query(`INSERT INTO messages (conversation_id, bucket, ts, msg_id, sender_id, type, text, reply_to_preview, is_deleted, created_at)
		VALUES (?, ?, ?, ?, ?, 'text', ?, '', false, ?)`, uuidToGocql(conv), bucket, ts, uuidToGocql(msgID), uuidToGocql(sender), secret, ts).Exec(); err != nil {
		t.Fatal(err)
	}
	if got, err := store.GetMessage(ctx, conv, bucket, ts, msgID); err != nil || got.Text != secret {
		t.Fatalf("legacy row reads %q, %v", got.Text, err)
	}
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	progress := &memProgressForTest{resume: map[string]string{}, done: map[string]bool{}}
	if _, err := fieldbackfill.Run(ctx, store.crypt, store.BackfillTargets()[:1], progress, fieldbackfill.Options{Batch: 200}, quiet); err != nil {
		t.Fatal(err)
	}
	var text string
	_ = session.Query(`SELECT text FROM messages WHERE conversation_id = ? AND bucket = ? AND ts = ? AND msg_id = ?`,
		uuidToGocql(conv), bucket, ts, uuidToGocql(msgID)).Scan(&text)
	if !fieldcrypt.IsSealed(text) {
		t.Fatal("the legacy row is still plaintext after the backfill")
	}
	if got, err := store.GetMessage(ctx, conv, bucket, ts, msgID); err != nil || got.Text != secret {
		t.Fatalf("backfilled row reads %q, %v", got.Text, err)
	}
	progress = &memProgressForTest{resume: map[string]string{}, done: map[string]bool{}}
	reports, err := fieldbackfill.Run(ctx, store.crypt, store.BackfillTargets()[:1], progress, fieldbackfill.Options{Batch: 200}, quiet)
	if err != nil || reports[0].Sealed != 0 {
		t.Fatalf("second run sealed %d rows, %v", reports[0].Sealed, err)
	}
}

type memProgressForTest struct {
	resume map[string]string
	done   map[string]bool
}

func (p *memProgressForTest) LoadProgress(_ context.Context, t string) (string, bool, error) {
	return p.resume[t], p.done[t], nil
}

func (p *memProgressForTest) SaveProgress(_ context.Context, t, r string, d bool, _, _ int) error {
	p.resume[t], p.done[t] = r, d
	return nil
}
