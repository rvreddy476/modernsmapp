package postgres

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/atpost/chat-shared/fieldcrypt"
	"github.com/google/uuid"
)

// Field encryption at rest (22 Oct 2026): every content-bearing column this
// store writes reaches PostgreSQL sealed, reads back as plaintext, reads a
// pre-encryption (legacy) row unchanged, and refuses a ciphertext moved to
// another row.

const secret = "meet me at the old banyan tree, 6pm — నమస్తే"

func testCipher(t testing.TB) *fieldcrypt.Cipher {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i*7 + 3)
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

func newFakeStore(t testing.TB, rows func(sql string, args []any) [][]any) (*ConversationStore, *fakeDB) {
	f := &fakeDB{rows: rows}
	return &ConversationStore{db: f, crypt: testCipher(t)}, f
}

// assertNoPlaintext fails when any argument of any statement carries the
// plaintext (or a recognisable piece of it), and when no argument is sealed.
func assertNoPlaintext(t *testing.T, f *fakeDB, needles ...string) {
	t.Helper()
	if len(f.calls) == 0 {
		t.Fatal("nothing was written")
	}
	sealedSeen := false
	for _, c := range f.calls {
		for _, a := range c.args {
			s := argText(a)
			for _, n := range needles {
				if strings.Contains(s, n) {
					t.Fatalf("plaintext reached PostgreSQL in %.60q: arg %.40q", c.sql, s)
				}
			}
			if fieldcrypt.IsSealed(s) || fieldcrypt.IsSealedJSON([]byte(s)) {
				sealedSeen = true
			}
		}
	}
	if !sealedSeen {
		t.Fatal("no sealed value was written")
	}
}

var needles = []string{secret, "banyan", "నమస్తే"}

