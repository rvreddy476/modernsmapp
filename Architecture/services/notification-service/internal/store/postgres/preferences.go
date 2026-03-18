package postgres

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// NotificationPreferences stores per-user notification settings.
type NotificationPreferencesLegacy struct {
	UserID          uuid.UUID       `json:"user_id"`
	EmailEnabled    bool            `json:"email_enabled"`
	PushEnabled     bool            `json:"push_enabled"`
	SMSEnabled      bool            `json:"sms_enabled"`
	QuietHoursStart *string         `json:"quiet_hours_start,omitempty"` // HH:MM format
	QuietHoursEnd   *string         `json:"quiet_hours_end,omitempty"`
	MutedTypes      json.RawMessage `json:"muted_types,omitempty"` // ["reaction", "follow", ...]
	UpdatedAt       time.Time       `json:"updated_at"`
}

// GetPreferences returns the notification preferences for a user.
// Returns default preferences if none exist.
func (s *Store) GetPreferences(ctx context.Context, userID uuid.UUID) (*NotificationPreferencesLegacy, error) {
	var p NotificationPreferencesLegacy
	err := s.db.QueryRow(ctx, `
		SELECT user_id, email_enabled, push_enabled, sms_enabled,
			quiet_hours_start, quiet_hours_end, muted_types, updated_at
		FROM notification_preferences WHERE user_id = $1
	`, userID).Scan(
		&p.UserID, &p.EmailEnabled, &p.PushEnabled, &p.SMSEnabled,
		&p.QuietHoursStart, &p.QuietHoursEnd, &p.MutedTypes, &p.UpdatedAt,
	)
	if err != nil {
		if err == pgx.ErrNoRows {
			return &NotificationPreferencesLegacy{
				UserID:       userID,
				EmailEnabled: true,
				PushEnabled:  true,
				SMSEnabled:   false,
				MutedTypes:   json.RawMessage("[]"),
				UpdatedAt:    time.Now(),
			}, nil
		}
		return nil, err
	}
	return &p, nil
}

// UpsertPreferences creates or updates notification preferences.
func (s *Store) UpsertPreferences(ctx context.Context, p *NotificationPreferencesLegacy) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO notification_preferences (user_id, email_enabled, push_enabled, sms_enabled,
			quiet_hours_start, quiet_hours_end, muted_types, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id) DO UPDATE SET
			email_enabled = $2, push_enabled = $3, sms_enabled = $4,
			quiet_hours_start = $5, quiet_hours_end = $6, muted_types = $7, updated_at = $8
	`, p.UserID, p.EmailEnabled, p.PushEnabled, p.SMSEnabled,
		p.QuietHoursStart, p.QuietHoursEnd, p.MutedTypes, time.Now())
	return err
}
