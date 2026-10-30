package outbox

import (
	"context"
	"errors"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	kafka "github.com/segmentio/kafka-go"
)

// fakeStore is an in-memory relayStore with the claim semantics of pgStore.
type fakeStore struct {
	rows      []outboxRow
	published map[int64]bool
	claimed   map[int64]bool
	dead      map[int64]bool
	attempts  map[int64]int
	lastErr   map[int64]string
	backoff   map[int64]bool

	lockHeld   bool // another replica holds the lock
	locks      int
	unlocks    int
	claims     int
	markCalls  [][]int64
	releases   [][]int64
	failures   []failure
	recordErr  error
	claimLimit int
}

func newFakeStore(n int) *fakeStore {
	s := &fakeStore{
		published: map[int64]bool{}, claimed: map[int64]bool{}, dead: map[int64]bool{},
		attempts: map[int64]int{}, lastErr: map[int64]string{}, backoff: map[int64]bool{},
	}
	for i := 1; i <= n; i++ {
		key := "order-" + strconv.Itoa(i%3)
		s.rows = append(s.rows, outboxRow{
			ID: int64(i), EventType: "commerce.order.paid", PartitionKey: key,
			Payload: []byte(`{"n":` + strconv.Itoa(i) + `}`),
		})
	}
	return s
}

func (s *fakeStore) tryLock(context.Context) (func(), bool, error) {
	if s.lockHeld {
		return nil, false, nil
	}
	s.locks++
	return func() { s.unlocks++ }, true, nil
}

func (s *fakeStore) claim(_ context.Context, limit int, _ time.Duration) ([]outboxRow, error) {
	s.claims++
	s.claimLimit = limit
	var out []outboxRow
	// Return in reverse id order: the relay must sort (RETURNING has no order).
	for _, r := range s.rows {
		if s.published[r.ID] || s.dead[r.ID] || s.claimed[r.ID] || s.backoff[r.ID] {
			continue
		}
		if len(out) == limit {
			break
		}
		s.claimed[r.ID] = true
		out = append([]outboxRow{r}, out...)
	}
	return out, nil
}

func (s *fakeStore) markPublished(ctx context.Context, ids []int64) error {
	if err := ctx.Err(); err != nil {
		return err // as pgx does on a cancelled context
	}
	s.markCalls = append(s.markCalls, ids)
	for _, id := range ids {
		s.published[id] = true
		delete(s.claimed, id)
	}
	return nil
}

func (s *fakeStore) release(_ context.Context, ids []int64, reason string) error {
	s.releases = append(s.releases, ids)
	for _, id := range ids {
		delete(s.claimed, id)
		s.lastErr[id] = reason
	}
	return nil
}

func (s *fakeStore) recordFailure(_ context.Context, id int64, f failure, maxAttempts int) (bool, error) {
	if s.recordErr != nil {
		return false, s.recordErr
	}
	s.failures = append(s.failures, f)
	delete(s.claimed, id)
	s.attempts[id]++
	s.lastErr[id] = f.Reason
	if f.Permanent || s.attempts[id] >= maxAttempts {
		s.dead[id] = true
		return true, nil
	}
	s.backoff[id] = true
	return false, nil
}

// fakeWriter records each WriteMessages call; fail decides its error.
type fakeWriter struct {
	calls [][]kafka.Message
	fail  func(call int, msgs []kafka.Message) error
}

func (w *fakeWriter) WriteMessages(_ context.Context, msgs ...kafka.Message) error {
	w.calls = append(w.calls, msgs)
	if w.fail != nil {
		return w.fail(len(w.calls), msgs)
	}
	return nil
}

func newTestPublisher(st relayStore, batch int) *Publisher {
	p := New(nil, Config{DBSchema: "commerce", BatchSize: batch})
	p.store = st
	return p
}

func header(m kafka.Message, key string) string {
	for _, h := range m.Headers {
		if h.Key == key {
			return string(h.Value)
		}
	}
	return ""
}

