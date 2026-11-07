package postgres

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/google/uuid"
)

// Unsend and expiry reach the copies the 22 Oct redaction missed (review
// 7 Nov 2026): a MessageCreated row still queued for the relay, and the
// attachment's media reference.

// queuedCreatedStore answers the queued-row lock with a sealed MessageCreated
// payload carrying the text and preview.
func queuedCreatedStore(t *testing.T, conv, msg uuid.UUID, dedupeForSeal string) (*ConversationStore, *fakeDB) {
	t.Helper()
	sealer, _ := newFakeStore(t, nil)
	sealed, err := sealer.sealOutboxPayload(context.Background(), "MessageCreated", dedupeForSeal, map[string]string{
		"message_id": msg.String(), "conversation_id": conv.String(), "text": secret, "preview": secret,
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	return newFakeStore(t, func(sql string, _ []any) [][]any {
		switch {
		case sql == selectQueuedOutboxRowSQL:
			return [][]any{{int64(7), "MessageCreated", sealed}}
		case strings.Contains(sql, "SELECT deleted_at FROM chat.message_expiries"):
			return [][]any{{nil}}
		case strings.Contains(sql, "SELECT idempotency_key, completed_at, member_ids"):
			return [][]any{{"k", &now, []uuid.UUID{}}}
		}
		return nil
	})
}

func TestUnsendAndExpiryStripTheQueuedMessageCreatedRow(t *testing.T) {
	ctx := context.Background()
	conv, msg := uuid.New(), uuid.New()
	dedupe := "message-created:" + msg.String()
	paths := map[string]func(s *ConversationStore) error{
		"unsend": func(s *ConversationStore) error { return s.RedactUnsentMessageRecords(ctx, conv, msg) },
		"expiry": func(s *ConversationStore) error {
			_, err := s.ExpireMessageRecords(ctx, MessageExpiry{MessageID: msg, ConversationID: conv, SenderID: uuid.New(), MessageTS: time.Now()},
				false, "MessageDeleted", map[string]string{"message_id": msg.String()})
			return err
		},
	}
	for name, run := range paths {
		t.Run(name, func(t *testing.T) {
			s, f := queuedCreatedStore(t, conv, msg, dedupe)
			if err := run(s); err != nil {
				t.Fatal(err)
			}
			var rewritten []byte
			for _, c := range f.calls {
				if c.kind == "exec" && strings.HasPrefix(c.sql, "UPDATE chat.outbox_events SET payload = $2 WHERE id = $1") {
					if c.args[0] != int64(7) {
						t.Fatalf("rewrote outbox row %v, want the queued one (7)", c.args[0])
					}
					rewritten = c.args[1].([]byte)
				}
			}
			if rewritten == nil {
				t.Fatal("the queued MessageCreated row was left with its text: the relay would publish it")
			}
			opened, err := s.openJSON(ctx, contentcrypt.ScopeOutboxPayload, contentcrypt.OutboxRow("MessageCreated", dedupe), rewritten)
			if err != nil {
				t.Fatalf("the rewritten payload must stay sealed to its row: %v", err)
			}
			var doc map[string]any
			if err := json.Unmarshal(opened, &doc); err != nil {
				t.Fatal(err)
			}
			if _, ok := doc["text"]; ok {
				t.Fatalf("queued payload still carries text: %s", opened)
			}
			if _, ok := doc["preview"]; ok {
				t.Fatalf("queued payload still carries preview: %s", opened)
			}
			if doc["message_id"] != msg.String() {
				t.Fatalf("the event lost its ids: %s", opened)
			}
			assertNoPlaintext(t, f, needles...)
		})
	}
}

// A queued payload that does not open fails the unsend: leaving it would let
// the relay publish the text once the key is reachable again.
func TestUnsendFailsClosedWhenTheQueuedPayloadDoesNotOpen(t *testing.T) {
	conv, msg := uuid.New(), uuid.New()
	s, _ := queuedCreatedStore(t, conv, msg, "message-created:"+uuid.NewString()) // sealed to another row
	if err := s.RedactUnsentMessageRecords(context.Background(), conv, msg); err == nil {
		t.Fatal("unsend succeeded with the queued text still unreachable")
	}
}

// The attachment's media reference goes in the same transaction, so
// ViewerMayAccessChatMedia stops serving an unsent file (the expiry path
// already dropped it).
func TestUnsendRevokesTheAttachmentMediaReference(t *testing.T) {
	conv, msg := uuid.New(), uuid.New()
	s, f := newFakeStore(t, nil)
	if err := s.RedactUnsentMessageRecords(context.Background(), conv, msg); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.calls {
		if c.kind == "exec" && c.sql == deleteMessageMediaReferenceSQL {
			if c.args[0] != msg || c.args[1] != conv {
				t.Fatalf("media reference delete args = %v, want (message, conversation)", c.args)
			}
			return
		}
	}
	t.Fatal("unsend kept the message's media reference: the attachment stays downloadable")
}
