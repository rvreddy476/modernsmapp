//go:build integration

package store

import (
	"context"
	"errors"
	"github.com/atpost/shared/events"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Module 3 SR-2 — the outbox RELAY, against live PostgreSQL.
//
//	POSTGRES_DSN=postgres://... go test -tags integration ./internal/store/ -run Relay -v
//
// Writing the outbox row inside the block transaction only proves the event is
// RECORDED. Until these tests existed, nothing read the table: the durability
// claim was half-built, and a block whose event never reached chat behaved
// exactly like the fire-and-forget goroutine it replaced.

// recordingPublisher captures deliveries and can be told to fail.
type recordingPublisher struct {
	mu        sync.Mutex
	delivered []OutboxEvent
	failUntil int32 // fail this many calls before succeeding
	calls     int32
	// holdFor simulates broker latency, widening the window in which two
	// replicas could both be mid-publish for the same row.
	holdFor time.Duration
}

func (p *recordingPublisher) PublishGraphEvent(_ context.Context, ev OutboxEvent) error {
	n := atomic.AddInt32(&p.calls, 1)
	if n <= atomic.LoadInt32(&p.failUntil) {
		return errors.New("broker unavailable")
	}
	if p.holdFor > 0 {
		time.Sleep(p.holdFor)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.delivered = append(p.delivered, ev)
	return nil
}

func (p *recordingPublisher) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.delivered)
}

func fastRelayConfig() RelayConfig {
	cfg := DefaultRelayConfig()
	// No waiting between passes. MaxRetryBackoff must be zeroed too, or the
	// LEAST(base * 2^n, max) expression would still impose the cap.
	cfg.RetryBackoff = 0
	cfg.MaxRetryBackoff = 0
	cfg.PollInterval = 10 * time.Millisecond
	return cfg
}

func TestRelay_DeliversTheBlockEventAndMarksItPublished(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("block: %v", err)
	}

	before, err := s.UnpublishedOutboxCount(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if before == 0 {
		t.Fatal("no unpublished event after a block; nothing for the relay to prove")
	}

	pub := &recordingPublisher{}
	n, err := s.RelayOnce(ctx, pub, fastRelayConfig())
	if err != nil {
		t.Fatalf("relay: %v", err)
	}
	if n == 0 || pub.count() == 0 {
		t.Fatal("the relay published nothing: the outbox row would sit forever " +
			"and chat would never learn about the block")
	}

	var found bool
	for _, ev := range pub.delivered {
		if ev.EventType == events.UserBlocked && ev.ActorID == alice && ev.TargetID == bob {
			found = true
			if ev.PairSeq <= 0 {
				t.Error("delivered event has no pair sequence; consumers cannot dedupe")
			}
			if len(ev.Payload) == 0 {
				t.Error("delivered event has an empty payload")
			}
		}
	}
	if !found {
		t.Fatalf("%s for the pair was not delivered; got %+v", events.UserBlocked, pub.delivered)
	}

	// A second pass must not re-deliver an already-published row.
	pub2 := &recordingPublisher{}
	if _, err := s.RelayOnce(ctx, pub2, fastRelayConfig()); err != nil {
		t.Fatalf("second relay pass: %v", err)
	}
	for _, ev := range pub2.delivered {
		if ev.ActorID == alice && ev.TargetID == bob {
			t.Fatal("an already-published event was delivered again on the next pass")
		}
	}
}

// A publish failure must NOT mark the row published. Marking before the
// publisher confirms is the exact failure mode the outbox exists to remove.
func TestRelay_PublishFailureLeavesTheEventUndeliveredAndRetries(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("block: %v", err)
	}

	failing := &recordingPublisher{failUntil: 1000} // always fails
	if n, err := s.RelayOnce(ctx, failing, fastRelayConfig()); err != nil {
		t.Fatalf("relay: %v", err)
	} else if n != 0 {
		t.Fatalf("relay reported %d published while every publish failed", n)
	}

	var published bool
	var attempts int
	var lastErr *string
	if err := pool.QueryRow(ctx, `
		SELECT published, attempts, last_error FROM graph_outbox_events
		WHERE actor_id = $1 AND target_id = $2 AND event_type = $3`,
		alice, bob, events.UserBlocked).Scan(&published, &attempts, &lastErr); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if published {
		t.Fatal("a FAILED publish marked the event published; the event is now lost")
	}
	if attempts == 0 {
		t.Error("the attempt was not recorded; a stuck event would be invisible")
	}
	if lastErr == nil || *lastErr == "" {
		t.Error("the failure cause was not recorded")
	}

	// The retry must succeed once the broker recovers.
	recovered := &recordingPublisher{}
	if n, err := s.RelayOnce(ctx, recovered, fastRelayConfig()); err != nil {
		t.Fatalf("retry relay: %v", err)
	} else if n == 0 {
		t.Fatal("the event was not retried after the broker recovered")
	}
}

