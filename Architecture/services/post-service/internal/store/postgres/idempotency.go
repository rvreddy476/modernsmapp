package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5"
)

// IdempotencyRecord stores the result of a previous request for replay.
type IdempotencyRecord struct {
	Key          string          `json:"key"`
	ResultStatus int             `json:"result_status"`
	ResultBody   json.RawMessage `json:"result_body,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	ExpiresAt    time.Time       `json:"expires_at"`
}

// GetIdempotencyRecord returns a previously stored idempotency result, or nil if not found.
func (s *Store) GetIdempotencyRecord(ctx context.Context, key string) (*IdempotencyRecord, error) {
	var r IdempotencyRecord
	err := s.db.QueryRow(ctx, `
		SELECT key, result_status, result_body, created_at, expires_at
		FROM idempotency_keys
		WHERE key = $1 AND expires_at > NOW()
	`, key).Scan(&r.Key, &r.ResultStatus, &r.ResultBody, &r.CreatedAt, &r.ExpiresAt)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &r, nil
}

// StoreIdempotencyRecord saves the result of a request for future replay.
func (s *Store) StoreIdempotencyRecord(ctx context.Context, key string, resultStatus int, resultBody interface{}) error {
	bodyBytes, err := json.Marshal(resultBody)
	if err != nil {
		return err
	}

	_, err = s.db.Exec(ctx, `
		INSERT INTO idempotency_keys (key, result_status, result_body, created_at, expires_at)
		VALUES ($1, $2, $3, NOW(), NOW() + INTERVAL '24 hours')
		ON CONFLICT (key) DO NOTHING
	`, key, resultStatus, bodyBytes)
	return err
}

// CleanupExpiredIdempotencyKeys removes expired idempotency records.
func (s *Store) CleanupExpiredIdempotencyKeys(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM idempotency_keys WHERE expires_at < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
