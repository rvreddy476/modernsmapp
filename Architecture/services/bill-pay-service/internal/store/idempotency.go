package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrIdempotencyMismatch is returned when an idempotency key is replayed
// but against a different user. Replays must match on user.
var ErrIdempotencyMismatch = errors.New("idempotency: key reused by different user")

// ErrIdempotencyKeyNotFound is returned by FindIdempotency when nothing
// matches.
var ErrIdempotencyKeyNotFound = errors.New("idempotency: key not found")

// FindIdempotency looks up a previous record for a given key + user. Returns
// ErrIdempotencyKeyNotFound if nothing recorded yet, ErrIdempotencyMismatch
// if the key was used by a different user.
func (s *Store) FindIdempotency(ctx context.Context, key string, userID uuid.UUID) (*IdempotencyRecord, error) {
	const q = `
        SELECT key, user_id, payment_id, response_body, created_at, expires_at
        FROM billpay.idempotency
        WHERE key = $1 AND expires_at > now()`
	row := s.db.QueryRow(ctx, q, key)
	var rec IdempotencyRecord
	if err := row.Scan(&rec.Key, &rec.UserID, &rec.PaymentID, &rec.ResponseBody, &rec.CreatedAt, &rec.ExpiresAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrIdempotencyKeyNotFound
		}
		return nil, fmt.Errorf("find idempotency: %w", err)
	}
	if rec.UserID != userID {
		return nil, ErrIdempotencyMismatch
	}
	return &rec, nil
}

// RecordIdempotency stores the (key, user) → payment_id + response_body
// mapping. The unique PK on `key` makes a duplicate INSERT a silent no-op,
// keeping the call idempotent across retries.
func (s *Store) RecordIdempotency(ctx context.Context, key string, userID uuid.UUID, paymentID *uuid.UUID, responseBody []byte) error {
	const q = `
        INSERT INTO billpay.idempotency (key, user_id, payment_id, response_body)
        VALUES ($1, $2, $3, $4)
        ON CONFLICT (key) DO NOTHING`
	if _, err := s.db.Exec(ctx, q, key, userID, paymentID, responseBody); err != nil {
		return fmt.Errorf("record idempotency: %w", err)
	}
	return nil
}

// PurgeExpiredIdempotency deletes rows older than now(). Run from a
// periodic job. Returns the number of rows removed.
func (s *Store) PurgeExpiredIdempotency(ctx context.Context) (int64, error) {
	const q = `DELETE FROM billpay.idempotency WHERE expires_at < now()`
	tag, err := s.db.Exec(ctx, q)
	if err != nil {
		return 0, fmt.Errorf("purge expired idempotency: %w", err)
	}
	return tag.RowsAffected(), nil
}
