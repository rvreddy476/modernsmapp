package dlqreplay

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/segmentio/kafka-go"
)

// A fake topic: what the shared consumer writes to a dead-letter topic,
// replayed through a fake writer that appends to the same in-memory topics.

type fakeKafka struct {
	topics  map[string][]kafka.Message
	failOn  int // fail the n-th write (1-based); 0 never
	writes  int
	cleared []string
}

func newFakeKafka() *fakeKafka { return &fakeKafka{topics: map[string][]kafka.Message{}} }

func (k *fakeKafka) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	for _, m := range msgs {
		k.writes++
		if k.writes == k.failOn {
			return errors.New("broker refused")
		}
		m.Offset = int64(len(k.topics[m.Topic]))
		k.topics[m.Topic] = append(k.topics[m.Topic], m)
	}
	return nil
}

func (k *fakeKafka) Clear(_ context.Context, key string) error {
	k.cleared = append(k.cleared, key)
	return nil
}

func dead(offset int64, source, group, eventID, eventType string) kafka.Message {
	return kafka.Message{
		Topic: "media.events.dlq", Partition: 0, Offset: offset, Key: []byte("asset-" + eventID),
		Value: []byte(`{"event_id":"` + eventID + `","event_type":"` + eventType + `"}`),
		Headers: []kafka.Header{
			{Key: "traceparent", Value: []byte("00-abc-def-01")},
			{Key: HeaderError, Value: []byte("post not found")},
			{Key: HeaderConsumerGroup, Value: []byte(group)},
			{Key: HeaderOriginalTopic, Value: []byte(source)},
		},
	}
}

func seeded() *fakeKafka {
	k := newFakeKafka()
	k.topics["media.events.dlq"] = []kafka.Message{
		dead(0, "media.events", "post-service-media-transcode", "e1", "TranscodeCompleted"),
		dead(1, "media.events", "post-service-media-transcode", "e2", "TranscodeFailed"),
		dead(2, "media.events", "post-service-live-vod", "e3", "TranscodeCompleted"),
	}
	return k
}

func refs(es []Entry) string {
	var out []string
	for _, e := range es {
		out = append(out, e.Ref())
	}
	return strings.Join(out, ",")
}

func TestSelect(t *testing.T) {
	entries := Load(seeded().topics["media.events.dlq"])
	cases := []struct {
		name string
		sel  Selection
		want string
		err  bool
	}{
		{"all", Selection{All: true}, "0/0,0/1,0/2", false},
		{"all of one group", Selection{All: true, Group: "post-service-media-transcode"}, "0/0,0/1", false},
		{"all of one event type", Selection{All: true, EventType: "TranscodeCompleted"}, "0/0,0/2", false},
		{"named", Selection{Refs: []string{"0/1"}}, "0/1", false},
		{"named outside the group filter", Selection{Refs: []string{"0/2"}, Group: "post-service-media-transcode"}, "", false},
		{"a ref not on the topic", Selection{Refs: []string{"0/9"}}, "", true},
		{"nothing chosen", Selection{}, "", true},
		{"both", Selection{All: true, Refs: []string{"0/1"}}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := Select(entries, tc.sel)
			if (err != nil) != tc.err {
				t.Fatalf("err = %v, want error %v", err, tc.err)
			}
			if refs(got) != tc.want {
				t.Fatalf("selected %q, want %q", refs(got), tc.want)
			}
		})
	}
}

func TestReplay_DryRunWritesNothing(t *testing.T) {
	k := seeded()
	chosen, _, _ := Select(Load(k.topics["media.events.dlq"]), Selection{All: true})
	rep, err := Replay(context.Background(), "media.events.dlq", chosen, k, k, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Replayed) != 3 || k.writes != 0 || len(k.cleared) != 0 {
		t.Fatalf("dry run: report %+v, writes %d, receipts cleared %v", rep, k.writes, k.cleared)
	}
}

