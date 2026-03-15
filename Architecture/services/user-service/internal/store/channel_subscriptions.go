package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ChannelSubscription represents a user's subscription to a channel.
type ChannelSubscription struct {
	ChannelID    uuid.UUID `json:"channel_id"`
	UserID       uuid.UUID `json:"user_id"`
	NotifyOn     string    `json:"notify_on"`
	SubscribedAt time.Time `json:"subscribed_at"`
}

// SubscribeToChannel inserts a channel_subscriptions row.
// If the row already exists it updates the notify_on preference.
func (s *Store) SubscribeToChannel(ctx context.Context, channelID, userID uuid.UUID, notifyOn string) error {
	if notifyOn == "" {
		notifyOn = "all"
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO channel_subscriptions (channel_id, user_id, notify_on, subscribed_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (channel_id, user_id) DO UPDATE SET notify_on = EXCLUDED.notify_on
	`, channelID, userID, notifyOn)
	return err
}

// UnsubscribeFromChannel deletes the subscription row.
func (s *Store) UnsubscribeFromChannel(ctx context.Context, channelID, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM channel_subscriptions WHERE channel_id = $1 AND user_id = $2
	`, channelID, userID)
	return err
}

// GetChannelSubscription returns one subscription row or nil if not subscribed.
func (s *Store) GetChannelSubscription(ctx context.Context, channelID, userID uuid.UUID) (*ChannelSubscription, error) {
	var sub ChannelSubscription
	err := s.db.QueryRow(ctx, `
		SELECT channel_id, user_id, notify_on, subscribed_at
		FROM channel_subscriptions
		WHERE channel_id = $1 AND user_id = $2
	`, channelID, userID).Scan(&sub.ChannelID, &sub.UserID, &sub.NotifyOn, &sub.SubscribedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

// ListUserChannelSubscriptions returns all channels a user subscribes to (paginated).
func (s *Store) ListUserChannelSubscriptions(ctx context.Context, userID uuid.UUID, limit, offset int) ([]ChannelSubscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT channel_id, user_id, notify_on, subscribed_at
		FROM channel_subscriptions
		WHERE user_id = $1
		ORDER BY subscribed_at DESC
		LIMIT $2 OFFSET $3
	`, userID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []ChannelSubscription
	for rows.Next() {
		var sub ChannelSubscription
		if err := rows.Scan(&sub.ChannelID, &sub.UserID, &sub.NotifyOn, &sub.SubscribedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// ListChannelSubscribers returns all subscribers of a channel (paginated).
func (s *Store) ListChannelSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]ChannelSubscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT channel_id, user_id, notify_on, subscribed_at
		FROM channel_subscriptions
		WHERE channel_id = $1
		ORDER BY subscribed_at DESC
		LIMIT $2 OFFSET $3
	`, channelID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []ChannelSubscription
	for rows.Next() {
		var sub ChannelSubscription
		if err := rows.Scan(&sub.ChannelID, &sub.UserID, &sub.NotifyOn, &sub.SubscribedAt); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}