func TestPostgresWritePathsStoreCiphertext(t *testing.T) {
	ctx := context.Background()
	conv, sender, msg := uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	secretPtr := func() *string { s := secret; return &s }

	cases := map[string]func(s *ConversationStore) error{
		"conversations.last_message_preview (SetLastMessage)": func(s *ConversationStore) error {
			return s.SetLastMessage(ctx, conv, sender, secret, now)
		},
		"conversations.last_message_preview (ReplaceLastMessage)": func(s *ConversationStore) error {
			return s.ReplaceLastMessage(ctx, conv, now, secret, &sender, &now)
		},
		"outbox_events.payload (InsertOutboxEvent)": func(s *ConversationStore) error {
			return s.InsertOutboxEvent(ctx, "MessageEdited", map[string]string{"text": secret})
		},
		"outbox_events.payload (InsertOutboxEventOnce)": func(s *ConversationStore) error {
			return s.InsertOutboxEventOnce(ctx, "message-created:"+msg.String(), "MessageCreated", map[string]string{"text": secret, "preview": secret})
		},
		"idempotency_keys.response": func(s *ConversationStore) error {
			return s.SaveIdempotencyResponse(ctx, "key-1", "hash", map[string]string{"text": secret})
		},
		"message_delivery_intents": func(s *ConversationStore) error {
			_, _ = s.ReserveMessageDeliveryIntent(ctx, MessageDeliveryIntent{
				IdempotencyKey: "k", ConversationID: conv, MessageID: msg, SenderID: sender,
				MessageText: secret, ReplyToPreview: secret, Extra: []byte(`{"caption":"` + secret + `"}`),
			})
			return nil // the read-back is not faked; the write is what is checked
		},
		"message_requests.preview": func(s *ConversationStore) error {
			return s.SetMessageRequestPreview(ctx, conv, secret)
		},
		"starred_messages.message_preview": func(s *ConversationStore) error {
			_, _ = s.StarMessage(ctx, sender, conv, msg, secretPtr())
			return nil
		},
		"scheduled_messages.content": func(s *ConversationStore) error {
			_, _ = s.CreateScheduledMessage(ctx, &ScheduledMessage{ConversationID: conv, SenderID: sender, Type: "text", Content: secretPtr(), SendAt: now})
			return nil
		},
		"message_translations.translated_text": func(s *ConversationStore) error {
			return s.UpsertMessageTranslation(ctx, &MessageTranslation{MessageID: msg, ConversationID: conv, TargetLang: "te", TranslatedText: secret})
		},
		"message_threads.last_reply_preview": func(s *ConversationStore) error {
			return s.IncrementThreadReplyCount(ctx, uuid.New(), secret)
		},
		"notes.text": func(s *ConversationStore) error {
			return s.ReplaceNote(ctx, Note{ID: uuid.New(), UserID: sender, Text: secret, Audience: "everyone", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
		},
		"outbox_events.payload (ExpireMessageRecords)": func(s *ConversationStore) error {
			_, err := s.ExpireMessageRecords(ctx, MessageExpiry{MessageID: msg, ConversationID: conv, SenderID: sender, MessageTS: now}, false,
				"MessageDeleted", map[string]string{"text": secret})
			return err
		},
	}
	for name, write := range cases {
		t.Run(name, func(t *testing.T) {
			s, f := newFakeStore(t, func(sql string, _ []any) [][]any {
				switch {
				case strings.Contains(sql, "SELECT deleted_at FROM chat.message_expiries"):
					return [][]any{{nil}}
				case strings.Contains(sql, "SELECT idempotency_key, completed_at, member_ids"):
					return [][]any{{"k", &now, []uuid.UUID{sender}}}
				}
				return nil
			})
			if err := write(s); err != nil {
				t.Fatalf("write: %v", err)
			}
			assertNoPlaintext(t, f, needles...)
		})
	}
}

func TestPostgresReadPathsOpenSealedAndLegacyValues(t *testing.T) {
	ctx := context.Background()
	c := testCipher(t)
	conv, msg := uuid.New(), uuid.New()
	now := time.Now().UTC()

	sealedPreview, _ := c.SealString(ctx, contentcrypt.ScopeConversationPreview, contentcrypt.ConversationRow(conv), secret)
	sealedResp, _ := c.SealJSON(ctx, contentcrypt.ScopeIdempotencyResponse, contentcrypt.IdempotencyRow("key-1"), []byte(`{"text":"`+secret+`"}`))
	sealedText, _ := c.SealString(ctx, contentcrypt.ScopeIntentText, contentcrypt.MessageRow(conv, msg), secret)
	sealedExtra, _ := c.SealJSON(ctx, contentcrypt.ScopeIntentExtra, contentcrypt.MessageRow(conv, msg), []byte(`{"caption":"x"}`))

	for name, stored := range map[string]struct{ preview, resp, text, extra string }{
		"sealed": {sealedPreview, string(sealedResp), sealedText, string(sealedExtra)},
		"legacy": {secret, `{"text":"` + secret + `"}`, secret, `{"caption":"x"}`},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := newFakeStore(t, func(sql string, _ []any) [][]any {
				switch {
				case strings.Contains(sql, "FROM chat.conversations WHERE id = $1"):
					return [][]any{{conv, "direct", nil, nil, false, now, now, nil, &now, stored.preview, nil, "", "chat", nil}}
				case strings.Contains(sql, "FROM chat.idempotency_keys"):
					return [][]any{{"hash", json.RawMessage(stored.resp)}}
				case strings.Contains(sql, "FROM chat.message_delivery_intents WHERE idempotency_key"):
					return [][]any{{"k", "h", conv, uuid.New(), msg, "202610", now, "text", stored.text,
						nil, []uuid.UUID{}, false, nil, "chat", nil, nil, "", nil, nil, []byte(stored.extra), nil}}
				}
				return nil
			})
			got, err := s.GetConversation(ctx, conv)
			if err != nil || got.LastMessagePreview != secret {
				t.Fatalf("GetConversation preview = %q, %v", got.LastMessagePreview, err)
			}
			res, err := s.CheckIdempotencyKey(ctx, "key-1")
			if err != nil || string(res.Response) != `{"text":"`+secret+`"}` {
				t.Fatalf("CheckIdempotencyKey = %s, %v", res.Response, err)
			}
			intent, err := s.GetMessageDeliveryIntent(ctx, "k")
			if err != nil || intent.MessageText != secret || string(intent.Extra) != `{"caption":"x"}` {
				t.Fatalf("GetMessageDeliveryIntent = %+v, %v", intent, err)
			}
		})
	}
}

