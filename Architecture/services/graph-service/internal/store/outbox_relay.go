package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 3 SR-2 — the outbox RELAY.
//
// Writing the outbox row inside the relationship transaction guarantees the
// event is RECORDED. It does not deliver anything. Until this file existed,
// graph_outbox_events accumulated rows that nothing ever read: the durability
// claim was half-built, and the observable behaviour — a block whose event
// never reaches chat — was identical to the fire-and-forget goroutine it
// replaced.
//
// The relay is the other half:
//
//   - It LEASES rows, so N replicas can run without publishing twice. The
//     lease is taken with FOR UPDATE SKIP LOCKED inside a transaction, which
//     is the only construct that gives at-most-one-worker-per-row without a
//     separate coordination service.
//   - It marks published only AFTER the publish call returns success. The
//     ordering is deliberate: mark-then-publish would lose events on a crash
//     between the two, which is exactly the failure mode being fixed.
//   - A publish failure records the error and backs off, so a broker outage
//     produces retries rather than silent loss.
//
// Delivery is therefore AT LEAST ONCE. Consumers dedupe on (actor, target,
// pair_seq) — that is what the monotonic per-pair sequence is for.

// OutboxEvent is one durable graph event awaiting delivery.
type OutboxEvent struct {
	ID        uuid.UUID
	EventType string
	ActorID   uuid.UUID
	TargetID  uuid.UUID
	PairSeq   int64
	Payload   json.RawMessage
	Attempts  int
}

// OutboxPublisher delivers one event. Returning an error causes a retry.
type OutboxPublisher interface {
	PublishGraphEvent(ctx context.Context, ev OutboxEvent) error
}

// RelayConfig tunes the relay loop.
type RelayConfig struct {
	// BatchSize is how many rows one pass leases.
	BatchSize int
	// PollInterval is the idle wait when there was nothing to do.
	PollInterval time.Duration
	// AlertAfterAttempts is the attempt count at which a row is reported as
	// STUCK. It is NOT a cap.
	//
	// LB-2: this used to be MaxAttempts=10, and the lease query excluded rows
	// at the cap. With a 2-second backoff, ten attempts is roughly twenty
	// seconds — so a routine broker restart permanently parked a safety event,
	// and a process restart did not retry it. A block that never reaches chat
	// is not an event to file for later; it is a user who is still reachable
	// by someone they blocked. Delivery must keep trying.
	AlertAfterAttempts int
	// RetryBackoff is the base delay before a failed row is retried. The
	// actual delay grows exponentially with the attempt count, capped at
	// MaxRetryBackoff, so a long outage produces paced retries rather than a
	// hot loop — without ever giving up.
	RetryBackoff time.Duration
	// MaxRetryBackoff caps the exponential growth.
	MaxRetryBackoff time.Duration
}

func DefaultRelayConfig() RelayConfig {
	return RelayConfig{
		BatchSize:          100,
		PollInterval:       500 * time.Millisecond,
		AlertAfterAttempts: 10,
		RetryBackoff:       2 * time.Second,
		MaxRetryBackoff:    5 * time.Minute,
	}
}

