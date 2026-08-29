package events

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/segmentio/kafka-go"
)

// Durable-consumption contract for the call consumer (CALL-LB-4), driven
// through the PRODUCTION CallConsumer.Start path — not an extracted helper —
// so rewiring Start back to a lossy loop fails these tests (the required
// production-wiring guard).
//
// Semantics pinned here:
//   - a valid event whose processing fails transiently is retried on the
//     SAME message with NO attempt budget and NO commit until it succeeds;
//   - shutdown mid-retry commits nothing (the broker redelivers);
//   - only permanently unprocessable input (malformed JSON/envelope/UUIDs)
//     is poison, and it is durably quarantined BEFORE its offset commits;
//   - a failed quarantine write blocks the commit;
//   - redelivery of an already-processed event binds to the same
//     event-id+recipient identity, so the durable row cannot duplicate.

// scriptedSource is the broker seam: a fixed message list, then a fetch that
// blocks until the context ends (like a quiet partition).
type scriptedSource struct {
	mu       sync.Mutex
	messages []kafka.Message
	next     int
	trace    []string
	commits  []int64
}

func (s *scriptedSource) FetchMessage(ctx context.Context) (kafka.Message, error) {
	s.mu.Lock()
	if s.next < len(s.messages) {
		m := s.messages[s.next]
		s.next++
		s.trace = append(s.trace, fmt.Sprintf("fetch:%d", m.Offset))
		s.mu.Unlock()
		return m, nil
	}
	s.mu.Unlock()
	<-ctx.Done()
	return kafka.Message{}, ctx.Err()
}

func (s *scriptedSource) CommitMessages(_ context.Context, msgs ...kafka.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		s.trace = append(s.trace, fmt.Sprintf("commit:%d", m.Offset))
		s.commits = append(s.commits, m.Offset)
	}
	return nil
}

func (s *scriptedSource) snapshot() ([]string, []int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.trace...), append([]int64(nil), s.commits...)
}

// flakyNotifier fails the first failFirst attempts with a transient
// dependency error, then stores rows idempotently by identity.
type flakyNotifier struct {
	mu         sync.Mutex
	failFirst  int
	attempts   int
	identities []string
	rows       map[string]bool
}

func (f *flakyNotifier) CreateCallNotification(_ context.Context, _, _ uuid.UUID,
	_, _ string, _ uuid.UUID, _ string, _ time.Time, identity string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.failFirst {
		return errors.New("scylla unavailable")
	}
	f.identities = append(f.identities, identity)
	if f.rows == nil {
		f.rows = map[string]bool{}
	}
	f.rows[identity] = true
	return nil
}

func (f *flakyNotifier) state() (int, []string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.attempts, append([]string(nil), f.identities...), len(f.rows)
}

// recordingQuarantine fails the first failFirst writes, then records.
type recordingQuarantine struct {
	mu        sync.Mutex
	failFirst int
	attempts  int
	records   []string
	onRecord  func()
}

func (q *recordingQuarantine) Quarantine(_ context.Context, m kafka.Message, reason string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.attempts++
	if q.attempts <= q.failFirst {
		return errors.New("dlq broker unavailable")
	}
	q.records = append(q.records, fmt.Sprintf("offset=%d reason=%s", m.Offset, reason))
	if q.onRecord != nil {
		q.onRecord()
	}
	return nil
}

func (q *recordingQuarantine) state() (int, []string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.attempts, append([]string(nil), q.records...)
}

