package http

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/realtime"
	"github.com/redis/go-redis/v9"
)

// fakeStreamStore is an in-memory Redis Streams stand-in with XREAD's
// per-stream "strictly after this id" semantics. It never blocks, so "$"
// (newer than this call) always yields nothing.
type fakeStreamStore struct {
	streams      map[string][]redis.XMessage
	failRevRange bool
}

func newFakeStreamStore() *fakeStreamStore {
	return &fakeStreamStore{streams: map[string][]redis.XMessage{}}
}

func (f *fakeStreamStore) add(topic, id string) {
	body, _ := json.Marshal(realtime.Event{Topic: topic, EventType: "test", Data: json.RawMessage(`{}`)})
	key := realtime.StreamKey(topic)
	f.streams[key] = append(f.streams[key], redis.XMessage{
		ID:     id,
		Values: map[string]interface{}{"event_type": "test", "payload": string(body)},
	})
	msgs := f.streams[key]
	sort.Slice(msgs, func(i, j int) bool { return streamIDLess(msgs[i].ID, msgs[j].ID) })
}

func parseStreamID(id string) (uint64, uint64) {
	parts := strings.SplitN(id, "-", 2)
	ms, _ := strconv.ParseUint(parts[0], 10, 64)
	var seq uint64
	if len(parts) == 2 {
		seq, _ = strconv.ParseUint(parts[1], 10, 64)
	}
	return ms, seq
}

func streamIDLess(a, b string) bool {
	am, as := parseStreamID(a)
	bm, bs := parseStreamID(b)
	return am < bm || (am == bm && as < bs)
}

func (f *fakeStreamStore) XRead(_ context.Context, a *redis.XReadArgs) *redis.XStreamSliceCmd {
	n := len(a.Streams) / 2
	var out []redis.XStream
	for i := 0; i < n; i++ {
		key, after := a.Streams[i], a.Streams[n+i]
		if after == liveTail {
			continue
		}
		var msgs []redis.XMessage
		for _, m := range f.streams[key] {
			if streamIDLess(after, m.ID) {
				msgs = append(msgs, m)
				if a.Count > 0 && int64(len(msgs)) == a.Count {
					break
				}
			}
		}
		if len(msgs) > 0 {
			out = append(out, redis.XStream{Stream: key, Messages: msgs})
		}
	}
	if len(out) == 0 {
		return redis.NewXStreamSliceCmdResult(nil, redis.Nil)
	}
	return redis.NewXStreamSliceCmdResult(out, nil)
}

func (f *fakeStreamStore) XRevRangeN(_ context.Context, stream, _, _ string, _ int64) *redis.XMessageSliceCmd {
	if f.failRevRange {
		return redis.NewXMessageSliceCmdResult(nil, fmt.Errorf("redis unavailable"))
	}
	msgs := f.streams[stream]
	if len(msgs) == 0 {
		return redis.NewXMessageSliceCmdResult([]redis.XMessage{}, nil)
	}
	return redis.NewXMessageSliceCmdResult([]redis.XMessage{msgs[len(msgs)-1]}, nil)
}

func drainCursorReader(t *testing.T, r *topicCursorReader) []cursorEvent {
	t.Helper()
	var all []cursorEvent
	for i := 0; i < 100; i++ {
		batch, err := r.Read(context.Background())
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if len(batch) == 0 {
			return all
		}
		all = append(all, batch...)
	}
	t.Fatal("reader never drained")
	return nil
}

func eventKeys(events []cursorEvent) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, e.Topic+"@"+e.StreamID)
	}
	return out
}

const (
	cursorTopicA = "food.order.a"
	cursorTopicB = "food.restaurant.r.orders"
)