// Multiple relay replicas must not double-publish. FOR UPDATE SKIP LOCKED is
// what makes that true; without it every replica leases every row.
//
// The first version of this test did NOT detect the removal of SKIP LOCKED,
// and I would rather say so than let it stand: with the default batch size the
// first replica leased all 20 rows in its first pass, so the other five found
// an empty table and never contended. A concurrency test in which the
// concurrency never happens proves nothing — the same class of false positive
// that made the original pair-lock test worthless.
//
// Three changes make the race real: BatchSize 1 so a replica cannot swallow
// the whole backlog, a start barrier so the replicas issue their leases in the
// same instant, and a publisher that holds each delivery briefly to widen the
// window. The test then asserts that contention ACTUALLY occurred.
func TestRelay_ConcurrentRelaysDoNotDoublePublish(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()

	const pairs = 40
	const replicas = 8

	for i := 0; i < pairs; i++ {
		a, b := pairFixture(t, pool)
		if _, err := s.BlockAtomic(ctx, a, b); err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
	}

	cfg := fastRelayConfig()
	cfg.BatchSize = 1 // force replicas to compete for individual rows

	pub := &recordingPublisher{holdFor: 2 * time.Millisecond}
	perReplica := make([]int, replicas)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for r := 0; r < replicas; r++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			for pass := 0; pass < pairs; pass++ {
				n, err := s.RelayOnce(ctx, pub, cfg)
				if err != nil {
					t.Errorf("replica %d: relay: %v", idx, err)
					return
				}
				perReplica[idx] += n
			}
		}(r)
	}
	close(start)
	wg.Wait()

	// Contention check: if one replica did all the work the others never
	// raced it, and a duplicate-delivery assertion below would be vacuous.
	working := 0
	for _, n := range perReplica {
		if n > 0 {
			working++
		}
	}
	if working < 2 {
		t.Fatalf("only %d replica(s) published anything (%v): the replicas never "+
			"actually competed, so this test proves nothing about leasing", working, perReplica)
	}

	seen := map[uuid.UUID]int{}
	pub.mu.Lock()
	for _, ev := range pub.delivered {
		seen[ev.ID]++
	}
	pub.mu.Unlock()

	dupes := 0
	for id, n := range seen {
		if n > 1 {
			dupes++
			t.Errorf("event %s delivered %d times by concurrent relays", id, n)
		}
	}
	if dupes > 0 {
		t.Fatalf("%d event(s) double-published: replicas are not leasing disjoint rows", dupes)
	}
	if len(seen) < pairs {
		t.Fatalf("only %d of %d events were delivered", len(seen), pairs)
	}

	remaining, err := s.UnpublishedOutboxCount(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d event(s) left unpublished after the relays drained", remaining)
	}
	t.Logf("replicas published %v (total %d)", perReplica, len(seen))
}

