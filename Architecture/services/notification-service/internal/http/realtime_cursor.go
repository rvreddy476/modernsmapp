package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/atpost/shared/realtime"
	"github.com/redis/go-redis/v9"
)

// Per-topic resume cursors for GET /v1/realtime/sse (lane B5b, 2026-09-13).
//
// THE DEFECT. Redis Stream ids are per stream: "1717000000123-0" on
// rts:food.order.A says nothing about rts:food.restaurant.R.orders. The
// handler used to take ONE id (Last-Event-ID or ?since=), start every topic
// from it, and emit each event's own stream id as the SSE id. A client
// following two topics resumed from whichever topic's id it saw last, so the
// other topic replayed (its ids were lower) or skipped (its ids were higher —
// e.g. a 100-message XREAD page on one stream beside one newer message on
// another).
//
// THE FORMAT. A resume cursor is a comma-separated list of topic=streamID
// pairs, in the connection's topic order:
//
//	food.order.9f…=1717000000123-0,food.restaurant.4c….orders=1717000000456-2
//
// Every `id:` the handler emits is the WHOLE vector as of that event: the
// event's own topic advanced, every other topic at the position already
// delivered on this connection. Whichever id the client saw last — the only
// one an EventSource sends back — therefore resumes EVERY topic exactly where
// the client left off: no replay, no gap. The `connected` frame carries an id
// too, so a client that received no events still resumes without a gap.
//
// Parsing (parseResumeCursor):
//   - empty → live tail on every topic.
//   - one bare stream id ("1717…-0" or "1717…") → LEGACY single cursor from
//     pre-B5b clients. It is applied to every topic, which is exact for the
//     one-topic connection those clients open. With several topics it is the
//     old best-effort behaviour — still better than dropping the gap — and
//     the first vector id this handler emits replaces it.
//   - topic=id pairs → per-topic. Pairs naming topics not on this connection
//     are ignored; topics with no pair start live.
//   - anything else (bad id, empty topic, duplicate topic, stray separator)
//     → INVALID: live tail on every topic, and "resume":"invalid" in the
//     connected frame.
//
// Why invalid means live and not 400: EventSource retries with the same
// Last-Event-ID, and browsers stop reconnecting for good on a non-200. A
// corrupt cursor would brick realtime until a full reload, and mobile clients
// would loop on the 400. Starting live and saying so lets the client re-sync
// from REST, and the fresh vector on the connected frame replaces the bad
// cursor immediately.
//
// "Live" is resolved to a concrete position when the connection opens (the
// stream's newest id, or 0-0 for a stream that does not exist yet) instead of
// passing "$" to every XREAD: "$" means "newer than this call", so an event
// added while the handler was writing the previous batch was silently lost.
//
// Size: one pair per topic (topic length + ~16 bytes). Topics cannot contain
// ',' (the topics query is CSV) and the handler rejects topics containing '='
// or line breaks, so the format is unambiguous and cannot break SSE framing.

type resumeKind string

const (
	resumeNone    resumeKind = "none"
	resumeLegacy  resumeKind = "legacy"
	resumeVector  resumeKind = "vector"
	resumeInvalid resumeKind = "invalid"
)

// liveTail is XREAD's "only messages newer than this call" id. It survives in
// a cursor only when resolving the stream's newest id failed (degraded mode).
const liveTail = "$"

// cursorReadCount caps messages per stream per XREAD.
const cursorReadCount = 100

var (
	vectorStreamIDRe = regexp.MustCompile(`^[0-9]+-[0-9]+$`)
	legacyStreamIDRe = regexp.MustCompile(`^[0-9]+(-[0-9]+)?$`)
)

// validCursorTopic reports whether a topic can appear in a resume vector and
// an SSE id line without ambiguity or frame injection.
func validCursorTopic(topic string) bool {
	return topic != "" && !strings.ContainsAny(topic, "=,\r\n")
}

// parseResumeCursor returns the starting stream id for every topic and how
// the cursor was interpreted. See the format notes above.
func parseResumeCursor(raw string, topics []string) (map[string]string, resumeKind) {
	start := make(map[string]string, len(topics))
	for _, t := range topics {
		start[t] = liveTail
	}
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return start, resumeNone

	case legacyStreamIDRe.MatchString(raw):
		id := raw
		if !strings.Contains(id, "-") {
			id += "-0"
		}
		for _, t := range topics {
			start[t] = id
		}
		return start, resumeLegacy

	case strings.Contains(raw, "="):
		parsed := make(map[string]string)
		for _, pair := range strings.Split(raw, ",") {
			i := strings.LastIndex(pair, "=")
			if i <= 0 {
				return start, resumeInvalid
			}
			topic, id := pair[:i], pair[i+1:]
			if !validCursorTopic(topic) || !vectorStreamIDRe.MatchString(id) {
				return start, resumeInvalid
			}
			if _, dup := parsed[topic]; dup {
				return start, resumeInvalid
			}
			parsed[topic] = id
		}
		// Validation is complete before anything is applied, so an invalid
		// cursor above always leaves every topic live.
		for _, t := range topics {
			if id, ok := parsed[t]; ok {
				start[t] = id
			}
		}
		return start, resumeVector

	default:
		return start, resumeInvalid
	}
}