// THE defect (B5b): two topics whose stream ids interleave. A's page holds ids
// both above and below B's, so any single id the client keeps is wrong for one
// of the topics. For every point at which the connection can drop, the
// reconnect must deliver every event exactly once.
func TestResumeCursor_TwoTopicsInterleaved_ResumeWithoutLossOrDuplication(t *testing.T) {
	topics := []string{cursorTopicA, cursorTopicB}
	want := []string{
		cursorTopicA + "@5-0", cursorTopicA + "@7-0",
		cursorTopicB + "@3-0", cursorTopicB + "@6-0",
		cursorTopicA + "@9-0", cursorTopicB + "@8-0",
	}
	for cut := 0; cut <= 4; cut++ {
		t.Run(fmt.Sprintf("disconnect_after_%d_events", cut), func(t *testing.T) {
			ctx := context.Background()
			store := newFakeStreamStore()
			first, kind := newTopicCursorReader(ctx, store, topics, "", time.Second)
			if kind != resumeNone {
				t.Fatalf("fresh connection kind = %s, want none", kind)
			}
			// The id the connected frame carries.
			lastID := first.Cursor()

			store.add(cursorTopicA, "5-0")
			store.add(cursorTopicA, "7-0")
			store.add(cursorTopicB, "3-0")
			store.add(cursorTopicB, "6-0")
			batch := drainCursorReader(t, first)
			if len(batch) != 4 {
				t.Fatalf("first connection read %v, want 4 events", eventKeys(batch))
			}

			seen := map[string]int{}
			for _, e := range batch[:cut] {
				seen[e.Topic+"@"+e.StreamID]++
				lastID = e.Cursor
			}

			// Connection drops; more events land before the reconnect.
			store.add(cursorTopicA, "9-0")
			store.add(cursorTopicB, "8-0")

			second, kind := newTopicCursorReader(ctx, store, topics, lastID, time.Second)
			if kind != resumeVector {
				t.Fatalf("resume id %q parsed as %s, want vector", lastID, kind)
			}
			for _, e := range drainCursorReader(t, second) {
				seen[e.Topic+"@"+e.StreamID]++
			}
			for _, k := range want {
				if seen[k] != 1 {
					t.Errorf("%s delivered %d times across the reconnect, want exactly once (resume id %q)", k, seen[k], lastID)
				}
			}
			if len(seen) != len(want) {
				t.Errorf("unexpected events delivered: %v", seen)
			}
		})
	}
}

// Pre-B5b clients send one bare stream id for their one topic.
func TestResumeCursor_LegacySingleIDStillResumesOneTopic(t *testing.T) {
	for _, since := range []string{"2-0", "2"} {
		t.Run(since, func(t *testing.T) {
			store := newFakeStreamStore()
			store.add(cursorTopicA, "1-0")
			store.add(cursorTopicA, "2-0")
			store.add(cursorTopicA, "3-0")
			r, kind := newTopicCursorReader(context.Background(), store, []string{cursorTopicA}, since, time.Second)
			if kind != resumeLegacy {
				t.Fatalf("kind = %s, want legacy", kind)
			}
			got := eventKeys(drainCursorReader(t, r))
			if len(got) != 1 || got[0] != cursorTopicA+"@3-0" {
				t.Fatalf("legacy resume from %q delivered %v, want only 3-0", since, got)
			}
			if c := r.Cursor(); c != cursorTopicA+"=3-0" {
				t.Fatalf("cursor after legacy resume = %q", c)
			}
		})
	}
}

// A malformed cursor starts live (no replay of old events, nothing new lost)
// and is reported as invalid rather than rejected — see realtime_cursor.go.
func TestResumeCursor_MalformedStartsLive(t *testing.T) {
	bad := []string{
		"garbage",
		cursorTopicA + "=notanid",
		"=1-0",
		"1-0-0",
		"-1-0",
		cursorTopicA + "=1",
		cursorTopicA + "=1-0,",
		cursorTopicA + "=1-0," + cursorTopicA + "=2-0",
	}
	for _, since := range bad {
		t.Run(since, func(t *testing.T) {
			store := newFakeStreamStore()
			store.add(cursorTopicA, "1-0")
			store.add(cursorTopicA, "2-0")
			r, kind := newTopicCursorReader(context.Background(), store, []string{cursorTopicA}, since, time.Second)
			if kind != resumeInvalid {
				t.Fatalf("kind = %s, want invalid", kind)
			}
			if got := drainCursorReader(t, r); len(got) != 0 {
				t.Fatalf("malformed cursor replayed history: %v", eventKeys(got))
			}
			store.add(cursorTopicA, "3-0")
			got := eventKeys(drainCursorReader(t, r))
			if len(got) != 1 || got[0] != cursorTopicA+"@3-0" {
				t.Fatalf("live tail after malformed cursor delivered %v, want 3-0", got)
			}
		})
	}
}

