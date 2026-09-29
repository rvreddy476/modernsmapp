package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Enforcement outbox (Copyright Match plan section 6.4, P-7) ──────────────
//
// A row here is written in the SAME transaction as the change it announces
// (strike issue/void, purge erase), so the change and its event commit or
// roll back together. The dispatcher (internal/outbox) publishes rows in id
// order and marks them; a crash between the two re-publishes the same
// event_id, which consumers dedupe.

// DefaultTrustEventsTopic is where trust-safety's own events go today
// (ReportFiled and friends are written there directly by the service).
const DefaultTrustEventsTopic = "social.events.v1"

// OutboxEvent is one trust.enforcement_outbox row. Payload is the exact
// Kafka message value; PartitionKey the message key.
type OutboxEvent struct {
	ID           int64
	EventID      uuid.UUID
	EventType    string
	Topic        string
	PartitionKey string
	Payload      json.RawMessage
	CreatedAt    time.Time
	Attempts     int
}

// newEnvelopeEvent wraps payload in the platform EventEnvelope, minting the
// event id the envelope and the row share.
func newEnvelopeEvent(topic, eventType, partitionKey string, occurredAt time.Time, payload any) (OutboxEvent, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("outbox: marshal %s payload: %w", eventType, err)
	}
	id := uuid.New()
	value, err := json.Marshal(events.EventEnvelope{
		EventID:    id.String(),
		EventType:  eventType,
		OccurredAt: occurredAt.UTC(),
		Payload:    body,
	})
	if err != nil {
		return OutboxEvent{}, fmt.Errorf("outbox: marshal %s envelope: %w", eventType, err)
	}
	return OutboxEvent{EventID: id, EventType: eventType, Topic: topic, PartitionKey: partitionKey, Payload: value}, nil
}

// enqueueOutbox writes one row inside tx. The caller must roll the change
// back if this fails.
func enqueueOutbox(ctx context.Context, tx pgx.Tx, ev OutboxEvent) error {
	if ev.EventID == uuid.Nil || ev.EventType == "" || ev.Topic == "" || len(ev.Payload) == 0 {
		return fmt.Errorf("outbox: event is incomplete (type=%q topic=%q)", ev.EventType, ev.Topic)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO trust.enforcement_outbox (event_id, event_type, topic, partition_key, payload)
		VALUES ($1, $2, $3, $4, $5::jsonb)
	`, ev.EventID, ev.EventType, ev.Topic, ev.PartitionKey, ev.Payload); err != nil {
		return fmt.Errorf("outbox: enqueue %s: %w", ev.EventType, err)
	}
	return nil
}

// OutboxStore is the dispatcher's view of trust.enforcement_outbox.
type OutboxStore struct {
	db *pgxpool.Pool
}

// NewOutboxStore constructs an OutboxStore.
func NewOutboxStore(db *pgxpool.Pool) *OutboxStore {
	return &OutboxStore{db: db}
}

// PendingOutbox returns unpublished rows whose retry time has come, oldest
// first. Two dispatchers may read the same row; that yields a duplicate
// with the same event_id, never a loss.
func (s *OutboxStore) PendingOutbox(ctx context.Context, limit int) ([]OutboxEvent, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, event_id, event_type, topic, partition_key, payload, created_at, attempts
		  FROM trust.enforcement_outbox
		 WHERE published_at IS NULL
		   AND next_attempt_at <= clock_timestamp()
		 ORDER BY id ASC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("outbox: list pending: %w", err)
	}
	defer rows.Close()
	out := make([]OutboxEvent, 0, limit)
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventID, &e.EventType, &e.Topic, &e.PartitionKey, &e.Payload, &e.CreatedAt, &e.Attempts); err != nil {
			return nil, fmt.Errorf("outbox: scan pending: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// MarkOutboxPublished records a successful publish. Idempotent: a row
// already marked keeps its first published_at.
func (s *OutboxStore) MarkOutboxPublished(ctx context.Context, id int64) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE trust.enforcement_outbox
		   SET published_at = COALESCE(published_at, clock_timestamp()),
		       attempts = attempts + 1,
		       last_error = NULL
		 WHERE id = $1
	`, id)
	if err != nil {
		return fmt.Errorf("outbox: mark published: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("outbox: mark published: row %d not found", id)
	}
	return nil
}

// RecordOutboxFailure counts a failed attempt and holds the row back for
// retryAfter (zero: eligible again immediately).
func (s *OutboxStore) RecordOutboxFailure(ctx context.Context, id int64, cause error, retryAfter time.Duration) error {
	msg := "unknown error"
	if cause != nil {
		msg = cause.Error()
	}
	if len(msg) > 2000 {
		msg = msg[:2000]
	}
	_, err := s.db.Exec(ctx, `
		UPDATE trust.enforcement_outbox
		   SET attempts = attempts + 1,
		       last_error = $2,
		       next_attempt_at = clock_timestamp() + ($3::bigint * interval '1 millisecond')
		 WHERE id = $1 AND published_at IS NULL
	`, id, msg, retryAfter.Milliseconds())
	if err != nil {
		return fmt.Errorf("outbox: record failure: %w", err)
	}
	return nil
}

// OldestPendingAge is the age of the oldest unpublished row (zero when the
// outbox is drained). The plan's enforcement_outbox_oldest_pending_seconds.
func (s *OutboxStore) OldestPendingAge(ctx context.Context) (time.Duration, error) {
	var age *float64
	if err := s.db.QueryRow(ctx, `
		SELECT EXTRACT(EPOCH FROM clock_timestamp() - min(created_at))
		  FROM trust.enforcement_outbox WHERE published_at IS NULL
	`).Scan(&age); err != nil {
		return 0, fmt.Errorf("outbox: oldest pending: %w", err)
	}
	if age == nil {
		return 0, nil
	}
	return time.Duration(*age * float64(time.Second)), nil
}
