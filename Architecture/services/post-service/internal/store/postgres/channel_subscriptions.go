package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Tube channel subscriptions (2026-09-12). Founder decisions: Subscribe is
// follow + notify behind one button, unsubscribing removes both, every
// subscriber is notified by default and the per-channel bell opts out.
//
// The rows live in the shared `channel_subscriptions` table (migration 046
// adopts user-service's 004). post-service owns them because it owns the
// channels: the fan-out reads (subscriber ids for a new upload, the owners a
// viewer subscribes to) and every write happen here, each write with its
// outbox event in the same transaction so a subscription and its
// tube.channel.subscribed / .unsubscribed event can never disagree.

// NotifyOn values. 'highlights' is gone (migration 046 folded it into 'none').
const (
	NotifyOnAll  = "all"
	NotifyOnNone = "none"
)

// ChannelSubscription is one stored row.
type ChannelSubscription struct {
	ChannelID    uuid.UUID
	UserID       uuid.UUID
	NotifyOn     string
	SubscribedAt time.Time
}

// SubscriptionRow is a subscription hydrated with its channel for the
// viewer's subscriptions page.
type SubscriptionRow struct {
	Channel      Channel
	NotifyOn     string
	SubscribedAt time.Time
}

// SubscriptionCursor is the keyset position for ListSubscriptionsForUser:
// rows strictly older than (SubscribedAt, ChannelID) in the page order.
type SubscriptionCursor struct {
	SubscribedAt time.Time
	ChannelID    uuid.UUID
}

// ErrNotSubscribed: the bell was set on a channel the user does not subscribe to.
var ErrNotSubscribed = errors.New("not subscribed to this channel")

func subscriptionPayload(channelID, ownerID, subscriberID uuid.UUID, notifyOn string, at time.Time) events.ChannelSubscriptionPayload {
	return events.ChannelSubscriptionPayload{
		ChannelID:    channelID.String(),
		OwnerID:      ownerID.String(),
		SubscriberID: subscriberID.String(),
		NotifyOn:     notifyOn,
		OccurredAt:   at,
	}
}

// channelOwnerTx reads the owner of a channel inside the caller's
// transaction, or ErrChannelNotFound.
func channelOwnerTx(ctx context.Context, tx pgx.Tx, channelID uuid.UUID) (uuid.UUID, error) {
	var owner uuid.UUID
	err := tx.QueryRow(ctx, `SELECT user_id FROM channels WHERE id = $1`, channelID).Scan(&owner)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrChannelNotFound
	}
	return owner, err
}

// Subscribe inserts the subscription and enqueues tube.channel.subscribed in
// one transaction. Returns inserted=false, and writes nothing, when the row
// already exists: a retried tap must not reset a bell the user has since
// turned off, and must not emit a second event.
func (s *Store) Subscribe(ctx context.Context, channelID, userID uuid.UUID, notifyOn string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ownerID, err := channelOwnerTx(ctx, tx, channelID)
	if err != nil {
		return false, err
	}
	var subscribedAt time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO channel_subscriptions (channel_id, user_id, notify_on)
		VALUES ($1, $2, $3)
		ON CONFLICT (channel_id, user_id) DO NOTHING
		RETURNING subscribed_at`, channelID, userID, notifyOn).Scan(&subscribedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	payload := subscriptionPayload(channelID, ownerID, userID, notifyOn, subscribedAt)
	if err := InsertOutboxEventTx(ctx, tx, events.TubeChannelSubscribed, "channel_subscription", channelID, payload); err != nil {
		return false, fmt.Errorf("enqueue channel.subscribed: %w", err)
	}
	return true, tx.Commit(ctx)
}

// Unsubscribe deletes the subscription and enqueues tube.channel.unsubscribed
// in one transaction. deleted=false when there was no row (nothing emitted).
func (s *Store) Unsubscribe(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	ownerID, err := channelOwnerTx(ctx, tx, channelID)
	if err != nil {
		return false, err
	}
	deleted, err := deleteSubscriptionTx(ctx, tx, channelID, ownerID, userID)
	if err != nil {
		return false, err
	}
	if !deleted {
		return false, nil
	}
	return true, tx.Commit(ctx)
}

// deleteSubscriptionTx removes one row and, when it existed, writes the
// unsubscribed outbox event in the same transaction.
func deleteSubscriptionTx(ctx context.Context, tx pgx.Tx, channelID, ownerID, userID uuid.UUID) (bool, error) {
	tag, err := tx.Exec(ctx, `DELETE FROM channel_subscriptions WHERE channel_id = $1 AND user_id = $2`, channelID, userID)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() == 0 {
		return false, nil
	}
	payload := subscriptionPayload(channelID, ownerID, userID, "", time.Now().UTC())
	if err := InsertOutboxEventTx(ctx, tx, events.TubeChannelUnsubscribed, "channel_subscription", channelID, payload); err != nil {
		return false, fmt.Errorf("enqueue channel.unsubscribed: %w", err)
	}
	return true, nil
}

// SetNotifyOn flips the bell. ErrNotSubscribed when there is no row: the
// bell is a property of a subscription, never a way to create one.
func (s *Store) SetNotifyOn(ctx context.Context, channelID, userID uuid.UUID, notifyOn string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE channel_subscriptions SET notify_on = $3
		WHERE channel_id = $1 AND user_id = $2`, channelID, userID, notifyOn)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotSubscribed
	}
	return nil
}