// A ciphertext copied from another row does not open there: the intent
// (authoritative) fails the read, the inbox preview (a display copy) shows
// nothing.
func TestPostgresMovedCiphertextDoesNotOpen(t *testing.T) {
	ctx := context.Background()
	c := testCipher(t)
	convA, convB, msgA, msgB := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC()
	previewOfA, _ := c.SealString(ctx, contentcrypt.ScopeConversationPreview, contentcrypt.ConversationRow(convA), secret)
	textOfA, _ := c.SealString(ctx, contentcrypt.ScopeIntentText, contentcrypt.MessageRow(convA, msgA), secret)

	s, _ := newFakeStore(t, func(sql string, _ []any) [][]any {
		switch {
		case strings.Contains(sql, "FROM chat.conversations WHERE id = $1"):
			return [][]any{{convB, "direct", nil, nil, false, now, now, nil, &now, previewOfA, nil, "", "chat", nil}}
		case strings.Contains(sql, "FROM chat.message_delivery_intents WHERE idempotency_key"):
			return [][]any{{"k", "h", convA, uuid.New(), msgB, "202610", now, "text", textOfA,
				nil, []uuid.UUID{}, false, nil, "chat", nil, nil, "", nil, nil, nil, nil}}
		}
		return nil
	})
	got, err := s.GetConversation(ctx, convB)
	if err != nil || got.LastMessagePreview != "" {
		t.Fatalf("moved preview read as %q, %v", got.LastMessagePreview, err)
	}
	if intent, err := s.GetMessageDeliveryIntent(ctx, "k"); err == nil {
		t.Fatalf("moved intent text opened: %q", intent.MessageText)
	}
}

func TestOutboxRelayReadsOpenedPayloadsAndSkipsOnesThatDoNotOpen(t *testing.T) {
	ctx := context.Background()
	c := testCipher(t)
	now := time.Now()
	good, _ := c.SealJSON(ctx, contentcrypt.ScopeOutboxPayload, contentcrypt.OutboxRow("MessageCreated", "message-created:1"), []byte(`{"text":"hi"}`))
	moved, _ := c.SealJSON(ctx, contentcrypt.ScopeOutboxPayload, contentcrypt.OutboxRow("MessageCreated", "message-created:9"), []byte(`{"text":"x"}`))
	s, _ := newFakeStore(t, func(string, []any) [][]any {
		return [][]any{
			{int64(1), "MessageCreated", "message-created:1", json.RawMessage(good), now},
			{int64(2), "MessageCreated", "message-created:2", json.RawMessage(moved), now},
			{int64(3), "MemberAdded", "", json.RawMessage(`{"legacy":true}`), now},
		}
	})
	events, err := s.FetchUnpublishedOutboxEvents(ctx, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].ID != 1 || string(events[0].Payload) != `{"text":"hi"}` ||
		events[1].ID != 3 || string(events[1].Payload) != `{"legacy":true}` {
		t.Fatalf("events = %+v", events)
	}
}

// Retention: completing a delivery drops the intent's copy of the content.
func TestCompletingADeliveryBlanksTheIntentContent(t *testing.T) {
	s, f := newFakeStore(t, nil)
	if err := s.CompleteMessageDeliveryIntent(context.Background(), "k"); err != nil {
		t.Fatal(err)
	}
	sql := f.calls[0].sql
	for _, want := range []string{"message_text = ''", "reply_to_preview = ''", "extra = NULL", "completed_at = COALESCE"} {
		if !strings.Contains(sql, want) {
			t.Fatalf("completion SQL lacks %q:\n%s", want, sql)
		}
	}
}

// Unsend: the intent, the cached response, the published MessageCreated
// payload, translations and star previews lose the content.
func TestUnsendRedactsThePostgresCopies(t *testing.T) {
	conv, msg := uuid.New(), uuid.New()
	s, f := newFakeStore(t, func(sql string, _ []any) [][]any {
		if strings.Contains(sql, "SELECT idempotency_key FROM chat.message_delivery_intents") {
			return [][]any{{"send-key"}}
		}
		return nil
	})
	if err := s.RedactUnsentMessageRecords(context.Background(), conv, msg); err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, c := range f.calls {
		if c.kind == "exec" {
			all = append(all, c.sql)
		}
	}
	joined := strings.Join(all, "\n")
	for _, want := range []string{
		"SET message_text = '', reply_to_preview = '', extra = NULL",
		"payload - 'text' - 'preview'",
		"ELSE '{}'::jsonb",
		"DELETE FROM chat.message_translations",
		"SET message_preview = NULL",
		"UPDATE chat.idempotency_keys SET response = NULL",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unsend redaction lacks %q:\n%s", want, joined)
		}
	}
}

