package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Module 1 P0-3 — internal subscriber fan-out contract.
//
// These queries back an INTERNAL-ONLY endpoint used by
// notification-service to fan out upload notifications. Subscriber
// identities are never exposed through a public contract (Codex P0-3);
// the gateway blocks /internal/ for non-admin callers and the handler
// additionally requires X-Internal-Service-Key.
//
// Pagination is keyset-based on user_id (not OFFSET) so a channel with
// millions of subscribers pages in stable, index-ordered batches with no
// silent truncation — the 5,000-row cap Codex flagged is gone.

// GetChannelByOwner returns the owning user's canonical channel id.
// (nil, nil) when the user has no channel.
func (s *Store) GetChannelByOwner(ctx context.Context, ownerID uuid.UUID) (*uuid.UUID, error) {
	var channelID uuid.UUID
	err := s.db.QueryRow(ctx, `
		SELECT id FROM channels
		WHERE user_id = $1
		ORDER BY created_at ASC
		LIMIT 1`, ownerID).Scan(&channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &channelID, nil
}

// ListSubscriberIDsAfter returns up to limit subscriber user IDs for a
// channel with user_id strictly greater than after, ascending. Passing
// uuid.Nil starts at the beginning. Only subscribers whose notify_on
// preference opts into upload notifications are returned ("all" or
// "uploads"); "none" is filtered out in SQL so muted subscribers never
// enter the fan-out at all.
func (s *Store) ListSubscriberIDsAfter(ctx context.Context, channelID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id
		FROM channel_subscriptions
		WHERE channel_id = $1
		  AND user_id > $2
		  AND notify_on IN ('all', 'uploads')
		ORDER BY user_id ASC
		LIMIT $3`, channelID, after, limit)
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

// ListSubscribedChannelOwners returns the user IDs that own the channels
// a viewer subscribes to. feed-service uses this to build the PostTube
// Subscriptions feed from real channel subscriptions rather than the
// social follow graph (Module 1 P0-3).
// Codex P2-2: the previous version silently capped at 5,000 with no
// pagination, so a viewer with more subscriptions than that lost the
// remainder from their Subscriptions feed without any signal. Now the
// contract is keyset-paginated on owner id and the caller loops.
func (s *Store) ListSubscribedChannelOwners(ctx context.Context, viewerID uuid.UUID, limit int) ([]uuid.UUID, error) {
	return s.ListSubscribedChannelOwnersAfter(ctx, viewerID, uuid.Nil, limit)
}

// ListSubscribedChannelOwnersAfter returns one keyset page of creator IDs
// the viewer subscribes to, ordered by owner id ascending.
func (s *Store) ListSubscribedChannelOwnersAfter(ctx context.Context, viewerID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT c.user_id
		FROM channel_subscriptions cs
		JOIN channels c ON c.id = cs.channel_id
		WHERE cs.user_id = $1 AND c.user_id > $2
		ORDER BY c.user_id ASC
		LIMIT $3`, viewerID, after, limit)
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

// CountChannelSubscribers returns the total subscriber count (all
// notify_on values) — used for observability on fan-out jobs.
func (s *Store) CountChannelSubscribers(ctx context.Context, channelID uuid.UUID) (int64, error) {
	var n int64
	err := s.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM channel_subscriptions WHERE channel_id = $1`, channelID).Scan(&n)
	return n, err
}
