package events

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/atpost/qa-service/internal/store"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// fakeReader hands out queued messages, records commits, and cancels the
// consumer once the queue is drained.
type fakeReader struct {
	mu        sync.Mutex
	queue     []kafka.Message
	committed []int64
	done      context.CancelFunc
}

func (r *fakeReader) FetchMessage(ctx context.Context) (kafka.Message, error) {
	r.mu.Lock()
	if len(r.queue) == 0 {
		r.mu.Unlock()
		r.done()
		<-ctx.Done()
		return kafka.Message{}, ctx.Err()
	}
	m := r.queue[0]
	r.queue = r.queue[1:]
	r.mu.Unlock()
	return m, nil
}

func (r *fakeReader) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range msgs {
		r.committed = append(r.committed, m.Offset)
	}
	return nil
}

func (r *fakeReader) Close() error { return nil }

// flakyEraser fails its first failures calls.
type flakyEraser struct {
	mu       sync.Mutex
	failures int
	calls    int
}

func (f *flakyEraser) AnonymizeUser(context.Context, uuid.UUID) (store.Erased, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.calls <= f.failures {
		return store.Erased{}, errors.New("postgres failover")
	}
	return store.Erased{}, nil
}

type recordingDeadLetters struct {
	mu      sync.Mutex
	offsets []int64
	fail    bool
}

func (d *recordingDeadLetters) RecordPlatformEventDeadLetter(_ context.Context, _ string, _ int, offset int64, _ string, _ []byte, _ int, _ string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.fail {
		return errors.New("db down")
	}
	d.offsets = append(d.offsets, offset)
	return nil
}

func deletionMessage(t *testing.T, offset int64) kafka.Message {
	t.Helper()
	payload, _ := json.Marshal(events.UserDeletionRequestedPayload{UserID: uuid.NewString()})
	env, _ := json.Marshal(events.EventEnvelope{EventType: events.EventUserDeletionRequested, Payload: payload})
	return kafka.Message{Topic: "platform-events", Offset: offset, Value: env}
}

func runConsumer(t *testing.T, c *Consumer, r *fakeReader) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r.done = cancel
	c.reader = r
	c.backoff, c.maxBackoff = time.Millisecond, time.Millisecond
	c.Start(ctx)
}

// M8: a failing erasure is retried and its offset is committed only after
// it succeeded (ReadMessage used to commit first and drop the failure).
func TestConsumerCommitsOnlyAfterTheEventIsApplied(t *testing.T) {
	eraser := &flakyEraser{failures: 3}
	r := &fakeReader{queue: []kafka.Message{deletionMessage(t, 7)}}
	c := (&Consumer{}).WithEraser(eraser)
	c.deadLetters = &recordingDeadLetters{}
	runConsumer(t, c, r)
	if eraser.calls != 4 {
		t.Fatalf("erasure ran %d times; want 3 failures then 1 success", eraser.calls)
	}
	if len(r.committed) != 1 || r.committed[0] != 7 {
		t.Fatalf("committed %v; want [7] once, after success", r.committed)
	}
}

// After MaxAttempts the event is parked durably, and only then committed.
func TestConsumerParksAPoisonEventThenCommits(t *testing.T) {
	eraser := &flakyEraser{failures: 1000}
	dl := &recordingDeadLetters{}
	r := &fakeReader{queue: []kafka.Message{deletionMessage(t, 3), deletionMessage(t, 4)}}
	c := (&Consumer{maxAttempts: 2}).WithEraser(eraser)
	c.deadLetters = dl
	runConsumer(t, c, r)
	if len(dl.offsets) != 2 || dl.offsets[0] != 3 || dl.offsets[1] != 4 {
		t.Fatalf("dead letters %v; want [3 4]", dl.offsets)
	}
	if len(r.committed) != 2 {
		t.Fatalf("committed %v; want both after parking", r.committed)
	}
	if eraser.calls != 4 {
		t.Fatalf("erasure ran %d times; want 2 per event", eraser.calls)
	}
}

// When the event can be neither applied nor parked, the offset is held and
// the event keeps being retried until shutdown; nothing is committed.
func TestConsumerHoldsTheOffsetWhileNothingIsDurable(t *testing.T) {
	eraser := &flakyEraser{failures: 1 << 30}
	r := &fakeReader{queue: []kafka.Message{deletionMessage(t, 9)}}
	c := (&Consumer{maxAttempts: 1}).WithEraser(eraser)
	c.deadLetters = &recordingDeadLetters{fail: true}
	ctx, cancel := context.WithCancel(context.Background())
	r.done = cancel
	c.reader = r
	c.backoff, c.maxBackoff = time.Millisecond, time.Millisecond
	go func() {
		for {
			eraser.mu.Lock()
			n := eraser.calls
			eraser.mu.Unlock()
			if n >= 5 {
				cancel()
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()
	c.Start(ctx)
	if len(r.committed) != 0 {
		t.Fatalf("committed %v while the erasure never became durable", r.committed)
	}
}

// A malformed event can never be applied: it is skipped (committed) at once.
func TestConsumerSkipsMalformedEvents(t *testing.T) {
	bad := []kafka.Message{
		{Offset: 1, Value: []byte("{not json")},
		{Offset: 2, Value: mustEnvelope(t, events.EventUserDeletionRequested, `{"user_id":"not-a-uuid"}`)},
		{Offset: 3, Value: mustEnvelope(t, events.EventCommunityMemberBanned, `{"community_id":"x","user_id":"y"}`)},
	}
	eraser := &flakyEraser{}
	dl := &recordingDeadLetters{}
	r := &fakeReader{queue: bad}
	c := (&Consumer{maxAttempts: 1}).WithEraser(eraser)
	c.deadLetters = dl
	runConsumer(t, c, r)
	if len(r.committed) != 3 || len(dl.offsets) != 0 || eraser.calls != 0 {
		t.Fatalf("committed %v, parked %v, erasures %d; want all 3 skipped without retries", r.committed, dl.offsets, eraser.calls)
	}
}

func mustEnvelope(t *testing.T, eventType, payload string) []byte {
	t.Helper()
	b, err := json.Marshal(events.EventEnvelope{EventType: eventType, Payload: json.RawMessage(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