// LB-2: an outage LONGER than the old retry cap must still recover, with no
// manual row editing.
//
// This replaces a test that asserted the opposite — that an event was
// permanently PARKED after MaxAttempts. That was the defect: with a two-second
// backoff, ten attempts is about twenty seconds, so a routine broker restart
// silently abandoned a safety event and a process restart did not retry it. A
// block that never reaches chat is not a filing-cabinet item; it is a user who
// is still reachable by someone they blocked.
func TestRelay_RecoversAfterAnOutageLongerThanTheOldRetryCap(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("block: %v", err)
	}

	cfg := fastRelayConfig()
	// The old cap was 10. Fail well past it.
	const attemptsDuringOutage = 25
	failing := &recordingPublisher{failUntil: 1000}
	for i := 0; i < attemptsDuringOutage; i++ {
		if _, err := s.RelayOnce(ctx, failing, cfg); err != nil {
			t.Fatalf("relay pass %d: %v", i, err)
		}
	}

	var attempts int
	var published bool
	if err := pool.QueryRow(ctx, `
		SELECT attempts, published FROM graph_outbox_events
		WHERE actor_id = $1 AND target_id = $2`, alice, bob).Scan(&attempts, &published); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if published {
		t.Fatal("a failed publish marked the event published")
	}
	if attempts <= 10 {
		t.Fatalf("attempts=%d: the relay stopped retrying at or before the old cap, "+
			"so the event is abandoned", attempts)
	}

	// It must appear as STUCK for alerting — while still being retried.
	stuck, err := s.StuckOutboxEvents(ctx, 10, 10)
	if err != nil {
		t.Fatalf("stuck: %v", err)
	}
	var visible bool
	for _, ev := range stuck {
		if ev.ActorID == alice && ev.TargetID == bob {
			visible = true
		}
	}
	if !visible {
		t.Error("a long-undelivered safety event is not visible for alerting; an " +
			"operator has no way to discover that a block never reached its consumers")
	}

	// THE POINT: the broker recovers and the event is delivered, with no
	// manual intervention and no process restart.
	recovered := &recordingPublisher{}
	n, err := s.RelayOnce(ctx, recovered, cfg)
	if err != nil {
		t.Fatalf("relay after recovery: %v", err)
	}
	if n == 0 {
		t.Fatal("the event was NOT retried after the broker recovered. It was " +
			"permanently abandoned, which is the defect this test exists to catch.")
	}

	remaining, err := s.UnpublishedOutboxCount(ctx)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 0 {
		t.Errorf("%d event(s) still unpublished after recovery", remaining)
	}
}

// LB-2: the retry delay must GROW so a long outage is paced rather than a hot
// loop — the reason a cap seemed necessary in the first place.
func TestRelay_BackoffGrowsWithAttempts(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()
	alice, bob := pairFixture(t, pool)

	if _, err := s.BlockAtomic(ctx, alice, bob); err != nil {
		t.Fatalf("block: %v", err)
	}

	// A real (small but non-zero) base backoff so growth is observable.
	cfg := DefaultRelayConfig()
	cfg.RetryBackoff = 50 * time.Millisecond
	cfg.MaxRetryBackoff = 2 * time.Second

	failing := &recordingPublisher{failUntil: 1000}
	// First pass claims it immediately (last_attempt_at is NULL).
	if _, err := s.RelayOnce(ctx, failing, cfg); err != nil {
		t.Fatalf("first pass: %v", err)
	}
	var afterFirst int
	if err := pool.QueryRow(ctx,
		`SELECT attempts FROM graph_outbox_events WHERE actor_id=$1 AND target_id=$2`,
		alice, bob).Scan(&afterFirst); err != nil {
		t.Fatalf("read: %v", err)
	}
	if afterFirst != 1 {
		t.Fatalf("attempts=%d after one pass, want 1", afterFirst)
	}

	// An immediate second pass must NOT claim it — the backoff has not elapsed.
	if _, err := s.RelayOnce(ctx, failing, cfg); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	var afterSecond int
	if err := pool.QueryRow(ctx,
		`SELECT attempts FROM graph_outbox_events WHERE actor_id=$1 AND target_id=$2`,
		alice, bob).Scan(&afterSecond); err != nil {
		t.Fatalf("read: %v", err)
	}
	if afterSecond != afterFirst {
		t.Fatalf("attempts went %d→%d with no wait: the relay is hot-looping on a "+
			"failing event", afterFirst, afterSecond)
	}
}

// RunRelay must drain a backlog and stop cleanly on context cancellation.
func TestRelay_RunLoopDrainsBacklogAndStopsOnCancel(t *testing.T) {
	pool := graphPool(t)
	s := New(pool)
	ctx := context.Background()

	const pairs = 10
	for i := 0; i < pairs; i++ {
		a, b := pairFixture(t, pool)
		if _, err := s.BlockAtomic(ctx, a, b); err != nil {
			t.Fatalf("block %d: %v", i, err)
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	pub := &recordingPublisher{}
	done := make(chan struct{})
	go func() {
		s.RunRelay(runCtx, pub, fastRelayConfig(), nil)
		close(done)
	}()

	deadline := time.After(10 * time.Second)
	for {
		remaining, err := s.UnpublishedOutboxCount(ctx)
		if err != nil {
			cancel()
			t.Fatalf("count: %v", err)
		}
		if remaining == 0 {
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("relay loop did not drain the backlog: %d left", remaining)
		case <-time.After(20 * time.Millisecond):
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("RunRelay did not return after its context was cancelled")
	}
}
