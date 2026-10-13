package service

import (
	"context"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Seller/customer events stay on their existing install, never the delivery/service app.
func (s *Service) eventDevices(ctx context.Context, userID uuid.UUID, _ string) ([]postgres.UserDevice, error) {
	return s.pgStore.GetUserDevices(ctx, userID)
}
