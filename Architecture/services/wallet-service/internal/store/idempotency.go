package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrIdempotencyMismatch is returned when an idempotency key is replayed but
// against a different operation (e.g. same key first used for top-up now
// re-presented for send). Replays must match exactly.
var ErrIdempotencyMismatch = errors.New("idempotency: key reused with different operation")

// ErrIdempotencyKeyNotFound is returned by FindIdempotency when nothing
// matches.
var ErrIdempotencyKeyNotFound = errors.New("idempotency: key not found")

// FindIdempotency looks up a previous record for the (key, user, operation)
// triple. Returns ErrIdempotencyKeyNotFound if nothing recorded yet.
// Returns ErrIdempotencyMismatch if the key was used by this user for a
// different operation.
func (s *Store) FindIdempotency(ctx context.Context, key string, userID uuid.UUID, operation string) (*IdempotencyRecord, error) {
	const q = `
        SELECT key, user_id, operation, transaction_id, response_body, created_at, expires_at
        FROM wallet.idempotency
        WHERE key = $1 AND expires_at > now()`
	row := s.db.QueryRow(ctx, q, key)
	var rec IdempotencyRecord
	if err := row.Scan(&rec.Key, &rec.UserID, &rec.Operation, &rec.TransactionID, &rec.ResponseBody, &rec.CreatedAt, &rec.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIdempotencyKeyNotFound
		}
		return nil, fmt.Errorf("find idempotency: %w", err)
	}
	if rec.UserID != userID {
		return nil, ErrIdempotencyMismatch
	}
	if rec.Operation != operation {
		return nil, ErrIdempotencyMismatch
	}
	return &rec, nil
}

// RecordIdempotency stores the (key, user, operation) → transaction_id +
// response_body mapping. The unique PK on `key` makes a duplicate INSERT
// fail with a constraint error which the caller can treat as "already
// recorded — read it back". Idempotent across retries.
func (s *Store) RecordIdempotency(ctx context.Context, key string, userID uuid.UUID, operation string, txID *uuid.UUID, responseBody []byte) error {
	const q = `
        INSERT INTO wallet.idempotency (key, user_id, operation, transaction_id, response_body)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (key) DO NOTHING`
	if _, err := s.db.Exec(ctx, q, key, userID, operation, txID, responseBody); err != nil {
		return fmt.Errorf("record idempotency: %w", err)
	}
	return nil
}

// PurgeExpiredIdempotency deletes rows older than now(). Run from a periodic
// job (or piggybacked on cmd/expirer). Returns the number of rows removed.
func (s *Store) PurgeExpiredIdempotency(ctx context.Context) (int64, error) {
	const q = `DELETE FROM wallet.idempotency WHERE expires_at < now()`
	tag, err := s.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("purge expired idempotency: %w", err)
	}
	return tag.RowsAffected(), nil
}
