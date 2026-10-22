package scylla

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/google/uuid"
)

// Field encryption at rest (22 Oct 2026) on the Scylla side: the message
// row's text, quote snapshot and rich payload, and the inbox projection's
// last_text, are sealed before they reach Scylla, open on read, read
// pre-encryption rows unchanged, and refuse a ciphertext moved to another
// row. Unsend blanks the content.

const secret = "the locker code is 4471 — నమస్తే"

type recordedCQL struct {
	stmt string
	args []interface{}
}

type recordingExecer struct{ calls []recordedCQL }

func (r *recordingExecer) Exec(_ context.Context, stmt string, args ...interface{}) error {
	r.calls = append(r.calls, recordedCQL{stmt, args})
	return nil
}

func testCipher(t testing.TB) *fieldcrypt.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(200 - i)
	}
	ring, err := fieldcrypt.ParseKeyList("v1:" + base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatal(err)
	}
	c, err := fieldcrypt.New(ring)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func fakeStore(t testing.TB) (*MessageStore, *recordingExecer) {
	rec := &recordingExecer{}
	return &MessageStore{exec: rec, crypt: testCipher(t)}, rec
}

func cqlArgText(a interface{}) string {
	switch v := a.(type) {
	case string:
		return v
	case *string:
		if v == nil {
			return ""
		}
		return *v
	default:
		return fmt.Sprintf("%v", v)
	}
}

func assertNoPlaintextCQL(t *testing.T, rec *recordingExecer) {
	t.Helper()
	if len(rec.calls) == 0 {
		t.Fatal("nothing was written")
	}
	sealed := 0
	for _, c := range rec.calls {
		for _, a := range c.args {
			s := cqlArgText(a)
			for _, n := range []string{secret, "locker", "4471", "నమస్తే"} {
				if strings.Contains(s, n) {
					t.Fatalf("plaintext reached Scylla in %q", strings.TrimSpace(c.stmt)[:30])
				}
			}
			if fieldcrypt.IsSealed(s) {
				sealed++
			}
		}
	}
	if sealed == 0 {
		t.Fatal("no sealed value was written")
	}
}

func TestScyllaWritePathsStoreCiphertext(t *testing.T) {
	ctx := context.Background()
	conv, msg, user := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	for name, write := range map[string]func(*MessageStore) error{
		"CreateMessage": func(s *MessageStore) error {
			return s.CreateMessage(ctx, &Message{ConversationID: conv, MsgID: msg, SenderID: user, Ts: now, Bucket: "202610",
				Type: "text", Text: secret, ReplyToPreview: secret, Extra: `{"caption":"` + secret + `"}`, CreatedAt: now})
		},
		"UpdateMessageText": func(s *MessageStore) error {
			return s.UpdateMessageText(ctx, conv, "202610", now, msg, secret, now)
		},
		"UpsertInbox": func(s *MessageStore) error {
			return s.UpsertInbox(ctx, user, conv, user, secret, now)
		},
	} {
		t.Run(name, func(t *testing.T) {
			s, rec := fakeStore(t)
			if err := write(s); err != nil {
				t.Fatal(err)
			}
			assertNoPlaintextCQL(t, rec)
		})
	}
}

func TestCreateMessageSealsEveryContentColumn(t *testing.T) {
	ctx := context.Background()
	s, rec := fakeStore(t)
	conv, msg := uuid.New(), uuid.New()
	if err := s.CreateMessage(ctx, &Message{ConversationID: conv, MsgID: msg, Text: "a", ReplyToPreview: "b", Extra: `{"c":1}`}); err != nil {
		t.Fatal(err)
	}
	args := rec.calls[0].args
	// INSERT column order: text is 7th, reply_to_preview 10th, extra 13th.
	for _, i := range []int{6, 9, 12} {
		if !fieldcrypt.IsSealed(cqlArgText(args[i])) {
			t.Fatalf("insert argument %d is not sealed", i+1)
		}
	}
	// Reading the stored row back yields the plaintext.
	m := Message{ConversationID: conv, MsgID: msg, Text: cqlArgText(args[6]), ReplyToPreview: cqlArgText(args[9]), Extra: cqlArgText(args[12])}
	if err := s.openMessage(ctx, &m); err != nil || m.Text != "a" || m.ReplyToPreview != "b" || m.Extra != `{"c":1}` {
		t.Fatalf("round trip = %+v, %v", m, err)
	}
}

// Rows written before encryption read through unchanged (dual read).
func TestOpenMessageReadsLegacyPlaintext(t *testing.T) {
	s, _ := fakeStore(t)
	m := Message{ConversationID: uuid.New(), MsgID: uuid.New(), Text: secret, ReplyToPreview: "quoted", Extra: `{"voice":{"ms":1}}`}
	if err := s.openMessage(context.Background(), &m); err != nil || m.Text != secret || m.ReplyToPreview != "quoted" || m.Extra != `{"voice":{"ms":1}}` {
		t.Fatalf("legacy row = %+v, %v", m, err)
	}
}

