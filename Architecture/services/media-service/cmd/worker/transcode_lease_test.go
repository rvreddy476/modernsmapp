package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

type fakeStamper struct {
	calls atomic.Int32
	fail  bool
}

func (f *fakeStamper) StampTranscodeHeartbeat(ctx context.Context, _ uuid.UUID) error {
	f.calls.Add(1)
	if f.fail {
		return errors.New("database unavailable")
	}
	return nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not reached")
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestHeartbeatStampsAtStartAndWhileRunning(t *testing.T) {
	f := &fakeStamper{}
	stop := startTranscodeHeartbeat(context.Background(), f, uuid.New(), 5*time.Millisecond)
	if f.calls.Load() < 1 {
		t.Fatal("no heartbeat stamped when the job started")
	}
	waitFor(t, func() bool { return f.calls.Load() >= 3 })
	stop()
	after := f.calls.Load()
	time.Sleep(30 * time.Millisecond)
	if f.calls.Load() != after {
		t.Fatalf("heartbeat kept stamping after stop: %d -> %d", after, f.calls.Load())
	}
	stop() // idempotent
}

func TestHeartbeatFailureNeverStopsTheJobOrTheBeat(t *testing.T) {
	f := &fakeStamper{fail: true}
	stop := startTranscodeHeartbeat(context.Background(), f, uuid.New(), 5*time.Millisecond)
	// Failing writes keep being retried on the next tick; nothing panics and
	// nothing is surfaced to the job.
	waitFor(t, func() bool { return f.calls.Load() >= 3 })
	stop()
}

func TestHeartbeatEndsWithTheJobContext(t *testing.T) {
	f := &fakeStamper{}
	ctx, cancel := context.WithCancel(context.Background())
	stop := startTranscodeHeartbeat(ctx, f, uuid.New(), 5*time.Millisecond)
	waitFor(t, func() bool { return f.calls.Load() >= 2 })
	cancel()
	// Without anyone calling stop, the job context alone ends the beat.
	time.Sleep(20 * time.Millisecond)
	settled := f.calls.Load()
	time.Sleep(40 * time.Millisecond)
	if f.calls.Load() != settled {
		t.Fatalf("heartbeat kept stamping after its job context ended: %d -> %d", settled, f.calls.Load())
	}
	done := make(chan struct{})
	go func() { stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat goroutine outlived its job context")
	}
}

func TestBusyTracker(t *testing.T) {
	boot := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	b := newBusyTracker(boot)
	grace := 5 * time.Minute
	if !b.busy(boot.Add(time.Minute), grace) {
		t.Fatal("a just-booted worker must count as busy: redelivered work has not re-stamped yet")
	}
	if b.busy(boot.Add(6*time.Minute), grace) {
		t.Fatal("idle past the grace must count as idle")
	}
	b.begin()
	if !b.busy(boot.Add(time.Hour), grace) {
		t.Fatal("handling a message must count as busy however long it takes")
	}
	b.end(boot.Add(2 * time.Hour))
	if !b.busy(boot.Add(2*time.Hour+time.Minute), grace) {
		t.Fatal("within the grace after a message must count as busy")
	}
	if b.busy(boot.Add(2*time.Hour+6*time.Minute), grace) {
		t.Fatal("past the grace after a message must count as idle")
	}
}

type fakeLookup struct {
	id    string
	found bool
	err   error
}

func (f fakeLookup) CurrentTranscodeRequest(context.Context, uuid.UUID) (string, bool, error) {
	return f.id, f.found, f.err
}

func TestSupersededDelivery(t *testing.T) {
	ctx := context.Background()
	id := uuid.New()
	cases := []struct {
		name string
		look fakeLookup
		want bool
	}{
		{"current request", fakeLookup{id: "ev-1", found: true}, false},
		{"re-queued since", fakeLookup{id: "ev-2", found: true}, true},
		{"no outbox row (pre-outbox event)", fakeLookup{}, false},
		{"lookup failed: fail open", fakeLookup{err: errors.New("db down")}, false},
	}
	for _, c := range cases {
		if got, _ := supersededDelivery(ctx, c.look, id, "ev-1"); got != c.want {
			t.Errorf("%s: superseded=%v, want %v", c.name, got, c.want)
		}
	}
}

type fakeSweeper struct {
	mu        sync.Mutex
	localBusy []bool
}

func (f *fakeSweeper) ReclaimStalledTranscodes(_ context.Context, _ postgres.StallPolicy, localBusy bool) ([]postgres.StallOutcome, error) {
	f.mu.Lock()
	f.localBusy = append(f.localBusy, localBusy)
	f.mu.Unlock()
	return []postgres.StallOutcome{
		{MediaID: uuid.New(), Action: postgres.StallRequeue, EventID: "ev", Attempts: 1, Reason: "r"},
		{MediaID: uuid.New(), Action: postgres.StallGiveUp, EventID: "ev", Attempts: 3, Reason: "r"},
		{MediaID: uuid.New(), Action: postgres.StallSkip},
	}, errors.New("one row failed")
}

func TestSweepPassesThisWorkersBusyState(t *testing.T) {
	f := &fakeSweeper{}
	b := newBusyTracker(time.Now().Add(-time.Hour))
	sweepStalledTranscodes(context.Background(), f, b, postgres.DefaultStallPolicy())
	b.begin()
	sweepStalledTranscodes(context.Background(), f, b, postgres.DefaultStallPolicy())
	if len(f.localBusy) != 2 || f.localBusy[0] || !f.localBusy[1] {
		t.Fatalf("localBusy passed %v, want [false true]", f.localBusy)
	}
}

func TestStallSweepKillSwitch(t *testing.T) {
	t.Setenv("MEDIA_TRANSCODE_STALL_SWEEP", "")
	if !stallSweepEnabled() {
		t.Fatal("sweeper must be on by default")
	}
	t.Setenv("MEDIA_TRANSCODE_STALL_SWEEP", "off")
	if stallSweepEnabled() {
		t.Fatal("MEDIA_TRANSCODE_STALL_SWEEP=off must disable the sweeper")
	}
}
