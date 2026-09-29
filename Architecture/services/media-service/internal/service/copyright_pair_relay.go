package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// The pair relay (Copyright Match plan 6.1, P-15). Its own goroutine, its
// own table, its own topic (media.copyright.pairs). media_event_outbox and
// StartMediaEventOutboxRelay are untouched.
//
//   - claim ≤ 50 due rows with SKIP LOCKED under a 60 s lease;
//   - publish each, keyed by pair_id;
//   - a failure increments attempts, backs the row off (doubling, up to
//     15 min) and CONTINUES with the next row: one poison row never blocks
//     the others, and never touches media.events;
//   - past pairRelayAlertAttempts the row is counted as stuck and logged
//     at alert level on every retry;
//   - ≤ pairRelayBudgetPerSecond events per second per replica;
//   - COPYRIGHT_PAIR_RELAY is the switch, re-read every tick. Default off,
//     like every other Copyright Match flag: rows stay in the outbox.

// CopyrightPairsTopic is the relay's topic, for cmd/server to build the
// producer with.
const CopyrightPairsTopic = events.MediaCopyrightPairsTopic

const (
	pairRelayInterval        = 2 * time.Second
	pairRelayLease           = 60 * time.Second
	pairRelayBatch           = 50
	pairRelayMaxBackoff      = 15 * time.Minute
	pairRelayAlertAttempts   = 20
	pairRelayBudgetPerSecond = 200
)

var (
	pairRelayTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "media_copyright_pair_relay_total",
		Help: "Pair outbox rows the relay handled, by result.",
	}, []string{"result"})
	pairOutboxPending = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "media_copyright_pair_outbox_pending",
		Help: "Unpublished pair outbox rows.",
	})
	pairOutboxOldest = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "media_copyright_pair_outbox_oldest_pending_seconds",
		Help: "Age of the oldest unpublished pair outbox row.",
	})
	pairOutboxStuck = promauto.NewGauge(prometheus.GaugeOpts{
		Name: "media_copyright_pair_outbox_stuck",
		Help: "Unpublished pair outbox rows past the alert attempt threshold.",
	})
)

// pairOutboxStore is the store surface the relay uses.
type pairOutboxStore interface {
	ClaimPairEvents(ctx context.Context, limit int, lease time.Duration) ([]postgres.PairOutboxRow, error)
	MarkPairEventPublished(ctx context.Context, eventID uuid.UUID) error
	RecordPairEventFailure(ctx context.Context, eventID uuid.UUID, cause error, backoff time.Duration) (int, error)
	PairOutboxStats(ctx context.Context, alertAttempts int) (postgres.PairOutboxStats, error)
}

// pairPublisher is the producer surface the relay uses.
type pairPublisher interface {
	PublishEnvelopeKeyed(ctx context.Context, key string, envelope events.EventEnvelope) error
}

// PairRelayEnabled reads the switch: COPYRIGHT_PAIR_RELAY=on|true|1.
func PairRelayEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COPYRIGHT_PAIR_RELAY"))) {
	case "on", "true", "1", "enabled", "yes":
		return true
	}
	return false
}

