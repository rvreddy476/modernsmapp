package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/segmentio/kafka-go"
)

// Audit H3 (30 Oct 2026): the relay writer batches with a short timeout and
// partitions by key.
func TestRelayWriterBatchesQuicklyAndPartitionsByKey(t *testing.T) {
	p := NewProducer([]string{"127.0.0.1:1"}, "chat.events.v1")
	defer p.Close()
	w := p.writer
	if _, ok := w.Balancer.(*kafka.Hash); !ok {
		t.Fatalf("balancer = %T, want *kafka.Hash (LeastBytes ignores the key)", w.Balancer)
	}
	if w.BatchTimeout <= 0 || w.BatchTimeout > 50*time.Millisecond {
		t.Fatalf("BatchTimeout = %v; the 1 s default made each sync write take a second", w.BatchTimeout)
	}
	if w.BatchSize < outboxBatchFloor {
		t.Fatalf("BatchSize = %d, want >= %d", w.BatchSize, outboxBatchFloor)
	}
	if w.RequiredAcks != kafka.RequireAll {
		t.Fatalf("RequiredAcks = %v; the outbox marks rows published on ack, it needs RequireAll", w.RequiredAcks)
	}
}

// outboxBatchFloor is the relay's claim size; one claim fits in one batch.
const outboxBatchFloor = 100

func TestOutboxEventIDIsStablePerRow(t *testing.T) {
	a := OutboxEventID(7, "message-created:abc")
	if a != OutboxEventID(99, "message-created:abc") {
		t.Fatal("the same dedupe key must give the same event id whatever the row id")
	}
	if a == OutboxEventID(7, "message-created:abd") {
		t.Fatal("different dedupe keys gave the same event id")
	}
	legacy := OutboxEventID(7, "")
	if legacy != OutboxEventID(7, "") || legacy == OutboxEventID(8, "") {
		t.Fatal("a legacy row's id must be stable per row id and differ between rows")
	}
	if legacy != OutboxEventID(0, "outbox:7") {
		t.Fatal(`a legacy row must use the backfill's "outbox:<id>" key`)
	}
}

func TestBuildOutboxBatchUsesRowIdentityAndRefusesOversized(t *testing.T) {
	p := &Producer{maxBytes: 4096}
	at := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	msgs := []OutboxMessage{
		{RowID: 1, DedupeKey: "message-created:1", EventType: "MessageCreated", Key: "conv-a", OccurredAt: at, Payload: json.RawMessage(`{"conversation_id":"conv-a"}`)},
		{RowID: 2, DedupeKey: "evt:2", EventType: "MessageEdited", Key: "conv-a", OccurredAt: at, Payload: json.RawMessage(`{"text":"` + strings.Repeat("x", 5000) + `"}`)},
		{RowID: 3, EventType: "MemberAdded", OccurredAt: at, Payload: json.RawMessage(`{}`)},
	}
	errs := make([]error, len(msgs))
	kmsgs, index := p.buildOutboxBatch(msgs, errs)
	if !errors.Is(errs[1], ErrMessageTooLarge) {
		t.Fatalf("oversized message error = %v, want ErrMessageTooLarge", errs[1])
	}
	if errs[0] != nil || errs[2] != nil {
		t.Fatalf("errs = %v", errs)
	}
	if len(kmsgs) != 2 || index[0] != 0 || index[1] != 2 {
		t.Fatalf("sent %d messages, index %v", len(kmsgs), index)
	}
	if string(kmsgs[0].Key) != "conv-a" {
		t.Fatalf("key = %q, want the conversation id", kmsgs[0].Key)
	}
	if kmsgs[1].Key != nil {
		t.Fatalf("an empty key must be nil (round robin), not %q", kmsgs[1].Key)
	}
	var env sharedEvents.EventEnvelope
	if err := json.Unmarshal(kmsgs[0].Value, &env); err != nil {
		t.Fatal(err)
	}
	if env.EventID != OutboxEventID(1, "message-created:1") || !env.OccurredAt.Equal(at) || env.EventType != "MessageCreated" {
		t.Fatalf("envelope = %+v", env)
	}
	// A re-publish is the same bytes.
	again, _ := p.buildOutboxBatch(msgs, make([]error, len(msgs)))
	if string(again[0].Value) != string(kmsgs[0].Value) {
		t.Fatal("re-publishing a row produced a different envelope")
	}
}

func TestMapWriteErrorSpreadsErrorsOverTheBatch(t *testing.T) {
	sent := []kafka.Message{{Value: []byte("a")}, {Value: []byte("b")}}
	index := []int{0, 2}

	errs := make([]error, 3)
	boom := errors.New("boom")
	mapWriteError(kafka.WriteErrors{nil, boom}, sent, index, errs)
	if errs[0] != nil || errs[1] != nil || !errors.Is(errs[2], boom) {
		t.Fatalf("WriteErrors mapped to %v", errs)
	}

	errs = make([]error, 3)
	mapWriteError(kafka.MessageTooLargeError{Message: sent[1]}, sent, index, errs)
	if errors.Is(errs[0], ErrMessageTooLarge) || errs[0] == nil || !errors.Is(errs[2], ErrMessageTooLarge) {
		t.Fatalf("MessageTooLargeError mapped to %v", errs)
	}

	errs = make([]error, 3)
	mapWriteError(boom, sent, index, errs)
	if !errors.Is(errs[0], boom) || errs[1] != nil || !errors.Is(errs[2], boom) {
		t.Fatalf("a whole-call error mapped to %v", errs)
	}

	errs = make([]error, 3)
	mapWriteError(nil, sent, index, errs)
	for _, e := range errs {
		if e != nil {
			t.Fatalf("nil error mapped to %v", errs)
		}
	}
}

func TestIsRecordErrorSeparatesDataFromAvailability(t *testing.T) {
	for _, err := range []error{kafka.MessageSizeTooLarge, fmt.Errorf("wrapped: %w", kafka.InvalidRecord)} {
		if !IsRecordError(err) {
			t.Fatalf("%v must count toward dead-lettering", err)
		}
	}
	for _, err := range []error{kafka.LeaderNotAvailable, kafka.NotEnoughReplicas, errors.New("dial tcp: connection refused")} {
		if IsRecordError(err) {
			t.Fatalf("%v is an availability error; it must never dead-letter a row", err)
		}
	}
}
