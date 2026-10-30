package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Outbox relay claims and dead letters (audit H3/H4, 30 Oct 2026; columns
// from migration 019).
const (
	// outboxClaimLease is how long a claimed row is reserved for the relay
	// that claimed it. A relay that dies mid-publish holds its rows this
	// long; a live one marks them published (or failed) well before.
	outboxClaimLease = 30 * time.Second
	// OutboxMaxAttempts is how many counted failures (a payload that does
	// not open, a message the broker refuses) a row gets before it is
	// dead-lettered. Transient failures (Kafka unreachable) are not
	// counted: an outage delays events, it never dead-letters them.
	OutboxMaxAttempts = 10
	// outboxRelayLockKey is the advisory lock that makes one replica at a
	// time the relay, so the events of one conversation reach Kafka in id
	// order even with several replicas running.
	outboxRelayLockKey int64 = 0x63686174_6f757462 // "chatoutb"
)

// claimOutboxEventsSQL claims due rows ($1 = limit, $2 = lease seconds).
const claimOutboxEventsSQL = `
	UPDATE chat.outbox_events o
	SET claimed_until = NOW() + make_interval(secs => $2::float8)
	WHERE o.id IN (
		SELECT id FROM chat.outbox_events
		WHERE published_at IS NULL AND failed_at IS NULL
		  AND (claimed_until IS NULL OR claimed_until <= NOW())
		ORDER BY id
		LIMIT $1
		FOR UPDATE SKIP LOCKED
	)
	RETURNING o.id, o.event_type, COALESCE(o.dedupe_key, ''), o.payload, o.created_at`

// OutboxFailure describes why one claimed row was not published.
type OutboxFailure struct {
	// Reason is stored in last_error (truncated). It must never carry the
	// payload: callers pass error text, not content.
	Reason string
	// Permanent dead-letters the row now (a message too large to ever
	// publish).
	Permanent bool
	// Counted counts the failure toward OutboxMaxAttempts and backs the row
	// off exponentially. An uncounted (transient) failure releases the
	// claim so the next poll retries the row in order.
	Counted bool
}

// recordOutboxFailureSQL: $1 id, $2 reason, $3 permanent, $4 counted,
// $5 max attempts. Returns whether the row is now dead-lettered.
const recordOutboxFailureSQL = `
	UPDATE chat.outbox_events
	SET attempts = attempts + 1,
	    last_error = $2,
	    failed_at = COALESCE(failed_at,
	        CASE WHEN $3::bool OR ($4::bool AND attempts + 1 >= $5) THEN NOW() END),
	    claimed_until = CASE
	        WHEN failed_at IS NOT NULL OR $3::bool OR ($4::bool AND attempts + 1 >= $5) THEN NULL
	        WHEN $4::bool THEN NOW() + LEAST(INTERVAL '5 minutes', INTERVAL '1 second' * power(2, LEAST(attempts, 16)))
	        ELSE NULL
	    END
	WHERE id = $1 AND published_at IS NULL
	RETURNING failed_at IS NOT NULL`

// RecordOutboxEventFailure records a failed publish of one claimed row and
// reports whether the row is now dead-lettered (failed_at set). A
// dead-lettered row stays in the table for an operator; requeue it with
// UPDATE chat.outbox_events SET failed_at = NULL, attempts = 0 WHERE id = ...
func (s *ConversationStore) RecordOutboxEventFailure(ctx context.Context, id int64, f OutboxFailure) (bool, error) {
	reason := f.Reason
	if len(reason) > 1000 {
		reason = reason[:1000]
	}
	var dead bool
	err := s.db.QueryRow(ctx, recordOutboxFailureSQL, id, reason, f.Permanent, f.Counted, OutboxMaxAttempts).Scan(&dead)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // published meanwhile
	}
	return dead, err
}

// MarkOutboxEventsPublished marks a published batch in one statement.
func (s *ConversationStore) MarkOutboxEventsPublished(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx, `
		UPDATE chat.outbox_events SET published_at = NOW(), claimed_until = NULL
		WHERE id = ANY($1) AND published_at IS NULL`, ids)
	return err
}

// poolAcquirer is the part of *pgxpool.Pool the relay lock needs.
type poolAcquirer interface {
	Acquire(ctx context.Context) (*pgxpool.Conn, error)
}

// TryOutboxRelayLock makes this replica the outbox relay for one pass. It
// takes a session advisory lock on a dedicated connection; ok is false when
// another replica holds it (that replica relays this pass). release unlocks
// and returns the connection. If the process dies, the connection closes
// and Postgres drops the lock. Against a store without a pool (unit tests)
// the lock is a no-op that always succeeds; the row claim alone still
// prevents a double publish.
func (s *ConversationStore) TryOutboxRelayLock(ctx context.Context) (release func(), ok bool, err error) {
	pool, isPool := s.db.(poolAcquirer)
	if !isPool {
		return func() {}, true, nil
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, outboxRelayLockKey).Scan(&got); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := conn.Exec(unlockCtx, `SELECT pg_advisory_unlock($1)`, outboxRelayLockKey); err != nil {
			// The lock is session-scoped: close the connection rather than
			// return a pooled connection that still holds it.
			_ = conn.Conn().Close(unlockCtx)
		}
		conn.Release()
	}, true, nil
}