// RelayOnce leases and delivers up to cfg.BatchSize events, returning how many
// were published successfully. Exposed separately from Run so tests can drive
// exactly one pass and assert on the result without racing a background loop.
func (s *Store) RelayOnce(ctx context.Context, pub OutboxPublisher, cfg RelayConfig) (published int, err error) {
	if cfg.BatchSize <= 0 {
		cfg = DefaultRelayConfig()
	}

	// Lease inside its own transaction. The lease transaction commits BEFORE
	// publishing so the rows are not held under lock for the duration of a
	// network call to the broker — a slow broker would otherwise block every
	// other replica behind SKIP LOCKED for as long as the publish took.
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("relay: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	leaseUntil := time.Now().Add(30 * time.Second)

	// LB-2: there is NO attempts cap in this predicate.
	//
	// The previous version had `attempts < MaxAttempts`, which meant a row that
	// exhausted its attempts became permanently invisible to the relay — a
	// safety event silently abandoned after roughly twenty seconds of broker
	// trouble, unrecoverable without editing the row by hand.
	//
	// Instead the backoff GROWS with the attempt count (base * 2^(attempts-1),
	// capped), so a long outage is paced rather than abandoned. Rows that
	// exceed AlertAfterAttempts are reported by StuckOutboxEvents for alerting
	// while still being retried.
	backoffExpr := `LEAST($1::interval * POWER(2, LEAST(e2.attempts, 20)), $2::interval)`
	rows, err := tx.Query(ctx, `
		WITH claimed AS (
			SELECT e2.id FROM graph_outbox_events e2
			WHERE e2.published = FALSE
			  AND (e2.leased_until IS NULL OR e2.leased_until < NOW())
			  AND (e2.last_attempt_at IS NULL
			       OR e2.last_attempt_at < NOW() - `+backoffExpr+`)
			ORDER BY e2.occurred_at
			LIMIT $3
			FOR UPDATE SKIP LOCKED
		)
		UPDATE graph_outbox_events e
		SET leased_until = $4, attempts = e.attempts + 1, last_attempt_at = NOW()
		FROM claimed
		WHERE e.id = claimed.id
		RETURNING e.id, e.event_type, e.actor_id, e.target_id, e.pair_seq, e.payload, e.attempts`,
		fmt.Sprintf("%d milliseconds", cfg.RetryBackoff.Milliseconds()),
		fmt.Sprintf("%d milliseconds", cfg.MaxRetryBackoff.Milliseconds()),
		cfg.BatchSize, leaseUntil)
	if err != nil {
		return 0, fmt.Errorf("relay: lease: %w", err)
	}

	var batch []OutboxEvent
	for rows.Next() {
		var ev OutboxEvent
		if err := rows.Scan(&ev.ID, &ev.EventType, &ev.ActorID, &ev.TargetID,
			&ev.PairSeq, &ev.Payload, &ev.Attempts); err != nil {
			rows.Close()
			return 0, fmt.Errorf("relay: scan: %w", err)
		}
		batch = append(batch, ev)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("relay: rows: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("relay: commit lease: %w", err)
	}

	// Publish OUTSIDE the lease transaction, and mark published only after the
	// publisher confirms. A crash here re-delivers on the next pass, which is
	// the correct direction to fail for a safety signal.
	for _, ev := range batch {
		if err := pub.PublishGraphEvent(ctx, ev); err != nil {
			if markErr := s.markOutboxFailed(ctx, ev.ID, err); markErr != nil {
				return published, fmt.Errorf("relay: record failure for %s: %w", ev.ID, markErr)
			}
			continue
		}
		if err := s.markOutboxPublished(ctx, ev.ID); err != nil {
			// The event WAS delivered but we could not record it. The next
			// pass re-delivers; consumers dedupe on (actor, target, pair_seq).
			return published, fmt.Errorf("relay: mark published %s: %w", ev.ID, err)
		}
		published++
	}
	return published, nil
}

func (s *Store) markOutboxPublished(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE graph_outbox_events
		SET published = TRUE, published_at = NOW(), leased_until = NULL, last_error = NULL
		WHERE id = $1`, id)
	return err
}

func (s *Store) markOutboxFailed(ctx context.Context, id uuid.UUID, cause error) error {
	_, err := s.db.Exec(ctx, `
		UPDATE graph_outbox_events
		SET leased_until = NULL, last_error = $2
		WHERE id = $1`, id, cause.Error())
	return err
}

// RunRelay is the long-running loop. It returns when ctx is cancelled.
func (s *Store) RunRelay(ctx context.Context, pub OutboxPublisher, cfg RelayConfig, logf func(string, ...any)) {
	if cfg.BatchSize <= 0 {
		cfg = DefaultRelayConfig()
	}
	if logf == nil {
		logf = func(string, ...any) {}
	}
	for {
		n, err := s.RelayOnce(ctx, pub, cfg)
		switch {
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			return
		case err != nil:
			logf("[graph] outbox relay pass failed: %v", err)
		}

		if n > 0 {
			// Drain aggressively while there is a backlog; a block that has
			// not reached chat is a live safety gap, not a queue depth metric.
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(cfg.PollInterval):
		}
	}
}

// UnpublishedOutboxCount is the operational signal that matters: a growing
// count means safety events are not reaching consumers. Wire it to a gauge.
func (s *Store) UnpublishedOutboxCount(ctx context.Context) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM graph_outbox_events WHERE published = FALSE`).Scan(&n)
	return n, err
}

// StuckOutboxEvents are rows that have exceeded AlertAfterAttempts and are
// STILL BEING RETRIED.
//
// LB-2: this replaces ParkedOutboxEvents, which reported rows the relay had
// permanently abandoned. Nothing is abandoned now — this is an alerting
// signal, not a graveyard. Wire it to a gauge: a non-zero value means a safety
// event has not reached its consumers yet, and the number is how many people
// are currently unprotected downstream.
func (s *Store) StuckOutboxEvents(ctx context.Context, afterAttempts, limit int) ([]OutboxEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_type, actor_id, target_id, pair_seq, payload, attempts
		FROM graph_outbox_events
		WHERE published = FALSE AND attempts >= $1
		ORDER BY occurred_at
		LIMIT $2`, afterAttempts, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []OutboxEvent
	for rows.Next() {
		var ev OutboxEvent
		if err := rows.Scan(&ev.ID, &ev.EventType, &ev.ActorID, &ev.TargetID,
			&ev.PairSeq, &ev.Payload, &ev.Attempts); err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}

var _ = pgx.ErrNoRows