// Pairs for topics not on this connection are ignored; a topic with no pair
// starts live.
func TestResumeCursor_VectorIgnoresForeignTopicsAndStartsMissingTopicsLive(t *testing.T) {
	store := newFakeStreamStore()
	for _, topic := range []string{cursorTopicA, cursorTopicB} {
		store.add(topic, "1-0")
		store.add(topic, "2-0")
	}
	since := cursorTopicA + "=1-0,food.order.someone_else=0-0"
	r, kind := newTopicCursorReader(context.Background(), store, []string{cursorTopicA, cursorTopicB}, since, time.Second)
	if kind != resumeVector {
		t.Fatalf("kind = %s, want vector", kind)
	}
	got := eventKeys(drainCursorReader(t, r))
	if len(got) != 1 || got[0] != cursorTopicA+"@2-0" {
		t.Fatalf("delivered %v, want only %s@2-0", got, cursorTopicA)
	}
}

// Live start is pinned at connect: an event landing before the first XREAD is
// delivered (a bare "$" would have dropped it) and older history is not.
func TestResumeCursor_LiveStartKeepsEventsBetweenReads(t *testing.T) {
	store := newFakeStreamStore()
	store.add(cursorTopicA, "1-0")
	r, _ := newTopicCursorReader(context.Background(), store, []string{cursorTopicA}, "", time.Second)
	if c := r.Cursor(); c != cursorTopicA+"=1-0" {
		t.Fatalf("connected-frame cursor = %q, want the stream tip", c)
	}
	store.add(cursorTopicA, "2-0")
	got := eventKeys(drainCursorReader(t, r))
	if len(got) != 1 || got[0] != cursorTopicA+"@2-0" {
		t.Fatalf("delivered %v, want only 2-0", got)
	}
}

// Degraded mode: when the tip cannot be resolved the topic falls back to "$"
// and stays out of the vector rather than emitting a position it never had.
func TestResumeCursor_UnresolvableTipIsOmittedFromVector(t *testing.T) {
	store := newFakeStreamStore()
	store.failRevRange = true
	r, _ := newTopicCursorReader(context.Background(), store, []string{cursorTopicA}, "", time.Second)
	if c := r.Cursor(); c != "" {
		t.Fatalf("cursor = %q, want empty while the tip is unknown", c)
	}
}

func TestResumeCursor_EncodeParseRoundTrip(t *testing.T) {
	topics := []string{cursorTopicA, cursorTopicB}
	in := map[string]string{cursorTopicA: "1717000000123-4", cursorTopicB: "0-0"}
	encoded := encodeResumeCursor(topics, in)
	if strings.ContainsAny(encoded, "\r\n") {
		t.Fatalf("cursor would break SSE framing: %q", encoded)
	}
	out, kind := parseResumeCursor(encoded, topics)
	if kind != resumeVector {
		t.Fatalf("kind = %s", kind)
	}
	for _, topic := range topics {
		if out[topic] != in[topic] {
			t.Fatalf("round trip %s: got %q want %q", topic, out[topic], in[topic])
		}
	}
}

func TestValidCursorTopic(t *testing.T) {
	for _, topic := range []string{"", "a=b", "a,b", "a\nb", "a\rb"} {
		if validCursorTopic(topic) {
			t.Errorf("%q accepted as a cursor topic", topic)
		}
	}
	for _, topic := range []string{cursorTopicA, cursorTopicB, "food.delivery_partner.u.assignments", "food.order.*"} {
		if !validCursorTopic(topic) {
			t.Errorf("%q rejected", topic)
		}
	}
}
