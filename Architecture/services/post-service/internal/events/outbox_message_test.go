package events

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// feed-07: what the outbox relay puts on the wire for one row.

func decodeOutboxMessage(t *testing.T, msg kafka.Message) events.EventEnvelope {
	t.Helper()
	var env events.EventEnvelope
	if err := json.Unmarshal(msg.Value, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	return env
}

// Two publishes of one row carry the same event id (the row id) and the
// aggregate id as the key. Both used to be a fresh uuid per publish.
func TestOutboxMessage_SameRowTwice_SameEventIDAndAggregateKey(t *testing.T) {
	ctx := context.Background()
	rowID, postID := uuid.New(), uuid.New()
	payload := json.RawMessage(`{"post_id":"` + postID.String() + `"}`)

	first, err := OutboxMessage(ctx, rowID, events.PostDeleted, postID, payload)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OutboxMessage(ctx, rowID, events.PostDeleted, postID, payload)
	if err != nil {
		t.Fatal(err)
	}

	a, b := decodeOutboxMessage(t, first), decodeOutboxMessage(t, second)
	if a.EventID != rowID.String() || b.EventID != rowID.String() {
		t.Fatalf("event ids = %q, %q; want the outbox row id %s both times", a.EventID, b.EventID, rowID)
	}
	if string(first.Key) != postID.String() || string(second.Key) != postID.String() {
		t.Fatalf("keys = %q, %q; want the aggregate id %s both times", first.Key, second.Key, postID)
	}
	if a.EventType != events.PostDeleted || string(a.Payload) != string(payload) {
		t.Fatalf("envelope = %+v; event type or payload changed", a)
	}
}

// Two rows of one post (a delete, then its restore) share a key, so they
// share a partition, and keep their own event ids.
func TestOutboxMessage_TwoRowsOfOnePost_ShareTheKeyOnly(t *testing.T) {
	ctx := context.Background()
	postID := uuid.New()
	del, err := OutboxMessage(ctx, uuid.New(), events.PostDeleted, postID, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	res, err := OutboxMessage(ctx, uuid.New(), events.PostRestored, postID, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(del.Key) != string(res.Key) {
		t.Fatalf("keys differ for one post: %q vs %q", del.Key, res.Key)
	}
	if decodeOutboxMessage(t, del).EventID == decodeOutboxMessage(t, res).EventID {
		t.Fatal("two rows share an event id")
	}
}

// A row without an aggregate id is keyed by its own id, never by an empty
// or all-zero key that would pile every such row on one partition.
func TestOutboxMessage_NoAggregate_KeyedByRow(t *testing.T) {
	rowID := uuid.New()
	msg, err := OutboxMessage(context.Background(), rowID, events.PostCreated, uuid.Nil, json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(msg.Key) != rowID.String() {
		t.Fatalf("key = %q, want the row id %s", msg.Key, rowID)
	}
}

// The key only orders anything if the writer partitions by it: the relay's
// writer hashes the key, and the direct publishes keep the writer they had.
func TestProducer_RelayWriterHashesTheKey(t *testing.T) {
	p := NewProducer([]string{"127.0.0.1:1"}, "post.events.test")
	t.Cleanup(func() { _ = p.Close() })

	if p.relay == nil {
		t.Fatal("no relay writer")
	}
	if _, ok := p.relay.Balancer.(*kafka.Hash); !ok {
		t.Fatalf("relay balancer = %T, want *kafka.Hash (key-respecting)", p.relay.Balancer)
	}
	if p.relay.Topic != p.writer.Topic {
		t.Fatalf("relay topic = %q, direct topic = %q", p.relay.Topic, p.writer.Topic)
	}
	if _, ok := p.writer.Balancer.(*kafka.LeastBytes); !ok {
		t.Fatalf("direct writer balancer = %T; it was not this change's to move", p.writer.Balancer)
	}
}

// A Producer built without a relay writer (tests do) refuses rather than
// dereferencing nil.
func TestProducer_PublishOutbox_NoRelayWriter(t *testing.T) {
	if err := (&Producer{}).PublishOutbox(context.Background(), kafka.Message{}); err == nil {
		t.Fatal("PublishOutbox with no relay writer returned nil")
	}
}