func TestWriterConfig_HashBalancerAndShortBatchTimeout(t *testing.T) {
	cfg := writerConfig([]string{"b:9092"}, Config{DefaultTopic: "social.events.v1", BatchSize: 100}, nil)
	if _, ok := cfg.Balancer.(*kafka.Hash); !ok {
		t.Fatalf("Balancer = %T, want *kafka.Hash (LeastBytes ignores the partition key)", cfg.Balancer)
	}
	if cfg.BatchTimeout <= 0 || cfg.BatchTimeout > 50*time.Millisecond {
		t.Fatalf("BatchTimeout = %v, want a short positive timeout (kafka-go defaults to 1s)", cfg.BatchTimeout)
	}
	if cfg.RequiredAcks != int(kafka.RequireAll) {
		t.Fatalf("RequiredAcks = %d, want RequireAll", cfg.RequiredAcks)
	}
	if cfg.BatchSize != 100 || cfg.BatchBytes != MaxMessageBytes {
		t.Fatalf("BatchSize/BatchBytes = %d/%d", cfg.BatchSize, cfg.BatchBytes)
	}
	one := writerConfig([]string{"b:9092"}, Config{LeaderAcksOnly: true}, nil)
	if one.RequiredAcks != int(kafka.RequireOne) {
		t.Fatalf("LeaderAcksOnly RequiredAcks = %d, want RequireOne", one.RequiredAcks)
	}
}

func TestHashBalancer_SameKeySamePartition(t *testing.T) {
	cfg := writerConfig([]string{"b:9092"}, Config{}, nil)
	parts := []int{0, 1, 2, 3, 4, 5, 6, 7}
	for _, key := range []string{"order-1", "intent-9", "3f0c1c55-8d0e-4a57-9b9e-0d0c7a9b1f00"} {
		first := cfg.Balancer.Balance(kafka.Message{Key: []byte(key)}, parts...)
		for i := 0; i < 20; i++ {
			if got := cfg.Balancer.Balance(kafka.Message{Key: []byte(key), Value: []byte(strconv.Itoa(i))}, parts...); got != first {
				t.Fatalf("key %q went to partition %d then %d", key, first, got)
			}
		}
	}
}

func TestPass_PublishesBatchInOrderWithKeysAndStableIDs(t *testing.T) {
	st := newFakeStore(5)
	st.rows = append(st.rows, outboxRow{ID: 6, EventType: "x", PartitionKey: "", Payload: []byte(`{}`)})
	w := &fakeWriter{}
	p := newTestPublisher(st, 100)

	p.pass(context.Background(), w)

	if len(w.calls) != 1 {
		t.Fatalf("WriteMessages calls = %d, want 1 batch", len(w.calls))
	}
	msgs := w.calls[0]
	if len(msgs) != 6 {
		t.Fatalf("batch size = %d, want 6", len(msgs))
	}
	for i, m := range msgs {
		id := int64(i + 1)
		if got := header(m, HeaderOutboxID); got != strconv.FormatInt(id, 10) {
			t.Fatalf("message %d outbox_id = %q, want rows in id order", i, got)
		}
		if got := header(m, HeaderEventID); got != EventID("commerce.outbox_events", id) {
			t.Fatalf("message %d event_id = %q, want the stable id", i, got)
		}
		if header(m, HeaderEventType) == "" {
			t.Fatalf("message %d has no event_type header", i)
		}
	}
	if string(msgs[0].Key) != "order-1" {
		t.Fatalf("key = %q, want the row's partition_key", msgs[0].Key)
	}
	if msgs[5].Key != nil {
		t.Fatalf("empty partition_key sent as %q, want nil (round robin)", msgs[5].Key)
	}
	if len(st.markCalls) != 1 || len(st.markCalls[0]) != 6 {
		t.Fatalf("mark calls = %v, want one mark of 6", st.markCalls)
	}
	if st.locks != 1 || st.unlocks != 1 {
		t.Fatalf("locks/unlocks = %d/%d, want 1/1", st.locks, st.unlocks)
	}
}

func TestPass_SkipsWhenAnotherReplicaHoldsTheLock(t *testing.T) {
	st := newFakeStore(3)
	st.lockHeld = true
	w := &fakeWriter{}
	newTestPublisher(st, 100).pass(context.Background(), w)
	if st.claims != 0 || len(w.calls) != 0 {
		t.Fatalf("claims=%d writes=%d without the relay lock, want none", st.claims, len(w.calls))
	}
}

