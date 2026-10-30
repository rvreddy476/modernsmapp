package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/events"
	"github.com/atpost/chat-message-service/internal/purge"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Outbox relay, chat backend audit H3/H4 (30 Oct 2026).

type relayFakeStore struct {
	ConversationStore // unused methods panic

	mu        sync.Mutex
	queue     []postgres.OutboxEvent // unpublished, id order
	published []int64
	failures  map[int64]postgres.OutboxFailure
	fetches   int
	lockHeld  bool // another replica holds the relay lock
	locked    int
	released  int
	dead      map[int64]bool
}

func (f *relayFakeStore) FetchUnpublishedOutboxEvents(_ context.Context, limit int) ([]postgres.OutboxEvent, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches++
	var out []postgres.OutboxEvent
	for _, e := range f.queue {
		if len(out) == limit {
			break
		}
		if f.dead[e.ID] {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (f *relayFakeStore) MarkOutboxEventsPublished(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, ids...)
	done := map[int64]bool{}
	for _, id := range ids {
		done[id] = true
	}
	kept := f.queue[:0]
	for _, e := range f.queue {
		if !done[e.ID] {
			kept = append(kept, e)
		}
	}
	f.queue = kept
	return nil
}

func (f *relayFakeStore) RecordOutboxEventFailure(_ context.Context, id int64, fl postgres.OutboxFailure) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failures == nil {
		f.failures = map[int64]postgres.OutboxFailure{}
	}
	if f.dead == nil {
		f.dead = map[int64]bool{}
	}
	f.failures[id] = fl
	if fl.Permanent {
		f.dead[id] = true
	}
	return fl.Permanent, nil
}

func (f *relayFakeStore) TryOutboxRelayLock(context.Context) (func(), bool, error) {
	if f.lockHeld {
		return nil, false, nil
	}
	f.locked++
	return func() { f.released++ }, true, nil
}

type batchCall struct{ msgs []events.OutboxMessage }

type fakeBatchProducer struct {
	mu    sync.Mutex
	calls []batchCall
	fail  func(m events.OutboxMessage) error
}

func (p *fakeBatchProducer) PublishOutbox(_ context.Context, msgs []events.OutboxMessage) []error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, batchCall{append([]events.OutboxMessage(nil), msgs...)})
	errs := make([]error, len(msgs))
	if p.fail != nil {
		for i, m := range msgs {
			errs[i] = p.fail(m)
		}
	}
	return errs
}
func (p *fakeBatchProducer) PublishRaw(context.Context, string, string, json.RawMessage) error {
	panic("the relay must publish batches")
}
func (p *fakeBatchProducer) Close() error { return nil }

