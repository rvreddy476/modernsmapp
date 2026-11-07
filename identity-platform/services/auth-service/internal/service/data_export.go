// Account data access: the DataExport bundle and ExportUserData for GDPR
// portability, GetUserContact for internal service lookups, and phone
// masking. Pure move out of auth.go; behaviour is unchanged.

package service

import (
	"context"
	"strings"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// DeleteAccount / DeactivateAccount live in account_lifecycle.go.

// DataExport holds all personal data for a user, for GDPR data portability.
type DataExport struct {
	UserID     string      `json:"user_id"`
	User       interface{} `json:"user"`
	Sessions   interface{} `json:"sessions"`
	Devices    interface{} `json:"devices"`
	ExportedAt time.Time   `json:"exported_at"`
}

// GetUserContact returns the user record for internal service-to-service lookups.
// Used by commerce, notification, etc. to resolve email/phone.
func (s *Service) GetUserContact(ctx context.Context, userID uuid.UUID) (*store.User, error) {
	return s.store.GetUserByID(ctx, userID)
}

// ExportUserData collects a user's personal data from the auth store for GDPR portability.
func (s *Service) ExportUserData(ctx context.Context, userID string) (*DataExport, error) {
	uid, err := uuid.Parse(userID)
	if err != nil {
		return nil, err
	}

	// Fetch user record
	user, err := s.store.GetUserByID(ctx, uid)
	if err != nil {
		return nil, err
	}

	// Fetch active sessions
	sessions, _ := s.store.ListActiveSessions(ctx, uid)

	// Fetch trusted devices
	devices, _ := s.store.ListTrustedDevices(ctx, uid)

	return &DataExport{
		UserID:     userID,
		User:       user,
		Sessions:   sessions,
		Devices:    devices,
		ExportedAt: time.Now(),
	}, nil
}

func maskPhone(phone string) string {
	trimmed := strings.TrimSpace(phone)
	if trimmed == "" {
		return ""
	}
	if len(trimmed) <= 4 {
		return strings.Repeat("*", len(trimmed))
	}
	return strings.Repeat("*", len(trimmed)-2) + trimmed[len(trimmed)-2:]
}