func TestReplay_RepublishesToTheSourceAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	k := seeded()
	chosen, _, _ := Select(Load(k.topics["media.events.dlq"]), Selection{Refs: []string{"0/1"}})
	if _, err := Replay(ctx, "media.events.dlq", chosen, k, k, true); err != nil {
		t.Fatal(err)
	}
	out := k.topics["media.events"]
	if len(out) != 1 {
		t.Fatalf("source topic got %d messages, want 1", len(out))
	}
	m := out[0]
	if string(m.Value) != `{"event_id":"e2","event_type":"TranscodeFailed"}` || string(m.Key) != "asset-e2" {
		t.Fatalf("the replay changed the message: key %q value %s", m.Key, m.Value)
	}
	if header(m.Headers, HeaderError) != "" || header(m.Headers, HeaderConsumerGroup) != "" || header(m.Headers, HeaderOriginalTopic) != "" {
		t.Fatalf("dead-letter headers rode along: %v", m.Headers)
	}
	if header(m.Headers, "traceparent") == "" || header(m.Headers, HeaderReplayedFrom) != "media.events.dlq/0/1" {
		t.Fatalf("headers %v: want traceparent kept and x-dlq-replayed-from set", m.Headers)
	}
	// Only the group that dead-lettered it has its receipt cleared.
	if len(k.cleared) != 1 || k.cleared[0] != "consumed:post-service-media-transcode:media.events:e2" {
		t.Fatalf("receipts cleared %v", k.cleared)
	}

	// A second run, of everything, does not send 0/1 again.
	chosen, skipped, _ := Select(Load(k.topics["media.events.dlq"]), Selection{All: true})
	if refs(chosen) != "0/0,0/2" || refs(skipped) != "0/1" {
		t.Fatalf("after one replay: chosen %q skipped %q", refs(chosen), refs(skipped))
	}
	if _, err := Replay(ctx, "media.events.dlq", chosen, k, k, true); err != nil {
		t.Fatal(err)
	}
	chosen, _, _ = Select(Load(k.topics["media.events.dlq"]), Selection{All: true})
	if len(chosen) != 0 || len(k.topics["media.events"]) != 3 {
		t.Fatalf("a third run would replay %q; source has %d", refs(chosen), len(k.topics["media.events"]))
	}
}

func TestReplay_RefusesAForeignSource(t *testing.T) {
	k := newFakeKafka()
	k.topics["media.events.dlq"] = []kafka.Message{dead(0, "payments.events", "x", "e9", "Captured")}
	chosen, _, _ := Select(Load(k.topics["media.events.dlq"]), Selection{All: true})
	rep, err := Replay(context.Background(), "media.events.dlq", chosen, k, k, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Refused) != 1 || k.writes != 0 {
		t.Fatalf("a message from another topic was written: %+v, writes %d", rep, k.writes)
	}
}

func TestReplay_NeedsReceiptsWhenTheGroupDedupes(t *testing.T) {
	k := seeded()
	chosen, _, _ := Select(Load(k.topics["media.events.dlq"]), Selection{Refs: []string{"0/0"}})
	if _, err := Replay(context.Background(), "media.events.dlq", chosen, k, nil, true); err == nil || k.writes != 0 {
		t.Fatalf("replayed without clearing the receipt: err %v, writes %d", err, k.writes)
	}
}

func TestReplay_StopsAtTheFirstFailedWrite(t *testing.T) {
	k := seeded()
	k.failOn = 3 // 0/0 and its marker succeed; 0/1's re-publish fails
	chosen, _, _ := Select(Load(k.topics["media.events.dlq"]), Selection{All: true})
	rep, err := Replay(context.Background(), "media.events.dlq", chosen, k, k, true)
	if err == nil {
		t.Fatal("want the write error")
	}
	if strings.Join(rep.Replayed, ",") != "0/0" || len(k.topics["media.events"]) != 1 {
		t.Fatalf("report %+v, source %d", rep, len(k.topics["media.events"]))
	}
}

func TestGuardEnvironment(t *testing.T) {
	for _, tc := range []struct {
		env, confirm string
		ok           bool
	}{
		{"dev", "", true},
		{"staging", "", true},
		{"prod", "", false},
		{"production", "", false},
		{"PROD", "other.dlq", false},
		{"prod", "media.events.dlq", true},
	} {
		err := GuardEnvironment(tc.env, "media.events.dlq", tc.confirm)
		if (err == nil) != tc.ok {
			t.Errorf("env %q confirm %q: err %v, want ok %v", tc.env, tc.confirm, err, tc.ok)
		}
	}
}

func TestSourceOfAndParseRefs(t *testing.T) {
	if s, err := SourceOf("media.events.dlq"); err != nil || s != "media.events" {
		t.Fatalf("SourceOf = %q %v", s, err)
	}
	if _, err := SourceOf("media.events"); err == nil {
		t.Fatal("a topic without .dlq was accepted as a dead-letter topic")
	}
	if r, err := ParseRefs("0/12, 1/7"); err != nil || strings.Join(r, ",") != "0/12,1/7" {
		t.Fatalf("ParseRefs = %v %v", r, err)
	}
	for _, bad := range []string{"12", "a/1", "0/x"} {
		if _, err := ParseRefs(bad); err == nil {
			t.Errorf("ParseRefs(%q) accepted", bad)
		}
	}
}
