package events

import (
	"context"
	"encoding/json"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/atpost/search-service/internal/store/search"
	"github.com/segmentio/kafka-go"
)

// fakeChatIndex records what the consumer asked the store to do, keyed so a
// replayed event lands on the same document (the store's own reordering
// guards are proven against a live cluster in chat_integration_test.go).
type fakeChatIndex struct {
	upserts    map[string]search.ChatMessageDoc
	upsertN    int
	edits      map[string]string
	tombstones map[string]bool
	removed    []string
	cleared    []string
}

func newFakeChatIndex() *fakeChatIndex {
	return &fakeChatIndex{
		upserts:    map[string]search.ChatMessageDoc{},
		edits:      map[string]string{},
		tombstones: map[string]bool{},
	}
}

func (f *fakeChatIndex) UpsertChatMessage(_ context.Context, d search.ChatMessageDoc) error {
	f.upsertN++
	f.upserts[d.MessageID] = d
	return nil
}
func (f *fakeChatIndex) EditChatMessage(_ context.Context, id, _ string, text string, _ time.Time) error {
	f.edits[id] = text
	return nil
}
func (f *fakeChatIndex) TombstoneChatMessage(_ context.Context, id, _ string) error {
	f.tombstones[id] = true
	return nil
}
func (f *fakeChatIndex) RemoveChatMember(_ context.Context, conv, user string) error {
	f.removed = append(f.removed, conv+"/"+user)
	return nil
}
func (f *fakeChatIndex) ClearChatRequest(_ context.Context, conv string) error {
	f.cleared = append(f.cleared, conv)
	return nil
}

func chatEnvelope(t *testing.T, eventType string, payload any) kafka.Message {
	t.Helper()
	p, _ := json.Marshal(payload)
	v, _ := json.Marshal(map[string]any{
		"event_id":    "e-1",
		"event_type":  eventType,
		"occurred_at": time.Now().UTC(),
		"payload":     json.RawMessage(p),
	})
	return kafka.Message{Value: v}
}

func chatConsumer(ix ChatIndex) *Consumer {
	return (&Consumer{topic: "chat.events.v1", groupID: "t"}).WithChatIndex(ix)
}

func TestChatConsumer_IndexesTextMessageWithAllMembers(t *testing.T) {
	ix := newFakeChatIndex()
	c := chatConsumer(ix)
	at := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)
	m := chatEnvelope(t, ChatMessageCreated, map[string]any{
		"message_id": "m1", "conversation_id": "c1", "sender_id": "a",
		"type": "text", "text": "hello world", "recipient_ids": []string{"b", "c"},
		"created_at": at,
	})
	if err := c.processMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	d, ok := ix.upserts["m1"]
	if !ok {
		t.Fatal("text message was not indexed")
	}
	got := append([]string(nil), d.MemberIDs...)
	sort.Strings(got)
	if !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("member_ids = %v, want sender + recipients", d.MemberIDs)
	}
	if d.Text != "hello world" || d.ConversationID != "c1" || d.SenderID != "a" || !d.CreatedAt.Equal(at) {
		t.Fatalf("doc = %+v", d)
	}

	// Redelivery addresses the same document (id = message_id): no second row.
	if err := c.processMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if len(ix.upserts) != 1 || ix.upsertN != 2 {
		t.Fatalf("redelivery: %d docs after %d writes, want 1 doc", len(ix.upserts), ix.upsertN)
	}
}

