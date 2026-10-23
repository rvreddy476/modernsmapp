package postgres

import (
	"context"
	"encoding/json"
	"errors"
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

// PauseSubscription sets a subscription to paused with a pause_until
// timestamp. The paid period is not moved: a paused subscription keeps
// access only until current_period_end, and is not renewed while paused.
func (s *Store) PauseSubscription(ctx context.Context, subID uuid.UUID, pauseUntil time.Time) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE subscriptions SET status = 'paused', pause_until = $2 WHERE id = $1 AND status = 'active'
	`, subID, pauseUntil)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionStateChanged
	}
	return nil
}

// ResumeSubscription reactivates a paused subscription.
func (s *Store) ResumeSubscription(ctx context.Context, subID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE subscriptions SET status = 'active', pause_until = NULL WHERE id = $1 AND status = 'paused'
	`, subID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionStateChanged
	}
	return nil
}

// CancelAtPeriodEnd marks a subscription to cancel when the current period ends.
func (s *Store) CancelAtPeriodEnd(ctx context.Context, subID uuid.UUID, reason string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE subscriptions
		SET status = 'cancelled_at_period_end', cancelled_at = NOW(), cancellation_reason = $2,
		    pending_tier_id = NULL, pending_price = NULL
		WHERE id = $1
	`, subID, reason)
	return err
}

// GetPausedSubscriptionsToResume returns paused subscriptions whose pause_until is before now.
func (s *Store) GetPausedSubscriptionsToResume(ctx context.Context, now time.Time) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+subscriptionColumns+`
		FROM subscriptions
		WHERE status = 'paused' AND pause_until IS NOT NULL AND pause_until <= $1
		LIMIT 500
	`, now)
	if err != nil {
		return nil, err
	}
	return scanSubscriptions(rows)
}

// GetGracePeriodExpired returns subscriptions in grace status whose grace_period_end has passed.
func (s *Store) GetGracePeriodExpired(ctx context.Context, now time.Time) ([]Subscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+subscriptionColumns+`
		FROM subscriptions
		WHERE status = 'grace' AND grace_period_end IS NOT NULL AND grace_period_end <= $1
		LIMIT 500
	`, now)
	if err != nil {
		return nil, err
	}
	return scanSubscriptions(rows)
}

// GetSubscriptionByID returns a subscription by its ID, regardless of status.
func (s *Store) GetSubscriptionByID(ctx context.Context, subID uuid.UUID) (*Subscription, error) {
	sub, err := scanSubscription(s.db.QueryRow(ctx, `
		SELECT `+subscriptionColumns+`
		FROM subscriptions
		WHERE id = $1
	`, subID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return sub, err
}
