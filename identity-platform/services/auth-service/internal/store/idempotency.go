package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// IdempotencyTTL is how long a completed response stays replayable.
//
// Long enough to cover a mobile client retrying across a network change or an
// app restart; short enough that the table stays a retry window rather than a
// second copy of the domain.
const IdempotencyTTL = 24 * time.Hour

// ErrIdempotencyKeyReused means the key matched but the body did not.
//
// Reported rather than replayed: returning the first response for a different
// request would tell the caller something succeeded that was never processed.
var ErrIdempotencyKeyReused = errors.New("idempotency key reused with a different request body")

// IdempotentResponse is a previously completed result.
type IdempotentResponse struct {
	StatusCode int
	Body       []byte
}

// LookupIdempotentResponse returns the stored response for (endpoint, key).
//
// Returns (nil, ErrIdempotencyKeyReused) when the key exists under a different
// request hash, and (nil, nil) when there is nothing stored or it has expired.
func (s *Store) LookupIdempotentResponse(
	ctx context.Context,
	endpoint, key, requestHash string,
) (*IdempotentResponse, error) {
	var (
		storedHash string
		status     int
		body       []byte
	)
	err := s.db.QueryRow(ctx, `
		SELECT request_hash, status_code, response_body
		FROM auth.idempotency_keys
		WHERE endpoint = $1 AND idempotency_key = $2 AND expires_at > now()
	`, endpoint, key).Scan(&storedHash, &status, &body)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if storedHash != requestHash {
		return nil, ErrIdempotencyKeyReused
	}
	return &IdempotentResponse{StatusCode: status, Body: body}, nil
}

// SaveIdempotentResponse records a completed response.
//
// ON CONFLICT DO NOTHING because two concurrent retries can both finish: the
// first writer wins and the second is a no-op, which is exactly the desired
// outcome — one stored result, not a lost-update race.
func (s *Store) SaveIdempotentResponse(
	ctx context.Context,
	endpoint, key, requestHash string,
	statusCode int,
	body []byte,
) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.idempotency_keys
		    (endpoint, idempotency_key, request_hash, status_code, response_body, expires_at)
		VALUES ($1, $2, $3, $4, $5, now() + $6::interval)
		ON CONFLICT (endpoint, idempotency_key) DO NOTHING
	`, endpoint, key, requestHash, statusCode, body, IdempotencyTTL.String())
	return err
}

// PurgeExpiredIdempotencyKeys removes lapsed rows. Bounded so a sweep cannot
// lock the table for an unbounded period.
func (s *Store) PurgeExpiredIdempotencyKeys(ctx context.Context, limit int) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM auth.idempotency_keys
		WHERE ctid IN (
		    SELECT ctid FROM auth.idempotency_keys WHERE expires_at <= now() LIMIT $1
		)
	`, limit)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
