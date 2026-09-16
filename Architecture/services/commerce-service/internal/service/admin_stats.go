package service

import (
	"context"

	"github.com/atpost/commerce-service/internal/store/postgres"
)

// AdminStats returns the MStore admin dashboard counts (read-only).
func (s *Service) AdminStats(ctx context.Context) (*postgres.AdminStats, error) {
	return s.store.AdminStats(ctx)
}