func quietService(store ConversationStore, producer EventProducer) *Service {
	return &Service{convStore: store, producer: producer, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func chatRow(id int64, conv string) postgres.OutboxEvent {
	return postgres.OutboxEvent{
		ID: id, EventType: "MessageCreated", DedupeKey: fmt.Sprintf("message-created:%d", id),
		Payload:   json.RawMessage(`{"conversation_id":"` + conv + `","message_id":"m"}`),
		CreatedAt: time.Date(2026, 10, 30, 9, 0, 0, int(id), time.UTC),
	}
}

func TestRelayPublishesAWholeBatchKeyedByConversation(t *testing.T) {
	store := &relayFakeStore{queue: []postgres.OutboxEvent{chatRow(1, "conv-a"), chatRow(2, "conv-b"), chatRow(3, "conv-a")}}
	prod := &fakeBatchProducer{}
	quietService(store, prod).processOutbox(context.Background())

	if len(prod.calls) != 1 || len(prod.calls[0].msgs) != 3 {
		t.Fatalf("calls = %+v; want one batch of 3", prod.calls)
	}
	for i, m := range prod.calls[0].msgs {
		row := []postgres.OutboxEvent{chatRow(1, "conv-a"), chatRow(2, "conv-b"), chatRow(3, "conv-a")}[i]
		if m.RowID != row.ID || m.DedupeKey != row.DedupeKey || !m.OccurredAt.Equal(row.CreatedAt) {
			t.Fatalf("message %d lost the row identity: %+v", i, m)
		}
	}
	if k := []string{prod.calls[0].msgs[0].Key, prod.calls[0].msgs[1].Key, prod.calls[0].msgs[2].Key}; k[0] != "conv-a" || k[1] != "conv-b" || k[2] != "conv-a" {
		t.Fatalf("keys = %v; want the conversation ids", k)
	}
	if len(store.published) != 3 || len(store.queue) != 0 {
		t.Fatalf("published = %v", store.published)
	}
	if store.locked != 1 || store.released != 1 {
		t.Fatalf("lock taken %d, released %d", store.locked, store.released)
	}
}

func TestRelaySkipsAPassWhenAnotherReplicaHoldsTheLock(t *testing.T) {
	store := &relayFakeStore{lockHeld: true, queue: []postgres.OutboxEvent{chatRow(1, "c")}}
	prod := &fakeBatchProducer{}
	quietService(store, prod).processOutbox(context.Background())
	if store.fetches != 0 || len(prod.calls) != 0 {
		t.Fatal("a replica without the relay lock claimed or published")
	}
}

func TestRelayDrainsABacklogInOnePass(t *testing.T) {
	var q []postgres.OutboxEvent
	for i := int64(1); i <= 2*outboxRelayBatch+7; i++ {
		q = append(q, chatRow(i, "c"))
	}
	store := &relayFakeStore{queue: q}
	prod := &fakeBatchProducer{}
	quietService(store, prod).processOutbox(context.Background())
	if len(store.queue) != 0 || len(prod.calls) != 3 {
		t.Fatalf("left %d rows after %d batches", len(store.queue), len(prod.calls))
	}
}

// H4: a message too large to publish is dead-lettered; the rest of the batch
// still goes out and later polls are not blocked by it.
func TestRelayDeadLettersAnOversizedEventAndKeepsGoing(t *testing.T) {
	store := &relayFakeStore{queue: []postgres.OutboxEvent{chatRow(1, "c"), chatRow(2, "c"), chatRow(3, "c")}}
	prod := &fakeBatchProducer{fail: func(m events.OutboxMessage) error {
		if m.RowID == 1 {
			return fmt.Errorf("%w: 3 MB", events.ErrMessageTooLarge)
		}
		return nil
	}}
	svc := quietService(store, prod)
	svc.processOutbox(context.Background())

	if f, ok := store.failures[1]; !ok || !f.Permanent {
		t.Fatalf("oversized row failure = %+v", store.failures[1])
	}
	if len(store.published) != 2 || store.published[0] != 2 || store.published[1] != 3 {
		t.Fatalf("published = %v; the head row blocked the rest", store.published)
	}
	store.queue = append(store.queue, chatRow(4, "c"))
	svc.processOutbox(context.Background())
	if len(store.published) != 3 || store.published[2] != 4 {
		t.Fatalf("published = %v; the dead letter still blocks the queue", store.published)
	}
}

// A Kafka outage is not the rows' fault: no row is dead-lettered, the claim
// is released and the pass stops so the next poll retries in order.
func TestRelayTransientFailureReleasesAndStops(t *testing.T) {
	var q []postgres.OutboxEvent
	for i := int64(1); i <= outboxRelayBatch+5; i++ {
		q = append(q, chatRow(i, "c"))
	}
	store := &relayFakeStore{queue: q}
	prod := &fakeBatchProducer{fail: func(events.OutboxMessage) error { return errors.New("dial tcp: connection refused") }}
	quietService(store, prod).processOutbox(context.Background())
	if len(prod.calls) != 1 {
		t.Fatalf("%d batches after a transient failure; the pass must stop", len(prod.calls))
	}
	if len(store.failures) != outboxRelayBatch {
		t.Fatalf("%d failures recorded", len(store.failures))
	}
	for id, f := range store.failures {
		if f.Permanent || f.Counted {
			t.Fatalf("row %d: an outage was counted toward dead-lettering: %+v", id, f)
		}
	}
	if len(store.published) != 0 {
		t.Fatal("nothing was published but rows were marked")
	}
}

func TestClassifyPublishError(t *testing.T) {
	if f := classifyPublishError(fmt.Errorf("%w", events.ErrMessageTooLarge)); !f.Permanent {
		t.Fatalf("too large: %+v", f)
	}
	if f := classifyPublishError(fmt.Errorf("broker: %w", kafka.MessageSizeTooLarge)); f.Permanent || !f.Counted {
		t.Fatalf("broker refusal: %+v", f)
	}
	if f := classifyPublishError(errors.New("timeout")); f.Permanent || f.Counted {
		t.Fatalf("timeout: %+v", f)
	}
}

func TestRelayRoutesPurgeAcksByUser(t *testing.T) {
	user := uuid.NewString()
	ack := postgres.OutboxEvent{ID: 2, EventType: purge.EventUserPurgeAcked, DedupeKey: "evt:2",
		Payload: json.RawMessage(`{"user_id":"` + user + `","service":"chat"}`)}
	store := &relayFakeStore{queue: []postgres.OutboxEvent{chatRow(1, "c"), ack}}
	chat, acks := &fakeBatchProducer{}, &fakeBatchProducer{}
	svc := quietService(store, chat)
	svc.SetPurgeAckProducer(acks)
	svc.processOutbox(context.Background())
	if len(chat.calls) != 1 || len(chat.calls[0].msgs) != 1 || chat.calls[0].msgs[0].RowID != 1 {
		t.Fatalf("chat producer got %+v", chat.calls)
	}
	if len(acks.calls) != 1 || acks.calls[0].msgs[0].RowID != 2 || acks.calls[0].msgs[0].Key != user {
		t.Fatalf("ack producer got %+v", acks.calls)
	}
	if len(store.published) != 2 {
		t.Fatalf("published = %v", store.published)
	}
}

func TestRelayLeavesPurgeAcksWithoutAProducer(t *testing.T) {
	ack := postgres.OutboxEvent{ID: 2, EventType: purge.EventUserPurgeAcked, Payload: json.RawMessage(`{"user_id":"u"}`)}
	store := &relayFakeStore{queue: []postgres.OutboxEvent{ack}}
	quietService(store, &fakeBatchProducer{}).processOutbox(context.Background())
	if len(store.published) != 0 {
		t.Fatal("an ack was marked published with no producer")
	}
	if f, ok := store.failures[2]; !ok || f.Permanent || f.Counted {
		t.Fatalf("the ack must be released, never dead-lettered: %+v", store.failures)
	}
}

func TestOutboxChatKeyIsTheConversation(t *testing.T) {
	if k := outboxChatKey(chatRow(1, "conv-x")); k != "conv-x" {
		t.Fatalf("key = %q", k)
	}
	if k := outboxChatKey(postgres.OutboxEvent{Payload: json.RawMessage(`"sealed-string"`)}); k != "" {
		t.Fatalf("key = %q", k)
	}
}
