package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrIdempotencyMismatch is returned when an idempotency key is replayed but
// for a different (user, operation) pair. Mirrors wallet-service.
var ErrIdempotencyMismatch = errors.New("idempotency: key reused with different operation")

// ErrIdempotencyKeyNotFound is returned when no record matches.
var ErrIdempotencyKeyNotFound = errors.New("idempotency: key not found")

// FindIdempotency looks up a previous record for the (key, user, operation)
// triple. Returns ErrIdempotencyKeyNotFound when nothing recorded yet, or
// ErrIdempotencyMismatch when the key is used by this user for a different
// operation (or by a different user entirely).
func (s *Store) FindIdempotency(ctx context.Context, key string, userID uuid.UUID, operation string) (*IdempotencyRecord, error) {
	const q = `
        SELECT key, user_id, operation, resource_id, response_body, created_at, expires_at
        FROM rider_idempotency
        WHERE key = $1 AND expires_at > now()`
	row := s.db.QueryRow(ctx, q, key)
	var rec IdempotencyRecord
	if err := row.Scan(&rec.Key, &rec.UserID, &rec.Operation, &rec.ResourceID, &rec.ResponseBody, &rec.CreatedAt, &rec.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIdempotencyKeyNotFound
		}
		return nil, fmt.Errorf("find idempotency: %w", err)
	}
	if rec.UserID != userID || rec.Operation != operation {
		return nil, ErrIdempotencyMismatch
	}
	return &rec, nil
}

// RecordIdempotency stores the (key, user, operation) -> resource_id +
// response_body mapping. Duplicate keys are no-ops (ON CONFLICT DO NOTHING)
// so retries don't blow up.
func (s *Store) RecordIdempotency(ctx context.Context, key string, userID uuid.UUID, operation string, resourceID *uuid.UUID, responseBody []byte) error {
	const q = `
        INSERT INTO rider_idempotency (key, user_id, operation, resource_id, response_body)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (key) DO NOTHING`
	if _, err := s.db.Exec(ctx, q, key, userID, operation, resourceID, responseBody); err != nil {
		return fmt.Errorf("record idempotency: %w", err)
	}
	return nil
}

// PurgeExpiredIdempotency deletes rows older than now(). Run from a periodic
// job. Returns the number of rows removed.
func (s *Store) PurgeExpiredIdempotency(ctx context.Context) (int64, error) {
	const q = `DELETE FROM rider_idempotency WHERE expires_at < now()`
	tag, err := s.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("purge expired idempotency: %w", err)
	}
	return tag.RowsAffected(), nil
}