// encodeResumeCursor renders the vector in topic order. A topic still on
// liveTail (degraded) is omitted: it resumes live.
func encodeResumeCursor(topics []string, cursors map[string]string) string {
	var b strings.Builder
	for _, t := range topics {
		id := cursors[t]
		if id == "" || id == liveTail {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(t)
		b.WriteByte('=')
		b.WriteString(id)
	}
	return b.String()
}

// streamClient is the Redis surface the reader uses; *redis.Client satisfies
// it, and tests drive the reader with an in-memory stream store.
type streamClient interface {
	XRead(ctx context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd
	XRevRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
}

// cursorEvent is one stream message ready to write as an SSE frame.
type cursorEvent struct {
	Topic    string
	StreamID string
	// Cursor is the resume vector covering this event and everything
	// returned before it on this connection — the SSE `id:` value.
	Cursor string
	Event  realtime.Event
}

// topicCursorReader tails several topic streams with one cursor per topic.
type topicCursorReader struct {
	client  streamClient
	topics  []string
	cursors map[string]string
	block   time.Duration
}

func newTopicCursorReader(ctx context.Context, client streamClient, topics []string, since string, block time.Duration) (*topicCursorReader, resumeKind) {
	cursors, kind := parseResumeCursor(since, topics)
	if block <= 0 {
		block = 25 * time.Second
	}
	r := &topicCursorReader{
		client:  client,
		topics:  append([]string(nil), topics...),
		cursors: cursors,
		block:   block,
	}
	r.resolveLive(ctx)
	return r, kind
}

// resolveLive pins every live topic to the stream's current newest id.
func (r *topicCursorReader) resolveLive(ctx context.Context) {
	if r.client == nil {
		return
	}
	for _, t := range r.topics {
		if r.cursors[t] != liveTail {
			continue
		}
		msgs, err := r.client.XRevRangeN(ctx, realtime.StreamKey(t), "+", "-", 1).Result()
		if err != nil {
			// Degraded: stays "$" and out of the emitted vector until this
			// topic's first event arrives.
			continue
		}
		if len(msgs) == 0 {
			r.cursors[t] = "0-0"
			continue
		}
		r.cursors[t] = msgs[0].ID
	}
}

// Cursor is the resume vector for everything read so far.
func (r *topicCursorReader) Cursor() string {
	return encodeResumeCursor(r.topics, r.cursors)
}

// Read blocks up to the block window and returns new events across all
// topics, each carrying the resume vector as of that event. An empty slice
// with a nil error means the window passed with nothing new.
func (r *topicCursorReader) Read(ctx context.Context) ([]cursorEvent, error) {
	if len(r.topics) == 0 {
		return nil, nil
	}
	if r.client == nil {
		// No Redis: idle for one block window so the caller's keepalive loop
		// does not spin.
		select {
		case <-ctx.Done():
		case <-time.After(r.block):
		}
		return nil, nil
	}
	streams := make([]string, 0, 2*len(r.topics))
	for _, t := range r.topics {
		streams = append(streams, realtime.StreamKey(t))
	}
	for _, t := range r.topics {
		streams = append(streams, r.cursors[t])
	}
	res, err := r.client.XRead(ctx, &redis.XReadArgs{
		Streams: streams,
		Count:   cursorReadCount,
		Block:   r.block,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, nil
		}
		return nil, fmt.Errorf("realtime: XREAD: %w", err)
	}

	var out []cursorEvent
	for _, stream := range res {
		topic := realtime.TopicFromStreamKey(stream.Stream)
		if _, ok := r.cursors[topic]; !ok {
			continue
		}
		for _, msg := range stream.Messages {
			// Advance past undecodable messages too, or they are re-read
			// forever.
			r.cursors[topic] = msg.ID
			payload, ok := msg.Values["payload"].(string)
			if !ok {
				continue
			}
			var env realtime.Event
			if err := json.Unmarshal([]byte(payload), &env); err != nil {
				continue
			}
			out = append(out, cursorEvent{
				Topic:    topic,
				StreamID: msg.ID,
				Cursor:   r.Cursor(),
				Event:    env,
			})
		}
	}
	return out, nil
}
