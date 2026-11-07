package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// UserReputation stores a user's trust score and endorsement count.
type UserReputation struct {
	UserID              uuid.UUID       `json:"user_id"`
	TrustScore          float64         `json:"trust_score"` // 0.00 – 1.00
	EndorsementCount    int             `json:"endorsement_count"`
	CrossPlatformProofs json.RawMessage `json:"cross_platform_proofs,omitempty"`
}

// GetReputation returns the reputation record for a user.
func (s *Store) GetReputation(ctx context.Context, userID uuid.UUID) (*UserReputation, error) {
	var r UserReputation
	err := s.db.QueryRow(ctx, `
		SELECT user_id, trust_score, endorsement_count, cross_platform_proofs
		FROM user_reputation WHERE user_id = $1
	`, userID).Scan(&r.UserID, &r.TrustScore, &r.EndorsementCount, &r.CrossPlatformProofs)
	if err != nil {
		// Return default reputation if none exists
		return &UserReputation{
			UserID:              userID,
			TrustScore:          0.50,
			EndorsementCount:    0,
			CrossPlatformProofs: json.RawMessage("{}"),
		}, nil
	}
	return &r, nil
}

// UpdateStatus updates a user's status/mood.
func (s *Store) UpdateStatus(ctx context.Context, userID uuid.UUID, statusText, statusEmoji string, expiresAt *time.Time) error {
	_, err := s.db.Exec(ctx, `
		UPDATE users SET status_text = $2, status_emoji = $3, status_expires_at = $4, updated_at = NOW()
		WHERE id = $1
	`, userID, statusText, statusEmoji, expiresAt)
	return err
}

// ClearExpiredStatuses clears statuses that have expired.
func (s *Store) ClearExpiredStatuses(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE users SET status_text = '', status_emoji = '', status_expires_at = NULL, updated_at = NOW()
		WHERE status_expires_at IS NOT NULL AND status_expires_at < NOW()
	`)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// GetLinkAnalytics returns click counts for all of a user's links.
type LinkAnalytics struct {
	Platform   string `json:"platform"`
	URL        string `json:"url"`
	ClickCount int    `json:"click_count"`
}

func (s *Store) GetLinkAnalytics(ctx context.Context, userID uuid.UUID) ([]LinkAnalytics, error) {
	rows, err := s.db.Query(ctx, `
		SELECT platform, url, COALESCE(click_count, 0)
		FROM user_links WHERE user_id = $1 ORDER BY sort_order
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var analytics []LinkAnalytics
	for rows.Next() {
		var a LinkAnalytics
		if err := rows.Scan(&a.Platform, &a.URL, &a.ClickCount); err != nil {
			return nil, err
		}
		analytics = append(analytics, a)
	}
	return analytics, rows.Err()
}
