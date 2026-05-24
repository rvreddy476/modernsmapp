// streams.go — Redis Streams-backed realtime fanout.
//
// Why a sibling to Publisher / Subscriber:
//
// The original Pub/Sub-based realtime path is great for low-latency
// fire-and-forget pushes but has three operational limits that bite
// at scale:
//
//  1. Pub/Sub messages are not durable. Any subscriber that wasn't
//     connected at the moment XADD ran loses the message forever.
//     For an SSE gateway in front of millions of mobile clients, a
//     5-second disconnect == missed notifications.
//  2. No "since" cursor. A client reconnecting can't ask the server
//     "give me what I missed between 12:01:03 and now."
//  3. No backpressure visibility. A slow subscriber's buffer fills
//     and Redis silently drops messages.
//
// Redis Streams solves all three: XADD MAXLEN ~10000 keeps a bounded
// ring of recent messages per topic; XREAD BLOCK with a last-seen id
// gives the client both live-tail and gap-fill in one primitive.
//
// API parity: StreamPublisher.Publish mirrors Publisher.Publish so
// the call-site change is one constructor swap. StreamSubscriber.Read
// returns the same Event shape so handlers don't need new parsing.
package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// streamPrefix is the keyspace prefix for Redis Streams. Kept separate
// from `rt:` (Pub/Sub) so the two systems can coexist during rollout.
const streamPrefix = "rts:"

// maxStreamLen caps each topic's stream to ~10000 messages. At ~512 B
// per message that's ~5 MB per topic — bounded memory even for the
// highest-volume topics. MAXLEN ~ (approximate) is cheaper than = and
// is the recommended Redis-Streams trim strategy.
const maxStreamLen int64 = 10000

// StreamPublisher pushes events onto topic-scoped Redis Streams.
type StreamPublisher struct {
	rdb *redis.Client
}

// NewStreamPublisher returns a StreamPublisher backed by the supplied
// Redis client. Same constructor shape as NewPublisher for drop-in
// swap.
func NewStreamPublisher(rdb *redis.Client) *StreamPublisher {
	return &StreamPublisher{rdb: rdb}
}

// Publish writes the event to the topic's stream. The XADD uses a
// MAXLEN approximation so old messages are trimmed automatically —
// no separate trimmer cron required.
//
// On Redis outage Publish returns an error; callers typically
// log-and-continue because Kafka is the durable copy.
func (p *StreamPublisher) Publish(ctx context.Context, topic, eventType string, data any) error {
	if p == nil || p.rdb == nil {
		return nil
	}
	if topic == "" {
		return errors.New("realtime: empty topic")
	}
	raw, err := json.Marshal(data)
	if err != nil {
		return fmt.Errorf("realtime: marshal payload: %w", err)
	}
	env := Event{
		Topic:     topic,
		EventType: eventType,
		Data:      raw,
		EmittedAt: time.Now().UTC(),
	}
	body, err := json.Marshal(env)
	if err != nil {
		return fmt.Errorf("realtime: marshal envelope: %w", err)
	}
	_, err = p.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: StreamKey(topic),
		MaxLen: maxStreamLen,
		Approx: true,
		Values: map[string]any{
			"event_type": eventType,
			"payload":    body,
		},
	}).Result()
	if err != nil {
		return fmt.Errorf("realtime: XADD %s: %w", topic, err)
	}
	return nil
}

// StreamKey returns the namespaced key name for a topic's stream.
func StreamKey(topic string) string { return streamPrefix + topic }

// TopicFromStreamKey undoes StreamKey. Non-namespaced inputs are
// returned unchanged.
func TopicFromStreamKey(key string) string {
	return strings.TrimPrefix(key, streamPrefix)
}

// StreamSubscriber holds the cursor + topic set for one SSE/WS client.
// Construct one per connection and call Read in a loop until the
// caller context is cancelled.
type StreamSubscriber struct {
	rdb     *redis.Client
	topics  []string
	cursors map[string]string // topic → last-delivered stream ID
	block   time.Duration
}

// NewStreamSubscriber initializes a subscriber. `since` is the last
// stream id the client saw (or "" / "$" for live-tail). The same
// `since` is used as the starting cursor for every topic — the
// expected client behavior is to persist the latest stream id it
// received across topics, since per-topic cursors would require the
// client to track N states.
func NewStreamSubscriber(rdb *redis.Client, topics []string, since string, block time.Duration) *StreamSubscriber {
	cursors := make(map[string]string, len(topics))
	start := since
	if start == "" {
		// "$" tells XREAD to return only messages newer than the call
		// — the standard live-tail pattern.
		start = "$"
	}
	for _, t := range topics {
		cursors[t] = start
	}
	if block <= 0 {
		block = 25 * time.Second
	}
	return &StreamSubscriber{rdb: rdb, topics: topics, cursors: cursors, block: block}
}

// DeliveredEvent is one Event plus the stream id the client should
// persist as its cursor. Cursor format is whatever Redis gave us
// (e.g. "1717000000000-0"); the client treats it as opaque.
type DeliveredEvent struct {
	StreamID string
	Topic    string
	Event    Event
}

// Read blocks for up to `block` (default 25s) and returns any new
// events across all subscribed topics. An empty slice + nil error
// means "no events within the block window" — the caller should
// emit an SSE keepalive and loop. Cancel ctx to break out of the
// XREAD.
func (s *StreamSubscriber) Read(ctx context.Context) ([]DeliveredEvent, error) {
	if s == nil || s.rdb == nil || len(s.topics) == 0 {
		return nil, nil
	}
	streams := make([]string, 0, 2*len(s.topics))
	for _, t := range s.topics {
		streams = append(streams, StreamKey(t))
	}
	for _, t := range s.topics {
		streams = append(streams, s.cursors[t])
	}
	res, err := s.rdb.XRead(ctx, &redis.XReadArgs{
		Streams: streams,
		Count:   100,
		Block:   s.block,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, fmt.Errorf("realtime: XREAD: %w", err)
	}

	var out []DeliveredEvent
	for _, stream := range res {
		topic := TopicFromStreamKey(stream.Stream)
		for _, msg := range stream.Messages {
			var env Event
			// The XADD wrote "payload" as a JSON []byte; XREAD returns
			// it as a string.
			payloadStr, ok := msg.Values["payload"].(string)
			if !ok {
				continue
			}
			if err := json.Unmarshal([]byte(payloadStr), &env); err != nil {
				continue
			}
			out = append(out, DeliveredEvent{
				StreamID: msg.ID,
				Topic:    topic,
				Event:    env,
			})
			// Advance cursor for this topic so the next XREAD picks up
			// where we left off, even if the caller is consuming the
			// batch slowly.
			s.cursors[topic] = msg.ID
		}
	}
	return out, nil
}

// LatestCursor returns the highest stream id observed across all
// topics. The SSE handler ships this back to the client as the
// "Last-Event-ID" header so reconnects can resume.
func (s *StreamSubscriber) LatestCursor() string {
	var latest string
	for _, c := range s.cursors {
		if c == "$" || c == "" {
			continue
		}
		if latest == "" || c > latest {
			latest = c
		}
	}
	return latest
}