func TestPass_DrainsFullBatches(t *testing.T) {
	st := newFakeStore(25)
	w := &fakeWriter{}
	newTestPublisher(st, 10).pass(context.Background(), w)
	if len(w.calls) != 3 {
		t.Fatalf("writes = %d, want 3 (10+10+5)", len(w.calls))
	}
	if len(st.published) != 25 {
		t.Fatalf("published = %d, want 25", len(st.published))
	}
	if st.claimLimit != 10 {
		t.Fatalf("claim limit = %d, want BatchSize", st.claimLimit)
	}
}

func TestPass_BrokerOutageReleasesClaimsAndStopsPass(t *testing.T) {
	st := newFakeStore(25)
	w := &fakeWriter{fail: func(int, []kafka.Message) error { return io.ErrUnexpectedEOF }}
	p := newTestPublisher(st, 10)

	p.pass(context.Background(), w)

	if len(w.calls) != 1 {
		t.Fatalf("writes = %d, want the pass to stop after the first failed batch", len(w.calls))
	}
	if len(st.published) != 0 || len(st.markCalls) != 0 {
		t.Fatalf("published %v after an outage", st.published)
	}
	if len(st.releases) != 1 || len(st.releases[0]) != 10 {
		t.Fatalf("releases = %v, want the 10 claimed rows released", st.releases)
	}
	if len(st.failures) != 0 || len(st.dead) != 0 {
		t.Fatalf("an outage counted failures %v / dead-lettered %v", st.failures, st.dead)
	}
	if st.unlocks != 1 {
		t.Fatalf("lock not released after the failed pass")
	}

	// Broker back: the next pass starts from the same head, in order.
	w.fail = nil
	w.calls = nil
	p.pass(context.Background(), w)
	if got := header(w.calls[0][0], HeaderOutboxID); got != "1" {
		t.Fatalf("first message after recovery = row %s, want row 1", got)
	}
	if len(st.published) != 25 {
		t.Fatalf("published = %d after recovery, want 25", len(st.published))
	}
}

func TestPass_PartialTransientFailureStopsPassAndKeepsSuccesses(t *testing.T) {
	st := newFakeStore(10)
	w := &fakeWriter{fail: func(call int, msgs []kafka.Message) error {
		if call > 1 {
			return nil
		}
		werrs := make(kafka.WriteErrors, len(msgs))
		for i, m := range msgs {
			if string(m.Key) == "order-2" {
				werrs[i] = kafka.LeaderNotAvailable
			}
		}
		return werrs
	}}
	newTestPublisher(st, 10).pass(context.Background(), w)

	if len(w.calls) != 1 {
		t.Fatalf("writes = %d, want the pass to stop", len(w.calls))
	}
	for _, r := range st.rows {
		failed := r.PartitionKey == "order-2"
		if st.published[r.ID] == failed {
			t.Fatalf("row %d (key %s) published=%v", r.ID, r.PartitionKey, st.published[r.ID])
		}
	}
	if len(st.releases) != 1 || len(st.failures) != 0 {
		t.Fatalf("releases=%v failures=%v, want one release and no counted failure", st.releases, st.failures)
	}
}

func TestPass_TooLargeIsDeadLetteredAndRestPublish(t *testing.T) {
	st := newFakeStore(3)
	st.rows[1].Payload = []byte(`"` + strings.Repeat("x", MaxMessageBytes) + `"`)
	w := &fakeWriter{}
	newTestPublisher(st, 100).pass(context.Background(), w)

	if !st.dead[2] {
		t.Fatalf("oversized row not dead-lettered")
	}
	if len(st.failures) != 1 || !st.failures[0].Permanent {
		t.Fatalf("failures = %+v, want one permanent", st.failures)
	}
	if !st.published[1] || !st.published[3] {
		t.Fatalf("other rows not published: %v", st.published)
	}
	if len(w.calls) != 1 || len(w.calls[0]) != 2 {
		t.Fatalf("the oversized row was sent to Kafka")
	}
}

func TestPass_KafkaMessageTooLargeOnlyThatRowPermanent(t *testing.T) {
	st := newFakeStore(3)
	w := &fakeWriter{fail: func(call int, msgs []kafka.Message) error {
		if call > 1 {
			return nil
		}
		return kafka.MessageTooLargeError{Message: msgs[1]}
	}}
	newTestPublisher(st, 100).pass(context.Background(), w)
	if !st.dead[2] || st.dead[1] || st.dead[3] {
		t.Fatalf("dead = %v, want only row 2", st.dead)
	}
	if st.published[1] || st.published[3] {
		t.Fatalf("rows marked published though kafka-go wrote nothing")
	}
	if len(st.releases) != 1 || len(st.releases[0]) != 2 {
		t.Fatalf("releases = %v, want rows 1 and 3 released for retry", st.releases)
	}
}