func TestChatConsumer_SkipsWhatIsNotSearchableText(t *testing.T) {
	cases := map[string]map[string]any{
		"media":         {"message_id": "m", "conversation_id": "c", "sender_id": "a", "type": "media", "text": "caption", "recipient_ids": []string{"b"}},
		"post card":     {"message_id": "m", "conversation_id": "c", "sender_id": "a", "type": "post", "recipient_ids": []string{"b"}},
		"no text field": {"message_id": "m", "conversation_id": "c", "sender_id": "a", "type": "text", "recipient_ids": []string{"b"}},
		"blank text":    {"message_id": "m", "conversation_id": "c", "sender_id": "a", "type": "text", "text": "   ", "recipient_ids": []string{"b"}},
		"dating":        {"message_id": "m", "conversation_id": "c", "sender_id": "a", "type": "text", "text": "hi", "source_app": "dating", "recipient_ids": []string{"b"}},
		"missing ids":   {"conversation_id": "c", "sender_id": "a", "type": "text", "text": "hi"},
	}
	for name, payload := range cases {
		ix := newFakeChatIndex()
		if err := chatConsumer(ix).processMessage(context.Background(), chatEnvelope(t, ChatMessageCreated, payload)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(ix.upserts) != 0 {
			t.Fatalf("%s: indexed %v, want skipped", name, ix.upserts)
		}
	}
}

func TestChatConsumer_RequestFlagRidesTheDocument(t *testing.T) {
	ix := newFakeChatIndex()
	c := chatConsumer(ix)
	_ = c.processMessage(context.Background(), chatEnvelope(t, ChatMessageCreated, map[string]any{
		"message_id": "m1", "conversation_id": "c1", "sender_id": "a", "type": "text",
		"text": "hi there", "is_request": true, "recipient_ids": []string{"b"},
	}))
	if !ix.upserts["m1"].IsRequest {
		t.Fatal("first message of a request must be indexed as is_request (excluded from search)")
	}
	_ = c.processMessage(context.Background(), chatEnvelope(t, ChatMessageRequestAccepted, map[string]any{
		"conversation_id": "c1", "sender_id": "a", "receiver_id": "b",
	}))
	if !reflect.DeepEqual(ix.cleared, []string{"c1"}) {
		t.Fatalf("accepted request did not clear is_request: %v", ix.cleared)
	}
}

func TestChatConsumer_EditDeleteAndMembership(t *testing.T) {
	ix := newFakeChatIndex()
	c := chatConsumer(ix)
	ctx := context.Background()
	msgs := []kafka.Message{
		chatEnvelope(t, ChatMessageEdited, map[string]any{"message_id": "m1", "conversation_id": "c1", "text": "fixed typo", "edited_at": time.Now()}),
		chatEnvelope(t, ChatMessageEdited, map[string]any{"message_id": "m2", "conversation_id": "c1"}), // no text → skip
		chatEnvelope(t, ChatMessageDeleted, map[string]any{"message_id": "m3", "conversation_id": "c1", "deleted_by": "a"}),
		chatEnvelope(t, ChatMemberRemoved, map[string]any{"conversation_id": "c1", "user_id": "u1", "removed_by": "a"}),
		chatEnvelope(t, ChatMemberLeft, map[string]any{"conversation_id": "c1", "user_id": "u2"}),
		chatEnvelope(t, "MemberAdded", map[string]any{"conversation_id": "c1", "user_id": "u3"}), // deliberately not applied
		chatEnvelope(t, "ReactionToggled", map[string]any{"message_id": "m1"}),                   // not ours
	}
	for _, m := range msgs {
		if err := c.processMessage(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	if ix.edits["m1"] != "fixed typo" || len(ix.edits) != 1 {
		t.Fatalf("edits = %v", ix.edits)
	}
	if !ix.tombstones["m3"] {
		t.Fatal("deleted message was not tombstoned")
	}
	if !reflect.DeepEqual(ix.removed, []string{"c1/u1", "c1/u2"}) {
		t.Fatalf("removed = %v", ix.removed)
	}
}

// Without a chat index wired (the social / identity consumers before this
// lane), chat events fall through and are ignored rather than crashing.
func TestConsumerWithoutChatIndexIgnoresChatEvents(t *testing.T) {
	c := &Consumer{topic: "x", groupID: "x"}
	m := chatEnvelope(t, ChatMessageCreated, map[string]any{
		"message_id": "m1", "conversation_id": "c1", "sender_id": "a", "type": "text", "text": "hi",
	})
	if err := c.processMessage(context.Background(), m); err != nil {
		t.Fatal(err)
	}
}
