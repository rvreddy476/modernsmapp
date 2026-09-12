package workers

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// The consumer wire contract, as a LITERAL copy of feed-service's structs
// (internal/consumers/channel_updates.go). Deliberately NOT imported from
// feed-service: the point is to pin the bytes on the wire, so drift on either
// side breaks this gate instead of the product.
//
// Finding (2026-09-12): the fan-out worker produced a flat
// {recipient_id, update_id, ...} object while feed-service decoded an
// {event_type, payload:{target_user_id, item_id, ...}} envelope. Unmarshal
// succeeded with a zero payload, the consumer logged "missing target_user_id
// or item_id, skipping" and committed the offset, so no community-channel
// update ever reached a home timeline. Notifications on the same worker used
// matching structs, which is why they kept working.
type consumerFeedInjectPayload struct {
	TargetUserID string          `json:"target_user_id"`
	ItemType     string          `json:"item_type"`
	ItemID       string          `json:"item_id"`
	SourceType   string          `json:"source_type"`
	SourceID     string          `json:"source_id"`
	Score        int64           `json:"score"`
	PublishedAt  string          `json:"published_at"`
	PreviewJSON  json.RawMessage `json:"preview_json"`
}

type consumerFeedInjectEvent struct {
	EventType string                    `json:"event_type"`
	Payload   consumerFeedInjectPayload `json:"payload"`
}

func TestFeedInjectMessageMatchesTheConsumersLiteralWireShape(t *testing.T) {
	recipient := uuid.New()
	channelID := uuid.New().String()
	updateID := uuid.New().String()
	authorID := uuid.New().String()
	p := UpdatePublishedPayload{
		UpdateID:    updateID,
		ChannelID:   channelID,
		ChannelName: "Momentum Devs",
		AuthorID:    authorID,
		UpdateType:  "announcement",
		Title:       "Release 1.4",
		PublishedAt: "2026-09-12T10:00:00Z",
	}

	m := newFeedInjectMessage(p, recipient)

	if m.Topic != "atpost.channel.feed-inject" {
		t.Fatalf("topic %q, want atpost.channel.feed-inject", m.Topic)
	}
	if string(m.Key) != recipient.String() {
		t.Fatalf("key %q, want recipient %s", m.Key, recipient)
	}

	// Raw-key assertions first: these are the two fields the consumer gates
	// on before doing anything else, so name them explicitly in the failure.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(m.Value, &raw); err != nil {
		t.Fatalf("message is not a JSON object: %v\n%s", err, m.Value)
	}
	if _, ok := raw["event_type"]; !ok {
		t.Fatalf("message has no top-level event_type\n%s", m.Value)
	}
	if _, ok := raw["payload"]; !ok {
		t.Fatalf("message has no top-level payload\n%s", m.Value)
	}

	var ev consumerFeedInjectEvent
	if err := json.Unmarshal(m.Value, &ev); err != nil {
		t.Fatalf("consumer struct cannot decode message: %v\n%s", err, m.Value)
	}
	if ev.EventType == "" {
		t.Fatalf("event_type empty\n%s", m.Value)
	}
	if ev.Payload.TargetUserID != recipient.String() {
		t.Fatalf("payload.target_user_id %q, want %s\n%s", ev.Payload.TargetUserID, recipient, m.Value)
	}
	if ev.Payload.ItemID != updateID {
		t.Fatalf("payload.item_id %q, want %s\n%s", ev.Payload.ItemID, updateID, m.Value)
	}
	if ev.Payload.ItemType != "channel_update" {
		t.Fatalf("payload.item_type %q, want channel_update", ev.Payload.ItemType)
	}
	if ev.Payload.SourceType != "channel" {
		t.Fatalf("payload.source_type %q, want channel", ev.Payload.SourceType)
	}
	// The consumer uuid.Parses source_id and stores it as the timeline
	// author, so it must be the channel id, not a name or empty.
	if ev.Payload.SourceID != channelID {
		t.Fatalf("payload.source_id %q, want channel %s", ev.Payload.SourceID, channelID)
	}
	if ev.Payload.PublishedAt != p.PublishedAt {
		t.Fatalf("payload.published_at %q, want %s", ev.Payload.PublishedAt, p.PublishedAt)
	}

	// The flat shape carried author_id and update_type; the consumer payload
	// has no slot for them, so they ride in preview_json rather than vanish.
	var preview map[string]string
	if err := json.Unmarshal(ev.Payload.PreviewJSON, &preview); err != nil {
		t.Fatalf("preview_json is not an object: %v\n%s", err, ev.Payload.PreviewJSON)
	}
	if preview["author_id"] != authorID {
		t.Fatalf("preview_json.author_id %q, want %s", preview["author_id"], authorID)
	}
	if preview["update_type"] != "announcement" {
		t.Fatalf("preview_json.update_type %q, want announcement", preview["update_type"])
	}
}

// The old flat keys must not survive at the top level; if they did, a reader
// could mistake the message for the pre-fix shape and the envelope would be
// carrying two contracts at once.
func TestFeedInjectMessageDoesNotEmitTheOldFlatKeys(t *testing.T) {
	m := newFeedInjectMessage(UpdatePublishedPayload{
		UpdateID: uuid.New().String(), ChannelID: uuid.New().String(),
	}, uuid.New())

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(m.Value, &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"recipient_id", "update_id", "channel_id"} {
		if _, ok := raw[k]; ok {
			t.Fatalf("top-level %q still present; the consumer never reads it\n%s", k, m.Value)
		}
	}
}
