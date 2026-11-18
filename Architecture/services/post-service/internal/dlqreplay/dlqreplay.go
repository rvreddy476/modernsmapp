// Package dlqreplay lists a dead-letter topic and re-publishes chosen
// messages to the topic they came from (condition 6 / reels-5, 18 Nov 2026).
// cmd/dlq-replay is the operator command; docs/runbooks/dlq-replay.md is how
// to use it.
//
// Safety, in the order it is applied:
//
//   - Dry run unless the caller asks to write: Replay with write=false only
//     reports what it would do.
//   - Production is refused unless the operator names the topic again
//     (GuardEnvironment).
//   - Only back to the topic the dead-letter topic belongs to: a message
//     whose recorded source is not <dlq> minus ".dlq" is never written.
//   - Idempotent: after a message is re-published, a marker naming its
//     partition and offset is written to the dead-letter topic itself. A
//     later run reads the markers and never re-publishes that message again.
//     A crash between the two writes can re-publish one message twice; the
//     consumers dedupe on the event id and their handlers are at-least-once.
//   - The consumer that dead-lettered the message wrote its dedupe receipt
//     for the event (shared/kafka consumer: a dead letter is a terminal
//     disposition), so a replay would be dropped as a duplicate. The receipt
//     of THAT consumer group only is cleared just before the re-publish;
//     every other group on the topic keeps its receipt and drops the repeat.
package dlqreplay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/segmentio/kafka-go"
)

// Headers the dead-letter writers set. shared/kafka (post-service's
// consumers) writes x-dlq-*; notification-service's call consumer writes
// x-source-topic and x-quarantine-reason.
const (
	HeaderOriginalTopic = "x-dlq-original-topic"
	HeaderConsumerGroup = "x-dlq-consumer-group"
	HeaderError         = "x-dlq-error"
	HeaderSourceTopic   = "x-source-topic"
	HeaderQuarantine    = "x-quarantine-reason"

	// HeaderReplayedFrom rides the re-published message: <dlq>/<partition>/<offset>.
	HeaderReplayedFrom = "x-dlq-replayed-from"
	// HeaderReplayMarker marks a marker message on the dead-letter topic:
	// <partition>/<offset> of the message it says was replayed.
	HeaderReplayMarker = "x-dlq-replay-of"
)

// Entry is one dead-lettered message.
type Entry struct {
	Partition   int
	Offset      int64
	Key, Value  []byte
	Headers     []kafka.Header
	SourceTopic string
	Group       string
	Error       string
	EventID     string
	EventType   string
	Time        time.Time
	// Replayed: a marker says this message was already re-published.
	Replayed bool
}

// Ref is the entry's address on the dead-letter topic: "partition/offset".
func (e Entry) Ref() string { return fmt.Sprintf("%d/%d", e.Partition, e.Offset) }

func header(hs []kafka.Header, key string) string {
	for _, h := range hs {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

// Load turns the messages read from a dead-letter topic into entries, in
// partition and offset order, with the replay markers applied.
func Load(msgs []kafka.Message) []Entry {
	replayed := map[string]bool{}
	var out []Entry
	for _, m := range msgs {
		if ref := header(m.Headers, HeaderReplayMarker); ref != "" {
			replayed[ref] = true
			continue
		}
		e := Entry{
			Partition: m.Partition, Offset: m.Offset, Key: m.Key, Value: m.Value, Headers: m.Headers, Time: m.Time,
			Group: header(m.Headers, HeaderConsumerGroup),
			Error: header(m.Headers, HeaderError),
		}
		e.SourceTopic = header(m.Headers, HeaderOriginalTopic)
		if e.SourceTopic == "" {
			e.SourceTopic = header(m.Headers, HeaderSourceTopic)
		}
		if e.Error == "" {
			e.Error = header(m.Headers, HeaderQuarantine)
		}
		var env struct {
			EventID   string `json:"event_id"`
			EventType string `json:"event_type"`
		}
		if json.Unmarshal(m.Value, &env) == nil {
			e.EventID, e.EventType = env.EventID, env.EventType
		}
		out = append(out, e)
	}
	for i := range out {
		out[i].Replayed = replayed[out[i].Ref()]
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Partition != out[j].Partition {
			return out[i].Partition < out[j].Partition
		}
		return out[i].Offset < out[j].Offset
	})
	return out
}

// Selection says which entries to replay. Exactly one of All and Refs.
type Selection struct {
	All  bool
	Refs []string // "partition/offset"
	// Optional filters, applied to both.
	Group     string
	EventType string
}

// ParseRefs reads "0/12,1/7".
func ParseRefs(s string) ([]string, error) {
	var out []string
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, o, ok := strings.Cut(part, "/")
		if !ok {
			return nil, fmt.Errorf("offset %q: want partition/offset", part)
		}
		if _, err := strconv.Atoi(p); err != nil {
			return nil, fmt.Errorf("offset %q: bad partition", part)
		}
		if _, err := strconv.ParseInt(o, 10, 64); err != nil {
			return nil, fmt.Errorf("offset %q: bad offset", part)
		}
		out = append(out, part)
	}
	return out, nil
}

// ErrNoSelection: neither -all nor -offsets.
var ErrNoSelection = errors.New("choose what to replay: -all or -offsets partition/offset,...")

