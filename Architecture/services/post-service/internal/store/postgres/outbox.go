package postgres

import (
	"context"
	"encoding/json"
	"hash/fnv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// OutboxEvent represents a transactional outbox record for reliable Kafka publishing.
type OutboxEvent struct {
	ID            uuid.UUID       `json:"id"`
	EventType     string          `json:"event_type"`
	AggregateType string          `json:"aggregate_type"`
	AggregateID   uuid.UUID       `json:"aggregate_id"`
	Payload       json.RawMessage `json:"payload"`
	Published     bool            `json:"published"`
	PublishedAt   *time.Time      `json:"published_at,omitempty"`
	CreatedAt     time.Time       `json:"created_at"`
}

// InsertOutboxEvent inserts a new outbox event within the given transaction.
func InsertOutboxEventTx(ctx context.Context, tx pgx.Tx, eventType, aggregateType string, aggregateID uuid.UUID, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO post_outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, eventType, aggregateType, aggregateID, payloadBytes)
	return err
}

// InsertOutboxEvent inserts a new outbox event (non-transactional convenience).
func (s *Store) InsertOutboxEvent(ctx context.Context, eventType, aggregateType string, aggregateID uuid.UUID, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO post_outbox_events (event_type, aggregate_type, aggregate_id, payload, created_at)
		VALUES ($1, $2, $3, $4, NOW())
	`, eventType, aggregateType, aggregateID, payloadBytes)
	return err
}

// outboxRelayLockKey is the advisory lock one relay pass holds: FNV-1a of
// the name below, masked to a positive bigint the way shared/outbox derives
// its keys (a different name, so the two can never collide).
var outboxRelayLockKey = func() int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("post-service:outbox-relay:post_outbox_events"))
	return int64(h.Sum64() & 0x7FFFFFFFFFFFFFFF)
}()

const (
	outboxRelayTryLockSQL = `SELECT pg_try_advisory_lock($1)`
	outboxRelayUnlockSQL  = `SELECT pg_advisory_unlock($1)`
	// outboxRelayUnlockTimeout bounds the unlock, which runs on a context of
	// its own so a cancelled pass (shutdown) still releases.
	outboxRelayUnlockTimeout = 5 * time.Second
)

// TryOutboxRelayLock takes the outbox relay's advisory lock without waiting
// (feed-07): ok is false when another replica's pass holds it, and the
// caller then skips its pass. Every replica runs the relay, and with no
// claim two of them read the same unpublished rows and published each twice.
//
// The lock is session-scoped, so it lives on one connection taken out of the
// pool for the whole pass (the pass's own queries use other connections) and
// release must be called exactly once: it unlocks and returns the
// connection. If the unlock cannot be confirmed the connection is closed
// instead of being pooled with the lock still on it; if the process dies,
// the connection closes and Postgres drops the lock. The same shape as
// shared/outbox (store.go tryLock).
func (s *Store) TryOutboxRelayLock(ctx context.Context) (release func(), ok bool, err error) {
	conn, err := s.db.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var got bool
	if err := conn.QueryRow(ctx, outboxRelayTryLockSQL, outboxRelayLockKey).Scan(&got); err != nil {
		conn.Release()
		return nil, false, err
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	return func() {
		unlockCtx, cancel := context.WithTimeout(context.Background(), outboxRelayUnlockTimeout)
		defer cancel()
		var unlocked bool
		if err := conn.QueryRow(unlockCtx, outboxRelayUnlockSQL, outboxRelayLockKey).Scan(&unlocked); err != nil || !unlocked {
			_ = conn.Conn().Close(unlockCtx)
		}
		conn.Release()
	}, true, nil
}

// unpublishedOutboxSQL reads the relay's next rows, oldest first.
//
// created_at is the writing transaction's start time, so the events one
// transaction writes for one post tie on it; seq (migration 085, a sequence
// value taken at INSERT) puts them in the order they were written. Rows
// from before 085 have no seq and sort first within their created_at, by
// id, so a retried pass reads the same order either way.
const unpublishedOutboxSQL = `
		SELECT id, event_type, aggregate_type, aggregate_id, payload, published, published_at, created_at
		FROM post_outbox_events
		WHERE published = FALSE
		ORDER BY created_at ASC, seq ASC NULLS FIRST, id ASC
		LIMIT $1
	`

// GetUnpublishedOutboxEvents returns unpublished outbox events in the order
// the relay publishes them (unpublishedOutboxSQL).
func (s *Store) GetUnpublishedOutboxEvents(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := s.db.Query(ctx, unpublishedOutboxSQL, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.AggregateType, &e.AggregateID,
			&e.Payload, &e.Published, &e.PublishedAt, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// MarkOutboxEventPublished marks an outbox event as successfully published.
func (s *Store) MarkOutboxEventPublished(ctx context.Context, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE post_outbox_events SET published = TRUE, published_at = NOW()
		WHERE id = $1
	`, id)
	return err
}

// CleanupOldOutboxEvents removes published outbox events older than the retention period.
func (s *Store) CleanupOldOutboxEvents(ctx context.Context, retentionHours int) (int64, error) {
	// make_interval(hours => $1) takes an int directly — avoids the
	// ($1 || ' hours') text concatenation that errored on the int arg
	// ("cannot encode 48 into text").
	tag, err := s.db.Exec(ctx, `
		DELETE FROM post_outbox_events
		WHERE published = TRUE AND published_at < NOW() - make_interval(hours => $1)
	`, retentionHours)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
