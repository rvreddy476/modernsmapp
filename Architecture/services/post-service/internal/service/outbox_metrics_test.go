package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// lv-21: the relay counts what it did with each row and reports the age of
// the oldest row it left unpublished.

func promCounter(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func promGauge(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetGauge().GetValue()
}

func relayRowCounts(t *testing.T) map[string]float64 {
	out := map[string]float64{}
	for _, r := range []string{outboxPublished, outboxPublishFailed, outboxMarkFailed, outboxBuildFailed, outboxHeld} {
		out[r] = promCounter(t, outboxRelayRows.WithLabelValues(r))
	}
	return out
}

func TestRelayOutboxPass_Metrics(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	stuck, fine, unmarkable := uuid.New(), uuid.New(), uuid.New()
	first := outboxRow(events.PostCreated, stuck, now.Add(-10*time.Minute))
	behind := outboxRow(events.PostReacted, stuck, now.Add(-9*time.Minute))
	ok := outboxRow(events.PostCreated, fine, now.Add(-8*time.Minute))
	marked := outboxRow(events.PostCreated, unmarkable, now.Add(-7*time.Minute))
	store := &fakeOutboxStore{
		rows:    []postgres.OutboxEvent{first, behind, ok, marked},
		markErr: map[uuid.UUID]error{marked.ID: errors.New("postgres went away")},
	}
	pub := &fakeOutboxPublisher{fail: map[string]error{first.ID.String(): errors.New("kafka down")}}

	before := relayRowCounts(t)
	if err := relayOutboxPass(ctx, store, pub.publish); err != nil {
		t.Fatal(err)
	}
	after := relayRowCounts(t)
	want := map[string]float64{outboxPublishFailed: 1, outboxHeld: 1, outboxPublished: 1, outboxMarkFailed: 1, outboxBuildFailed: 0}
	for r, n := range want {
		if got := after[r] - before[r]; got != n {
			t.Errorf("outbox_events_total{%s} moved by %v, want %v", r, got, n)
		}
	}
	if age := promGauge(t, outboxOldestUnpublished); age < 599 || age > 660 {
		t.Fatalf("oldest unpublished = %v s, want about 600 (the row Kafka refused)", age)
	}

	// Everything goes out: the gauge drops to 0.
	pub.fail, store.markErr = nil, nil
	if err := relayOutboxPass(ctx, store, pub.publish); err != nil {
		t.Fatal(err)
	}
	if age := promGauge(t, outboxOldestUnpublished); age != 0 {
		t.Fatalf("every row published, gauge = %v, want 0", age)
	}

	// A replica that does not hold the lock reports 0; a lock error counts.
	outboxOldestUnpublished.Set(42)
	if err := relayOutboxPass(ctx, &fakeOutboxStore{lockHeldElsewhere: true}, pub.publish); err != nil {
		t.Fatal(err)
	}
	if age := promGauge(t, outboxOldestUnpublished); age != 0 {
		t.Fatalf("a non-holder left the gauge at %v", age)
	}
	lockErrs := promCounter(t, outboxRelayErrors.WithLabelValues("lock"))
	_ = relayOutboxPass(ctx, &fakeOutboxStore{lockErr: errors.New("db down")}, pub.publish)
	if got := promCounter(t, outboxRelayErrors.WithLabelValues("lock")) - lockErrs; got != 1 {
		t.Fatalf("lock errors moved by %v, want 1", got)
	}
}
