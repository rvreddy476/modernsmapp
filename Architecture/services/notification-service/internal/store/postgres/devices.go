package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Installed apps a push token can belong to (migration 007). An FCM token is
// minted per app install, so the app is part of the device's identity: a push
// goes only to devices registered for the app it is meant for.
const (
	// AppMomentum is the main app, including Feast's customer surface.
	// Registrations that name no app are Momentum's.
	AppMomentum = "momentum"
	// AppFeastKitchen is the restaurant app.
	AppFeastKitchen = "feast_kitchen"
	// AppFeastRider is the delivery-partner app.
	AppFeastRider = "feast_rider"
)

// ValidDeviceApp reports whether app is a known install target.
func ValidDeviceApp(app string) bool {
	switch app {
	case AppMomentum, AppFeastKitchen, AppFeastRider:
		return true
	}
	return false
}

// NormalizeDeviceApp maps an omitted app to Momentum, the only app that
// registered devices before the field existed.
func NormalizeDeviceApp(app string) string {
	if app == "" {
		return AppMomentum
	}
	return app
}

// UserDevice represents a registered push notification device.
type UserDevice struct {
	ID        uuid.UUID `json:"id"`
	UserID    uuid.UUID `json:"user_id"`
	Platform  string    `json:"platform"` // ios, android, web
	PushToken string    `json:"push_token"`
	App       string    `json:"app"` // momentum, feast_kitchen, feast_rider
	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// RegisterDevice adds or updates a push notification device for a user. An
// empty app registers for Momentum.
func (s *Store) RegisterDevice(ctx context.Context, userID uuid.UUID, platform, pushToken, app string) (*UserDevice, error) {
	app = NormalizeDeviceApp(app)
	if !ValidDeviceApp(app) {
		return nil, fmt.Errorf("unknown device app %q", app)
	}
	device := &UserDevice{
		ID:        uuid.New(),
		UserID:    userID,
		Platform:  platform,
		PushToken: pushToken,
		App:       app,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// A token is minted by exactly one install, so the latest registration
	// is authoritative about which app it belongs to.
	_, err := s.db.Exec(ctx, `
		INSERT INTO user_devices (id, user_id, platform, push_token, app, is_active, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (user_id, platform, push_token) DO UPDATE SET
			is_active = TRUE, app = EXCLUDED.app, updated_at = $8
	`, device.ID, device.UserID, device.Platform, device.PushToken, device.App, device.IsActive, device.CreatedAt, device.UpdatedAt)
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

// RetireDeviceToken durably deactivates ONE permanently rejected push token
// (CALL-LB-4): the provider said this token can never deliver (unregistered,
// invalid, app uninstalled), so it must leave the active set — otherwise the
// call path would retry a dead token forever and stall its Kafka partition.
// Idempotent: retiring an already-retired or absent token is a no-op.
func (s *Store) RetireDeviceToken(ctx context.Context, userID uuid.UUID, pushToken string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE user_devices SET is_active = FALSE, updated_at = NOW()
		WHERE user_id = $1 AND push_token = $2`, userID, pushToken)
	return err
}

// DeactivateDeviceTokens marks all push-notification devices for the given user
// as inactive, fulfilling the GDPR right-to-erasure requirement for device tokens.
// Every app's devices go: erasure is per person, not per install.
func (s *Store) DeactivateDeviceTokens(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`UPDATE user_devices SET is_active = FALSE WHERE user_id = $1`, userID)
	return err
}

// GetUserDevices returns the user's active MOMENTUM devices — the target of
// every social, chat, call and upload push. Feast Kitchen and Feast Rider
// installs are reached only through GetUserDevicesForApp.
func (s *Store) GetUserDevices(ctx context.Context, userID uuid.UUID) ([]UserDevice, error) {
	return s.GetUserDevicesForApp(ctx, userID, AppMomentum)
}

// GetUserDevicesForApp returns the user's active devices registered for app.
func (s *Store) GetUserDevicesForApp(ctx context.Context, userID uuid.UUID, app string) ([]UserDevice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, platform, push_token, app, is_active, created_at, updated_at
		FROM user_devices WHERE user_id = $1 AND app = $2 AND is_active = TRUE
		ORDER BY updated_at DESC
	`, userID, app)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []UserDevice
	for rows.Next() {
		var d UserDevice
		if err := rows.Scan(&d.ID, &d.UserID, &d.Platform, &d.PushToken, &d.App, &d.IsActive, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}