func validInviteMessage(t *testing.T, offset int64, eventID string, invitee uuid.UUID) kafka.Message {
	t.Helper()
	payload, err := json.Marshal(events.CallInvitedPayload{
		CallID:        uuid.New().String(),
		InviteID:      uuid.New().String(),
		InviterUserID: uuid.New().String(),
		InviteeUserID: invitee.String(),
		CallType:      "direct_audio",
		CreatedAt:     time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	env, err := json.Marshal(events.EventEnvelope{
		EventID:   eventID,
		EventType: events.EventCallInvited,
		Payload:   payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	return kafka.Message{Offset: offset, Value: env}
}

func startConsumer(t *testing.T, source *scriptedSource, quarantine *recordingQuarantine,
	notifier *flakyNotifier) (cancel func(), done chan struct{}) {
	t.Helper()
	ctx, cancelCtx := context.WithCancel(context.Background())
	c := newCallConsumerForTest(source, quarantine, notifier, time.Millisecond)
	done = make(chan struct{})
	go func() {
		defer close(done)
		c.Start(ctx)
	}()
	return cancelCtx, done
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// A valid invite that fails transiently many times is retried on the same
// message with NO poison budget: no quarantine, no commit until the
// dependency recovers, then exactly one commit and one durable row.
func TestCallConsumerTransientFailureRetriesUntilSuccessWithoutCommit(t *testing.T) {
	invitee := uuid.New()
	source := &scriptedSource{messages: []kafka.Message{validInviteMessage(t, 7, "evt-1", invitee)}}
	quarantine := &recordingQuarantine{}
	notifier := &flakyNotifier{failFirst: 8} // > any old 5-attempt budget

	cancel, done := startConsumer(t, source, quarantine, notifier)
	defer cancel()

	waitFor(t, "success after 8 transient failures", func() bool {
		attempts, _, rows := notifier.state()
		return attempts >= 9 && rows == 1
	})
	waitFor(t, "the single commit", func() bool {
		_, commits := source.snapshot()
		return len(commits) == 1
	})

	trace, commits := source.snapshot()
	if commits[0] != 7 {
		t.Fatalf("committed wrong offset: %v", commits)
	}
	// The message was fetched ONCE and committed ONCE, after processing.
	if got := strings.Join(trace[:1], ","); got != "fetch:7" {
		t.Fatalf("trace does not start with the fetch: %v", trace)
	}
	if trace[len(trace)-1] != "commit:7" {
		t.Fatalf("trace does not end with the commit: %v", trace)
	}
	if qAttempts, _ := quarantine.state(); qAttempts != 0 {
		t.Fatalf("transient failure was quarantined as poison (CALL-LB-4)")
	}
	attempts, identities, _ := notifier.state()
	if attempts != 9 {
		t.Fatalf("want 9 attempts (8 failures + success), got %d", attempts)
	}
	want := "call:evt-1:" + invitee.String()
	if identities[0] != want {
		t.Fatalf("identity not bound to event id + recipient: %q != %q", identities[0], want)
	}
	cancel()
	<-done
}

// Shutdown during transient retries commits nothing — the broker redelivers
// after restart, which is exactly the durability the loop exists for.
func TestCallConsumerShutdownMidRetryCommitsNothing(t *testing.T) {
	source := &scriptedSource{messages: []kafka.Message{validInviteMessage(t, 3, "evt-2", uuid.New())}}
	notifier := &flakyNotifier{failFirst: 1 << 30} // never succeeds
	cancel, done := startConsumer(t, source, &recordingQuarantine{}, notifier)

	waitFor(t, "several retry attempts", func() bool {
		attempts, _, _ := notifier.state()
		return attempts >= 3
	})
	cancel()
	<-done

	if _, commits := source.snapshot(); len(commits) != 0 {
		t.Fatalf("shutdown mid-retry committed offsets %v (CALL-LB-4)", commits)
	}
}

// Only permanently unprocessable input is poison, and it must be DURABLY
// quarantined BEFORE its source offset commits.
func TestCallConsumerPermanentPoisonQuarantinesBeforeCommit(t *testing.T) {
	var beforeQuarantineCommits []int64
	source := &scriptedSource{messages: []kafka.Message{
		{Offset: 1, Value: []byte(`{not json`)},
		validInviteMessage(t, 2, "evt-3", uuid.New()),
	}}
	quarantine := &recordingQuarantine{}
	quarantine.onRecord = func() {
		_, beforeQuarantineCommits = source.snapshot()
	}
	notifier := &flakyNotifier{}
	cancel, done := startConsumer(t, source, quarantine, notifier)
	defer cancel()

	waitFor(t, "both offsets committed", func() bool {
		_, commits := source.snapshot()
		return len(commits) == 2
	})
	cancel()
	<-done

	if len(beforeQuarantineCommits) != 0 {
		t.Fatalf("offset committed BEFORE the durable quarantine write: %v (CALL-LB-4)",
			beforeQuarantineCommits)
	}
	_, records := quarantine.state()
	if len(records) != 1 || !strings.Contains(records[0], "offset=1") ||
		!strings.Contains(records[0], "malformed envelope JSON") {
		t.Fatalf("poison not quarantined with its reason: %v", records)
	}
	// The healthy next message still flowed.
	if _, commits := source.snapshot(); commits[1] != 2 {
		t.Fatalf("partition did not move past quarantined poison: %v", commits)
	}
}

// A failed DLQ write blocks the commit: the offset may not move past a
// message that has no durable record anywhere.
func TestCallConsumerQuarantineFailureBlocksCommit(t *testing.T) {
	source := &scriptedSource{messages: []kafka.Message{{Offset: 5, Value: []byte(`broken`)}}}
	quarantine := &recordingQuarantine{failFirst: 4}
	cancel, done := startConsumer(t, source, quarantine, &flakyNotifier{})
	defer cancel()

	waitFor(t, "quarantine retried past its failures then committed", func() bool {
		_, commits := source.snapshot()
		return len(commits) == 1
	})
	cancel()
	<-done

	qAttempts, records := quarantine.state()
	if qAttempts != 5 || len(records) != 1 {
		t.Fatalf("want 5 quarantine attempts (4 failures + success), got %d/%v", qAttempts, records)
	}
}

// Malformed or nil required UUIDs are permanent poison — never processed
// with uuid.Nil, never retried forever, quarantined durably.
func TestCallConsumerInvalidUUIDIsPermanentPoison(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{
		"call_id":         "not-a-uuid",
		"inviter_user_id": uuid.New().String(),
		"invitee_user_id": uuid.New().String(),
		"call_type":       "direct_audio",
	})
	env, _ := json.Marshal(events.EventEnvelope{
		EventID:   "evt-4",
		EventType: events.EventCallInvited,
		Payload:   payload,
	})
	source := &scriptedSource{messages: []kafka.Message{{Offset: 9, Value: env}}}
	quarantine := &recordingQuarantine{}
	notifier := &flakyNotifier{}
	cancel, done := startConsumer(t, source, quarantine, notifier)
	defer cancel()

	waitFor(t, "poison quarantined and committed", func() bool {
		_, commits := source.snapshot()
		return len(commits) == 1
	})
	cancel()
	<-done

	if attempts, _, _ := notifier.state(); attempts != 0 {
		t.Fatalf("malformed UUID reached the notifier %d times (uuid.Nil regression)", attempts)
	}
	if _, records := quarantine.state(); len(records) != 1 ||
		!strings.Contains(records[0], "call_id") {
		t.Fatalf("invalid UUID not quarantined as permanent: %v", records)
	}
}

// CALL-LB-4 (partition progress): a stale-token outcome is SUCCESS at the
// notifier seam — the device was durably retired inside CreateCallNotification
// — so the offset commits and the NEXT user's call event processes. The
// pre-fix classification retried the dead token forever and starved the
// partition for unrelated users.
func TestCallConsumerStaleDeviceOutcomeAdvancesThePartition(t *testing.T) {
	inviteeA, inviteeB := uuid.New(), uuid.New()
	source := &scriptedSource{messages: []kafka.Message{
		validInviteMessage(t, 20, "evt-stale", inviteeA),
		validInviteMessage(t, 21, "evt-next", inviteeB),
	}}
	notifier := &flakyNotifier{}
	cancel, done := startConsumer(t, source, &recordingQuarantine{}, notifier)
	defer cancel()

	waitFor(t, "both events processed and committed", func() bool {
		_, commits := source.snapshot()
		return len(commits) == 2
	})
	cancel()
	<-done

	_, ids, _ := notifier.state()
	if len(ids) != 2 || !strings.Contains(ids[1], "evt-next") {
		t.Fatalf("partition did not advance past the stale-device event: %v", ids)
	}
}

// Redelivery of an ALREADY-PROCESSED event (restart after a lost commit)
// binds to the same identity, so the idempotent store keeps exactly one row.
func TestCallConsumerRedeliveryKeepsExactlyOneDurableRow(t *testing.T) {
	invitee := uuid.New()
	m := validInviteMessage(t, 11, "evt-5", invitee)
	redelivered := m // same event, redelivered by the broker
	source := &scriptedSource{messages: []kafka.Message{m, redelivered}}
	notifier := &flakyNotifier{}
	cancel, done := startConsumer(t, source, &recordingQuarantine{}, notifier)
	defer cancel()

	waitFor(t, "both deliveries processed", func() bool {
		_, commits := source.snapshot()
		return len(commits) == 2
	})
	cancel()
	<-done

	_, identities, rows := notifier.state()
	if len(identities) != 2 || identities[0] != identities[1] {
		t.Fatalf("redelivery changed the idempotency identity: %v", identities)
	}
	if rows != 1 {
		t.Fatalf("redelivery duplicated the durable row: %d rows", rows)
	}
}
