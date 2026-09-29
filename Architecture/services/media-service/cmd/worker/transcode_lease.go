package main

import (
	"context"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// The worker's half of the transcode lease (migration 021; the rule and the
// store side are in internal/store/postgres/transcode_lease.go).
//
//   - every transcode stamps a heartbeat on its asset when it starts and every
//     heartbeatInterval while it runs;
//   - a sweeper goroutine in this same process re-queues assets whose job
//     died, and fails them after the attempts cap.
//
// The sweeper lives in the worker, not the API server, on purpose: it only
// runs while a consumer is alive, so a worker outage — messages safely
// waiting in Kafka — can never be mistaken for lost work and burn through
// every queued upload's attempts.

const (
	// heartbeatInterval is well inside the 10-minute stale threshold, so a
	// run of failed writes has to last twenty beats before a live job can be
	// mistaken for a dead one.
	heartbeatInterval = 30 * time.Second
	// heartbeatWriteTimeout bounds one stamp so a hung database cannot pile
	// up stamps behind it.
	heartbeatWriteTimeout = 10 * time.Second

	// stallSweepInterval: "every few minutes". The first sweep also waits one
	// interval, so after a restart the redelivered job re-stamps its own
	// heartbeat before anything judges it dead.
	stallSweepInterval = 5 * time.Minute
	// localIdleGrace: the worker counts as busy for this long after its last
	// message, so an asset whose message is about to be fetched is not
	// re-queued in the instant between two jobs.
	localIdleGrace = 5 * time.Minute
)

var stallReclaims = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "media_transcode_stall_reclaims_total",
	Help: "Stalled transcodes the sweeper re-queued or gave up on, by action.",
}, []string{"action"})

// heartbeatStamper is the one store call the heartbeat needs.
type heartbeatStamper interface {
	StampTranscodeHeartbeat(ctx context.Context, mediaID uuid.UUID) error
}

// startTranscodeHeartbeat stamps the lease now and then every interval until
// the returned stop is called or ctx ends. stop waits for the goroutine, so no
// stamp lands after the job has returned.
//
// A failed stamp is logged and nothing else: the job never learns about it.
// The worst a lost heartbeat can do is let the sweeper re-queue a job that is
// in fact alive, which costs a duplicate request; failing the job would cost
// the upload.
func startTranscodeHeartbeat(ctx context.Context, store heartbeatStamper, mediaID uuid.UUID, interval time.Duration) (stop func()) {
	stamp := func() {
		sctx, cancel := context.WithTimeout(ctx, heartbeatWriteTimeout)
		defer cancel()
		if err := store.StampTranscodeHeartbeat(sctx, mediaID); err != nil && ctx.Err() == nil {
			log.Printf("Warning: transcode heartbeat for media %s not written (job continues): %v", mediaID, err)
		}
	}
	stamp()

	hbCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				stamp()
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
}

// busyTracker records whether this worker is handling a message.
type busyTracker struct {
	mu       sync.Mutex
	active   int
	lastIdle time.Time // when the last message finished (or the worker booted)
}

func newBusyTracker(now time.Time) *busyTracker { return &busyTracker{lastIdle: now} }

func (b *busyTracker) begin() {
	b.mu.Lock()
	b.active++
	b.mu.Unlock()
}

func (b *busyTracker) end(now time.Time) {
	b.mu.Lock()
	if b.active > 0 {
		b.active--
	}
	if b.active == 0 {
		b.lastIdle = now
	}
	b.mu.Unlock()
}

// busy: handling a message now, or idle for less than grace.
func (b *busyTracker) busy(now time.Time, grace time.Duration) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.active > 0 || now.Sub(b.lastIdle) < grace
}

// stallSweeper is the store call the sweeper needs.
type stallSweeper interface {
	ReclaimStalledTranscodes(ctx context.Context, p postgres.StallPolicy, localBusy bool) ([]postgres.StallOutcome, error)
}

// stallSweepEnabled: MEDIA_TRANSCODE_STALL_SWEEP=off is the kill switch.
func stallSweepEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("MEDIA_TRANSCODE_STALL_SWEEP"))) {
	case "off", "false", "0", "disabled":
		return false
	}
	return true
}

// runStallSweeper sweeps every interval until ctx ends.
func runStallSweeper(ctx context.Context, store stallSweeper, tracker *busyTracker, policy postgres.StallPolicy, interval time.Duration) {
	log.Printf("Transcode stall sweeper started (every %s; heartbeat stale after %s, "+
		"never-started after %s with the pipeline idle, give up after %d re-queues)",
		interval, policy.HeartbeatStaleAfter, policy.OrphanAfter, policy.MaxAttempts)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sweepStalledTranscodes(ctx, store, tracker, policy)
		}
	}
}

// sweepStalledTranscodes runs one sweep and logs every action by media id.
func sweepStalledTranscodes(ctx context.Context, store stallSweeper, tracker *busyTracker, policy postgres.StallPolicy) {
	outcomes, err := store.ReclaimStalledTranscodes(ctx, policy, tracker.busy(time.Now(), localIdleGrace))
	if err != nil && ctx.Err() == nil {
		log.Printf("Transcode stall sweep error: %v", err)
	}
	skipped := 0
	for _, o := range outcomes {
		switch o.Action {
		case postgres.StallRequeue:
			stallReclaims.WithLabelValues("requeue").Inc()
			log.Printf("Transcode stall: RE-QUEUED media %s (event %s, attempt %d): %s",
				o.MediaID, o.EventID, o.Attempts, o.Reason)
		case postgres.StallGiveUp:
			stallReclaims.WithLabelValues("give_up").Inc()
			log.Printf("Transcode stall: GAVE UP on media %s, marked failed (event %s): %s",
				o.MediaID, o.EventID, o.Reason)
		default:
			skipped++
		}
	}
	if skipped > 0 {
		log.Printf("Transcode stall sweep: %d candidate(s) left alone (in flight, alive, or queued behind running work)", skipped)
	}
}

// requestLookup is the store call the supersede check needs.
type requestLookup interface {
	CurrentTranscodeRequest(ctx context.Context, mediaID uuid.UUID) (string, bool, error)
}

// supersededDelivery reports whether this delivery is no longer the asset's
// current transcode request — a re-queue (by the stall sweeper or the
// operator reprocess route) replaced it with a fresh event id while the
// original was still waiting in Kafka. Running it would be a second full
// transcode of the same asset, hours of it for a long video; the current
// request's own message does the work.
//
// Fails OPEN: a lookup error or a missing row processes the delivery, because
// doing the work twice is waste and skipping it could lose the asset.
func supersededDelivery(ctx context.Context, store requestLookup, mediaID uuid.UUID, eventID string) (bool, string) {
	current, found, err := store.CurrentTranscodeRequest(ctx, mediaID)
	if err != nil {
		log.Printf("Warning: could not check whether transcode event %s for media %s is current (processing it): %v",
			eventID, mediaID, err)
		return false, ""
	}
	if !found || current == eventID {
		return false, current
	}
	return true, current
}