func TestPass_RecordErrorIsCountedThenDeadLettered(t *testing.T) {
	st := newFakeStore(1)
	w := &fakeWriter{fail: func(_ int, msgs []kafka.Message) error {
		return kafka.WriteErrors{kafka.InvalidRecord}
	}}
	p := New(nil, Config{BatchSize: 10, MaxAttempts: 3})
	p.store = st
	for i := 0; i < 3; i++ {
		delete(st.backoff, 1) // the back-off has run out
		p.pass(context.Background(), w)
	}
	if st.attempts[1] != 3 || !st.dead[1] {
		t.Fatalf("attempts=%d dead=%v, want dead after MaxAttempts counted failures", st.attempts[1], st.dead[1])
	}
	for _, f := range st.failures {
		if !f.Counted || f.Permanent {
			t.Fatalf("failure %+v, want counted", f)
		}
	}
	if len(st.releases) != 0 {
		t.Fatalf("a refused record was released as transient")
	}
}

func TestPass_UnrecordedFailureEndsPass(t *testing.T) {
	st := newFakeStore(30)
	st.recordErr = errors.New("legacy table")
	w := &fakeWriter{fail: func(_ int, msgs []kafka.Message) error {
		werrs := make(kafka.WriteErrors, len(msgs))
		werrs[0] = kafka.InvalidRecord
		return werrs
	}}
	newTestPublisher(st, 10).pass(context.Background(), w)
	if len(w.calls) != 1 {
		t.Fatalf("writes = %d, want the pass to end when a failure cannot be recorded", len(w.calls))
	}
}

func TestPass_CancelledContextStillMarksPublished(t *testing.T) {
	st := newFakeStore(2)
	ctx, cancel := context.WithCancel(context.Background())
	w := &fakeWriter{fail: func(int, []kafka.Message) error { cancel(); return nil }}
	newTestPublisher(st, 100).pass(ctx, w)
	if len(st.published) != 2 {
		t.Fatalf("published = %v, want the written batch marked despite shutdown", st.published)
	}
}

func TestEventID_StableAndDistinct(t *testing.T) {
	a := EventID("payments.outbox_events", 42)
	if a != EventID("payments.outbox_events", 42) {
		t.Fatal("EventID is not stable")
	}
	if a == EventID("payments.outbox_events", 43) || a == EventID("outbox_events", 42) {
		t.Fatal("EventID collides across rows or tables")
	}
}

func TestRelayLockKey_PerTable(t *testing.T) {
	if relayLockKey("outbox_events") == relayLockKey("payments.outbox_events") {
		t.Fatal("two tables share a relay lock")
	}
	if relayLockKey("food.outbox_events") < 0 {
		t.Fatal("negative lock key")
	}
}

func TestClassifyPublishError(t *testing.T) {
	if f := classifyPublishError(io.EOF); f.Permanent || f.Counted {
		t.Fatalf("connection error classified %+v, want transient", f)
	}
	if f := classifyPublishError(kafka.InvalidRecord); !f.Counted {
		t.Fatalf("InvalidRecord classified %+v, want counted", f)
	}
	if f := classifyPublishError(ErrMessageTooLarge); !f.Permanent {
		t.Fatalf("ErrMessageTooLarge classified %+v, want permanent", f)
	}
	if f := classifyPublishError(errors.New(strings.Repeat("e", 5000))); len(f.Reason) > maxErrorText {
		t.Fatalf("reason not truncated: %d", len(f.Reason))
	}
}

func TestMigrationSQL_Additive(t *testing.T) {
	sql := MigrationSQL("rider.outbox_events")
	for _, col := range ClaimColumns {
		if !strings.Contains(sql, "ADD COLUMN IF NOT EXISTS "+col) {
			t.Fatalf("migration does not add %s idempotently:\n%s", col, sql)
		}
	}
	if !strings.Contains(sql, "attempts      INT NOT NULL DEFAULT 0") {
		t.Fatalf("attempts must have a constant default (no rewrite):\n%s", sql)
	}
}