// Development without keys writes plaintext (and only there; fieldkeys
// refuses it in production).
func TestPlaintextModeWritesClear(t *testing.T) {
	f := &fakeDB{}
	s := &ConversationStore{db: f}
	if err := s.SetMessageRequestPreview(context.Background(), uuid.New(), secret); err != nil {
		t.Fatal(err)
	}
	if got := argText(f.calls[0].args[1]); got != secret {
		t.Fatalf("plaintext mode wrote %q", got)
	}
}

func TestPreviewIsCutOnACharacterBoundaryBeforeSealing(t *testing.T) {
	ctx := context.Background()
	s, f := newFakeStore(t, nil)
	conv := uuid.New()
	long := strings.Repeat("న", 60) // 3 bytes each: a byte cut at 140 splits one
	if err := s.SetLastMessage(ctx, conv, uuid.New(), long, time.Now()); err != nil {
		t.Fatal(err)
	}
	stored := argText(f.calls[0].args[2])
	opened, err := s.crypt.OpenString(ctx, contentcrypt.ScopeConversationPreview, contentcrypt.ConversationRow(conv), stored)
	if err != nil {
		t.Fatal(err)
	}
	if opened != strings.Repeat("న", 46) {
		t.Fatalf("preview = %d bytes, want 46 whole characters (138 bytes)", len(opened))
	}
}

func TestBackfillStepSealsLegacyRowsOnlyAndIsCompareAndSet(t *testing.T) {
	ctx := context.Background()
	c := testCipher(t)
	idLegacy, idSealed, idEmpty := uuid.New(), uuid.New(), uuid.New()
	already, _ := c.SealString(ctx, contentcrypt.ScopeConversationPreview, contentcrypt.ConversationRow(idSealed), "x")
	f := &fakeDB{rows: func(string, []any) [][]any {
		return [][]any{
			{idLegacy.String(), idLegacy.String(), secret},
			{idSealed.String(), idSealed.String(), already},
			{idEmpty.String(), idEmpty.String(), ""},
		}
	}}
	target := pgBackfillTarget{db: f, t: pgBackfillTables[0]}
	res, err := target.Step(ctx, c, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if res.Scanned != 3 || res.Sealed != 1 || !res.Done || res.Next != idEmpty.String() {
		t.Fatalf("step = %+v", res)
	}
	var updates []dbCall
	for _, call := range f.calls {
		if call.kind == "exec" {
			updates = append(updates, call)
		}
	}
	if len(updates) != 1 {
		t.Fatalf("%d updates, want 1 (only the legacy row)", len(updates))
	}
	u := updates[0]
	if argText(u.args[0]) != idLegacy.String() || argText(u.args[2]) != secret {
		t.Fatal("the update is not keyed and compare-and-set on the value read")
	}
	newValue := argText(u.args[1])
	if strings.Contains(newValue, "banyan") || !fieldcrypt.IsSealed(newValue) {
		t.Fatal("the backfill wrote plaintext")
	}
	if got, err := c.OpenString(ctx, contentcrypt.ScopeConversationPreview, contentcrypt.ConversationRow(idLegacy), newValue); err != nil || got != secret {
		t.Fatalf("backfilled value opens as %q, %v", got, err)
	}
}

func TestBackfillTablesAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, tb := range pgBackfillTables {
		if seen[tb.name] {
			t.Fatalf("duplicate target %s", tb.name)
		}
		seen[tb.name] = true
		// Every new value and every compare-and-set value has a placeholder.
		want := 1 + 2*len(tb.fields)
		if !strings.Contains(tb.updateSQL, "$"+strconv.Itoa(want)) || strings.Contains(tb.updateSQL, "$"+strconv.Itoa(want+1)) {
			t.Fatalf("%s: update placeholders do not match %d fields", tb.name, len(tb.fields))
		}
		if !strings.Contains(tb.updateSQL, "IS NOT DISTINCT FROM") {
			t.Fatalf("%s: update is not compare-and-set", tb.name)
		}
	}
}