func TestOpenMessageRefusesACiphertextFromAnotherRow(t *testing.T) {
	ctx := context.Background()
	s, rec := fakeStore(t)
	conv, msgA := uuid.New(), uuid.New()
	_ = s.CreateMessage(ctx, &Message{ConversationID: conv, MsgID: msgA, Text: secret})
	stolen := cqlArgText(rec.calls[0].args[6])
	for name, m := range map[string]Message{
		"other message":      {ConversationID: conv, MsgID: uuid.New(), Text: stolen},
		"other conversation": {ConversationID: uuid.New(), MsgID: msgA, Text: stolen},
		"other column":       {ConversationID: conv, MsgID: msgA, Extra: stolen},
	} {
		if err := s.openMessage(ctx, &m); err == nil {
			t.Fatalf("%s: a moved ciphertext opened as %q / %q", name, m.Text, m.Extra)
		}
	}
}

// Unsend blanks the text, the quote snapshot and the rich payload (it used
// to set is_deleted and keep the text).
func TestUnsendRedactsTheScyllaRow(t *testing.T) {
	s, rec := fakeStore(t)
	if err := s.SoftDeleteMessage(context.Background(), uuid.New(), "202610", time.Now(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	stmt := rec.calls[0].stmt
	for _, want := range []string{"text = ''", "reply_to_preview = ''", "extra = null", "is_deleted = true"} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("unsend statement lacks %q:\n%s", want, stmt)
		}
	}
}

func TestInboxTextIsCutOnACharacterBoundary(t *testing.T) {
	ctx := context.Background()
	s, rec := fakeStore(t)
	user, conv := uuid.New(), uuid.New()
	ts := time.Date(2026, 10, 22, 10, 0, 0, 123456789, time.UTC)
	long := strings.Repeat("న", 40) // 3 bytes each; a 100-byte cut splits one
	if err := s.UpsertInbox(ctx, user, conv, user, long, ts); err != nil {
		t.Fatal(err)
	}
	stored := cqlArgText(rec.calls[0].args[5])
	// Scylla gives last_ts back at millisecond precision; the binding uses
	// exactly that, so the row read back opens.
	readBack := ts.Truncate(time.Millisecond)
	got, err := s.crypt.OpenString(ctx, contentcrypt.ScopeInboxLastText, contentcrypt.InboxRow(user, conv, readBack), stored)
	if err != nil {
		t.Fatal(err)
	}
	if got != strings.Repeat("న", 33) {
		t.Fatalf("inbox text is %d bytes, want 33 whole characters (99 bytes)", len(got))
	}
}

func TestPlaintextModeStoresClear(t *testing.T) {
	rec := &recordingExecer{}
	s := &MessageStore{exec: rec}
	if err := s.UpdateMessageText(context.Background(), uuid.New(), "202610", time.Now(), uuid.New(), "hello", time.Now()); err != nil {
		t.Fatal(err)
	}
	if cqlArgText(rec.calls[0].args[0]) != "hello" {
		t.Fatal("plaintext mode did not store the text as given")
	}
}

// --- Benchmarks: AES-GCM per field must add < 1 ms per request. ---

func BenchmarkSealOpen2000RuneMessage(b *testing.B) {
	ctx := context.Background()
	s, rec := fakeStore(b)
	text := strings.Repeat("నమస్తే hello ", 200)[:0]
	for len([]rune(text)) < 2000 {
		text += "నమస్తే hello "
	}
	text = string([]rune(text)[:2000])
	conv, msg := uuid.New(), uuid.New()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rec.calls = rec.calls[:0]
		if err := s.CreateMessage(ctx, &Message{ConversationID: conv, MsgID: msg, Text: text}); err != nil {
			b.Fatal(err)
		}
		m := Message{ConversationID: conv, MsgID: msg, Text: cqlArgText(rec.calls[0].args[6])}
		if err := s.openMessage(ctx, &m); err != nil || m.Text != text {
			b.Fatal("round trip failed")
		}
	}
}

func BenchmarkOpen50MessageHistoryPage(b *testing.B) {
	ctx := context.Background()
	s, rec := fakeStore(b)
	conv := uuid.New()
	page := make([]Message, 50)
	for i := range page {
		id := uuid.New()
		_ = s.CreateMessage(ctx, &Message{ConversationID: conv, MsgID: id,
			Text:           fmt.Sprintf("message %d: are we still on for dinner on Friday at the usual place?", i),
			ReplyToPreview: "dinner?", Extra: `{"forwarded":false,"reply_kind":"text"}`})
		args := rec.calls[len(rec.calls)-1].args
		page[i] = Message{ConversationID: conv, MsgID: id, Text: cqlArgText(args[6]), ReplyToPreview: cqlArgText(args[9]), Extra: cqlArgText(args[12])}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for j := range page {
			m := page[j]
			if err := s.openMessage(ctx, &m); err != nil {
				b.Fatal(err)
			}
		}
	}
}
