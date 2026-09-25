package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
Replay records for cross-posting.

One row per (request, target group). A retry of a batch that half-worked must
finish the job rather than duplicate the half that succeeded, so each target
that has already been decided replays its recorded answer and is not
attempted again.

The stored outcome matters as much as the post id. Re-running the rules on a
retry would let a refusal quietly turn into a success — someone unbans the
author between the two attempts and "2 of 5" becomes "3 of 5" with the user
having pressed nothing. A replay reproduces the original answer.
*/

// PostRequestRecord is one target's decided outcome within a cross-post batch.
type PostRequestRecord struct {
	GroupID uuid.UUID
	PostID  *uuid.UUID
	Outcome string
}

// FindPostRequests returns every target already decided for this request,
// keyed by group. An empty map means nothing has been attempted.
func (s *Store) FindPostRequests(ctx context.Context, authorID uuid.UUID, key string) (map[uuid.UUID]PostRequestRecord, error) {
	if key == "" {
		return map[uuid.UUID]PostRequestRecord{}, nil
	}
	rows, err := s.db.Query(ctx,
		`SELECT group_id, post_id, outcome FROM group_post_requests
		 WHERE author_id = $1 AND idempotency_key = $2`, authorID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[uuid.UUID]PostRequestRecord{}
	for rows.Next() {
		var r PostRequestRecord
		if err := rows.Scan(&r.GroupID, &r.PostID, &r.Outcome); err != nil {
			return nil, err
		}
		out[r.GroupID] = r
	}
	return out, rows.Err()
}

// RecordPostRequest remembers one target's outcome.
//
// ON CONFLICT DO NOTHING rather than an upsert: the first answer for a target
// is the answer. Two concurrent retries of the same request must not race to
// overwrite each other with different outcomes.
func (s *Store) RecordPostRequest(ctx context.Context, authorID uuid.UUID, key string, r PostRequestRecord) error {
	if key == "" {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO group_post_requests (author_id, idempotency_key, group_id, post_id, outcome)
		 VALUES ($1,$2,$3,$4,$5) ON CONFLICT DO NOTHING`,
		authorID, key, r.GroupID, r.PostID, r.Outcome)
	return err
}

/*
LockPostRequest serialises concurrent attempts at the same batch.

Two tabs pressing Post at the same moment, or a client retrying while the
first attempt is still in flight, would otherwise both find no replay record
and both post. The lock is held for the transaction, mirroring what
CreateGroup already does for group creation.
*/
func (s *Store) LockPostRequest(ctx context.Context, tx pgx.Tx, authorID uuid.UUID, key string) error {
	if key == "" {
		return nil
	}
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, authorID.String()+":"+key)
	return err
}

// BeginTx exposes a transaction to the service for the batch lock. The
// service owns the lifecycle; this exists so the lock and the replay read
// happen under one transaction rather than two connections.
func (s *Store) BeginTx(ctx context.Context) (pgx.Tx, error) {
	return s.db.Begin(ctx)
}

// GetGroupPostByID is GetGroupPostV2 without the not-found error, for the
// replay path: a recorded post that has since been deleted replays as its
// recorded outcome with no row to return.
func (s *Store) GetGroupPostByID(ctx context.Context, id uuid.UUID) (*GroupPostV2, error) {
	p, err := s.GetGroupPostV2(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return p, err
}
