package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// OutboxEvent represents a row in the calls.outbox_events table.
type OutboxEvent struct {
	ID        int64           `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt time.Time       `json:"created_at"`
}

// IdempotencyResult is a stored idempotency key response.
type IdempotencyResult struct {
	RequestHash string          `json:"request_hash"`
	Response    json.RawMessage `json:"response"`
}

func (s *CallStore) InsertOutboxEvent(ctx context.Context, eventType string, payload interface{}) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		INSERT INTO calls.outbox_events (event_type, payload) VALUES ($1, $2)`,
		eventType, payloadBytes,
	)
	return err
}

func (s *CallStore) FetchUnpublished(ctx context.Context, limit int) ([]OutboxEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, event_type, payload, created_at
		FROM calls.outbox_events
		WHERE published_at IS NULL
		ORDER BY id ASC
		LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		if err := rows.Scan(&e.ID, &e.EventType, &e.Payload, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

func (s *CallStore) MarkPublished(ctx context.Context, id int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE calls.outbox_events SET published_at = NOW() WHERE id = $1`, id)
	return err
}

// --- Idempotency ---

func HashRequestPayload(payload interface{}) (string, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:]), nil
}

func (s *CallStore) CreateIdempotencyKey(ctx context.Context, key, requestHash string) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO calls.idempotency_keys (key, request_hash)
		VALUES ($1, $2)
		ON CONFLICT (key) DO NOTHING`, key, requestHash)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *CallStore) CheckIdempotencyKey(ctx context.Context, key string) (*IdempotencyResult, error) {
	row := s.db.QueryRow(ctx, `
		SELECT request_hash, response FROM calls.idempotency_keys WHERE key = $1`, key)

	var result IdempotencyResult
	err := row.Scan(&result.RequestHash, &result.Response)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *CallStore) SaveIdempotencyResponse(ctx context.Context, key, requestHash string, response interface{}) error {
	respBytes, err := json.Marshal(response)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(ctx, `
		UPDATE calls.idempotency_keys SET response = $2
		WHERE key = $1 AND request_hash = $3`, key, respBytes, requestHash)
	return err
}

func (s *CallStore) ReleaseIdempotencyKey(ctx context.Context, key, requestHash string) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM calls.idempotency_keys WHERE key = $1 AND request_hash = $2`, key, requestHash)
	return err
}