// Select picks the entries to replay. Already replayed entries are never
// selected (they are reported as skipped); a named ref that is not on the
// topic is an error, so a typo cannot quietly replay nothing.
func Select(entries []Entry, sel Selection) (chosen []Entry, skipped []Entry, err error) {
	if sel.All == (len(sel.Refs) > 0) {
		return nil, nil, ErrNoSelection
	}
	want := map[string]bool{}
	for _, r := range sel.Refs {
		want[r] = true
	}
	found := map[string]bool{}
	for _, e := range entries {
		if !sel.All && !want[e.Ref()] {
			continue
		}
		found[e.Ref()] = true
		if sel.Group != "" && e.Group != sel.Group {
			continue
		}
		if sel.EventType != "" && e.EventType != sel.EventType {
			continue
		}
		if e.Replayed {
			skipped = append(skipped, e)
			continue
		}
		chosen = append(chosen, e)
	}
	for _, r := range sel.Refs {
		if !found[r] {
			return nil, nil, fmt.Errorf("offset %s is not on the dead-letter topic", r)
		}
	}
	return chosen, skipped, nil
}

// SourceOf is the topic a dead-letter topic belongs to: "media.events.dlq"
// → "media.events".
func SourceOf(dlqTopic string) (string, error) {
	src, ok := strings.CutSuffix(dlqTopic, ".dlq")
	if !ok || src == "" {
		return "", fmt.Errorf("%q is not a dead-letter topic (want <topic>.dlq)", dlqTopic)
	}
	return src, nil
}

// GuardEnvironment refuses production unless confirm names the dead-letter
// topic again.
func GuardEnvironment(env, dlqTopic, confirm string) error {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "prod", "production":
		if confirm != dlqTopic {
			return fmt.Errorf("refusing to run against production (ENV=%s): pass -confirm-production %s", env, dlqTopic)
		}
	}
	return nil
}

// Writer writes Kafka messages; each message names its topic.
type Writer interface {
	WriteMessages(ctx context.Context, msgs ...kafka.Message) error
}

// Receipts clears a consumer group's dedupe receipt for one event.
type Receipts interface {
	Clear(ctx context.Context, key string) error
}

// ReceiptKey is shared/kafka's dedupe key: consumed:<group>:<topic>:<event id>.
func ReceiptKey(group, topic, eventID string) string {
	return fmt.Sprintf("consumed:%s:%s:%s", group, topic, eventID)
}

// Report is what a Replay did (or, dry, would do).
type Report struct {
	Replayed []string // refs written (or that would be)
	Refused  []string // "ref: reason"
}

// replayHeaders drops the dead-letter bookkeeping and adds where the copy
// came from.
func replayHeaders(dlqTopic string, e Entry) []kafka.Header {
	var out []kafka.Header
	for _, h := range e.Headers {
		switch h.Key {
		case HeaderOriginalTopic, HeaderConsumerGroup, HeaderError, HeaderSourceTopic, HeaderQuarantine,
			"x-source-partition", "x-source-offset", HeaderReplayedFrom:
			continue
		}
		out = append(out, h)
	}
	return append(out, kafka.Header{Key: HeaderReplayedFrom, Value: []byte(dlqTopic + "/" + e.Ref())})
}

// Replay re-publishes the chosen entries, in order, to the source topic of
// dlqTopic. Dry (write=false) it validates and reports only. It stops at
// the first failed write and returns what was done so far: a re-run picks up
// where it stopped, because the markers say what went out.
func Replay(ctx context.Context, dlqTopic string, chosen []Entry, w Writer, rc Receipts, write bool) (Report, error) {
	var rep Report
	src, err := SourceOf(dlqTopic)
	if err != nil {
		return rep, err
	}
	for _, e := range chosen {
		if e.SourceTopic != src {
			rep.Refused = append(rep.Refused, fmt.Sprintf("%s: recorded source %q is not %s", e.Ref(), e.SourceTopic, src))
			continue
		}
		if !write {
			rep.Replayed = append(rep.Replayed, e.Ref())
			continue
		}
		if e.Group != "" && e.EventID != "" {
			if rc == nil {
				return rep, fmt.Errorf("%s: group %s's dedupe receipt must be cleared first, and no Redis is configured", e.Ref(), e.Group)
			}
			if err := rc.Clear(ctx, ReceiptKey(e.Group, src, e.EventID)); err != nil {
				return rep, fmt.Errorf("%s: clear the dedupe receipt: %w", e.Ref(), err)
			}
		}
		if err := w.WriteMessages(ctx, kafka.Message{Topic: src, Key: e.Key, Value: e.Value, Headers: replayHeaders(dlqTopic, e)}); err != nil {
			return rep, fmt.Errorf("%s: re-publish: %w", e.Ref(), err)
		}
		if err := w.WriteMessages(ctx, kafka.Message{Topic: dlqTopic, Key: e.Key,
			Headers: []kafka.Header{{Key: HeaderReplayMarker, Value: []byte(e.Ref())}}}); err != nil {
			return rep, fmt.Errorf("%s: re-published, but the replay marker failed (a re-run would send it again): %w", e.Ref(), err)
		}
		rep.Replayed = append(rep.Replayed, e.Ref())
	}
	return rep, nil
}