// GetSubscription returns the viewer's subscription to a channel, or nil.
func (s *Store) GetSubscription(ctx context.Context, channelID, userID uuid.UUID) (*ChannelSubscription, error) {
	var sub ChannelSubscription
	err := s.db.QueryRow(ctx, `
		SELECT channel_id, user_id, notify_on, subscribed_at
		FROM channel_subscriptions WHERE channel_id = $1 AND user_id = $2`, channelID, userID).
		Scan(&sub.ChannelID, &sub.UserID, &sub.NotifyOn, &sub.SubscribedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// ListSubscriberIDsAfter pages the subscribers to notify about a new upload,
// in user_id order after the cursor. ONLY notify_on = 'all' rows: the bell
// off means no push, and this is the one query the fan-out reads. (The
// user-service version compared against 'uploads', a value the CHECK never
// allowed, so it selected nobody.)
func (s *Store) ListSubscriberIDsAfter(ctx context.Context, channelID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		return []uuid.UUID{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT user_id FROM channel_subscriptions
		WHERE channel_id = $1 AND notify_on = 'all' AND user_id > $2
		ORDER BY user_id ASC
		LIMIT $3`, channelID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUUIDs(rows, limit)
}

// ListSubscribedOwnersAfter pages the owner user ids of every channel the
// viewer subscribes to, in owner id order after the cursor. feed-service
// builds the Tube Subscriptions feed from this. Bell state is irrelevant
// here: a subscription is a feed membership whether or not it pushes.
func (s *Store) ListSubscribedOwnersAfter(ctx context.Context, userID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	if limit <= 0 {
		return []uuid.UUID{}, nil
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.user_id
		FROM channel_subscriptions cs
		JOIN channels c ON c.id = cs.channel_id
		WHERE cs.user_id = $1 AND c.user_id > $2
		ORDER BY c.user_id ASC
		LIMIT $3`, userID, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanUUIDs(rows, limit)
}

func scanUUIDs(rows pgx.Rows, capacity int) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, capacity)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ListSubscriptionsForUser pages the viewer's subscriptions newest first,
// keyed on (subscribed_at DESC, channel_id) so two subscriptions in the same
// instant still page without a gap. A nil cursor is the first page. The
// channel is joined so the page renders without a second lookup.
func (s *Store) ListSubscriptionsForUser(ctx context.Context, userID uuid.UUID, cursor *SubscriptionCursor, limit int) ([]SubscriptionRow, error) {
	if limit <= 0 {
		return []SubscriptionRow{}, nil
	}
	var (
		cursorAt *time.Time
		cursorID *uuid.UUID
	)
	if cursor != nil {
		at, id := cursor.SubscribedAt, cursor.ChannelID
		cursorAt, cursorID = &at, &id
	}
	rows, err := s.db.Query(ctx, `
		SELECT c.id, c.user_id, c.name, c.handle, c.description, c.avatar_media_id, c.subscriber_count, c.created_at, c.updated_at,
		       cs.notify_on, cs.subscribed_at
		FROM channel_subscriptions cs
		JOIN channels c ON c.id = cs.channel_id
		WHERE cs.user_id = $1
		  AND ($2::timestamptz IS NULL
		       OR cs.subscribed_at < $2
		       OR (cs.subscribed_at = $2 AND cs.channel_id > $3))
		ORDER BY cs.subscribed_at DESC, cs.channel_id ASC
		LIMIT $4`, userID, cursorAt, cursorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SubscriptionRow, 0, limit)
	for rows.Next() {
		var r SubscriptionRow
		if err := rows.Scan(&r.Channel.ID, &r.Channel.UserID, &r.Channel.Name, &r.Channel.Handle, &r.Channel.About,
			&r.Channel.AvatarMediaID, &r.Channel.SubscriberCount, &r.Channel.CreatedAt, &r.Channel.UpdatedAt,
			&r.NotifyOn, &r.SubscribedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// DeleteSubscriptionByOwner removes the subscriber's subscription to the
// owner's channel and enqueues tube.channel.unsubscribed when a row went.
// This is the UserUnfollowed consumer's write: a subscription is a follow
// plus a bell, so an unfollow that arrives through graph-service (the
// profile's unfollow button, a block) must take the subscription with it.
// No channel or no row is a clean false.
func (s *Store) DeleteSubscriptionByOwner(ctx context.Context, ownerUserID, subscriberID uuid.UUID) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	var channelID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM channels WHERE user_id = $1`, ownerUserID).Scan(&channelID)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	deleted, err := deleteSubscriptionTx(ctx, tx, channelID, ownerUserID, subscriberID)
	if err != nil {
		return false, err
	}
	if !deleted {
		return false, nil
	}
	return true, tx.Commit(ctx)
}