// StartCopyrightPairRelay runs the relay until ctx ends. producer must
// write to events.MediaCopyrightPairsTopic.
func (s *Service) StartCopyrightPairRelay(ctx context.Context, producer pairPublisher) {
	if s == nil || s.pgStore == nil || producer == nil {
		return
	}
	go func() {
		r := &pairRelay{store: s.pgStore, producer: producer, enabled: PairRelayEnabled, now: time.Now, sleep: sleepCtx}
		ticker := time.NewTicker(pairRelayInterval)
		defer ticker.Stop()
		var lastEnabled *bool
		for {
			on := r.enabled()
			if lastEnabled == nil || *lastEnabled != on {
				state := "OFF (pair events stay in copyright_pair_outbox)"
				if on {
					state = "ON"
				}
				slog.Info("copyright pair relay: COPYRIGHT_PAIR_RELAY is " + state)
				lastEnabled = &on
			}
			r.exportStats(ctx)
			if on {
				r.drain(ctx)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func sleepCtx(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

// pairRelay is the relay with its dependencies injectable for tests.
type pairRelay struct {
	store    pairOutboxStore
	producer pairPublisher
	enabled  func() bool
	now      func() time.Time
	sleep    func(context.Context, time.Duration)

	mu sync.Mutex
}

func (r *pairRelay) exportStats(ctx context.Context) {
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	st, err := r.store.PairOutboxStats(sctx, pairRelayAlertAttempts)
	if err != nil {
		if ctx.Err() == nil {
			slog.Warn("copyright pair relay: stats failed", "error", err)
		}
		return
	}
	pairOutboxPending.Set(float64(st.Pending))
	pairOutboxOldest.Set(st.OldestPendingAgeS)
	pairOutboxStuck.Set(float64(st.StuckPastAttempts))
}

// pairRelayBackoff doubles from 2 s per attempt, capped at 15 min.
func pairRelayBackoff(attempts int) time.Duration {
	d := 2 * time.Second
	for i := 1; i < attempts && d < pairRelayMaxBackoff; i++ {
		d *= 2
	}
	if d > pairRelayMaxBackoff {
		d = pairRelayMaxBackoff
	}
	return d
}

// drain claims one batch and publishes it, continuing past failures.
// Returns how many rows were published.
func (r *pairRelay) drain(ctx context.Context) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	rows, err := r.store.ClaimPairEvents(ctx, pairRelayBatch, pairRelayLease)
	if err != nil {
		if ctx.Err() == nil {
			slog.Error("copyright pair relay: claim failed", "error", err)
		}
		return 0
	}
	published := 0
	windowStart := r.now()
	sent := 0
	for _, row := range rows {
		if ctx.Err() != nil {
			return published
		}
		// Budget: ≤ pairRelayBudgetPerSecond per replica.
		if sent >= pairRelayBudgetPerSecond {
			if elapsed := r.now().Sub(windowStart); elapsed < time.Second {
				r.sleep(ctx, time.Second-elapsed)
			}
			windowStart, sent = r.now(), 0
		}
		sent++
		envelope := events.EventEnvelope{
			EventID:    row.EventID.String(),
			EventType:  row.EventType,
			OccurredAt: row.CreatedAt,
			Payload:    json.RawMessage(row.Payload),
		}
		if err := r.producer.PublishEnvelopeKeyed(ctx, row.PairID.String(), envelope); err != nil {
			attempts, rerr := r.store.RecordPairEventFailure(ctx, row.EventID, err, pairRelayBackoff(row.Attempts+1))
			if rerr != nil {
				slog.Error("copyright pair relay: record failure failed", "event_id", row.EventID, "error", rerr)
			}
			pairRelayTotal.WithLabelValues("failed").Inc()
			if attempts >= pairRelayAlertAttempts {
				slog.Error("ALERT copyright pair relay: row stuck past the attempt threshold",
					"event_id", row.EventID, "pair_id", row.PairID, "attempts", attempts, "error", err)
			} else {
				slog.Warn("copyright pair relay: publish failed; continuing with the next row",
					"event_id", row.EventID, "pair_id", row.PairID, "attempts", attempts, "error", err)
			}
			continue // never stop on one row
		}
		if err := r.store.MarkPairEventPublished(ctx, row.EventID); err != nil {
			// The lease expires and the same event_id is published again;
			// the consumer inbox collapses it.
			slog.Error("copyright pair relay: mark published failed; duplicate will follow", "event_id", row.EventID, "error", err)
			pairRelayTotal.WithLabelValues("mark_failed").Inc()
			continue
		}
		pairRelayTotal.WithLabelValues("published").Inc()
		published++
	}
	return published
}
