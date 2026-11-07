package service

import (
	"context"
	"fmt"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// GetDigests returns recent digests for a user. The digest worker writes
// them straight through the store (workers/digest.go).
func (s *Service) GetDigests(ctx context.Context, userID uuid.UUID) ([]postgres.NotificationDigest, error) {
	if s.pgStore == nil {
		return nil, fmt.Errorf("PG store not configured")
	}
	return s.pgStore.GetDigests(ctx, userID, 20)
}
