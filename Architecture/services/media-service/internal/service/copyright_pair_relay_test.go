package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

type fakePairOutbox struct {
	rows      []postgres.PairOutboxRow
	published []uuid.UUID
	failures  map[uuid.UUID]int
	backoffs  map[uuid.UUID]time.Duration
}

func (f *fakePairOutbox) ClaimPairEvents(context.Context, int, time.Duration) ([]postgres.PairOutboxRow, error) {
	return f.rows, nil
}
func (f *fakePairOutbox) MarkPairEventPublished(_ context.Context, id uuid.UUID) error {
	f.published = append(f.published, id)
	return nil
}
func (f *fakePairOutbox) RecordPairEventFailure(_ context.Context, id uuid.UUID, _ error, backoff time.Duration) (int, error) {
	if f.failures == nil {
		f.failures = map[uuid.UUID]int{}
		f.backoffs = map[uuid.UUID]time.Duration{}
	}
	f.failures[id]++
	f.backoffs[id] = backoff
	return f.failures[id], nil
}
func (f *fakePairOutbox) PairOutboxStats(context.Context, int) (postgres.PairOutboxStats, error) {
	return postgres.PairOutboxStats{}, nil
}

type fakePairProducer struct {
	failFor map[string]bool // pair_id → fail
	keys    []string
	types   []string
}

func (p *fakePairProducer) PublishEnvelopeKeyed(_ context.Context, key string, env events.EventEnvelope) error {
	if p.failFor[key] {
		return errors.New("broker down for this key")
	}
	p.keys = append(p.keys, key)
	p.types = append(p.types, env.EventType)
	return nil
}

func row(pair uuid.UUID, rev int64, attempts int) postgres.PairOutboxRow {
	return postgres.PairOutboxRow{EventID: uuid.New(), PairID: pair, PairRevision: rev,
		EventType: events.MediaFingerprintPairFound, Payload: []byte(`{}`), Attempts: attempts, CreatedAt: time.Now()}
}

// T12-2: a poison row does not block the rows behind it, and it is backed
// off, not dropped.
func TestPairRelayContinuesPastAFailedRow(t *testing.T) {
	good1, poison, good2 := uuid.New(), uuid.New(), uuid.New()
	store := &fakePairOutbox{rows: []postgres.PairOutboxRow{row(good1, 1, 0), row(poison, 1, 3), row(good2, 2, 0)}}
	producer := &fakePairProducer{failFor: map[string]bool{poison.String(): true}}
	r := &pairRelay{store: store, producer: producer, enabled: func() bool { return true }, now: time.Now,
		sleep: func(context.Context, time.Duration) {}}
	if n := r.drain(context.Background()); n != 2 {
		t.Fatalf("published %d, want 2", n)
	}
	if len(producer.keys) != 2 || producer.keys[0] != good1.String() || producer.keys[1] != good2.String() {
		t.Fatalf("keys %v: must be keyed by pair_id and continue past the poison row", producer.keys)
	}
	if len(store.published) != 2 {
		t.Fatalf("marked %d", len(store.published))
	}
	if store.failures[store.rows[1].EventID] != 1 {
		t.Fatalf("poison row failure not recorded: %+v", store.failures)
	}
	// The poison row had 3 attempts; the 4th backs off 16 s.
	if got := store.backoffs[store.rows[1].EventID]; got != 16*time.Second {
		t.Fatalf("backoff %s, want 16s", got)
	}
}

func TestPairRelayBackoffCapsAtFifteenMinutes(t *testing.T) {
	cases := map[int]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 5: 32 * time.Second, 10: 15 * time.Minute, 40: 15 * time.Minute}
	for attempts, want := range cases {
		if got := pairRelayBackoff(attempts); got != want {
			t.Errorf("attempt %d: %s, want %s", attempts, got, want)
		}
	}
}

func TestPairRelayBudget(t *testing.T) {
	var rows []postgres.PairOutboxRow
	for i := 0; i < pairRelayBudgetPerSecond+1; i++ {
		rows = append(rows, row(uuid.New(), 1, 0))
	}
	store := &fakePairOutbox{rows: rows}
	slept := 0
	clock := time.Now()
	r := &pairRelay{store: store, producer: &fakePairProducer{}, enabled: func() bool { return true },
		now: func() time.Time { return clock }, sleep: func(context.Context, time.Duration) { slept++ }}
	if n := r.drain(context.Background()); n != pairRelayBudgetPerSecond+1 {
		t.Fatalf("published %d", n)
	}
	if slept != 1 {
		t.Fatalf("slept %d times, want once at the budget", slept)
	}
}

func TestPairRelayKillSwitch(t *testing.T) {
	t.Setenv("COPYRIGHT_PAIR_RELAY", "")
	if PairRelayEnabled() {
		t.Fatal("relay must default off")
	}
	t.Setenv("COPYRIGHT_PAIR_RELAY", "off")
	if PairRelayEnabled() {
		t.Fatal("off")
	}
	t.Setenv("COPYRIGHT_PAIR_RELAY", "on")
	if !PairRelayEnabled() {
		t.Fatal("on")
	}
}
