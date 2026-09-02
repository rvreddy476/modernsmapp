package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Private accounts — follow requests (migration 010).
//
// A follow of a PRIVATE account does not create the edge; it creates a
// pending follow_requests row the target must approve. Every transition here
// follows the pair-atomic rules from pair_atomic.go: relationship-CREATING
// paths (the request itself, and the accept that materialises the follow)
// run under the pair advisory lock with a post-lock symmetric block check,
// and every event is written to the outbox in the same transaction as the
// row change.

// FollowRequest is one row of follow_requests, shaped for the incoming-inbox
// listing.
type FollowRequest struct {
	RequesterID uuid.UUID  `json:"requester_id"`
	TargetID    uuid.UUID  `json:"target_id"`
	Status      string     `json:"status"`
	CreatedAt   time.Time  `json:"created_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

// UpsertFollowRequestPending creates (or revives) a pending follow request
// under the pair lock. Re-requesting after a decline/cancel is allowed; an
// already-pending or already-accepted row is left untouched. Returns whether
// a row actually transitioned to pending — only then is the outbox event
// written, so a double-tap does not announce two requests.
func (s *Store) UpsertFollowRequestPending(ctx context.Context, requesterID, targetID uuid.UUID) (bool, error) {
	var created bool
	err := s.withPairLock(ctx, requesterID, targetID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, requesterID, targetID); err != nil {
			return err
		}
		now := time.Now()
		ct, err := tx.Exec(ctx, `
			INSERT INTO follow_requests (requester_id, target_id, status, created_at, resolved_at)
			VALUES ($1, $2, 'pending', $3, NULL)
			ON CONFLICT (requester_id, target_id) DO UPDATE
			SET status = 'pending', created_at = EXCLUDED.created_at, resolved_at = NULL
			WHERE follow_requests.status IN ('declined', 'cancelled')`,
			requesterID, targetID, now)
		if err != nil {
			return fmt.Errorf("follow request: upsert: %w", err)
		}
		created = ct.RowsAffected() > 0
		if created {
			if _, err := appendGraphOutboxTx(ctx, tx, events.GraphFollowRequested, requesterID, targetID,
				events.FollowRequestedPayload{
					RequesterID: requesterID.String(),
					TargetID:    targetID.String(),
					CreatedAt:   now.UTC(),
				}); err != nil {
				return fmt.Errorf("follow request: outbox: %w", err)
			}
		}
		return nil
	})
	return created, err
}

// AcceptFollowRequestAtomic accepts a pending request in ONE transaction:
// the status flip, the follow edge, the canonical UserFollowed outbox event
// and the GraphFollowRequestAccepted outbox event all commit together.
//
// Returns whether a NEW follow edge landed, so the caller can skip its
// counter bump on a duplicate (the requester somehow already following).
// Accepting is a relationship CREATION long after the request was sent, so
// the block re-check runs after the lock is held — a block that landed in
// between refuses the accept.
func (s *Store) AcceptFollowRequestAtomic(ctx context.Context, requesterID, targetID uuid.UUID) (bool, error) {
	var inserted bool
	err := s.withPairLock(ctx, requesterID, targetID, func(tx pgx.Tx) error {
		if err := guardPairTx(ctx, tx, requesterID, targetID); err != nil {
			return err
		}
		ct, err := tx.Exec(ctx, `
			UPDATE follow_requests SET status = 'accepted', resolved_at = NOW()
			WHERE requester_id = $1 AND target_id = $2 AND status = 'pending'`,
			requesterID, targetID)
		if err != nil {
			return fmt.Errorf("accept follow request: update: %w", err)
		}
		if ct.RowsAffected() == 0 {
			return ErrNoPendingFollowRequest
		}

		now := time.Now().UTC()
		ct, err = tx.Exec(ctx, `
			INSERT INTO follows (follower_id, followee_id, created_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (follower_id, followee_id) DO NOTHING`, requesterID, targetID)
		if err != nil {
			return fmt.Errorf("accept follow request: insert follow: %w", err)
		}
		inserted = ct.RowsAffected() > 0
		if inserted {
			if _, err := appendGraphOutboxTx(ctx, tx, events.UserFollowed, requesterID, targetID,
				events.UserFollowedPayload{
					FollowerID: requesterID.String(),
					FolloweeID: targetID.String(),
					CreatedAt:  now,
				}); err != nil {
				return fmt.Errorf("accept follow request: follow outbox: %w", err)
			}
		}
		if _, err := appendGraphOutboxTx(ctx, tx, events.GraphFollowRequestAccepted, targetID, requesterID,
			events.FollowRequestAcceptedPayload{
				RequesterID: requesterID.String(),
				TargetID:    targetID.String(),
				AcceptedAt:  now,
			}); err != nil {
			return fmt.Errorf("accept follow request: accepted outbox: %w", err)
		}
		return nil
	})
	return inserted, err
}

// ErrNoPendingFollowRequest is returned by accept/decline/cancel when no
// pending row exists for the pair. Handlers map it to 404.
var ErrNoPendingFollowRequest = errors.New("no pending follow request found")

// DeclineFollowRequest flips a pending request to declined (target action).
// No lock needed: declining creates nothing.
func (s *Store) DeclineFollowRequest(ctx context.Context, requesterID, targetID uuid.UUID) error {
	ct, err := s.db.Exec(ctx, `
		UPDATE follow_requests SET status = 'declined', resolved_at = NOW()
		WHERE requester_id = $1 AND target_id = $2 AND status = 'pending'`,
		requesterID, targetID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNoPendingFollowRequest
	}
	return nil
}

// CancelFollowRequest lets the requester withdraw their own pending request.
func (s *Store) CancelFollowRequest(ctx context.Context, requesterID, targetID uuid.UUID) error {
	ct, err := s.db.Exec(ctx, `
		UPDATE follow_requests SET status = 'cancelled', resolved_at = NOW()
		WHERE requester_id = $1 AND target_id = $2 AND status = 'pending'`,
		requesterID, targetID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNoPendingFollowRequest
	}
	return nil
}

// ListIncomingFollowRequests returns one page of the target's pending
// requests, newest first, keyset-paginated on (created_at DESC,
// requester_id DESC) — same cursor shape as the follower lists
// ("<unix_micros>:<uuid>"). Backed by idx_follow_requests_incoming.
func (s *Store) ListIncomingFollowRequests(ctx context.Context, targetID uuid.UUID, limit int, cursor string) ([]FollowRequest, string, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	args := []any{targetID}
	cursorClause := ""
	if cursor != "" {
		ts, id, ok := parseFollowCursor(cursor)
		if ok {
			cursorClause = " AND (created_at, requester_id) < ($2, $3)"
			args = append(args, ts, id)
		}
	}
	args = append(args, limit+1)
	q := `
		SELECT requester_id, target_id, status, created_at, resolved_at
		FROM follow_requests
		WHERE target_id = $1 AND status = 'pending'` + cursorClause + `
		ORDER BY created_at DESC, requester_id DESC
		LIMIT $` + strconv.Itoa(len(args))
	rows, err := s.db.Query(ctx, q, args...)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var reqs []FollowRequest
	for rows.Next() {
		var r FollowRequest
		if err := rows.Scan(&r.RequesterID, &r.TargetID, &r.Status, &r.CreatedAt, &r.ResolvedAt); err != nil {
			return nil, "", err
		}
		reqs = append(reqs, r)
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	var next string
	if len(reqs) > limit {
		last := reqs[limit-1]
		next = fmt.Sprintf("%d:%s", last.CreatedAt.UnixMicro(), last.RequesterID.String())
		reqs = reqs[:limit]
	}
	return reqs, next, nil
}

// ListPendingFollowRequesterIDs returns up to `limit` requester IDs with a
// pending request toward targetID, oldest first — the work queue for the
// private→public auto-accept sweep. Oldest-first so the sweep makes forward
// progress across chunks even while new requests keep arriving.
func (s *Store) ListPendingFollowRequesterIDs(ctx context.Context, targetID uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	rows, err := s.db.Query(ctx, `
		SELECT requester_id FROM follow_requests
		WHERE target_id = $1 AND status = 'pending'
		ORDER BY created_at ASC
		LIMIT $2`, targetID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GetFollowRequestStatus reports the pending-request state between actor and
// target: "none", "pending_sent" (actor → target) or "pending_received"
// (target → actor). Used by callers that cannot take the consolidated
// GetRelationshipFull round trip.
func (s *Store) GetFollowRequestStatus(ctx context.Context, actorID, targetID uuid.UUID) (string, error) {
	var sent, received bool
	err := s.db.QueryRow(ctx, `
		SELECT
			EXISTS(SELECT 1 FROM follow_requests WHERE requester_id = $1 AND target_id = $2 AND status = 'pending'),
			EXISTS(SELECT 1 FROM follow_requests WHERE requester_id = $2 AND target_id = $1 AND status = 'pending')
	`, actorID, targetID).Scan(&sent, &received)
	if err != nil {
		return "", err
	}
	switch {
	case sent:
		return "pending_sent", nil
	case received:
		return "pending_received", nil
	default:
		return "none", nil
	}
}
