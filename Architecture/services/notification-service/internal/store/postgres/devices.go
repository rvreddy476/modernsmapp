package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// UserDevice represents a registered push notification device.
type UserDevice struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Platform  string    `json:"platform"` // ios, android, web
	PushToken string    `json:"push_token"`
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RegisterDevice adds or updates a push notification device for a user.
func (s *Store) RegisterDevice(ctx context.Context, userID uuid.UUID, platform, pushToken string) (*UserDevice, error) {
	device := &UserDevice{
		ID:        uuid.New(),
		UserID:    userID,
		Platform:  platform,
		PushToken: pushToken,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_, err := s.db.Exec(ctx, `
		INSERT INTO user_devices (id, user_id, platform, push_token, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (user_id, platform, push_token) DO UPDATE SET
			is_active = TRUE, updated_at = $7
	`, device.ID, device.UserID, device.Platform, device.PushToken, device.IsActive, device.CreatedAt, device.UpdatedAt)
	if err != nil {
		return nil, err
	}

	return device, nil
}

// UnregisterDevice deactivates a device by ID.
func (s *Store) UnregisterDevice(ctx context.Context, deviceID, userID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM user_devices WHERE id = $1 AND user_id = $2
	`, deviceID, userID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("DEVICE_NOT_FOUND")
	}
	return nil
}

// DeactivateDeviceTokens marks all push-notification devices for the given user
// as inactive, fulfilling the GDPR right-to-erasure requirement for device tokens.
func (s *Store) DeactivateDeviceTokens(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE user_devices SET is_active = FALSE WHERE user_id = $1`, userID)
	return err
}

// GetUserDevices returns all active devices for a user.
func (s *Store) GetUserDevices(ctx context.Context, userID uuid.UUID) ([]UserDevice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, platform, push_token, is_active, created_at, updated_at
		FROM user_devices WHERE user_id = $1 AND is_active = TRUE
		ORDER BY updated_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []UserDevice
	for rows.Next() {
		var d UserDevice
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.PushToken, &d.IsActive, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}
