package realtime

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// streamsTestClient returns a Redis client backed by REDIS_TEST_ADDR.
// Skips the test when the env is unset so unit-only CI runs cleanly.
//
// FLUSHALL is intentionally NOT called — multiple test packages may
// share the same Redis instance and clobbering globally is rude.
// Tests instead use uniquely-named topics + clean their own keys.
func streamsTestClient(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("REDIS_TEST_ADDR")
	if addr == "" {
		t.Skip("REDIS_TEST_ADDR not set; skipping Streams integration tests")
	}
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 1})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatalf("redis ping: %v", err)
	}
	return rdb
}

// TestStreamPublisher_RoundTrip confirms XADD + XREAD live-tail.
func TestStreamPublisher_RoundTrip(t *testing.T) {
	rdb := streamsTestClient(t)
	defer rdb.Close()
	topic := "test.rt.roundtrip." + time.Now().Format("150405.000000")
	defer rdb.Del(context.Background(), StreamKey(topic))

	pub := NewStreamPublisher(rdb)
	sub := NewStreamSubscriber(rdb, []string{topic}, "0", 2*time.Second)

	if err := pub.Publish(context.Background(), topic, "test.event", map[string]any{"hello": "world"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	events, err := sub.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event, got %d", len(events))
	}
	if events[0].Event.EventType != "test.event" {
		t.Errorf("want event_type=test.event, got %s", events[0].Event.EventType)
	}
	var data map[string]any
	_ = json.Unmarshal(events[0].Event.Data, &data)
	if data["hello"] != "world" {
		t.Errorf("payload roundtrip failed: %v", data)
	}
}

// TestStreamSubscriber_Replay confirms that a client reconnecting with
// a `since` cursor receives the messages it missed while disconnected.
// This is the headline benefit of Streams over Pub/Sub.
func TestStreamSubscriber_Replay(t *testing.T) {
	rdb := streamsTestClient(t)
	defer rdb.Close()
	topic := "test.rt.replay." + time.Now().Format("150405.000000")
	defer rdb.Del(context.Background(), StreamKey(topic))
	ctx := context.Background()

	pub := NewStreamPublisher(rdb)
	// Publish 3 events BEFORE any subscriber exists.
	for i := 0; i < 3; i++ {
		if err := pub.Publish(ctx, topic, "test.event", map[string]any{"i": i}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}

	// Client connects with since="0" — should receive all 3.
	sub := NewStreamSubscriber(rdb, []string{topic}, "0", 1*time.Second)
	events, err := sub.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("want 3 replayed events, got %d", len(events))
	}
	cursor := sub.LatestCursor()
	if cursor == "" {
		t.Fatal("expected non-empty cursor after replay")
	}

	// Now publish 2 more, and a fresh client connects with the prior
	// cursor — should receive only the 2 new ones.
	for i := 3; i < 5; i++ {
		if err := pub.Publish(ctx, topic, "test.event", map[string]any{"i": i}); err != nil {
			t.Fatalf("publish %d: %v", i, err)
		}
	}
	sub2 := NewStreamSubscriber(rdb, []string{topic}, cursor, 1*time.Second)
	events2, err := sub2.Read(ctx)
	if err != nil {
		t.Fatalf("read2: %v", err)
	}
	if len(events2) != 2 {
		t.Fatalf("want 2 incremental events, got %d", len(events2))
	}
}

// TestPublisher_BackCompat confirms the legacy Publisher.Publish path
// (which now double-writes) still puts data into the stream so
// migrated subscribers see it without a producer code change.
func TestPublisher_BackCompat(t *testing.T) {
	rdb := streamsTestClient(t)
	defer rdb.Close()
	topic := "test.rt.compat." + time.Now().Format("150405.000000")
	defer rdb.Del(context.Background(), StreamKey(topic))

	legacy := NewPublisher(rdb)
	if err := legacy.Publish(context.Background(), topic, "compat.event", map[string]any{"k": "v"}); err != nil {
		t.Fatalf("legacy publish: %v", err)
	}
	sub := NewStreamSubscriber(rdb, []string{topic}, "0", 1*time.Second)
	events, err := sub.Read(context.Background())
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("want 1 event from legacy publisher via stream, got %d", len(events))
	}
}
