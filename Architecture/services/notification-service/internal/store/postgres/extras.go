package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// NotificationDigest is a weekly or monthly summary of notifications for a user.
type NotificationDigest struct {
	ID          uuid.UUID       `json:"id"`
	UserID      uuid.UUID       `json:"user_id"`
	PeriodType  string          `json:"period_type"`
	PeriodStart time.Time       `json:"period_start"`
	Content     json.RawMessage `json:"content"`
	SentAt      *time.Time      `json:"sent_at,omitempty"`
}

// CreateDigest inserts a new notification digest record.
func (s *Store) CreateDigest(ctx context.Context, d *NotificationDigest) error {
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO notification_digests (id, user_id, period_type, period_start, content, sent_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (user_id, period_type, period_start) DO UPDATE
			SET content = $5, sent_at = $6
	`, d.ID, d.UserID, d.PeriodType, d.PeriodStart, d.Content, d.SentAt)
	return err
}

// GetDigests returns recent digests for a user, newest first.
func (s *Store) GetDigests(ctx context.Context, userID uuid.UUID, limit int) ([]NotificationDigest, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, period_type, period_start, content, sent_at
		FROM notification_digests
		WHERE user_id = $1
		ORDER BY period_start DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var digests []NotificationDigest
	for rows.Next() {
		var d NotificationDigest
		if err := rows.Scan(&d.ID, &d.UserID, &d.PeriodType, &d.PeriodStart, &d.Content, &d.SentAt); err != nil {
			return nil, err
		}
		digests = append(digests, d)
	}
	return digests, rows.Err()
}
