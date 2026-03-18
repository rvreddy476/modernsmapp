package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// SubscriptionEvent represents an entry in the subscription_events audit trail.
type SubscriptionEvent struct {
	ID             uuid.UUID       `json:"id"`
	SubscriptionID uuid.UUID       `json:"subscription_id"`
	EventType      string          `json:"event_type"`
	OldStatus      *string         `json:"old_status,omitempty"`
	NewStatus      *string         `json:"new_status,omitempty"`
	Metadata       json.RawMessage `json:"metadata,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
}

// InsertSubscriptionEvent appends an audit event to the subscription_events table.
func (s *Store) InsertSubscriptionEvent(ctx context.Context, subID uuid.UUID, eventType, oldStatus, newStatus string, metadata json.RawMessage) error {
	var oldPtr, newPtr *string
	if oldStatus != "" {
		oldPtr = &oldStatus
	}
	if newStatus != "" {
		newPtr = &newStatus
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO subscription_events (id, subscription_id, event_type, old_status, new_status, metadata, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`, uuid.New(), subID, eventType, oldPtr, newPtr, metadata)
	return err
}

// GetSubscriptionEvents returns paginated subscription events for a given subscription.
func (s *Store) GetSubscriptionEvents(ctx context.Context, subID uuid.UUID, limit, offset int) ([]SubscriptionEvent, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, subscription_id, event_type, old_status, new_status, metadata, created_at
		FROM subscription_events
		WHERE subscription_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3
	`, subID, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []SubscriptionEvent
	for rows.Next() {
		var e SubscriptionEvent
		if err := rows.Scan(&e.ID, &e.SubscriptionID, &e.EventType, &e.OldStatus, &e.NewStatus, &e.Metadata, &e.CreatedAt); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// SetSubscriptionStatus updates the status of a subscription.
func (s *Store) SetSubscriptionStatus(ctx context.Context, subID uuid.UUID, status string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions SET status = $2 WHERE id = $1
	`, subID, status)
	return err
}

// SetSubscriptionGracePeriod sets the grace_period_end on a subscription.
func (s *Store) SetSubscriptionGracePeriod(ctx context.Context, subID uuid.UUID, graceEnd time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions SET grace_period_end = $2, status = 'grace' WHERE id = $1
	`, subID, graceEnd)
	return err
}

// PauseSubscription sets a subscription to paused with a pause_until timestamp.
func (s *Store) PauseSubscription(ctx context.Context, subID uuid.UUID, pauseUntil time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions SET status = 'paused', pause_until = $2 WHERE id = $1
	`, subID, pauseUntil)
	return err
}

// ResumeSubscription reactivates a paused subscription.
func (s *Store) ResumeSubscription(ctx context.Context, subID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions SET status = 'active', pause_until = NULL WHERE id = $1
	`, subID)
	return err
}

// CancelAtPeriodEnd marks a subscription to cancel when the current period ends.
func (s *Store) CancelAtPeriodEnd(ctx context.Context, subID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions
		SET status = 'cancelled_at_period_end', cancelled_at = NOW(), cancellation_reason = $2
		WHERE id = $1
	`, subID, reason)
	return err
}

// IncrementRetryCount atomically increments the retry_count and returns the new value.
func (s *Store) IncrementRetryCount(ctx context.Context, subID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx, `
		UPDATE subscriptions SET retry_count = retry_count + 1
		WHERE id = $1
		RETURNING retry_count
	`, subID).Scan(&count)
	return count, err
}

// GetPausedSubscriptionsToResume returns paused subscriptions whose pause_until is before now.
func (s *Store) GetPausedSubscriptionsToResume(ctx context.Context, now time.Time) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status,
		       current_period_start, current_period_end, created_at
		FROM subscriptions
		WHERE status = 'paused' AND pause_until IS NOT NULL AND pause_until <= $1
		LIMIT 500
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(
			&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
			&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CreatedAt,
		); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// GetGracePeriodExpired returns subscriptions in grace status whose grace_period_end has passed.
func (s *Store) GetGracePeriodExpired(ctx context.Context, now time.Time) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status,
		       current_period_start, current_period_end, created_at
		FROM subscriptions
		WHERE status = 'grace' AND grace_period_end IS NOT NULL AND grace_period_end <= $1
		LIMIT 500
	`, now)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var subs []Subscription
	for rows.Next() {
		var sub Subscription
		if err := rows.Scan(
			&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
			&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CreatedAt,
		); err != nil {
			return nil, err
		}
		subs = append(subs, sub)
	}
	return subs, rows.Err()
}

// GetSubscriptionByID returns a subscription by its ID, regardless of status.
func (s *Store) GetSubscriptionByID(ctx context.Context, subID uuid.UUID) (*Subscription, error) {
	var sub Subscription
	err := s.db.QueryRow(ctx, `
		SELECT id, subscriber_id, creator_id, tier_id, tier_name, price, currency, status,
		       current_period_start, current_period_end, created_at
		FROM subscriptions
		WHERE id = $1
	`, subID).Scan(
		&sub.ID, &sub.SubscriberID, &sub.CreatorID, &sub.TierID, &sub.TierName,
		&sub.PricePaise, &sub.Currency, &sub.Status, &sub.CurrentPeriodStart, &sub.CurrentPeriodEnd, &sub.CreatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	return &sub, nil
}

// UpdateSubscriptionTier changes the tier and price of a subscription.
func (s *Store) UpdateSubscriptionTier(ctx context.Context, subID, newTierID uuid.UUID, newTierName string, newPrice int64) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions
		SET tier_id = $2, tier_name = $3, price = $4
		WHERE id = $1
	`, subID, newTierID, newTierName, newPrice)
	return err
}
